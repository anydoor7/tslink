package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
)

func resetRootJSONFlag(t *testing.T) {
	t.Helper()
	if err := rootCmd.PersistentFlags().Set("json", "false"); err != nil {
		t.Fatalf("reset json flag: %v", err)
	}
	rootCmd.SetArgs(nil)
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("json", "false")
		rootCmd.SetArgs(nil)
	})
}

// runAddCmd finds the add command, resets all flags, sets the given flags, and runs it.
func runAddCmd(t *testing.T, args []string, flags map[string]string) error {
	t.Helper()

	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Reset all flags to defaults
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("dir", "")
	addCmd.Flags().Set("tcp", "")
	addCmd.Flags().Set("ephemeral", "false")
	addCmd.Flags().Set("tags", "")
	addCmd.Flags().Set("allow", "")
	addCmd.Flags().Set("funnel", "false")
	addCmd.Flags().Set("public", "false")
	addCmd.Flags().Set("domain", "")
	addCmd.Flags().Set("acme-email", "")
	addCmd.Flags().Set("control-url", "")

	for k, v := range flags {
		addCmd.Flags().Set(k, v)
	}

	var buf bytes.Buffer
	addCmd.SetOut(&buf)

	return addCmd.RunE(addCmd, args)
}

// runAddCmdOutput is like runAddCmd but also returns stdout.
func runAddCmdOutput(t *testing.T, args []string, flags map[string]string) (string, error) {
	t.Helper()

	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Reset all flags to defaults
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("dir", "")
	addCmd.Flags().Set("tcp", "")
	addCmd.Flags().Set("ephemeral", "false")
	addCmd.Flags().Set("tags", "")
	addCmd.Flags().Set("allow", "")
	addCmd.Flags().Set("funnel", "false")
	addCmd.Flags().Set("public", "false")
	addCmd.Flags().Set("domain", "")
	addCmd.Flags().Set("acme-email", "")
	addCmd.Flags().Set("control-url", "")

	for k, v := range flags {
		addCmd.Flags().Set(k, v)
	}

	var buf bytes.Buffer
	addCmd.SetOut(&buf)

	err = addCmd.RunE(addCmd, args)
	return buf.String(), err
}

// --- add command tests ---

func TestAddCmd_NoFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	err := runAddCmd(t, []string{"myapp"}, nil)
	if err == nil {
		t.Fatal("expected error when no mode flag is provided")
	}
	if !strings.Contains(err.Error(), "exactly one of --proxy, --dir, or --tcp must be provided") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_ControlURLPersisted(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	oldRegPath := registryPathFn
	oldEnsureDir := ensureDirFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		ensureDirFn = oldEnsureDir
		_ = addCmd.Flags().Set("control-url", "")
	})
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return nil }

	err = runAddCmd(t, []string{"headscale-app"}, map[string]string{
		"proxy":       "localhost:3000",
		"control-url": "https://headscale.example.com",
	})
	if err != nil {
		t.Fatalf("run add: %v", err)
	}

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	if len(reg.Services) != 1 {
		t.Fatalf("services = %d, want 1", len(reg.Services))
	}
	if reg.Services[0].ControlURL != "https://headscale.example.com" {
		t.Fatalf("control_url = %q, want %q", reg.Services[0].ControlURL, "https://headscale.example.com")
	}
}

func TestAddCmd_MultipleFlags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	shareDir := filepath.Join(dir, "share")
	os.MkdirAll(shareDir, 0o700)

	err := runAddCmd(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000", "dir": shareDir})
	if err == nil {
		t.Fatal("expected error when multiple mode flags provided")
	}
	if !strings.Contains(err.Error(), "exactly one of --proxy, --dir, or --tcp must be provided") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_Proxy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	out, err := runAddCmdOutput(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "myapp") {
		t.Errorf("expected output to contain service name, got: %s", out)
	}
}

func TestAddCmd_Proxy_WithScheme(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	_, err := runAddCmdOutput(t, []string{"secure"}, map[string]string{"proxy": "https://localhost:8443"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	found := false
	for _, svc := range reg.Services {
		if svc.Name == "secure" {
			found = true
			if svc.Target != "https://localhost:8443" {
				t.Errorf("scheme should be preserved, got %s", svc.Target)
			}
		}
	}
	if !found {
		t.Error("secure service not found in registry")
	}
}

func TestAddCmd_Dir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	shareDir := filepath.Join(dir, "share")
	os.MkdirAll(shareDir, 0o700)

	out, err := runAddCmdOutput(t, []string{"docs"}, map[string]string{"dir": shareDir})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "docs") {
		t.Errorf("expected output to contain service name, got: %s", out)
	}
}

func TestAddCmd_Dir_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	filePath := filepath.Join(dir, "notadir.txt")
	os.WriteFile(filePath, []byte("hello"), 0o600)

	err := runAddCmd(t, []string{"bad"}, map[string]string{"dir": filePath})
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_Dir_NonExistent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"bad"}, map[string]string{"dir": filepath.Join(dir, "nonexistent")})
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestAddCmd_TCP(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	out, err := runAddCmdOutput(t, []string{"mydb"}, map[string]string{"tcp": "localhost:5432"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "mydb") {
		t.Errorf("expected output to contain service name, got: %s", out)
	}
	if !strings.Contains(out, "TCP") {
		t.Errorf("expected output to mention TCP, got: %s", out)
	}
}

func TestAddCmd_TCP_BadFormat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"bad"}, map[string]string{"tcp": "noporthere"})
	if err == nil {
		t.Fatal("expected error for bad TCP format")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_TCP_InvalidPort(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"bad"}, map[string]string{"tcp": "localhost:99999"})
	if err == nil {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(err.Error(), "invalid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_InvalidName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"My App!"}, map[string]string{"proxy": "localhost:3000"})
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
}

func TestAddCmd_WithEphemeral(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	_, err := runAddCmdOutput(t, []string{"ephapp"}, map[string]string{"proxy": "localhost:3000", "ephemeral": "true"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	for _, svc := range reg.Services {
		if svc.Name == "ephapp" {
			if !svc.Ephemeral {
				t.Error("expected ephemeral=true")
			}
			return
		}
	}
	t.Error("ephapp not found in registry")
}

func TestAddCmd_WithTags(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	_, err := runAddCmdOutput(t, []string{"tagapp"}, map[string]string{"proxy": "localhost:3000", "tags": "tag:web,tag:prod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	for _, svc := range reg.Services {
		if svc.Name == "tagapp" {
			if len(svc.Tags) != 2 {
				t.Errorf("expected 2 tags, got %d", len(svc.Tags))
			}
			return
		}
	}
	t.Error("tagapp not found in registry")
}

func TestAddCmd_WithAllow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	_, err := runAddCmdOutput(t, []string{"aclapp"}, map[string]string{"proxy": "localhost:3000", "allow": "alice@example.com,tag:admin"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	for _, svc := range reg.Services {
		if svc.Name == "aclapp" {
			if len(svc.AllowedUsers) != 2 {
				t.Errorf("expected 2 allowed users, got %d", len(svc.AllowedUsers))
			}
			return
		}
	}
	t.Error("aclapp not found in registry")
}

func TestAddCmd_WithFunnel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	out, err := runAddCmdOutput(t, []string{"funnelapp"}, map[string]string{"proxy": "localhost:3000", "funnel": "true", "public": "true"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Funnel") {
		t.Errorf("expected output to mention Funnel, got: %s", out)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	for _, svc := range reg.Services {
		if svc.Name == "funnelapp" {
			if !svc.Funnel {
				t.Error("expected funnel=true")
			}
			if !svc.PublicAck {
				t.Error("expected public_ack=true")
			}
			return
		}
	}
	t.Error("funnelapp not found in registry")
}

func TestAddCmd_FunnelWithoutProxy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"bad"}, map[string]string{"tcp": "localhost:5432", "funnel": "true"})
	if err == nil {
		t.Fatal("expected error when using --funnel with --tcp")
	}
	if !strings.Contains(err.Error(), "--funnel can only be used with --proxy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_DomainWithoutProxy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	shareDir := filepath.Join(dir, "share")
	os.MkdirAll(shareDir, 0o700)

	err := runAddCmd(t, []string{"domapp"}, map[string]string{"dir": shareDir, "domain": "app.example.com"})
	if err == nil {
		t.Fatal("expected error when using --domain with --dir")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- list command tests ---

func TestListCmd_Empty(t *testing.T) {
	resetRootJSONFlag(t)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	listCmd, _, _ := rootCmd.Find([]string{"list"})
	var buf bytes.Buffer
	listCmd.SetOut(&buf)

	err := listCmd.RunE(listCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No services registered") {
		t.Errorf("expected 'No services registered', got: %s", buf.String())
	}
}

func TestListCmd_WithServices(t *testing.T) {
	resetRootJSONFlag(t)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)
	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	_, _ = registry.Add(regPath, registry.Service{
		Name:   "svc1",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})

	listCmd, _, _ := rootCmd.Find([]string{"list"})
	var buf bytes.Buffer
	listCmd.SetOut(&buf)

	err := listCmd.RunE(listCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "svc1") {
		t.Errorf("expected output to contain svc1, got: %s", out)
	}
	if !strings.Contains(out, "proxy") {
		t.Errorf("expected output to contain proxy type, got: %s", out)
	}
}

func TestListCmd_FileService(t *testing.T) {
	resetRootJSONFlag(t)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)
	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	shareDir := t.TempDir()
	_, _ = registry.Add(regPath, registry.Service{
		Name: "docs",
		Type: registry.TypeFile,
		Path: shareDir,
	})

	listCmd, _, _ := rootCmd.Find([]string{"list"})
	var buf bytes.Buffer
	listCmd.SetOut(&buf)

	err := listCmd.RunE(listCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "docs") {
		t.Errorf("expected output to contain docs, got: %s", out)
	}
	if !strings.Contains(out, shareDir) {
		t.Errorf("expected output to contain path, got: %s", out)
	}
}

// --- stop command tests ---

func TestStopCmd_NotRunning(t *testing.T) {
	resetRootJSONFlag(t)

	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	stopCmd, _, _ := rootCmd.Find([]string{"stop"})
	var buf bytes.Buffer
	stopCmd.SetOut(&buf)

	err := stopCmd.RunE(stopCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not running") {
		t.Errorf("expected 'not running' message, got: %s", buf.String())
	}
}

// --- hasScheme tests ---

func TestHasScheme(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"http://localhost:3000", true},
		{"https://localhost:8443", true},
		{"localhost:3000", false},
		{"ftp://example.com", true},
		{"", false},
	}

	for _, tt := range tests {
		if got := hasScheme(tt.input); got != tt.want {
			t.Errorf("hasScheme(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// --- API additional tests ---

func TestAPIAdd_Proxy_MissingTarget(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "myapp", Type: "proxy"})
	if resp.OK {
		t.Fatal("expected error when target is missing for proxy type")
	}
	if !strings.Contains(resp.Error, "target is required") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_File_MissingPath(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "docs", Type: "file"})
	if resp.OK {
		t.Fatal("expected error when path is missing for file type")
	}
	if !strings.Contains(resp.Error, "path is required") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_File_NonExistentPath(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "docs", Type: "file", Path: "/nonexistent/path"})
	if resp.OK {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestAPIAdd_TCP_MissingTarget(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "mydb", Type: "tcp"})
	if resp.OK {
		t.Fatal("expected error when target is missing for tcp type")
	}
	if !strings.Contains(resp.Error, "target is required") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_TCP_InvalidPort(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "mydb", Type: "tcp", Target: "localhost:99999"})
	if resp.OK {
		t.Fatal("expected error for invalid port")
	}
	if !strings.Contains(resp.Error, "invalid port") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_TCP_ZeroPort(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "mydb", Type: "tcp", Target: "localhost:0"})
	if resp.OK {
		t.Fatal("expected error for zero port")
	}
	if !strings.Contains(resp.Error, "invalid port") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_TCP_NonNumericPort(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "mydb", Type: "tcp", Target: "localhost:abc"})
	if resp.OK {
		t.Fatal("expected error for non-numeric port")
	}
	if !strings.Contains(resp.Error, "invalid port") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIAdd_UnknownType(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "add", Name: "myapp", Type: "grpc"})
	if resp.OK {
		t.Fatal("expected error for unknown type")
	}
	if !strings.Contains(resp.Error, "type must be one of") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIRemove_MissingName(t *testing.T) {
	h, _ := newTestHandler(t)
	resp := sendRequest(t, h, APIRequest{Action: "remove"})
	if resp.OK {
		t.Fatal("expected error when name is missing")
	}
	if !strings.Contains(resp.Error, "name is required") {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestAPIList_InvalidRegistry(t *testing.T) {
	h, dir := newTestHandler(t)
	// Write invalid JSON to registry
	os.WriteFile(filepath.Join(dir, "registry.json"), []byte("{invalid"), 0o600)
	h.regPath = filepath.Join(dir, "registry.json")

	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if resp.OK {
		t.Fatal("expected error for invalid registry")
	}
}

// --- listServices handler tests ---

func TestListServices_Empty(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	var buf bytes.Buffer
	if err := listServices(regPath, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No services") {
		t.Errorf("expected 'No services', got: %s", buf.String())
	}
}

func TestListServices_WithServices(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	docsDir := t.TempDir()
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	_, _ = registry.Add(regPath, registry.Service{Name: "docs", Type: registry.TypeFile, Path: docsDir})

	var buf bytes.Buffer
	if err := listServices(regPath, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "web") || !strings.Contains(out, "docs") {
		t.Errorf("missing service names in output: %s", out)
	}
	if !strings.Contains(out, docsDir) {
		t.Errorf("file service should show path: %s", out)
	}
}

func TestListServices_InvalidRegistry(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	os.WriteFile(regPath, []byte("{bad"), 0o600)
	var buf bytes.Buffer
	if err := listServices(regPath, &buf); err == nil {
		t.Fatal("expected error for invalid registry")
	}
}

// --- getStatus / formatStatus handler tests ---

func TestGetStatus_NotRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	regPath := filepath.Join(dir, "registry.json")
	r, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatalf("getStatus() error = %v", err)
	}
	if r.DaemonRunning {
		t.Error("expected not running")
	}
	if r.ServiceCount != 0 {
		t.Error("expected 0 services")
	}
}

func TestGetStatus_WithServices(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	r, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatalf("getStatus() error = %v", err)
	}
	if r.ServiceCount != 1 {
		t.Errorf("expected 1 service, got %d", r.ServiceCount)
	}
}

func TestFormatStatus_Running(t *testing.T) {
	var buf bytes.Buffer
	formatStatus(StatusResult{DaemonRunning: true, DaemonPID: 1234, Authenticated: true, ServiceCount: 3}, &buf)
	out := buf.String()
	if !strings.Contains(out, "1234") {
		t.Error("expected PID in output")
	}
	if !strings.Contains(out, "authenticated") {
		t.Error("expected 'authenticated' in output")
	}
	if !strings.Contains(out, "3 registered") {
		t.Error("expected '3 registered' in output")
	}
}

func TestFormatStatus_NotRunning(t *testing.T) {
	var buf bytes.Buffer
	formatStatus(StatusResult{}, &buf)
	out := buf.String()
	if !strings.Contains(out, "not running") {
		t.Error("expected 'not running'")
	}
	if !strings.Contains(out, "not authenticated") {
		t.Error("expected 'not authenticated'")
	}
	if !strings.Contains(out, "0 registered") {
		t.Error("expected '0 registered'")
	}
}

// --- stopService handler tests ---

func TestStopService_NotRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	var buf bytes.Buffer
	if err := stopService(pidPath, false, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not running") {
		t.Errorf("expected 'not running', got: %s", buf.String())
	}
}

func TestStopService_StalePIDFile(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	os.WriteFile(pidPath, []byte("99999999"), 0o600)
	var buf bytes.Buffer
	if err := stopService(pidPath, false, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not running") {
		t.Errorf("expected 'not running', got: %s", buf.String())
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("expected PID file to be removed")
	}
}

// --- removeService handler tests ---

func TestRemoveService_Success(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	old := deleteDevicesFn
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{target.Hostname}}, nil
	}
	defer func() { deleteDevicesFn = old }()

	var out, errOut bytes.Buffer
	if err := removeService(regPath, "web", &out, &errOut, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "removed") {
		t.Errorf("expected 'removed', got: %s", out.String())
	}
}

func TestRemoveService_NotFound(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	var out, errOut bytes.Buffer
	if err := removeService(regPath, "nonexistent", &out, &errOut, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "not registered") {
		t.Fatalf("expected 'not registered' message, got %q", out.String())
	}
}

func TestRemoveService_TailapiWarning(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	old := deleteDevicesFn
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{}, fmt.Errorf("API error")
	}
	defer func() { deleteDevicesFn = old }()

	var out, errOut bytes.Buffer
	if err := removeService(regPath, "web", &out, &errOut, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut.String(), "warning") {
		t.Errorf("expected warning, got: %s", errOut.String())
	}
}

// --- logoutUser handler tests ---

func logoutCredentialStatus(apiKey, clientSecret bool) credentials.StoredCredentialStatus {
	status := credentials.StoredCredentialStatus{}
	if apiKey {
		status.APIKey.File = credentials.CredentialLocationStatus{Enabled: true, Present: true}
	}
	if clientSecret {
		status.ClientSecret.File = credentials.CredentialLocationStatus{Enabled: true, Present: true}
	}
	return status
}

func TestLogoutUser_NotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return credentials.StoredCredentialStatus{}, nil
	}
	deleteStoredCredentialsFn = func() error {
		t.Fatal("deleteStoredCredentialsFn must not run for a proved empty logout")
		return nil
	}
	defer func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	}()

	var buf bytes.Buffer
	err := logoutUser(
		filepath.Join(dir, "pid"),
		filepath.Join(dir, "authkey"),
		filepath.Join(dir, "nodes"),
		dir,
		false,
		&buf,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Not logged in") {
		t.Errorf("expected 'Not logged in', got: %s", buf.String())
	}
}

func TestLogoutUser_DaemonRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600)

	var buf bytes.Buffer
	err := logoutUser(pidPath, filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, false, &buf)
	if err == nil {
		t.Fatal("expected error when daemon is running")
	}
	if !strings.Contains(err.Error(), "currently running") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLogoutUser_CleansUp(t *testing.T) {
	dir := t.TempDir()
	authKeyPath := filepath.Join(dir, "authkey")
	nodesDir := filepath.Join(dir, "nodes")
	os.WriteFile(authKeyPath, []byte("key"), 0o600)
	os.MkdirAll(nodesDir, 0o700)

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	apiKey := "tskey-api-xxx"
	clientSecret := ""
	deleted := false
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(apiKey != "", clientSecret != ""), nil
	}
	deleteStoredCredentialsFn = func() error {
		deleted = true
		apiKey = ""
		clientSecret = ""
		return nil
	}
	defer func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	}()

	var buf bytes.Buffer
	err := logoutUser(filepath.Join(dir, "pid"), authKeyPath, nodesDir, dir, false, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Logged out") {
		t.Errorf("expected 'Logged out', got: %s", buf.String())
	}
	if !deleted {
		t.Error("expected deleteAPIKeyFn to be called")
	}
	if _, err := os.Stat(authKeyPath); !os.IsNotExist(err) {
		t.Error("authkey should be removed")
	}
	if _, err := os.Stat(nodesDir); !os.IsNotExist(err) {
		t.Error("nodes dir should be removed")
	}
}

func TestLogoutUser_WithClientSecret(t *testing.T) {
	dir := t.TempDir()

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	clientSecret := "tskey-client-secret"
	csDeleted := false
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(false, clientSecret != ""), nil
	}
	deleteStoredCredentialsFn = func() error {
		csDeleted = true
		clientSecret = ""
		return nil
	}
	defer func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	}()

	var buf bytes.Buffer
	err := logoutUser(filepath.Join(dir, "pid"), filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, false, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Logged out") {
		t.Errorf("expected 'Logged out', got: %s", buf.String())
	}
	if !csDeleted {
		t.Error("expected deleteClientSecretFn to be called")
	}
}

func TestLogoutUserFailsClosedOnCredentialReadFailure(t *testing.T) {
	dir := t.TempDir()
	authKeyPath := filepath.Join(dir, "authkey")
	if err := os.WriteFile(authKeyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	})

	deleteCalled := false
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return credentials.StoredCredentialStatus{}, fmt.Errorf("keychain unavailable")
	}
	deleteStoredCredentialsFn = func() error {
		deleteCalled = true
		return nil
	}

	var buf bytes.Buffer
	err := logoutUser(filepath.Join(dir, "pid"), authKeyPath, filepath.Join(dir, "nodes"), dir, false, &buf)
	if err == nil {
		t.Fatal("logoutUser() error = nil, want read failure")
	}
	if strings.Contains(buf.String(), "Logged out") {
		t.Fatalf("logout printed success after read failure: %q", buf.String())
	}
	if !deleteCalled {
		t.Fatal("deleteStoredCredentialsFn was not called after inspect failure")
	}
	if _, statErr := os.Stat(authKeyPath); !os.IsNotExist(statErr) {
		t.Fatalf("legacy auth key was not cleaned after inspect failure, stat err = %v", statErr)
	}
}

func TestLogoutUserFailsClosedOnCredentialDeleteFailure(t *testing.T) {
	dir := t.TempDir()
	authKeyPath := filepath.Join(dir, "authkey")
	if err := os.WriteFile(authKeyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	})

	apiKey := "tskey-api-xxx"
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(apiKey != "", false), nil
	}
	deleteStoredCredentialsFn = func() error { return fmt.Errorf("delete denied") }

	var buf bytes.Buffer
	err := logoutUser(filepath.Join(dir, "pid"), authKeyPath, filepath.Join(dir, "nodes"), dir, false, &buf)
	if err == nil {
		t.Fatal("logoutUser() error = nil, want delete failure")
	}
	if !strings.Contains(err.Error(), "delete credential stores") {
		t.Fatalf("logoutUser() error = %v, want credential-store delete context", err)
	}
	if strings.Contains(buf.String(), "Logged out") {
		t.Fatalf("logout printed success after delete failure: %q", buf.String())
	}
}

func TestLogoutUserFailsClosedWhenCredentialReadbackStillPresent(t *testing.T) {
	dir := t.TempDir()
	authKeyPath := filepath.Join(dir, "authkey")
	if err := os.WriteFile(authKeyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	oldInspect := inspectStoredCredentialsFn
	oldDelete := deleteStoredCredentialsFn
	t.Cleanup(func() {
		inspectStoredCredentialsFn = oldInspect
		deleteStoredCredentialsFn = oldDelete
	})

	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		return logoutCredentialStatus(true, false), nil
	}
	deleteStoredCredentialsFn = func() error { return nil }

	var buf bytes.Buffer
	err := logoutUser(filepath.Join(dir, "pid"), authKeyPath, filepath.Join(dir, "nodes"), dir, false, &buf)
	if err == nil {
		t.Fatal("logoutUser() error = nil, want readback failure")
	}
	if !strings.Contains(err.Error(), "still present") {
		t.Fatalf("logoutUser() error = %v, want readback context", err)
	}
	if strings.Contains(buf.String(), "Logged out") {
		t.Fatalf("logout printed success after readback failure: %q", buf.String())
	}
}

// --- buildService handler tests ---

func TestBuildService_Proxy(t *testing.T) {
	svc, err := buildService(AddParams{Name: "web", Proxy: "localhost:3000"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Type != registry.TypeProxy {
		t.Error("expected proxy type")
	}
	if svc.Target != "http://localhost:3000" {
		t.Errorf("expected http:// prefix, got %s", svc.Target)
	}
}

func TestBuildService_ProxyWithScheme(t *testing.T) {
	svc, err := buildService(AddParams{Name: "web", Proxy: "https://localhost:8443"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Target != "https://localhost:8443" {
		t.Errorf("scheme not preserved: %s", svc.Target)
	}
}

func TestBuildService_TCP(t *testing.T) {
	svc, err := buildService(AddParams{Name: "db", TCP: "localhost:5432"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Type != registry.TypeTCP {
		t.Error("expected tcp type")
	}
	if svc.Port != 5432 {
		t.Errorf("expected port 5432, got %d", svc.Port)
	}
}

func TestBuildService_Dir(t *testing.T) {
	svc, err := buildService(AddParams{Name: "docs", Dir: "/tmp/docs"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Type != registry.TypeFile {
		t.Error("expected file type")
	}
}

func TestBuildService_NoMode(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_MultipleMode(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", Proxy: "x", Dir: "y"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_FunnelWithoutProxy(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", TCP: "localhost:5432", Funnel: true})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_DomainWithoutProxy(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", Dir: "/tmp", Domain: "x.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_AcmeEmailWithoutDomain(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", Proxy: "localhost:3000", AcmeEmail: "user@example.com"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildService_AcmeEmailWithDomain(t *testing.T) {
	_, err := buildService(AddParams{
		Name: "acme-app", Proxy: "localhost:3000",
		Domain: "app.example.com", AcmeEmail: "admin@example.com",
	})
	if err == nil {
		t.Fatal("expected custom-domain/ACME unavailable error")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_AcmeEmailWithoutDomain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"acmeapp"}, map[string]string{
		"proxy":      "localhost:3000",
		"acme-email": "user@example.com",
	})
	if err == nil {
		t.Fatal("expected error when --acme-email used without --domain")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddCmd_AcmeEmailWithDomain(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	_, err := runAddCmdOutput(t, []string{"acmeapp"}, map[string]string{
		"proxy":      "localhost:3000",
		"domain":     "app.example.com",
		"acme-email": "admin@example.com",
	})
	if err == nil {
		t.Fatal("expected custom-domain/ACME unavailable error")
	}
	if !strings.Contains(err.Error(), "custom-domain/ACME runtime is not wired") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildService_InvalidName(t *testing.T) {
	_, err := buildService(AddParams{Name: "My App!", Proxy: "localhost:3000"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_WithAllOptions(t *testing.T) {
	svc, err := buildService(AddParams{
		Name: "full", Proxy: "localhost:3000",
		Ephemeral: true, Tags: "tag:web,tag:prod",
		Allow: "alice@example.com,bob@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !svc.Ephemeral {
		t.Error("expected ephemeral")
	}
	if len(svc.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(svc.Tags))
	}
	if len(svc.AllowedUsers) != 2 {
		t.Errorf("expected 2 allowed users, got %d", len(svc.AllowedUsers))
	}
	if svc.Funnel {
		t.Error("expected funnel=false when allow list is configured")
	}
}

func TestBuildService_TCP_BadFormat(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", TCP: "noporthere"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildService_TCP_InvalidPort(t *testing.T) {
	_, err := buildService(AddParams{Name: "bad", TCP: "localhost:99999"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- stopService: success and error paths ---

func TestStopService_Success(t *testing.T) {
	oldIsRunning, oldStop, oldRemove := isRunningFn, stopDaemonFn, removePIDFn
	defer func() { isRunningFn, stopDaemonFn, removePIDFn = oldIsRunning, oldStop, oldRemove }()

	isRunningFn = func(string) bool { return true }
	stopDaemonFn = func(string) error { return nil }
	removePIDFn = func(string) {}

	var buf bytes.Buffer
	if err := stopService("/fake/pid", false, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "tslink stopped") {
		t.Errorf("expected 'tslink stopped', got: %s", buf.String())
	}
}

func TestStopService_Error(t *testing.T) {
	oldIsRunning, oldStop, oldRemove := isRunningFn, stopDaemonFn, removePIDFn
	defer func() { isRunningFn, stopDaemonFn, removePIDFn = oldIsRunning, oldStop, oldRemove }()

	isRunningFn = func(string) bool { return true }
	stopDaemonFn = func(string) error { return fmt.Errorf("kill failed") }
	removePIDFn = func(string) {}

	var buf bytes.Buffer
	err := stopService("/fake/pid", false, &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "kill failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- getStatus: full coverage ---

func TestGetStatus_Running_Authenticated(t *testing.T) {
	oldIsRunning, oldReadPID, oldGetKey := isRunningFn, readPIDFn, getAPIKeyFn
	oldCS := hasClientSecretFn
	defer func() {
		isRunningFn, readPIDFn, getAPIKeyFn = oldIsRunning, oldReadPID, oldGetKey
		hasClientSecretFn = oldCS
	}()

	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return 42, nil }
	getAPIKeyFn = func() (string, error) { return "tskey-api-xxx", nil }
	hasClientSecretFn = func() bool { return false }

	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	r, err := getStatus(filepath.Join(dir, "pid"), regPath)
	if err != nil {
		t.Fatalf("getStatus() error = %v", err)
	}
	if !r.DaemonRunning {
		t.Error("expected running")
	}
	if r.DaemonPID != 42 {
		t.Errorf("expected PID 42, got %d", r.DaemonPID)
	}
	if !r.Authenticated {
		t.Error("expected authenticated")
	}
	if r.ServiceCount != 1 {
		t.Errorf("expected 1 service, got %d", r.ServiceCount)
	}
}

func TestGetStatus_AuthenticatedViaClientSecret(t *testing.T) {
	oldIsRunning, oldGetKey := isRunningFn, getAPIKeyFn
	oldCS := hasClientSecretFn
	defer func() { isRunningFn = oldIsRunning; getAPIKeyFn = oldGetKey; hasClientSecretFn = oldCS }()

	isRunningFn = func(string) bool { return false }
	getAPIKeyFn = func() (string, error) { return "", nil }
	hasClientSecretFn = func() bool { return true }

	dir := t.TempDir()
	r, err := getStatus(filepath.Join(dir, "pid"), filepath.Join(dir, "registry.json"))
	if err != nil {
		t.Fatalf("getStatus() error = %v", err)
	}
	if !r.Authenticated {
		t.Error("expected authenticated via client secret")
	}
}

// --- remove command Cobra-level test ---

// --- config command tests ---

func TestConfigSet_ValidURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	err := configSet("control-url", "https://headscale.example.com", &buf, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "control-url = https://headscale.example.com") {
		t.Errorf("expected confirmation, got: %s", buf.String())
	}
}

func TestConfigSet_ClearValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	// Set a value first
	if err := configSet("control-url", "https://headscale.example.com", &buf, false); err != nil {
		t.Fatalf("setup: %v", err)
	}

	buf.Reset()
	// Clear it
	if err := configSet("control-url", "", &buf, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "control-url cleared") {
		t.Errorf("expected 'cleared', got: %s", buf.String())
	}
}

func TestConfigSet_InvalidURL(t *testing.T) {
	for _, value := range []string{"/control", "https://", "ftp://example.com", "not-a-url"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			var buf bytes.Buffer
			err := configSet("control-url", value, &buf, false)
			if err == nil {
				t.Fatal("expected error for invalid URL")
			}
			if !strings.Contains(err.Error(), "invalid URL") {
				t.Errorf("expected 'invalid URL' error, got: %v", err)
			}
		})
	}
}

func TestConfigSet_UnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	err := configSet("nonexistent", "value", &buf, false)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected 'unknown config key' error, got: %v", err)
	}
}

func TestConfigGet_WithValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := configSet("control-url", "https://headscale.example.com", &buf, false); err != nil {
		t.Fatalf("setup: %v", err)
	}

	buf.Reset()
	if err := configGet("control-url", &buf, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "https://headscale.example.com") {
		t.Errorf("expected URL, got: %s", buf.String())
	}
}

func TestConfigGet_NotSet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := configGet("control-url", &buf, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "not set") {
		t.Errorf("expected 'not set', got: %s", buf.String())
	}
}

func TestConfigGet_UnknownKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	err := configGet("nonexistent", &buf, false)
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("expected 'unknown config key' error, got: %v", err)
	}
}

func TestConfigList_WithValue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := configSet("control-url", "https://headscale.example.com", &buf, false); err != nil {
		t.Fatalf("setup: %v", err)
	}

	buf.Reset()
	if err := configList(&buf, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "control-url = https://headscale.example.com") {
		t.Errorf("expected URL in list, got: %s", buf.String())
	}
}

func TestConfigList_Empty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var buf bytes.Buffer
	if err := configList(&buf, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "(not set)") {
		t.Errorf("expected '(not set)', got: %s", buf.String())
	}
}

func TestConfigCmd_SetThenGet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Use Cobra commands for integration test
	setCmd, _, _ := rootCmd.Find([]string{"config", "set"})
	var buf bytes.Buffer
	setCmd.SetOut(&buf)

	err := setCmd.RunE(setCmd, []string{"control-url", "https://hs.example.com"})
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	getCmd, _, _ := rootCmd.Find([]string{"config", "get"})
	buf.Reset()
	getCmd.SetOut(&buf)

	err = getCmd.RunE(getCmd, []string{"control-url"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(buf.String(), "https://hs.example.com") {
		t.Errorf("round-trip failed, got: %s", buf.String())
	}
}

func TestConfigCmd_SetClearWithOneArg(t *testing.T) {
	resetRootJSONFlag(t)

	t.Setenv("HOME", t.TempDir())

	// set with only 1 arg (key) should clear the value
	setCmd, _, _ := rootCmd.Find([]string{"config", "set"})
	var buf bytes.Buffer
	setCmd.SetOut(&buf)

	err := setCmd.RunE(setCmd, []string{"control-url"})
	if err != nil {
		t.Fatalf("set (clear): %v", err)
	}
	if !strings.Contains(buf.String(), "cleared") {
		t.Errorf("expected 'cleared', got: %s", buf.String())
	}
}

func TestConfigCmd_ListAlias(t *testing.T) {
	resetRootJSONFlag(t)

	t.Setenv("HOME", t.TempDir())

	// "ls" should resolve to list command
	listCmd, _, err := rootCmd.Find([]string{"config", "ls"})
	if err != nil {
		t.Fatalf("find config ls: %v", err)
	}

	var buf bytes.Buffer
	listCmd.SetOut(&buf)

	if err := listCmd.RunE(listCmd, []string{}); err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(buf.String(), "control-url") {
		t.Errorf("expected control-url in output, got: %s", buf.String())
	}
}

func TestConfigSet_LoadError(t *testing.T) {
	// HOME points to a file, not a directory — Dir() will fail
	tmp := t.TempDir()
	fakePath := filepath.Join(tmp, "not-a-dir")
	os.WriteFile(fakePath, []byte("x"), 0o600)
	t.Setenv("HOME", fakePath)

	var buf bytes.Buffer
	err := configSet("control-url", "https://example.com", &buf, false)
	if err == nil {
		t.Fatal("expected error when HOME is invalid")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("expected 'load config' error, got: %v", err)
	}
}

func TestConfigSet_SaveError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Create config dir (Load will succeed with IsNotExist),
	// then make it read-only so WriteFile fails.
	cfgDir := filepath.Join(tmp, ".config", "tslink")
	os.MkdirAll(cfgDir, 0o700)
	os.Chmod(cfgDir, 0o500)
	defer os.Chmod(cfgDir, 0o700)

	var buf bytes.Buffer
	err := configSet("control-url", "https://example.com", &buf, false)
	if err == nil {
		// On some systems (root, or macOS w/ SIP) chmod may not prevent writes.
		// Skip the test rather than fail.
		t.Skip("chmod did not prevent writes; skipping save-error test")
	}
	if !strings.Contains(err.Error(), "save config") {
		t.Errorf("expected 'save config' error, got: %v", err)
	}
}

func TestConfigGet_LoadError(t *testing.T) {
	tmp := t.TempDir()
	fakePath := filepath.Join(tmp, "not-a-dir")
	os.WriteFile(fakePath, []byte("x"), 0o600)
	t.Setenv("HOME", fakePath)

	var buf bytes.Buffer
	err := configGet("control-url", &buf, false)
	if err == nil {
		t.Fatal("expected error when HOME is invalid")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("expected 'load config' error, got: %v", err)
	}
}

func TestConfigList_LoadError(t *testing.T) {
	tmp := t.TempDir()
	fakePath := filepath.Join(tmp, "not-a-dir")
	os.WriteFile(fakePath, []byte("x"), 0o600)
	t.Setenv("HOME", fakePath)

	var buf bytes.Buffer
	err := configList(&buf, false)
	if err == nil {
		t.Fatal("expected error when HOME is invalid")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("expected 'load config' error, got: %v", err)
	}
}

// --- remove command Cobra-level test ---

func TestRemoveCmd_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".config", "tslink")
	os.MkdirAll(cfgDir, 0o700)
	regPath := filepath.Join(cfgDir, "registry.json")
	_, _ = registry.Add(regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	old := deleteDevicesFn
	deleteDevicesFn = func(ctx context.Context, target tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		return tailapi.CleanupResult{Deleted: []string{target.Hostname}}, nil
	}
	defer func() { deleteDevicesFn = old }()

	removeCmd, _, _ := rootCmd.Find([]string{"remove"})
	var buf bytes.Buffer
	removeCmd.SetOut(&buf)

	err := removeCmd.RunE(removeCmd, []string{"web"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "removed") {
		t.Errorf("expected 'removed', got: %s", buf.String())
	}
}

func TestExecute(t *testing.T) {
	// Execute wraps rootCmd.Execute(). Passing --help ensures it runs without
	// side effects and exercises the function.
	origArgs := os.Args
	t.Cleanup(func() { os.Args = origArgs })

	os.Args = []string{"tslink", "--help"}
	if err := Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestBuildService_InvalidDomain(t *testing.T) {
	_, err := buildService(AddParams{
		Name:   "web",
		Proxy:  "localhost:3000",
		Domain: "not a valid domain",
	})
	if err == nil {
		t.Fatal("buildService() error = nil, want domain validation error")
	}
	if !strings.Contains(err.Error(), "domain") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAddCmd_DefaultTag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	svc := reg.Services[0]
	if len(svc.Tags) != 1 || svc.Tags[0] != "tag:tsmain" {
		t.Fatalf("expected default tag [tag:tsmain], got: %v", svc.Tags)
	}
}

func TestAddCmd_ExplicitTagOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	os.MkdirAll(filepath.Join(dir, ".config", "tslink"), 0o700)

	err := runAddCmd(t, []string{"myapp"}, map[string]string{
		"proxy": "localhost:3000",
		"tags":  "tag:custom",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regPath := filepath.Join(dir, ".config", "tslink", "registry.json")
	reg, _ := registry.Load(regPath)
	svc := reg.Services[0]
	if len(svc.Tags) != 1 || svc.Tags[0] != "tag:custom" {
		t.Fatalf("expected [tag:custom], got: %v", svc.Tags)
	}
}
