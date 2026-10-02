package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestPortalCLIAndMCP(t *testing.T) {
	paths := peopleTestPaths(t)
	args := portalArguments{Owner: " Owner ", Admins: []string{" Admin "}}
	view, err := changePortal(paths, args, true)
	if err != nil || !view.Enabled || view.Hostname != "home" || view.State != "pending" || view.URL != "" {
		t.Fatalf("enable=%+v %v", view, err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil || reg.Portal.Owner != "owner" || reg.Portal.Admins[0] != "admin" {
		t.Fatalf("identity: %+v %v", reg, err)
	}
	before, _ := os.ReadFile(paths.Registry)
	_, err = changePortal(paths, portalArguments{Owner: "owner", Funnel: true}, true)
	code, _ := registry.ErrorCode(err)
	if code != registry.CodePortalFunnelRefused {
		t.Fatalf("Funnel refusal=%v", err)
	}
	after, _ := os.ReadFile(paths.Registry)
	if !bytes.Equal(before, after) {
		t.Fatal("Funnel refusal changed registry")
	}
	for _, tc := range []struct {
		tool, args string
		enabled    bool
	}{
		{"portal_disable", `{}`, false}, {"portal_enable", `{"owner":"owner","hostname":"family"}`, true},
	} {
		r, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), tc.tool, json.RawMessage(tc.args))
		if err != nil || r.IsError {
			t.Fatalf("tool result=%+v %v", r, err)
		}
		reg, _, err := registry.Preflight(paths.Registry)
		if err != nil || reg.Portal.Enabled != tc.enabled {
			t.Fatalf("MCP configuration: %+v %v", reg, err)
		}
	}
	for _, raw := range []string{`{}`, `{"owner":"owner","funnel":true}`, `{"owner":"owner","admins":["bad login"]}`, `{"owner":"owner","hostname":"Bad/Name"}`, `{"owner":"owner","funnel":"true"}`, `{"owner":"owner","unknown":true}`} {
		r, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "portal_enable", json.RawMessage(raw))
		if err != nil || !r.IsError {
			t.Fatalf("bad MCP accepted %s: %+v %v", raw, r, err)
		}
	}
	for _, mode := range []string{"enable", "disable"} {
		c := newPortalCmd()
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetErr(&out)
		if mode == "enable" {
			c.SetArgs([]string{mode, "--owner", "owner"})
		} else {
			c.SetArgs([]string{mode})
		}
		if err := c.Execute(); err != nil || !strings.Contains(out.String(), "Portal") {
			t.Fatalf("CLI: %s %v", out.String(), err)
		}
	}
}

func TestPortalStatusDoctorAndGuide(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := tsRuntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	if err := os.WriteFile(paths.PID, []byte("42"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(paths.PID, started, started); err != nil {
		t.Fatal(err)
	}
	snapshot := tsRuntime.NewSnapshot(42, started, fp, time.Now(), nil)
	snapshot.Portal = tsRuntime.PortalState{Enabled: true, Hostname: "home", State: "running", URL: "https://home.tailnet.ts.net"}
	if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	if v := readPortalView(reg, paths.Registry, true, 42); v.URL != snapshot.Portal.URL {
		t.Fatalf("exact URL=%+v", v)
	}
	if v := readPortalView(reg, paths.Registry, false, 42); v.URL != "" || v.State != "pending" {
		t.Fatalf("stopped URL=%+v", v)
	}
	if v := readPortalView(reg, paths.Registry, true, 99); v.URL != "" {
		t.Fatalf("PID mismatch URL=%+v", v)
	}
	oldRun, oldPID := inviteIsRunningFn, inviteReadPIDFn
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	t.Cleanup(func() { inviteIsRunningFn = oldRun; inviteReadPIDFn = oldPID })
	for _, asJSON := range []bool{false, true} {
		c := newPortalCmd()
		c.PersistentFlags().Bool("json", asJSON, "JSON result")
		var out bytes.Buffer
		c.SetOut(&out)
		c.SetArgs([]string{"enable", "--owner", "owner"})
		if err := c.Execute(); err != nil || !strings.Contains(out.String(), snapshot.Portal.URL) {
			t.Fatalf("ready CLI=%s %v", out.String(), err)
		}
	}
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, true)
	if err != nil || !strings.Contains(result.Message, snapshot.Portal.URL) || !strings.Contains(result.Message, "share the home node") {
		t.Fatalf("guide=%+v %v", result, err)
	}
	var b bytes.Buffer
	formatStatus(StatusResult{Portal: snapshot.Portal}, &b)
	formatDoctor(DoctorResult{Portal: snapshot.Portal}, &b)
	if strings.Count(b.String(), snapshot.Portal.URL) != 2 {
		t.Fatalf("status/doctor text=%s", b.String())
	}
	if err := registry.DisablePortal(paths.Registry); err != nil {
		t.Fatal(err)
	}
	reg, _, err = registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	if v := readPortalView(reg, paths.Registry, true, 42); v.Enabled || v.URL != "" {
		t.Fatalf("disabled view=%+v", v)
	}
	if v := portalView(nil, nil, false); v.State != "disabled" {
		t.Fatalf("default view=%+v", v)
	}
	reg.Portal.Enabled = true
	snapshot.Portal.State = "failed"
	if v := portalView(reg, &snapshot, true); v.URL != "" {
		t.Fatalf("failed portal promoted URL=%+v", v)
	}
	b.Reset()
	formatPortal(&b, tsRuntime.PortalState{Error: "portal_start_failed"})
	if b.String() != "Portal: disabled [portal_start_failed]\n" {
		t.Fatalf("error format=%s", b.String())
	}
}

func TestPortalCommandFailurePathsAndJSONDisable(t *testing.T) {
	paths := peopleTestPaths(t)
	for _, mode := range []string{"enable", "disable"} {
		t.Run(mode+"-path", func(t *testing.T) {
			old := shareRegistryPathFn
			shareRegistryPathFn = func() (string, error) { return "", errors.New("fixture path failure") }
			defer func() { shareRegistryPathFn = old }()
			c := newPortalCmd()
			c.SetOut(io.Discard)
			c.SetErr(io.Discard)
			c.SetArgs([]string{mode})
			if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "fixture path failure") {
				t.Fatalf("path failure=%v", err)
			}
		})
		t.Run(mode+"-registry", func(t *testing.T) {
			if err := os.WriteFile(paths.Registry, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			c := newPortalCmd()
			c.SetOut(io.Discard)
			c.SetErr(io.Discard)
			c.SetArgs([]string{mode, "--owner", "owner"})
			if mode == "disable" {
				c.SetArgs([]string{mode})
			}
			if err := c.Execute(); err == nil {
				t.Fatal("corrupt registry accepted")
			}
		})
	}
	if err := os.Remove(paths.Registry); err != nil {
		t.Fatal(err)
	}
	c := newPortalCmd()
	c.PersistentFlags().Bool("json", true, "JSON result")
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetArgs([]string{"disable"})
	if err := c.Execute(); err != nil || !strings.Contains(out.String(), `"state":"disabled"`) {
		t.Fatalf("JSON disable=%s %v", out.String(), err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(paths.Registry), "registry.json")); !os.IsNotExist(err) {
		t.Fatalf("disable missing config created registry: %v", err)
	}
}

func TestPortalConfigFailures(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "bad login"}, true); err == nil {
		t.Fatal("bad owner accepted")
	}
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Admins: []string{"bad login"}}, true); err == nil {
		t.Fatal("bad admin accepted")
	}
	if err := config.SaveGlobalConfig(config.GlobalConfig{MCP: &config.MCPConfig{Enabled: true, NodeName: "home"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err == nil || output.ExitCode(err) != 4 {
		t.Fatalf("MCP collision=%v", err)
	}
	if err := config.SaveGlobalConfig(config.GlobalConfig{MCP: &config.MCPConfig{Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := changePortal(paths, portalArguments{Owner: "owner", Hostname: "tslink-mcp"}, true); err == nil {
		t.Fatal("default MCP collision accepted")
	}
	configPath, _ := config.ConfigPath()
	if err := os.WriteFile(configPath, []byte(`{"broken":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err == nil {
		t.Fatal("unknown global config key dropped")
	}
}

func TestPortalStatusDuringRegistryRemoval(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, err := changePortal(paths, portalArguments{Owner: "owner"}, true); err != nil {
		t.Fatal(err)
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := tsRuntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	states := []tsRuntime.ServiceState{}
	for _, svc := range reg.Services {
		states = append(states, tsRuntime.ServiceState{Service: svc, RuntimeHost: svc.Name + ".tailnet.ts.net", RuntimeState: tsRuntime.ServiceRuntimeRunning})
	}
	snapshot := tsRuntime.NewSnapshot(42, now.Add(-time.Minute), fp, now, states)
	if err := tsRuntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 42, now.Add(-time.Minute))
	oldLoad := runtimeLoadSnapshotFn
	defer func() { runtimeLoadSnapshotFn = oldLoad }()
	removed := false
	runtimeLoadSnapshotFn = func(path string) (*tsRuntime.Snapshot, error) {
		if !removed {
			removed = true
			if _, err := registry.Remove(paths.Registry, "finance"); err != nil {
				t.Fatal(err)
			}
		}
		return tsRuntime.Load(path)
	}
	result, err := readOnlyStatus.getPollableStatus(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
	if err != nil || !removed || result.AuthorizedServiceCount != 1 || result.Portal.URL != "" {
		t.Fatalf("concurrent registry removal=%+v %v", result, err)
	}
}
