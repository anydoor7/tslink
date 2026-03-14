package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// helpers

func newTestHandler(t *testing.T) (*Handler, string, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	return New(regPath, pidPath), regPath, pidPath
}

func doRequest(t *testing.T, h *Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func doRequestRaw(t *testing.T, h *Handler, method, path, rawBody string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(rawBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func decodeResponse(t *testing.T, w *httptest.ResponseRecorder) APIResponse {
	t.Helper()
	var resp APIResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func decodeStatusData(t *testing.T, resp APIResponse) statusData {
	t.Helper()
	raw, _ := json.Marshal(resp.Data)
	var sd statusData
	if err := json.Unmarshal(raw, &sd); err != nil {
		t.Fatalf("decode status data: %v", err)
	}
	return sd
}

func decodeServiceFromData(t *testing.T, data interface{}) registry.Service {
	t.Helper()
	raw, _ := json.Marshal(data)
	var svc registry.Service
	if err := json.Unmarshal(raw, &svc); err != nil {
		t.Fatalf("decode service: %v", err)
	}
	return svc
}

// --- StartAdminNode ---

func TestStartAdminNode(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	h := StartAdminNode(regPath, pidPath)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

// --- List Services ---

func TestListServices_Empty(t *testing.T) {
	h, _, _ := newTestHandler(t)
	w := doRequest(t, h, http.MethodGet, "/api/services", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true, got %+v", resp)
	}
	b, _ := json.Marshal(resp.Data)
	if string(b) == "null" {
		t.Fatal("expected empty array, got null")
	}
}

func TestListServices_WithServices(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	svc := registry.Service{Name: "myapp", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	if err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("setup: %v", err)
	}

	w := doRequest(t, h, http.MethodGet, "/api/services", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true")
	}

	raw, _ := json.Marshal(resp.Data)
	var services []registry.Service
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].Name != "myapp" {
		t.Errorf("expected name myapp, got %s", services[0].Name)
	}
}

func TestListServices_CorruptRegistry(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	// Write invalid JSON to the registry file
	if err := os.WriteFile(regPath, []byte("{not json!"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := doRequest(t, h, http.MethodGet, "/api/services", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for corrupt registry")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error")
	}
}

// --- Add Service: Proxy ---

func TestAddService_Proxy(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "webapp",
		"type":   "proxy",
		"target": "localhost:8080",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true: %+v", resp)
	}
	if resp.Message != "service added" {
		t.Fatalf("expected message 'service added', got %q", resp.Message)
	}

	reg, _ := registry.Load(regPath)
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service in registry, got %d", len(reg.Services))
	}
	if !strings.HasPrefix(reg.Services[0].Target, "http://") {
		t.Errorf("expected http:// prefix, got %s", reg.Services[0].Target)
	}
}

func TestAddService_ProxyWithHTTPScheme(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "httpapp",
		"type":   "proxy",
		"target": "http://localhost:9000",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	svc := decodeServiceFromData(t, resp.Data)
	if svc.Target != "http://localhost:9000" {
		t.Fatalf("expected target 'http://localhost:9000', got %q", svc.Target)
	}
}

func TestAddService_ProxyWithHTTPSScheme(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "httpsapp",
		"type":   "proxy",
		"target": "https://example.com",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	svc := decodeServiceFromData(t, resp.Data)
	if svc.Target != "https://example.com" {
		t.Fatalf("expected target 'https://example.com', got %q", svc.Target)
	}
}

func TestAddService_ProxyEmptyTarget(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "nohost",
		"type":   "proxy",
		"target": "",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "target is required") {
		t.Fatalf("expected 'target is required' error, got %q", resp.Error)
	}
}

// --- Add Service: File ---

func TestAddService_File(t *testing.T) {
	h, regPath, _ := newTestHandler(t)
	dir := t.TempDir()

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "docs",
		"type":   "file",
		"target": dir,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	reg, _ := registry.Load(regPath)
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Path == "" {
		t.Error("expected Path to be set for file service")
	}
}

func TestAddService_FileEmptyTarget(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "nodir",
		"type":   "file",
		"target": "",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "target (directory path) is required") {
		t.Fatalf("expected directory path required error, got %q", resp.Error)
	}
}

func TestAddService_FileNonExistentPath(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "ghost",
		"type":   "file",
		"target": "/tmp/tslink-nonexistent-dir-xyz-12345",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "directory does not exist") {
		t.Fatalf("expected 'directory does not exist' error, got %q", resp.Error)
	}
}

func TestAddService_FileTargetIsFile(t *testing.T) {
	h, _, _ := newTestHandler(t)
	tmpFile := filepath.Join(t.TempDir(), "afile.txt")
	os.WriteFile(tmpFile, []byte("hello"), 0o600)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "notadir",
		"type":   "file",
		"target": tmpFile,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "directory does not exist") {
		t.Fatalf("expected 'directory does not exist' error, got %q", resp.Error)
	}
}

// --- Add Service: TCP ---

func TestAddService_TCP(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "db",
		"type":   "tcp",
		"target": "127.0.0.1:5432",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	reg, _ := registry.Load(regPath)
	if len(reg.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(reg.Services))
	}
	if reg.Services[0].Port != 5432 {
		t.Errorf("expected port 5432, got %d", reg.Services[0].Port)
	}
	if reg.Services[0].Target != "127.0.0.1:5432" {
		t.Errorf("expected target '127.0.0.1:5432', got %q", reg.Services[0].Target)
	}
}

func TestAddService_TCPEmptyTarget(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "notcp",
		"type":   "tcp",
		"target": "",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "target (host:port) is required") {
		t.Fatalf("expected tcp target required error, got %q", resp.Error)
	}
}

func TestAddService_TCPMissingPort(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "nocolon",
		"type":   "tcp",
		"target": "localhost",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "host:port") {
		t.Fatalf("expected 'host:port' error, got %q", resp.Error)
	}
}

func TestAddService_TCPInvalidPort(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "badport",
		"type":   "tcp",
		"target": "localhost:abc",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "invalid port") {
		t.Fatalf("expected 'invalid port' error, got %q", resp.Error)
	}
}

func TestAddService_TCPPortOutOfRange(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "bigport",
		"type":   "tcp",
		"target": "localhost:99999",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "invalid port") {
		t.Fatalf("expected 'invalid port' error, got %q", resp.Error)
	}
}

func TestAddService_TCPPortZero(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "zeroport",
		"type":   "tcp",
		"target": "localhost:0",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

// --- Add Service: Validation errors ---

func TestAddService_InvalidJSON(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequestRaw(t, h, http.MethodPost, "/api/services", "{not valid json")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false")
	}
	if !strings.Contains(resp.Error, "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' error, got %q", resp.Error)
	}
}

func TestAddService_EmptyName(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "",
		"type":   "proxy",
		"target": "localhost:3000",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false")
	}
}

func TestAddService_InvalidName(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "INVALID NAME!",
		"type":   "proxy",
		"target": "localhost:3000",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for invalid name")
	}
	if !strings.Contains(resp.Error, "invalid service name") {
		t.Fatalf("expected 'invalid service name' error, got %q", resp.Error)
	}
}

func TestAddService_UnknownType(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "bad",
		"type":   "grpc",
		"target": "localhost:9090",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "unknown service type") {
		t.Fatalf("expected 'unknown service type' error, got %q", resp.Error)
	}
}

func TestAddService_MissingType(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "myapp",
		"target": "localhost:3000",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for missing type")
	}
}

// --- Remove Service ---

func TestRemoveService(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	svc := registry.Service{Name: "gone", Type: registry.TypeProxy, Target: "http://localhost:9000"}
	if err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("setup: %v", err)
	}

	w := doRequest(t, h, http.MethodDelete, "/api/services/gone", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true: %+v", resp)
	}
	if resp.Message != "service removed" {
		t.Fatalf("expected message 'service removed', got %q", resp.Message)
	}

	reg, _ := registry.Load(regPath)
	if len(reg.Services) != 0 {
		t.Fatalf("expected 0 services after delete, got %d", len(reg.Services))
	}
}

func TestRemoveService_NotFound(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodDelete, "/api/services/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for not-found service")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Fatalf("expected 'not found' in error, got %q", resp.Error)
	}
}

// --- Status ---

func TestStatus_NotRunning(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := doRequest(t, h, http.MethodGet, "/api/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if !resp.OK {
		t.Fatalf("expected ok=true")
	}

	sd := decodeStatusData(t, resp)
	if sd.Running {
		t.Error("expected running=false when no pid file exists")
	}
	if sd.ServiceCount != 0 {
		t.Errorf("expected service_count=0, got %d", sd.ServiceCount)
	}
	if sd.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
}

func TestStatus_Running(t *testing.T) {
	h, _, pidPath := newTestHandler(t)

	if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	w := doRequest(t, h, http.MethodGet, "/api/status", nil)
	resp := decodeResponse(t, w)
	sd := decodeStatusData(t, resp)
	if !sd.Running {
		t.Error("expected running=true when pid file exists")
	}
}

func TestStatus_WithServices(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	for _, name := range []string{"svc1", "svc2"} {
		_ = registry.Add(regPath, registry.Service{
			Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000",
		})
	}

	w := doRequest(t, h, http.MethodGet, "/api/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	sd := decodeStatusData(t, resp)
	if sd.ServiceCount != 2 {
		t.Errorf("expected service_count=2, got %d", sd.ServiceCount)
	}
}

func TestStatus_EmptyPIDPath(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	h := New(regPath, "")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	sd := decodeStatusData(t, resp)
	if sd.Running {
		t.Error("expected running=false with empty PID path")
	}
}

func TestStatus_CorruptRegistry(t *testing.T) {
	h, regPath, _ := newTestHandler(t)

	if err := os.WriteFile(regPath, []byte("{bad json!"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := doRequest(t, h, http.MethodGet, "/api/status", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for corrupt registry")
	}
}

// --- Dashboard ---

func TestDashboard(t *testing.T) {
	h, _, _ := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content-type, got %s", ct)
	}
	body := w.Body.String()
	if len(body) == 0 {
		t.Fatal("expected non-empty HTML body")
	}
}

func TestDashboard_NotFoundForNonRoot(t *testing.T) {
	h, _, _ := newTestHandler(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Add/Remove edge cases for coverage ---

func TestAddService_RegistryWriteError(t *testing.T) {
	dir := t.TempDir()
	// Point registry to a path inside a non-existent, unwritable directory
	regPath := filepath.Join(dir, "readonly", "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	h := New(regPath, pidPath)

	// First, make the parent dir read-only so registry.Add fails on save
	readonlyDir := filepath.Join(dir, "readonly")
	os.MkdirAll(readonlyDir, 0o700)
	// Create the registry file so Load works, but make dir unwritable so save fails
	os.WriteFile(regPath, []byte(`{"services":[]}`), 0o600)
	os.Chmod(readonlyDir, 0o500)
	t.Cleanup(func() { os.Chmod(readonlyDir, 0o700) })

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "failsvc",
		"type":   "proxy",
		"target": "localhost:3000",
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for write error")
	}
}

func TestRemoveService_RegistryError(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	h := New(regPath, pidPath)

	// Write corrupt JSON so registry.Remove's Load fails
	os.WriteFile(regPath, []byte("{corrupt json!"), 0o600)

	w := doRequest(t, h, http.MethodDelete, "/api/services/anything", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for corrupt registry")
	}
	if resp.Error == "" {
		t.Fatal("expected non-empty error")
	}
}

// --- Content-Type checks ---

// TestRemoveService_EmptyName tests the empty name guard in handleRemoveService.
// The mux pattern {name} never produces an empty value, so this branch is
// unreachable via normal routing. We call the handler method directly to cover it.
func TestRemoveService_EmptyName(t *testing.T) {
	h, _, _ := newTestHandler(t)

	// Build a request with no path value for "name" — call handler directly.
	req := httptest.NewRequest(http.MethodDelete, "/api/services/", nil)
	w := httptest.NewRecorder()
	h.handleRemoveService(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if resp.OK {
		t.Fatal("expected ok=false for empty name")
	}
	if !strings.Contains(resp.Error, "service name is required") {
		t.Fatalf("expected 'service name is required' error, got %q", resp.Error)
	}
}

// TestAddService_FileAbsError covers the filepath.Abs error branch by
// injecting a failing absFunc. On macOS, filepath.Abs never returns an
// error (the kernel keeps deleted cwds accessible), so the only reliable
// way to exercise this branch cross-platform is via injection.
func TestAddService_FileAbsError(t *testing.T) {
	h, _, _ := newTestHandler(t)

	// Inject a failing absFunc and restore after test.
	origAbs := absFunc
	absFunc = func(path string) (string, error) {
		return "", fmt.Errorf("simulated getwd failure")
	}
	t.Cleanup(func() { absFunc = origAbs })

	w := doRequest(t, h, http.MethodPost, "/api/services", map[string]string{
		"name":   "absfail",
		"type":   "file",
		"target": "relative-path",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	resp := decodeResponse(t, w)
	if !strings.Contains(resp.Error, "cannot resolve path") {
		t.Fatalf("expected 'cannot resolve path' error, got %q", resp.Error)
	}
}

func TestAPIEndpoints_ReturnJSON(t *testing.T) {
	h, _, _ := newTestHandler(t)

	endpoints := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/services"},
		{http.MethodGet, "/api/status"},
	}

	for _, ep := range endpoints {
		w := doRequest(t, h, ep.method, ep.path, nil)
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("%s %s: expected application/json, got %q", ep.method, ep.path, ct)
		}
	}
}
