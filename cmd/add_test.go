package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

func TestAddDomainACMEFlagsAdvertiseRejectedReservedState(t *testing.T) {
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}
	for _, name := range []string{"domain", "acme-email"} {
		flag := addCmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("add flag %q missing", name)
		}
		for _, want := range []string{"Reserved", "unavailable", "feature_unavailable"} {
			if !strings.Contains(flag.Usage, want) {
				t.Fatalf("flag %s usage = %q, want %q", name, flag.Usage, want)
			}
		}
		for _, forbidden := range []string{"accepted", "stored"} {
			if strings.Contains(strings.ToLower(flag.Usage), forbidden) {
				t.Fatalf("flag %s usage = %q, must not claim %s", name, flag.Usage, forbidden)
			}
		}
	}
}

func TestAddFunnel_WithProxy_Persisted(t *testing.T) {
	dir := t.TempDir()
	regPath := dir + "/registry.json"

	// Directly add a service with funnel=true to registry
	svc := registry.Service{
		Name:      "funnel-test",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3000",
		Funnel:    true,
		PublicAck: true,
	}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}

	// Verify the registry persisted the public exposure bit. The default list
	// schema is intentionally slim and does not duplicate this verbose field.
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	if len(reg.Services) != 1 || !reg.Services[0].Funnel {
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

	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	if reg.Services[0].Funnel {
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
	if !strings.Contains(err.Error(), registry.ErrFunnelPublicAck) {
		t.Fatalf("unexpected error: %v", err)
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelPublicAckRequired {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelPublicAckRequired)
	}
}

func TestBuildService_FunnelRejectsAllowBeforeMissingPublicAck(t *testing.T) {
	_, err := buildService(AddParams{
		Name:   "app",
		Proxy:  "localhost:3000",
		Allow:  "alice@example.com",
		Funnel: true,
	})
	if err == nil {
		t.Fatal("expected funnel allowed_users error")
	}
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelAllowConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelAllowConflict)
	}
	if !strings.Contains(err.Error(), registry.CodeFunnelAllowConflict) {
		t.Fatalf("error = %q, want stable code", err.Error())
	}
	if strings.Contains(err.Error(), publicAckRequiredError) {
		t.Fatalf("error = %q, want allow conflict before public ack", err.Error())
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
	if !svc.PublicAck {
		t.Fatal("expected public_ack=true")
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
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelAllowConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelAllowConflict)
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
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeFunnelControlURLConflict {
		t.Fatalf("ErrorCode() = %q, %v; want %s, true", code, ok, registry.CodeFunnelControlURLConflict)
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

func TestBuildService_NormalizesAllowEmailCase(t *testing.T) {
	svc, err := buildService(AddParams{
		Name:  "app",
		Proxy: "localhost:3000",
		Allow: "User@Example.com,tag:admin",
	})
	if err != nil {
		t.Fatalf("buildService() error = %v", err)
	}
	got := strings.Join(svc.AllowedUsers, ",")
	if got != "user@example.com,tag:admin" {
		t.Fatalf("allowed_users = %q, want normalized email and preserved tag", got)
	}
}

func TestAddCmd_InvalidAllowEntryWarns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(dir+"/.config/tslink", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add command: %v", err)
	}
	_ = addCmd.Flags().Set("proxy", "")
	_ = addCmd.Flags().Set("dir", "")
	_ = addCmd.Flags().Set("tcp", "")
	_ = addCmd.Flags().Set("ephemeral", "false")
	_ = addCmd.Flags().Set("tags", "")
	_ = addCmd.Flags().Set("allow", "")
	_ = addCmd.Flags().Set("funnel", "false")
	_ = addCmd.Flags().Set("public", "false")
	_ = addCmd.Flags().Set("domain", "")
	_ = addCmd.Flags().Set("acme-email", "")
	_ = addCmd.Flags().Set("control-url", "")
	t.Cleanup(func() {
		addCmd.SetErr(os.Stderr)
	})

	var errBuf strings.Builder
	addCmd.SetErr(&errBuf)
	if _, err := runAddCmdOutput(t, []string{"app"}, map[string]string{
		"proxy": "localhost:3000",
		"allow": "not-an-email",
	}); err != nil {
		t.Fatalf("run add: %v", err)
	}
	if !strings.Contains(errBuf.String(), "Warning: --allow entry") {
		t.Fatalf("stderr = %q, want invalid allow warning", errBuf.String())
	}
}

func TestAddJSONIncludesInvalidAllowWarning(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	oldRegPath := registryPathFn
	oldEnsureDir := ensureDirFn
	t.Cleanup(func() {
		registryPathFn = oldRegPath
		ensureDirFn = oldEnsureDir
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})
	registryPathFn = func() (string, error) { return regPath, nil }
	ensureDirFn = func() error { return nil }
	_ = rootCmd.PersistentFlags().Set("json", "true")

	got := captureStdout(t, func() {
		if _, err := runAddCmdOutput(t, []string{"app"}, map[string]string{"proxy": "localhost:3000", "allow": "not-an-email"}); err != nil {
			t.Fatalf("run add: %v", err)
		}
	})
	data := dataMap(t, got)
	warnings, ok := data["warnings"].([]any)
	if !ok || len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one JSON warning", data["warnings"])
	}
	warning, _ := warnings[0].(map[string]any)
	if warning["code"] != "invalid_allow_entry" {
		t.Fatalf("warning = %#v, want invalid_allow_entry", warning)
	}
}

func TestAddDryRunPrintsServiceWithoutWriting(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("json", "false") })
	_ = rootCmd.PersistentFlags().Set("json", "true")

	got := captureStdout(t, func() {
		if _, err := runAddCmdOutput(t, []string{"preview"}, map[string]string{"proxy": "localhost:3000", "dry-run": "true"}); err != nil {
			t.Fatalf("run dry-run: %v", err)
		}
	})
	data := dataMap(t, got)
	if data["dry_run"] != true {
		t.Fatalf("data = %#v, want dry_run=true", data)
	}
	service, ok := data["service"].(map[string]any)
	if !ok || service["name"] != "preview" || service["type"] != registry.TypeProxy {
		t.Fatalf("service = %#v, want validated preview", data["service"])
	}
	if _, err := os.Stat(filepath.Join(configDir, "registry.json")); !os.IsNotExist(err) {
		t.Fatalf("registry stat err = %v, want not exist after dry-run", err)
	}
}

func TestAddWaitResolvesURLWhenRuntimeSnapshotArrives(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	svc := registry.Service{Name: "waiting", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	svc = reg.Services[0]
	fingerprint := statusRegistryFingerprint(t, regPath)
	withStatusURLSeams(t, true, 4242, startedAt)

	saved := make(chan error, 1)
	go func() {
		time.Sleep(20 * time.Millisecond)
		snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{{
			Service: svc, RuntimeHost: "node.example.ts.net",
		}})
		saved <- tsruntime.Save(snapshotPath, snapshot)
	}()

	result, err := buildAddResult(context.Background(), svc, true, pidPath, regPath, snapshotPath, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("buildAddResult: %v", err)
	}
	if err := <-saved; err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	if result.URL == nil || *result.URL != "https://node.example.ts.net" || result.URLPending {
		t.Fatalf("result = %+v, want exact waited URL", result)
	}

	add, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add: %v", err)
	}
	if flag := add.Flags().Lookup("wait"); flag == nil || flag.NoOptDefVal != "30s" {
		t.Fatalf("wait flag = %+v, want optional 30s value", flag)
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

	_ = rootCmd.PersistentFlags().Set("json", "true")
	got := captureStdout(t, func() {
		_, err := runAddCmdOutput(t, []string{"db"}, map[string]string{"tcp": "localhost:5432"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	if strings.Contains(got, "https://db.<tailnet>.ts.net") {
		t.Fatalf("add --json rendered TCP service as HTTPS: %s", got)
	}

	data := dataMap(t, got)
	if data["url"] != nil || data["url_pending"] != true {
		t.Fatalf("url = %v pending=%v, want null/pending until runtime evidence", data["url"], data["url_pending"])
	}
	endpoint, ok := data["endpoint"].(map[string]any)
	if !ok {
		t.Fatalf("endpoint = %T, want object", data["endpoint"])
	}
	if endpoint["kind"] != "tcp" || endpoint["display"] != "" || endpoint["state"] != "expected" {
		t.Fatalf("endpoint = %+v, want pending typed TCP endpoint without placeholder", endpoint)
	}
}

func TestAddJSON_FunnelIncludesPublicExposure(t *testing.T) {
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

	_ = rootCmd.PersistentFlags().Set("json", "true")
	got := captureStdout(t, func() {
		_, err := runAddCmdOutput(t, []string{"public-app"}, map[string]string{
			"proxy":  "localhost:3000",
			"funnel": "true",
			"public": "true",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	data := dataMap(t, got)
	exposure, ok := data["exposure"].(map[string]any)
	if !ok {
		t.Fatalf("exposure = %T, want object", data["exposure"])
	}
	if exposure["kind"] != inspect.ExposurePublicFunnel || exposure["public"] != true {
		t.Fatalf("exposure = %+v, want public_funnel public exposure", exposure)
	}
}

func TestAddHumanFunnelOutputIncludesPublicMarker(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(dir+"/.config/tslink", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, err := runAddCmdOutput(t, []string{"public-app"}, map[string]string{
		"proxy":  "localhost:3000",
		"funnel": "true",
		"public": "true",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "PUBLIC") {
		t.Fatalf("output = %q, want uppercase PUBLIC marker", out)
	}
}

func TestAddHumanTCPOutputIncludesBoundaryNote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(dir+"/.config/tslink", 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, err := runAddCmdOutput(t, []string{"db"}, map[string]string{"tcp": "localhost:5432"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "TSLink HTTP allow and identity headers do not apply to raw TCP") {
		t.Fatalf("output = %q, want raw-TCP boundary note", out)
	}
	if !strings.Contains(out, "Tailscale policy plus backend auth") {
		t.Fatalf("output = %q, want protection boundary", out)
	}
}
