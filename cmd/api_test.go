package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

// newTestHandler creates an apiHandler backed by temp files.
func newTestHandler(t *testing.T) (*apiHandler, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	return &apiHandler{regPath: regPath, pidPath: pidPath, runtimeSnapshotPath: snapshotPath}, dir
}

// parseResponse decodes the first JSON line written to buf.
func parseResponse(t *testing.T, buf *bytes.Buffer) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(buf).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v (raw: %q)", err, buf.String())
	}
	return resp
}

// sendRequest sends a single APIRequest to the handler and returns the response.
func sendRequest(t *testing.T, h *apiHandler, req APIRequest) APIResponse {
	t.Helper()
	var buf bytes.Buffer
	h.handle(req, &buf)
	return parseResponse(t, &buf)
}

func assertAPIRawJSONHasNoPrivateRegistryFields(t *testing.T, raw string, forbiddenValues ...string) {
	t.Helper()
	for _, forbidden := range []string{
		`"basic_auth"`,
		`"control_url"`,
		`"acme_email"`,
		`"domain"`,
		`"allowed_users"`,
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("api JSON leaked private registry field %s: %s", forbidden, raw)
		}
	}
	for _, forbidden := range forbiddenValues {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("api JSON leaked private value %q: %s", forbidden, raw)
		}
	}
}

// --- list ---

func TestAPIList_Empty(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 0 {
		t.Fatalf("expected 0 services, got %d", len(resp.Services))
	}
}

func TestAPIList_WithServices(t *testing.T) {
	h, _ := newTestHandler(t)

	// Pre-populate registry.
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "myapp",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	if resp.Services[0].Name != "myapp" {
		t.Errorf("expected name myapp, got %s", resp.Services[0].Name)
	}
	if resp.Services[0].Endpoint.Kind != inspect.EndpointKindHTTPS {
		t.Errorf("endpoint kind = %s, want https", resp.Services[0].Endpoint.Kind)
	}
	if resp.Services[0].Exposure.Kind != inspect.ExposureTailnet {
		t.Errorf("exposure = %s, want tailnet", resp.Services[0].Exposure.Kind)
	}
}

func TestAPIList_RedactsMiddlewareAuth(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "myapp",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "list"}, &buf)
	raw := buf.String()
	if strings.Contains(raw, "user:pass") {
		t.Fatalf("api list leaked credential: %s", raw)
	}
	if strings.Contains(raw, "basic_auth") {
		t.Fatalf("api list leaked private field name: %s", raw)
	}

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	if resp.Services[0].Middleware == nil || !resp.Services[0].Middleware.HTTPAuth {
		t.Fatalf("middleware summary = %+v, want redacted auth presence", resp.Services[0].Middleware)
	}
}

func TestAPIList_UsesPublicServiceViewsWithUsefulFields(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "docs",
		Type:         registry.TypeFile,
		Path:         "/tmp/docs",
		Tags:         []string{"tag:docs"},
		AllowedUsers: []string{"alice@example.com"},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Count != 1 {
		t.Fatalf("count = %d, want 1", resp.Count)
	}
	got := resp.Services[0]
	if got.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", got.SchemaVersion, inspect.SchemaVersion)
	}
	if got.Endpoint.Kind != inspect.EndpointKindHTTPS || got.Endpoint.Display != "https://docs.<tailnet>.ts.net" {
		t.Fatalf("endpoint = %+v, want https typed endpoint", got.Endpoint)
	}
	if got.Exposure.Kind != inspect.ExposureTailnetAllow {
		t.Fatalf("exposure = %+v, want tailnet_allow", got.Exposure)
	}
	if got.Backend.Kind != "directory" || got.Backend.Display != "/tmp/docs" {
		t.Fatalf("backend = %+v, want directory backend", got.Backend)
	}
	if got.Tags.Count != 1 || got.Tags.Entries[0] != "tag:docs" {
		t.Fatalf("tags = %+v, want useful tag summary", got.Tags)
	}
	if got.Allow.Mode != "restricted" || got.Allow.Count != 1 || !got.Allow.Redacted || len(got.Allow.Entries) != 0 {
		t.Fatalf("allow = %+v, want redacted allow summary", got.Allow)
	}
}

func TestAPIList_RedactsAllowPrincipals(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "docs",
		Type:         registry.TypeFile,
		Path:         "/tmp/docs",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "list"}, &buf)
	raw := buf.String()
	for _, principal := range []string{"alice@example.com", "tag:admin"} {
		if strings.Contains(raw, principal) {
			t.Fatalf("api list leaked allow principal %q: %s", principal, raw)
		}
	}

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	allow := resp.Services[0].Allow
	if allow.Mode != "restricted" || allow.Count != 2 || !allow.Redacted || len(allow.Entries) != 0 {
		t.Fatalf("allow = %+v, want redacted restricted summary", allow)
	}
}

func TestAPIList_TCPUsesTypedEndpoint(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "list"}, &buf)
	raw := buf.String()
	if strings.Contains(raw, "https://db.<tailnet>.ts.net") {
		t.Fatalf("api list rendered TCP service as HTTPS: %s", raw)
	}

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	endpoint := resp.Services[0].Endpoint
	if endpoint.Kind != inspect.EndpointKindTCP {
		t.Fatalf("endpoint kind = %q, want tcp", endpoint.Kind)
	}
	if endpoint.Display != "db.<tailnet>.ts.net:5432" || endpoint.Port != 5432 {
		t.Fatalf("endpoint = %+v, want typed TCP display and port", endpoint)
	}
}

// --- add proxy ---

func TestAPIAdd_Proxy(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "myapp",
		Type:   "proxy",
		Target: "localhost:3000",
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Message != "service added" {
		t.Errorf("unexpected message: %s", resp.Message)
	}
	if !strings.Contains(resp.URL, "myapp") {
		t.Errorf("URL should contain service name, got: %s", resp.URL)
	}
	if resp.Endpoint == nil || resp.Endpoint.Kind != inspect.EndpointKindHTTPS || resp.Endpoint.Display != "https://myapp.<tailnet>.ts.net" {
		t.Fatalf("endpoint = %+v, want https add endpoint", resp.Endpoint)
	}

	// Verify the scheme was prepended.
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if !strings.HasPrefix(reg.Services[0].Target, "http://") {
		t.Errorf("expected http:// prefix, got %s", reg.Services[0].Target)
	}
	if len(reg.Services[0].Tags) != 1 || reg.Services[0].Tags[0] != "tag:tsmain" {
		t.Errorf("expected default tag:tsmain, got %v", reg.Services[0].Tags)
	}
}

func TestAPIAdd_Proxy_WithScheme(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "secure",
		Type:   "proxy",
		Target: "https://localhost:8443",
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}

	reg, _ := registry.Load(h.regPath)
	if reg.Services[0].Target != "https://localhost:8443" {
		t.Errorf("scheme should not be changed, got %s", reg.Services[0].Target)
	}
}

func TestAPIAdd_Proxy_WithProvidedTags(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "tagged",
		Type:   "proxy",
		Target: "localhost:3000",
		Tags:   []string{"tag:web", "tag:internal"},
		Allow:  []string{"user@example.com", "tag:admin"},
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}

	reg, _ := registry.Load(h.regPath)
	svc := reg.Services[0]
	if strings.Join(svc.Tags, ",") != "tag:web,tag:internal" {
		t.Fatalf("tags = %v, want provided tags", svc.Tags)
	}
	if strings.Join(svc.AllowedUsers, ",") != "user@example.com,tag:admin" {
		t.Fatalf("allowed_users = %v, want provided allow list", svc.AllowedUsers)
	}
	if svc.Funnel {
		t.Fatal("expected funnel=false when allow list is configured")
	}
}

func TestAPIAdd_FunnelRejectsMissingPublicAck(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "public-app",
		Type:   "proxy",
		Target: "localhost:3000",
		Funnel: true,
	})
	if resp.OK {
		t.Fatal("expected missing public_ack error")
	}
	if resp.Error != "public_ack must be true when funnel is true" {
		t.Fatalf("error = %q, want exact API public_ack error", resp.Error)
	}
}

func TestAPIAdd_FunnelRejectsAllowWithPublicAck(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action:    "add",
		Name:      "public-app",
		Type:      "proxy",
		Target:    "localhost:3000",
		Allow:     []string{"alice@example.com"},
		Funnel:    true,
		PublicAck: true,
	})
	if resp.OK {
		t.Fatal("expected funnel allowed_users error")
	}
	if !strings.Contains(resp.Error, registry.ErrFunnelAllowedUsers) {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if !strings.Contains(resp.Error, registry.CodeFunnelAllowConflict) {
		t.Fatalf("error = %q, want stable code %s", resp.Error, registry.CodeFunnelAllowConflict)
	}
}

func TestAPIAdd_FunnelAcceptsPublicAck(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action:    "add",
		Name:      "public-app",
		Type:      "proxy",
		Target:    "localhost:3000",
		Funnel:    true,
		PublicAck: true,
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Endpoint == nil || resp.Endpoint.Kind != inspect.EndpointKindPublicHTTPS {
		t.Fatalf("endpoint = %+v, want public https endpoint", resp.Endpoint)
	}
	if resp.Exposure == nil || resp.Exposure.Kind != inspect.ExposurePublicFunnel || !resp.Exposure.Public {
		t.Fatalf("exposure = %+v, want public_funnel public exposure", resp.Exposure)
	}

	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(reg.Services))
	}
	svc := reg.Services[0]
	if !svc.Funnel {
		t.Fatal("expected funnel=true")
	}
	if len(svc.AllowedUsers) != 0 || svc.ControlURL != "" {
		t.Fatalf("service = %+v, want no allow/control_url", svc)
	}
}

func TestAPIAdd_Proxy_WithControlURL(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	h.handleLine(`{"action":"add","name":"headscale-app","type":"proxy","target":"localhost:3000","control_url":"https://headscale.example.com"}`, &buf)

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}

	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].ControlURL != "https://headscale.example.com" {
		t.Fatalf("control_url = %q, want %q", reg.Services[0].ControlURL, "https://headscale.example.com")
	}
}

func TestAPIAdd_RejectsInvalidControlURLWithoutCreatingService(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	h.handleLine(`{"action":"add","name":"headscale-app","type":"proxy","target":"localhost:3000","control_url":"not-a-url"}`, &buf)

	resp := parseResponse(t, &buf)
	if resp.OK {
		t.Fatal("expected error for invalid control_url")
	}
	if !strings.Contains(resp.Error, "invalid URL") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("services = %+v, want none after rejected request", reg.Services)
	}
}

// --- add file ---

func TestAPIAdd_File(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	// Use the temp dir itself as the file share directory.
	shareDir := filepath.Join(tmpDir, "share")
	if err := os.Mkdir(shareDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "docs",
		Type:   "file",
		Path:   shareDir,
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}

	reg, _ := registry.Load(h.regPath)
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Path != shareDir {
		t.Errorf("expected path %s, got %s", shareDir, reg.Services[0].Path)
	}
}

func TestAPIAdd_File_NotDirectory(t *testing.T) {
	h, tmpDir := newTestHandler(t)
	// Create a regular file instead of a directory.
	filePath := filepath.Join(tmpDir, "notadir.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "badfile",
		Type:   "file",
		Path:   filePath,
	})
	if resp.OK {
		t.Fatal("expected error for non-directory path")
	}
	if !strings.Contains(resp.Error, "not a directory") {
		t.Errorf("unexpected error message: %s", resp.Error)
	}
}

// --- add tcp ---

func TestAPIAdd_TCP(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "mydb",
		Type:   "tcp",
		Target: "localhost:5432",
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if strings.Contains(resp.URL, "https://mydb.<tailnet>.ts.net") {
		t.Fatalf("api add rendered TCP URL as HTTPS: %+v", resp)
	}
	if resp.URL != "mydb.<tailnet>.ts.net:5432" {
		t.Fatalf("url = %q, want typed TCP display", resp.URL)
	}
	if resp.Endpoint == nil || resp.Endpoint.Kind != inspect.EndpointKindTCP || resp.Endpoint.Display != "mydb.<tailnet>.ts.net:5432" || resp.Endpoint.Port != 5432 {
		t.Fatalf("endpoint = %+v, want typed TCP endpoint", resp.Endpoint)
	}

	reg, _ := registry.Load(h.regPath)
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	svc := reg.Services[0]
	if svc.Port != 5432 {
		t.Errorf("expected port 5432, got %d", svc.Port)
	}
	if svc.Type != registry.TypeTCP {
		t.Errorf("expected type tcp, got %s", svc.Type)
	}
}

func TestAPIAdd_TCP_BadFormat(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "badtcp",
		Type:   "tcp",
		Target: "noporthere",
	})
	if resp.OK {
		t.Fatal("expected error for bad host:port")
	}
}

func TestAPIAdd_TCP_RejectsAllow(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "mydb",
		Type:   "tcp",
		Target: "localhost:5432",
		Allow:  []string{"user@example.com"},
	})
	if resp.OK {
		t.Fatal("expected error for tcp allow")
	}
	if !strings.Contains(resp.Error, "allow is not supported") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_RejectsInconsistentFields(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add",
		Name:   "myapp",
		Type:   "proxy",
		Target: "localhost:3000",
		Path:   "/tmp/ignored",
	})
	if resp.OK {
		t.Fatal("expected error for proxy path")
	}
	if !strings.Contains(resp.Error, "path is not supported for proxy type") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

// --- remove ---

func TestAPIRemove(t *testing.T) {
	h, _ := newTestHandler(t)

	// Add then remove.
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "myapp",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	resp := sendRequest(t, h, APIRequest{Action: "remove", Name: "myapp"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Message != "service removed" {
		t.Errorf("unexpected message: %s", resp.Message)
	}

	reg, _ := registry.Load(h.regPath)
	if len(reg.Services) != 0 {
		t.Errorf("expected 0 services after remove, got %d", len(reg.Services))
	}
}

func TestAPIRemove_NotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "remove", Name: "xyz"})
	if !resp.OK {
		t.Fatalf("expected ok=true for idempotent remove, got error: %s", resp.Error)
	}
}

// --- status ---

func TestAPIStatus(t *testing.T) {
	h, _ := newTestHandler(t)
	// PID file does not exist, so running = false.
	resp := sendRequest(t, h, APIRequest{Action: "status"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Running {
		t.Error("expected running=false when no PID file")
	}
	if resp.Count != 0 {
		t.Errorf("expected count=0, got %d", resp.Count)
	}
}

func TestAPIStatus_WithServices(t *testing.T) {
	h, _ := newTestHandler(t)
	_, _ = registry.Add(h.regPath, registry.Service{Name: "svc1", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	_, _ = registry.Add(h.regPath, registry.Service{Name: "svc2", Type: registry.TypeProxy, Target: "http://localhost:4000"})

	resp := sendRequest(t, h, APIRequest{Action: "status"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Count)
	}
}

func TestAPIStatusURLsReturnsVNextPayload(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
		Middleware: &registry.MiddlewareConfig{
			BasicAuth: "user:pass",
		},
	}); err != nil {
		t.Fatalf("registry.Add web: %v", err)
	}
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}); err != nil {
		t.Fatalf("registry.Add db: %v", err)
	}
	withStatusURLSeams(t, true, 4242, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC))

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "status", URLs: true}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw, "alice@example.com", "tag:admin", "user:pass")

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.StatusURLs == nil {
		t.Fatalf("status_urls missing in response: %+v", resp)
	}
	result := *resp.StatusURLs
	if result.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", result.SchemaVersion, inspect.SchemaVersion)
	}
	if !result.DaemonRunning || result.DaemonPID != 4242 || result.ServiceCount != 2 {
		t.Fatalf("status urls = %+v, want running pid 4242 with 2 services", result)
	}
	if result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotMissing {
		t.Fatalf("runtime snapshot = %+v, want missing warning code", result.RuntimeSnapshot)
	}

	web := findStatusService(t, result, "web")
	if web.Endpoint.Kind != inspect.EndpointKindHTTPS || web.Endpoint.Display != "https://web.<tailnet>.ts.net" {
		t.Fatalf("web endpoint = %+v, want typed private HTTPS endpoint", web.Endpoint)
	}
	if web.Allow.Mode != "restricted" || web.Allow.Count != 2 || !web.Allow.Redacted || len(web.Allow.Entries) != 0 {
		t.Fatalf("web allow = %+v, want redacted allow summary", web.Allow)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeHTTPAuthConfigured) {
		t.Fatalf("web warnings = %+v, want http_auth_configured", web.Warnings)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("web warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}

	db := findStatusService(t, result, "db")
	if db.Endpoint.Kind != inspect.EndpointKindTCP || db.Endpoint.Port != 5432 || db.Endpoint.Display != "db.<tailnet>.ts.net:5432" {
		t.Fatalf("db endpoint = %+v, want typed TCP endpoint", db.Endpoint)
	}
	if !hasStatusWarningCode(db.Warnings, inspect.WarningCodeTCPHTTPACLNotApplicable) {
		t.Fatalf("db warnings = %+v, want tcp_http_acl_not_applicable", db.Warnings)
	}
}

func TestAPIDoctorReturnsVNextPayloadReadOnly(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	before, err := os.ReadFile(env.regPath)
	if err != nil {
		t.Fatalf("read registry before doctor: %v", err)
	}
	h := &apiHandler{regPath: env.regPath, pidPath: env.pidPath, runtimeSnapshotPath: env.snapshotPath}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "doctor"}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw, "tskey-api-secret-value")

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Doctor == nil {
		t.Fatalf("doctor missing in response: %+v", resp)
	}
	result := *resp.Doctor
	if result.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", result.SchemaVersion, inspect.SchemaVersion)
	}
	if result.Paths.Registry != env.regPath || result.Paths.RuntimeSnapshot != env.snapshotPath || result.Paths.PID != env.pidPath {
		t.Fatalf("doctor paths = %+v, want test env paths", result.Paths)
	}
	if result.Counts.Services != 0 || result.CredentialMode != doctorCredentialAPIToken || !result.Daemon.Running {
		t.Fatalf("doctor result = %+v, want local read-only status with API token and running daemon", result)
	}
	assertDoctorFinding(t, result, inspect.WarningCodeRuntimeSnapshotMissing)

	after, err := os.ReadFile(env.regPath)
	if err != nil {
		t.Fatalf("read registry after doctor: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("doctor mutated registry:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestAPIDoctorProbeExternalOption(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://192.0.2.10:3000",
	}})
	probeCalls := 0
	doctorProbeTargetFn = func(_ context.Context, address string, _ time.Duration) error {
		probeCalls++
		if address != "192.0.2.10:3000" {
			t.Fatalf("probe address = %q, want 192.0.2.10:3000", address)
		}
		return nil
	}
	h := &apiHandler{regPath: env.regPath, pidPath: env.pidPath, runtimeSnapshotPath: env.snapshotPath}

	resp := sendRequest(t, h, APIRequest{Action: "doctor", ProbeExternal: true})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Doctor == nil {
		t.Fatalf("doctor missing in response: %+v", resp)
	}
	if probeCalls != 1 {
		t.Fatalf("probeCalls = %d, want 1", probeCalls)
	}
	assertDoctorFinding(t, *resp.Doctor, inspect.WarningCodeProxyNonLoopbackTarget)
	assertDoctorNoFinding(t, *resp.Doctor, inspect.WarningCodeTargetProbeSkippedExternal)
}

func TestAPIAccessExplainReturnsRedactedVNextPayload(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "user:pass@localhost:3000?token=abc",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "access_explain", Name: "web"}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw, "alice@example.com", "tag:admin", "user:pass", "token=abc")

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.AccessExplain == nil {
		t.Fatalf("access_explain missing in response: %+v", resp)
	}
	result := *resp.AccessExplain
	if result.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", result.SchemaVersion, inspect.SchemaVersion)
	}
	if result.TSLinkKnown.Allow.Count != 2 || !result.TSLinkKnown.Allow.Redacted || len(result.TSLinkKnown.Allow.Entries) != 0 {
		t.Fatalf("known allow = %+v, want redacted summary", result.TSLinkKnown.Allow)
	}
	if result.TSLinkLocalEnforcement.FailureMode != accessIdentityFailureModeDenyWhenUnresolved {
		t.Fatalf("failure_mode = %q, want %q", result.TSLinkLocalEnforcement.FailureMode, accessIdentityFailureModeDenyWhenUnresolved)
	}
	if result.TSLinkKnown.Backend.Display != "localhost:3000" {
		t.Fatalf("backend display = %q, want schemeless secret redaction", result.TSLinkKnown.Backend.Display)
	}
	classification := result.TSLinkKnown.TargetLoopbackClassification
	if classification.Classification != "loopback_or_local" || classification.Host != "localhost" || classification.Port != "3000" {
		t.Fatalf("target classification = %+v, want redacted loopback localhost:3000", classification)
	}
}

func TestAPIAccessExplainNotFound(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "access_explain", Name: "missing"})
	if resp.OK {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(resp.Error, "service not found: missing") {
		t.Fatalf("error = %q, want stable not-found message", resp.Error)
	}
}

func TestAPITemplateListReturnsBuiltins(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "template_list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.TemplateList == nil {
		t.Fatalf("template_list missing in response: %+v", resp)
	}
	if resp.TemplateList.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", resp.TemplateList.SchemaVersion, inspect.SchemaVersion)
	}
	found := map[string]bool{}
	for _, tmpl := range resp.TemplateList.Templates {
		found[tmpl.Name] = true
	}
	for _, tmpl := range templateSummaries() {
		if !found[tmpl.Name] {
			t.Fatalf("template_list missing %q: %+v", tmpl.Name, resp.TemplateList.Templates)
		}
	}
	if resp.TemplateList.Count != len(templateSummaries()) {
		t.Fatalf("count = %d, want %d", resp.TemplateList.Count, len(templateSummaries()))
	}
}

func TestAPITemplatePlanDryRunDoesNotCreateRegistry(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "template_plan", Name: "personal-harness"}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw)

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.TemplateApply == nil {
		t.Fatalf("template_apply missing in response: %+v", resp)
	}
	if _, err := os.Stat(h.regPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run registry stat err = %v, want not exist", err)
	}
	result := *resp.TemplateApply
	if result.SchemaVersion != inspect.SchemaVersion || !result.DryRun || result.Applied {
		t.Fatalf("template plan = %+v, want vNext dry-run not applied", result)
	}
	if result.Created != 2 || result.Skipped != 0 {
		t.Fatalf("created/skipped = %d/%d, want 2/0", result.Created, result.Skipped)
	}
}

func TestAPITemplateApplyWritesMissingAndPreservesExisting(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "harness-web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:9999",
		Tags:   []string{"tag:custom"},
	}); err != nil {
		t.Fatalf("prepopulate registry: %v", err)
	}

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "template_apply", Name: "personal-harness"}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw)

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.TemplateApply == nil {
		t.Fatalf("template_apply missing in response: %+v", resp)
	}
	result := *resp.TemplateApply
	if result.DryRun || !result.Applied || result.Created != 1 || result.Skipped != 1 {
		t.Fatalf("template apply = %+v, want applied with 1 created and 1 skipped", result)
	}

	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 2 || !hasRegistryService(reg, "harness-api") {
		t.Fatalf("registry services = %+v, want harness-web and harness-api", reg.Services)
	}
	assertHarnessWebCustomized(t, reg)
}

// --- unknown action ---

func TestAPIUnknownAction(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "explode"})
	if resp.OK {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(resp.Error, "unknown action") {
		t.Errorf("unexpected error message: %s", resp.Error)
	}
}

// --- invalid JSON (scanner-level test) ---

func TestAPIInvalidJSON(t *testing.T) {
	h, _ := newTestHandler(t)

	line := `{not valid json`
	var buf bytes.Buffer
	h.handleLine(line, &buf)

	resp := parseResponse(t, &buf)
	if resp.OK {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if !strings.Contains(resp.Error, "invalid JSON") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIRejectsUnknownJSONFieldWithoutCreatingService(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	h.handleLine(`{"action":"add","name":"myapp","type":"proxy","target":"localhost:3000","unknown":true}`, &buf)

	resp := parseResponse(t, &buf)
	if resp.OK {
		t.Fatal("expected ok=false for unknown field")
	}
	if !strings.Contains(resp.Error, `unknown field "unknown"`) {
		t.Fatalf("error = %q, want unknown field", resp.Error)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("services = %+v, want none after rejected request", reg.Services)
	}
}

func TestAPIRejectsUnknownJSONFieldWithD7Fields(t *testing.T) {
	h, _ := newTestHandler(t)

	var buf bytes.Buffer
	h.handleLine(`{"action":"status","urls":true,"probe_external":false,"unknown":true}`, &buf)

	resp := parseResponse(t, &buf)
	if resp.OK {
		t.Fatal("expected ok=false for unknown field")
	}
	if !strings.Contains(resp.Error, `unknown field "unknown"`) {
		t.Fatalf("error = %q, want unknown field", resp.Error)
	}
}

// --- add validation ---

func TestAPIAdd_MissingName(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Type: "proxy", Target: "localhost:3000"})
	if resp.OK {
		t.Fatal("expected error when name is missing")
	}
}

func TestAPIAdd_MissingType(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "myapp"})
	if resp.OK {
		t.Fatal("expected error when type is missing")
	}
	if !strings.Contains(resp.Error, "type is required") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_InvalidName(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "My App!", Type: "proxy", Target: "localhost:3000"})
	if resp.OK {
		t.Fatal("expected error for invalid name")
	}
}

func TestAPIAdd_InvalidType(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "myapp", Type: "websocket", Target: "localhost:3000"})
	if resp.OK {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(resp.Error, "type must be one of") {
		t.Errorf("expected type validation error, got: %s", resp.Error)
	}
}
