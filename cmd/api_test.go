package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

// newTestHandler creates an apiHandler backed by temp files.
func newTestHandler(t *testing.T) (*apiHandler, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	return &apiHandler{regPath: regPath, pidPath: pidPath}, dir
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
	if err := registry.Add(h.regPath, registry.Service{
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

// --- remove ---

func TestAPIRemove(t *testing.T) {
	h, _ := newTestHandler(t)

	// Add then remove.
	if err := registry.Add(h.regPath, registry.Service{
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
	if resp.OK {
		t.Fatal("expected error when removing non-existent service")
	}
	if !strings.Contains(resp.Error, "service not found") {
		t.Errorf("unexpected error: %s", resp.Error)
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
	_ = registry.Add(h.regPath, registry.Service{Name: "svc1", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	_ = registry.Add(h.regPath, registry.Service{Name: "svc2", Type: registry.TypeProxy, Target: "http://localhost:4000"})

	resp := sendRequest(t, h, APIRequest{Action: "status"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Count != 2 {
		t.Errorf("expected count=2, got %d", resp.Count)
	}
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

	// Simulate what the cobra RunE does with a bad line.
	line := `{not valid json`
	var buf bytes.Buffer
	var req APIRequest
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		writeResponse(&buf, APIResponse{OK: false, Error: "invalid JSON: " + err.Error()})
	} else {
		h.handle(req, &buf)
	}

	resp := parseResponse(t, &buf)
	if resp.OK {
		t.Fatal("expected ok=false for invalid JSON")
	}
	if !strings.Contains(resp.Error, "invalid JSON") {
		t.Errorf("unexpected error: %s", resp.Error)
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
