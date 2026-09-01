package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// newTestHandler creates an apiHandler backed by temp files.
func newTestHandler(t *testing.T) (*apiHandler, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	authHandoffPath := filepath.Join(dir, "auth-handoff.json")
	return &apiHandler{regPath: regPath, pidPath: pidPath, runtimeSnapshotPath: snapshotPath, authHandoffPath: authHandoffPath}, dir
}

func testStringPointer(value string) *string {
	return &value
}

type apiTestResponse struct {
	OK            bool
	SchemaVersion int
	Code          int
	Error         string
	ErrorCode     string
	Next          []string
	ValidActions  []string

	Message         string
	URL             string
	URLPending      bool
	Created         bool
	FunnelExpiresAt *time.Time
	FunnelRearmed   bool
	Endpoint        *inspect.EndpointView
	Exposure        *inspect.ExposureView
	Warnings        []inspect.WarningView
	Services        []ListServiceSummary
	Running         bool
	Count           int
	Removed         bool

	StatusURLs    *StatusURLsResult
	Doctor        *DoctorResult
	AccessExplain *AccessExplainResult
	TemplateList  *TemplateListResult
	TemplatePlan  *TemplateApplyResult
	TemplateApply *TemplateApplyResult
}

// parseResponse decodes the first JSON line written to buf.
func parseResponse(t *testing.T, buf *bytes.Buffer) apiTestResponse {
	t.Helper()
	var envelope output.Result
	if err := json.NewDecoder(buf).Decode(&envelope); err != nil {
		t.Fatalf("decode response: %v (raw: %q)", err, buf.String())
	}

	resp := apiTestResponse{
		OK:            envelope.OK,
		SchemaVersion: envelope.SchemaVersion,
		Code:          envelope.Code,
	}
	if envelope.Error != nil {
		resp.Error = envelope.Error.Message
		resp.ErrorCode = envelope.Error.Code
		resp.Next = append([]string(nil), envelope.Error.Next...)
		if envelope.Error.Data != nil {
			dataBytes, err := json.Marshal(envelope.Error.Data)
			if err != nil {
				t.Fatalf("marshal error data: %v", err)
			}
			var data apiUnknownActionErrorData
			if err := json.Unmarshal(dataBytes, &data); err != nil {
				t.Fatalf("decode error data: %v", err)
			}
			resp.ValidActions = data.ValidActions
		}
	}
	if envelope.Data == nil {
		return resp
	}

	dataBytes, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	switch envelope.Command {
	case apiActionList:
		var result struct {
			Services []ListServiceSummary `json:"services"`
			Count    int                  `json:"count"`
		}
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode list response data: %v (raw: %s)", err, dataBytes)
		}
		resp.Services, resp.Count = result.Services, result.Count
	case apiActionAdd:
		var result AddResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode add response data: %v (raw: %s)", err, dataBytes)
		}
		if result.URL != nil {
			resp.URL = *result.URL
		}
		resp.URLPending = result.URLPending
		resp.Created = result.Created
		resp.FunnelExpiresAt = result.FunnelExpiresAt
		resp.FunnelRearmed = result.FunnelRearmed
		resp.Endpoint = &result.Endpoint
		resp.Exposure = &result.Exposure
		resp.Warnings = result.Warnings
	case apiActionRemove:
		var result RemoveResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode remove response data: %v", err)
		}
		resp.Removed = result.Removed
	case apiActionStatus:
		var keys map[string]json.RawMessage
		_ = json.Unmarshal(dataBytes, &keys)
		if _, ok := keys["runtime_snapshot"]; ok {
			var result StatusURLsResult
			if err := json.Unmarshal(dataBytes, &result); err != nil {
				t.Fatalf("decode status urls response data: %v", err)
			}
			resp.StatusURLs = &result
			resp.Running, resp.Count = result.DaemonRunning, result.ServiceCount
		} else {
			var result StatusResult
			if err := json.Unmarshal(dataBytes, &result); err != nil {
				t.Fatalf("decode status response data: %v", err)
			}
			resp.Running, resp.Count = result.DaemonRunning, result.ServiceCount
		}
	case apiActionDoctor:
		var result DoctorResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode doctor response data: %v", err)
		}
		resp.Doctor = &result
	case apiActionAccessExplain:
		var result AccessExplainResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode access response data: %v", err)
		}
		resp.AccessExplain = &result
	case apiActionTemplateList:
		var result TemplateListResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode template list response data: %v", err)
		}
		resp.TemplateList = &result
	case apiActionTemplatePlan:
		var result TemplateApplyResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode template plan response data: %v", err)
		}
		resp.TemplatePlan = &result
	case apiActionTemplateApply:
		var result TemplateApplyResult
		if err := json.Unmarshal(dataBytes, &result); err != nil {
			t.Fatalf("decode template apply response data: %v", err)
		}
		resp.TemplateApply = &result
	}
	return resp
}

// sendRequest sends a single APIRequest to the handler and returns the response.
func sendRequest(t *testing.T, h *apiHandler, req APIRequest) apiTestResponse {
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
	if resp.Services[0].Type != registry.TypeProxy || resp.Services[0].URL != nil || !resp.Services[0].URLPending {
		t.Errorf("service = %+v, want slim pending proxy", resp.Services[0])
	}
}

func TestAPIListRejectsMiddlewareConfig(t *testing.T) {
	h, _ := newTestHandler(t)
	rawRegistry := `{"schema_version":1,"services":[{"name":"myapp","type":"proxy","target":"http://localhost:3000","middleware":{"basic_auth":"user:pass"}}]}`
	if err := os.WriteFile(h.regPath, []byte(rawRegistry), 0o600); err != nil {
		t.Fatalf("WriteFile registry: %v", err)
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
	if resp.OK {
		t.Fatalf("expected feature_unavailable error, got ok response: %s", raw)
	}
	if resp.ErrorCode != registry.CodeFeatureUnavailable {
		t.Fatalf("error code = %q, want %s; raw=%s", resp.ErrorCode, registry.CodeFeatureUnavailable, raw)
	}
}

func TestAPIList_RedactsBackendURLSecrets(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://user:pass@localhost:3000/private?token=abc#frag-secret",
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

	var buf bytes.Buffer
	h.handle(APIRequest{Action: "list"}, &buf)
	raw := buf.String()
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw, "user:pass", "token=abc", "frag-secret")

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 2 {
		t.Fatalf("services = %d, want 2", len(resp.Services))
	}
	byName := map[string]ListServiceSummary{}
	for _, svc := range resp.Services {
		byName[svc.Name] = svc
	}
	if !byName["web"].URLPending || !byName["db"].URLPending || byName["web"].URL != nil || byName["db"].URL != nil {
		t.Fatalf("services = %+v, want slim pending URLs with no backend data", byName)
	}
}

func TestAPIList_UsesPublicServiceViewsWithUsefulFields(t *testing.T) {
	h, _ := newTestHandler(t)
	docsDir := t.TempDir()
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "docs",
		Type:         registry.TypeFile,
		Path:         docsDir,
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
	if got.Name != "docs" || got.Type != registry.TypeFile || got.URL != nil || !got.URLPending || got.State != "pending" {
		t.Fatalf("service = %+v, want token-efficient pending file summary", got)
	}
}

func TestAPIList_RedactsAllowPrincipals(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "docs",
		Type:         registry.TypeFile,
		Path:         t.TempDir(),
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
	if resp.Services[0].URL != nil || !resp.Services[0].URLPending {
		t.Fatalf("service = %+v, want slim response without allow principals", resp.Services[0])
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
	service := resp.Services[0]
	if service.Type != registry.TypeTCP || service.URL != nil || !service.URLPending {
		t.Fatalf("service = %+v, want pending typed TCP summary", service)
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
	if !resp.Created || resp.URL != "" || !resp.URLPending {
		t.Fatalf("add response = %+v, want created with null/pending URL", resp)
	}
	if resp.Endpoint == nil || resp.Endpoint.Kind != inspect.EndpointKindHTTPS || resp.Endpoint.Display != "" {
		t.Fatalf("endpoint = %+v, want pending https endpoint without placeholder", resp.Endpoint)
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

func TestAPIAddIfMissingPreservesExistingServiceAndReportsCreated(t *testing.T) {
	h, _ := newTestHandler(t)
	first := sendRequest(t, h, APIRequest{Action: apiActionAdd, Name: "app", Type: registry.TypeProxy, Target: "localhost:3000"})
	if !first.OK || !first.Created {
		t.Fatalf("first add = %+v, want created=true", first)
	}
	second := sendRequest(t, h, APIRequest{Action: apiActionAdd, Name: "app", Type: registry.TypeProxy, Target: "localhost:9999", IfMissing: true})
	if !second.OK || second.Created {
		t.Fatalf("second add = %+v, want created=false", second)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	if len(reg.Services) != 1 || reg.Services[0].Target != "http://localhost:3000" {
		t.Fatalf("services = %+v, want existing target preserved", reg.Services)
	}
}

func TestAPIAddReturnsInvalidAllowWarning(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: apiActionAdd, Name: "app", Type: registry.TypeProxy, Target: "localhost:3000", Allow: []string{"not-an-email"}})
	if !resp.OK || len(resp.Warnings) != 1 || resp.Warnings[0].Code != "invalid_allow_entry" {
		t.Fatalf("response warnings = %+v (ok=%v)", resp.Warnings, resp.OK)
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
	if resp.ErrorCode != registry.CodeFunnelPublicAckRequired {
		t.Fatalf("error code = %q, want %s", resp.ErrorCode, registry.CodeFunnelPublicAckRequired)
	}
	if resp.Error != registry.ErrFunnelPublicAck {
		t.Fatalf("error = %q, want public_ack guidance", resp.Error)
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
	if resp.ErrorCode != registry.CodeFunnelAllowConflict {
		t.Fatalf("error code = %q, want %s", resp.ErrorCode, registry.CodeFunnelAllowConflict)
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
	if !svc.PublicAck {
		t.Fatal("expected public_ack=true")
	}
	if len(svc.AllowedUsers) != 0 || svc.ControlURL != "" {
		t.Fatalf("service = %+v, want no allow/control_url", svc)
	}
}

func TestAPIAdd_FunnelTTLPreservesLegacyNeverUnlessExplicit(t *testing.T) {
	h, _ := newTestHandler(t)
	legacy := `{"schema_version":1,"services":[{"name":"legacy-public","type":"proxy","target":"http://localhost:3000","funnel":true,"public_ack":true,"created_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(h.regPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	resp := sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "legacy-public", Type: registry.TypeProxy,
		Target: "localhost:3000", Funnel: true, PublicAck: true,
	})
	if !resp.OK {
		t.Fatalf("legacy API upsert failed: %s", resp.Error)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Services[0].FunnelExpiresAt != nil {
		t.Fatalf("legacy funnel expiry = %v, want preserved never", reg.Services[0].FunnelExpiresAt)
	}

	resp = sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "legacy-public", Type: registry.TypeProxy,
		Target: "localhost:3000", Funnel: true, FunnelTTL: testStringPointer("7d"), PublicAck: true,
	})
	if !resp.OK {
		t.Fatalf("explicit 7d API upsert failed: %s", resp.Error)
	}
	reg, err = registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Services[0].FunnelExpiresAt; got == nil || time.Until(*got) < 6*24*time.Hour || time.Until(*got) > 8*24*time.Hour {
		t.Fatalf("explicit 7d funnel expiry = %v", got)
	}
}

func TestAPIAddRearmsExpiredFunnelAndReturnsPersistedDeadline(t *testing.T) {
	h, _ := newTestHandler(t)
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: false, PublicAck: true, FunnelExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	resp := sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "public-app", Type: registry.TypeProxy,
		Target: "localhost:3000", Funnel: true, PublicAck: true,
	})
	if !resp.OK || !resp.FunnelRearmed || resp.FunnelExpiresAt == nil {
		t.Fatalf("response = %+v, want successful expired Funnel re-arm", resp)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	stored := reg.Services[0].FunnelExpiresAt
	if stored == nil || !stored.Equal(*resp.FunnelExpiresAt) {
		t.Fatalf("response expiry=%v stored expiry=%v, want exact equality", resp.FunnelExpiresAt, stored)
	}
	if stored.Before(before.Add(23*time.Hour)) || stored.After(before.Add(25*time.Hour)) || !reg.Services[0].Funnel {
		t.Fatalf("stored service = %+v, want active default-24h Funnel", reg.Services[0])
	}
}

func TestAPIAddIfMissingExpiredFunnelDoesNotReportPublicExposure(t *testing.T) {
	h, _ := newTestHandler(t)
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: true, PublicAck: true, FunnelExpiresAt: &past,
	}); err != nil {
		t.Fatal(err)
	}
	resp := sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "public-app", Type: registry.TypeProxy,
		Target: "localhost:4000", Funnel: true, PublicAck: true, IfMissing: true,
	})
	if !resp.OK || resp.Created || resp.Exposure == nil {
		t.Fatalf("response = %+v, want successful if_missing hit", resp)
	}
	if resp.Exposure.Kind != inspect.ExposureTailnet || resp.Exposure.Public {
		t.Fatalf("expired if_missing exposure = %+v, want effective tailnet-only exposure", resp.Exposure)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 1 || !reg.Services[0].Funnel || reg.Services[0].FunnelExpiresAt == nil || !reg.Services[0].FunnelExpiresAt.Equal(past) {
		t.Fatalf("persisted service = %+v, want unchanged expired Funnel record", reg.Services)
	}
}

func TestAPIAddResultFunnelExpiryMatchesDiskAcrossWriteBranches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		seed      *time.Time
		ifMissing bool
		ttl       *string
	}{
		{name: "default upsert preserves future", seed: func() *time.Time { value := time.Now().UTC().Add(72 * time.Hour); return &value }()},
		{name: "if missing preserves existing", seed: func() *time.Time { value := time.Now().UTC().Add(48 * time.Hour); return &value }(), ifMissing: true},
		{name: "if missing creates", ifMissing: true},
		{name: "explicit ttl upsert", seed: func() *time.Time { value := time.Now().UTC().Add(72 * time.Hour); return &value }(), ttl: testStringPointer("1h")},
		{name: "explicit never", seed: func() *time.Time { value := time.Now().UTC().Add(72 * time.Hour); return &value }(), ttl: testStringPointer("never")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHandler(t)
			if tc.seed != nil {
				if _, err := registry.Add(h.regPath, registry.Service{
					Name: "public-app", Type: registry.TypeProxy, Target: "http://localhost:3000",
					Funnel: true, PublicAck: true, FunnelExpiresAt: tc.seed,
				}); err != nil {
					t.Fatal(err)
				}
			}
			resp := sendRequest(t, h, APIRequest{
				Action: apiActionAdd, Name: "public-app", Type: registry.TypeProxy,
				Target: "localhost:4000", Funnel: true, PublicAck: true,
				IfMissing: tc.ifMissing, FunnelTTL: tc.ttl,
			})
			if !resp.OK {
				t.Fatalf("response = %+v", resp)
			}
			reg, err := registry.Load(h.regPath)
			if err != nil {
				t.Fatal(err)
			}
			stored := reg.Services[0].FunnelExpiresAt
			switch {
			case stored == nil && resp.FunnelExpiresAt == nil:
			case stored == nil || resp.FunnelExpiresAt == nil || !stored.Equal(*resp.FunnelExpiresAt):
				t.Fatalf("response expiry=%v stored expiry=%v, want exact equality", resp.FunnelExpiresAt, stored)
			}
		})
	}
}

func TestAPIAddRejectsExplicitEmptyFunnelTTL(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "public-app", Type: registry.TypeProxy,
		Target: "localhost:3000", Funnel: true, FunnelTTL: testStringPointer(""), PublicAck: true,
	})
	if resp.OK || !strings.Contains(resp.Error, "funnel TTL must be one of") {
		t.Fatalf("response = %+v, want explicit empty TTL rejection", resp)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Services) != 0 {
		t.Fatalf("services = %+v, want no write after rejected empty TTL", reg.Services)
	}
}

func TestAPIAdd_FunnelTTLNeverAndStrictRejection(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: apiActionAdd, Name: "permanent-public", Type: registry.TypeProxy,
		Target: "localhost:3000", Funnel: true, FunnelTTL: testStringPointer("never"), PublicAck: true,
	})
	if !resp.OK {
		t.Fatalf("never API add failed: %s", resp.Error)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Services[0].FunnelExpiresAt != nil {
		t.Fatalf("never funnel expiry = %v, want nil", reg.Services[0].FunnelExpiresAt)
	}

	for _, ttl := range []string{"168h", "1w", "NEVER"} {
		resp = sendRequest(t, h, APIRequest{
			Action: apiActionAdd, Name: "rejected-public", Type: registry.TypeProxy,
			Target: "localhost:4000", Funnel: true, FunnelTTL: testStringPointer(ttl), PublicAck: true,
		})
		if resp.OK || !strings.Contains(resp.Error, "funnel TTL must be one of") {
			t.Fatalf("funnel_ttl %q response = %+v, want strict rejection", ttl, resp)
		}
	}
}

func TestAPIAdd_FunnelPersistsNoAutoProvision(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add", Name: "public-opt-out", Type: "proxy", Target: "localhost:3000",
		Funnel: true, PublicAck: true, NoAutoProvision: true,
	})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		t.Fatalf("registry.Load() error = %v", err)
	}
	if len(reg.Services) != 1 || !reg.Services[0].NoAutoProvision {
		t.Fatalf("registry services = %+v, want persisted no_auto_provision=true", reg.Services)
	}
	if got := strings.Join(reg.Services[0].Tags, ","); got != "tag:tsmain" {
		t.Fatalf("persisted Funnel tags = %q, want only user/default tags", got)
	}
}

func TestAPIAdd_RejectsNoAutoProvisionWithoutFunnel(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{
		Action: "add", Name: "private", Type: "proxy", Target: "localhost:3000", NoAutoProvision: true,
	})
	if resp.OK || !strings.Contains(resp.Error, "no_auto_provision is supported only when funnel is true") {
		t.Fatalf("response = %+v, want Funnel-only field error", resp)
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
	if resp.URL != "" || !resp.URLPending {
		t.Fatalf("url = %q pending=%v, want null/pending", resp.URL, resp.URLPending)
	}
	if resp.Endpoint == nil || resp.Endpoint.Kind != inspect.EndpointKindTCP || resp.Endpoint.Display != "" || resp.Endpoint.Port != 5432 {
		t.Fatalf("endpoint = %+v, want pending typed TCP endpoint", resp.Endpoint)
	}
	if !hasStatusWarningCode(resp.Warnings, inspect.WarningCodeTCPHTTPACLNotApplicable) {
		t.Fatalf("warnings = %+v, want tcp_http_acl_not_applicable", resp.Warnings)
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
	if !resp.Removed {
		t.Errorf("removed = false, want true")
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
	if resp.Removed {
		t.Fatalf("removed = true, want false for idempotent not-found remove")
	}
}

// --- status ---

func TestAPIStatus(t *testing.T) {
	h, _ := newTestHandler(t)
	// PID file does not exist, so running = false.
	var buf bytes.Buffer
	h.handle(APIRequest{Action: "status"}, &buf)
	raw := buf.String()
	if !strings.Contains(raw, `"daemon_running":false`) {
		t.Fatalf("status response omitted daemon_running:false: %s", raw)
	}
	if !strings.Contains(raw, `"service_count":0`) {
		t.Fatalf("status response omitted service_count:0: %s", raw)
	}

	resp := parseResponse(t, &buf)
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

func TestAPIStatusDataKeepsFalseAndZero(t *testing.T) {
	h, _ := newTestHandler(t)
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
		Target:       "http://user:pass@localhost:3000/private?token=abc#frag-secret",
		AllowedUsers: []string{"alice@example.com", "tag:admin"},
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
	assertAPIRawJSONHasNoPrivateRegistryFields(t, raw, "alice@example.com", "tag:admin", "user:pass", "token=abc", "frag-secret")

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
	if web.Endpoint.Kind != inspect.EndpointKindHTTPS || web.Endpoint.Display != "" || web.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("web endpoint = %+v, want missing endpoint without placeholder", web.Endpoint)
	}
	if web.Backend.Display != "http://localhost:3000/private" {
		t.Fatalf("web backend = %q, want sanitized URL with diagnostic path", web.Backend.Display)
	}
	if web.Allow.Mode != "restricted" || web.Allow.Count != 2 || !web.Allow.Redacted || len(web.Allow.Entries) != 0 {
		t.Fatalf("web allow = %+v, want redacted allow summary", web.Allow)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("web warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}

	db := findStatusService(t, result, "db")
	if db.Endpoint.Kind != inspect.EndpointKindTCP || db.Endpoint.Port != 5432 || db.Endpoint.Display != "" || db.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("db endpoint = %+v, want pending typed TCP endpoint without placeholder", db.Endpoint)
	}
	if db.Backend.Display != "localhost:5432" {
		t.Fatalf("db backend = %q, want sanitized schemeless TCP target", db.Backend.Display)
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
	h := &apiHandler{regPath: env.regPath, pidPath: env.pidPath, runtimeSnapshotPath: env.snapshotPath, authHandoffPath: env.authHandoff}

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
	if result.HealthStatus != doctorStatusWarning || result.HealthExitCode != output.ExitWarning {
		t.Fatalf("doctor health = %q/%d, want warning/%d", result.HealthStatus, result.HealthExitCode, output.ExitWarning)
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
	h := &apiHandler{regPath: env.regPath, pidPath: env.pidPath, runtimeSnapshotPath: env.snapshotPath, authHandoffPath: env.authHandoff}

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

func TestAPIDoctorUsesHandlerPaths(t *testing.T) {
	resetDoctorSeams(t)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "handler-registry.json")
	pidPath := filepath.Join(dir, "handler.pid")
	snapshotPath := filepath.Join(dir, "handler-runtime.json")
	authHandoffPath := filepath.Join(dir, "handler-auth-handoff.json")
	writeDoctorRegistry(t, regPath, []registry.Service{{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}})

	doctorConfigDirFn = func() (string, error) { return dir, nil }
	doctorRegistryPathFn = func() (string, error) {
		t.Fatalf("doctorRegistryPathFn should not be used when API handler has a registry path")
		return "", nil
	}
	doctorRuntimeSnapshotPathFn = func() (string, error) {
		t.Fatalf("doctorRuntimeSnapshotPathFn should not be used when API handler has a runtime snapshot path")
		return "", nil
	}
	doctorAuthHandoffPathFn = func() (string, error) {
		t.Fatalf("doctorAuthHandoffPathFn should not be used when API handler has an auth handoff path")
		return "", nil
	}
	doctorPIDPathFn = func() (string, error) {
		t.Fatalf("doctorPIDPathFn should not be used when API handler has a PID path")
		return "", nil
	}
	doctorAuthKeyPathFn = func() (string, error) { return filepath.Join(dir, "authkey"), nil }
	doctorLoadGlobalConfigFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	isRunningFn = func(path string) bool {
		if path != pidPath {
			t.Fatalf("isRunningFn path = %q, want handler PID path %q", path, pidPath)
		}
		return false
	}

	h := &apiHandler{regPath: regPath, pidPath: pidPath, runtimeSnapshotPath: snapshotPath, authHandoffPath: authHandoffPath}
	resp := sendRequest(t, h, APIRequest{Action: "doctor"})
	if !resp.OK {
		t.Fatalf("expected request processed, got error: %s", resp.Error)
	}
	if resp.Doctor == nil {
		t.Fatalf("doctor missing in response: %+v", resp)
	}
	result := *resp.Doctor
	if result.Paths.Registry != regPath || result.Paths.PID != pidPath || result.Paths.RuntimeSnapshot != snapshotPath || result.Paths.AuthHandoff != authHandoffPath {
		t.Fatalf("doctor paths = %+v, want handler paths", result.Paths)
	}
	if result.Counts.Services != 1 {
		t.Fatalf("services = %d, want handler registry service count", result.Counts.Services)
	}
}

func TestAPIDoctorTopLevelOKMeansRequestProcessed(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       "localhost:5432",
		Port:         5432,
		AllowedUsers: []string{"alice@example.com"},
	}})
	h := &apiHandler{regPath: env.regPath, pidPath: env.pidPath, runtimeSnapshotPath: env.snapshotPath, authHandoffPath: env.authHandoff}

	resp := sendRequest(t, h, APIRequest{Action: "doctor"})
	if !resp.OK {
		t.Fatalf("top-level ok = false, want true because doctor request was processed: %s", resp.Error)
	}
	if resp.Doctor == nil {
		t.Fatalf("doctor missing in response: %+v", resp)
	}
	if resp.Doctor.Status != doctorStatusError {
		t.Fatalf("doctor status = %q, want nested health error", resp.Doctor.Status)
	}
	if resp.Doctor.HealthStatus != doctorStatusError || resp.Doctor.HealthExitCode != output.ExitCritical {
		t.Fatalf("doctor health = %q/%d, want error/%d", resp.Doctor.HealthStatus, resp.Doctor.HealthExitCode, output.ExitCritical)
	}
	assertDoctorFinding(t, *resp.Doctor, inspect.WarningCodeTCPAllowedUsersInvalid)
}

func TestAPIAccessExplainReturnsRedactedVNextPayload(t *testing.T) {
	h, _ := newTestHandler(t)
	if _, err := registry.Add(h.regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://user:pass@localhost:3000?token=abc",
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
	if result.TSLinkKnown.Backend.Display != "http://localhost:3000" {
		t.Fatalf("backend display = %q, want URL secret redaction", result.TSLinkKnown.Backend.Display)
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
	if !strings.Contains(raw, `"dry_run":true`) || strings.Contains(raw, `"template_plan":`) {
		t.Fatalf("template_plan response must be flat dry-run data: %s", raw)
	}

	resp := parseResponse(t, &buf)
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.TemplatePlan == nil {
		t.Fatalf("template_plan missing in response: %+v", resp)
	}
	if resp.TemplateApply != nil {
		t.Fatalf("template_apply = %+v, want nil for template_plan action", resp.TemplateApply)
	}
	if _, err := os.Stat(h.regPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run registry stat err = %v, want not exist", err)
	}
	result := *resp.TemplatePlan
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
	if resp.Code != output.ExitUsage || resp.ErrorCode != "usage_error" {
		t.Fatalf("unknown action code = %d/%q, want stable usage_error/%d", resp.Code, resp.ErrorCode, output.ExitUsage)
	}
	if !slices.Equal(resp.ValidActions, apiActionNames()) {
		t.Fatalf("valid_actions = %v, want %v", resp.ValidActions, apiActionNames())
	}
	if len(resp.ValidActions) != 15 || !containsString(resp.ValidActions, apiActionInviteResend) || !containsString(resp.ValidActions, apiActionManifest) {
		t.Fatalf("valid_actions = %v, want the existing actions plus all five invite actions", resp.ValidActions)
	}
}

func TestAPIManifestActionReturnsGeneratedManifest(t *testing.T) {
	h, _ := newTestHandler(t)
	var buf bytes.Buffer
	result := h.handle(APIRequest{Action: apiActionManifest}, &buf)
	if !result.OK || result.Command != apiActionManifest || result.Code != output.ExitSuccess {
		t.Fatalf("manifest result = %+v, want successful manifest action", result)
	}

	var envelope struct {
		Data CLIManifest `json:"data"`
	}
	if err := json.NewDecoder(&buf).Decode(&envelope); err != nil {
		t.Fatalf("decode manifest response: %v", err)
	}
	if !slices.Equal(envelope.Data.APIActions, apiActionNames()) || !containsString(envelope.Data.APIActions, apiActionManifest) {
		t.Fatalf("manifest api_actions = %v, want generated list %v", envelope.Data.APIActions, apiActionNames())
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
	if !strings.Contains(resp.Error, "exactly one of --proxy") {
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
