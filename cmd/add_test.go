package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

func TestAddFunnel_WithProxy_Persisted(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	// Directly add a service with funnel=true to registry
	svc := registry.Service{
		Name:   "funnel-test",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Funnel: true,
	}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	// Verify funnel field is persisted via API handler
	h := &apiHandler{regPath: regPath, pidPath: dir + "/tslink.pid"}
	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if len(resp.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(resp.Services))
	}
	if !resp.Services[0].Funnel {
		t.Error("expected funnel=true in listed service")
	}
}

func TestAddFunnel_WithProxy_NotSet(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	// Add service without funnel
	svc := registry.Service{
		Name:   "no-funnel",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	h := &apiHandler{regPath: regPath, pidPath: dir + "/tslink.pid"}
	resp := sendRequest(t, h, APIRequest{Action: "list"})
	if !resp.OK {
		t.Fatalf("expected ok, got error: %s", resp.Error)
	}
	if resp.Services[0].Funnel {
		t.Error("expected funnel=false when not set")
	}
}

func TestAddFunnel_WithDir_Error(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := dir + "/.config/tslink"
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	shareDir := dir + "/share"
	if err := os.MkdirAll(shareDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Find the add command from rootCmd
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Set flags directly
	addCmd.Flags().Set("dir", shareDir)
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("tcp", "")
	addCmd.Flags().Set("funnel", "true")
	defer func() {
		addCmd.Flags().Set("dir", "")
		addCmd.Flags().Set("funnel", "false")
		addCmd.Flags().Set("public", "false")
		addCmd.Flags().Set("control-url", "")
	}()

	err = addCmd.RunE(addCmd, []string{"docs"})
	if err == nil {
		t.Fatal("expected error when using --funnel with --dir")
	}
	if !strings.Contains(err.Error(), "--funnel can only be used with --proxy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAddFunnel_WithTCP_Error(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	configDir := dir + "/.config/tslink"
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Find the add command from rootCmd
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}

	// Set flags directly
	addCmd.Flags().Set("tcp", "localhost:5432")
	addCmd.Flags().Set("proxy", "")
	addCmd.Flags().Set("dir", "")
	addCmd.Flags().Set("funnel", "true")
	defer func() {
		addCmd.Flags().Set("tcp", "")
		addCmd.Flags().Set("funnel", "false")
		addCmd.Flags().Set("public", "false")
		addCmd.Flags().Set("control-url", "")
	}()

	err = addCmd.RunE(addCmd, []string{"mydb"})
	if err == nil {
		t.Fatal("expected error when using --funnel with --tcp")
	}
	if !strings.Contains(err.Error(), "--funnel can only be used with --proxy") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildService_TCPRejectsAllow(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "mydb",
		TCP:   "localhost:5432",
		Allow: "alice@example.com",
	})
	if err == nil {
		t.Fatal("expected error when using --allow with --tcp")
	}
	if !strings.Contains(err.Error(), "--allow is not supported for --tcp") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_FunnelRejectsMissingPublicAck(t *testing.T) {
	_, err := buildService(AddParams{
		Name:   "app",
		Proxy:  "localhost:3000",
		Funnel: true,
	})
	if err == nil {
		t.Fatal("expected missing public acknowledgement error")
	}
	if !strings.Contains(err.Error(), publicAckRequiredError) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_FunnelAcceptsPublicAckWithoutAllow(t *testing.T) {
	svc, err := buildService(AddParams{
		Name:   "app",
		Proxy:  "localhost:3000",
		Funnel: true,
		Public: true,
	})
	if err != nil {
		t.Fatalf("buildService: %v", err)
	}
	if !svc.Funnel {
		t.Fatal("expected funnel=true")
	}
	if len(svc.AllowedUsers) != 0 {
		t.Fatalf("allowed_users = %v, want none", svc.AllowedUsers)
	}
}

func TestBuildService_FunnelRejectsAllowEvenWithPublicAck(t *testing.T) {
	_, err := buildService(AddParams{
		Name:   "app",
		Proxy:  "localhost:3000",
		Allow:  "alice@example.com",
		Funnel: true,
		Public: true,
	})
	if err == nil {
		t.Fatal("expected funnel allowed_users error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelAllowedUsers) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_RejectsPublicAckWithoutFunnel(t *testing.T) {
	_, err := buildService(AddParams{
		Name:   "app",
		Proxy:  "localhost:3000",
		Public: true,
	})
	if err == nil {
		t.Fatal("expected public without funnel error")
	}
	if !strings.Contains(err.Error(), "--public can only be used with --funnel") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_FunnelRejectsControlURL(t *testing.T) {
	_, err := buildService(AddParams{
		Name:       "app",
		Proxy:      "localhost:3000",
		Funnel:     true,
		Public:     true,
		ControlURL: "https://headscale.example.com",
	})
	if err == nil {
		t.Fatal("expected funnel control_url error")
	}
	if !strings.Contains(err.Error(), registry.ErrFunnelControlURL) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_ControlURLPersisted(t *testing.T) {
	svc, err := buildService(AddParams{
		Name:       "app",
		Proxy:      "localhost:3000",
		ControlURL: "https://headscale.example.com",
	})
	if err != nil {
		t.Fatalf("buildService: %v", err)
	}
	if svc.ControlURL != "https://headscale.example.com" {
		t.Fatalf("control_url = %q, want %q", svc.ControlURL, "https://headscale.example.com")
	}
}

func TestBuildService_RejectsInvalidControlURL(t *testing.T) {
	_, err := buildService(AddParams{
		Name:       "app",
		Proxy:      "localhost:3000",
		ControlURL: "not-a-url",
	})
	if err == nil {
		t.Fatal("expected invalid control-url error")
	}
	if !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildService_RejectsInvalidTags(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "app",
		Proxy: "localhost:3000",
		Tags:  "tag:",
	})
	if err == nil {
		t.Fatal("expected invalid tag error")
	}
}

func TestBuildService_RejectsInvalidAllowTag(t *testing.T) {
	_, err := buildService(AddParams{
		Name:  "app",
		Proxy: "localhost:3000",
		Allow: "tag:",
	})
	if err == nil {
		t.Fatal("expected invalid allow tag error")
	}
}

func TestAddJSON_TCPUsesTypedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	oldRegPath := registryPathFn
	oldEnsureDir := ensureDirFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		ensureDirFn = oldEnsureDir
	})
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return nil }
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})

	rootCmd.SetArgs([]string{"add", "db", "--tcp", "localhost:5432", "--json"})
	got := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if strings.Contains(got, "https://db.<tailnet>.ts.net") {
		t.Fatalf("add --json rendered TCP service as HTTPS: %s", got)
	}

	data := dataMap(t, got)
	if data["url"] != "db.<tailnet>.ts.net:5432" {
		t.Fatalf("url = %v, want typed TCP display", data["url"])
	}
	endpoint, ok := data["endpoint"].(map[string]any)
	if !ok {
		t.Fatalf("endpoint = %T, want object", data["endpoint"])
	}
	if endpoint["kind"] != "tcp" || endpoint["display"] != "db.<tailnet>.ts.net:5432" {
		t.Fatalf("endpoint = %+v, want typed TCP endpoint", endpoint)
	}
}
