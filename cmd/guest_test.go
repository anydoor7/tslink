package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func guestCommandPaths(t *testing.T) sharePaths {
	t.Helper()
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	paths, e := configSharePaths()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = registry.Add(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); e != nil {
		t.Fatal(e)
	}
	return paths
}
func TestGuestCommandAndMCP(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	a := guestArguments{App: "photos", For: "2h", Public: true, Label: "Aunt May"}
	result, e := createGuest(paths, a, now)
	if e != nil {
		t.Fatal(e)
	}
	if result.Link != nil || strings.Contains(result.Message, "/guest/") {
		t.Fatal("masked output exposed link")
	}
	raw, _ := json.Marshal(output.NewSuccess("guest create", result))
	if !strings.Contains(string(raw), `"schema_version":1`) {
		t.Fatal(string(raw))
	}
	reg, _, e := registry.Preflight(paths.Registry)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{reg.Guests[0].Salt, reg.Guests[0].TokenHash} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("hash in command output")
		}
	}
	a.PrintLink = true
	if _, e = createGuest(paths, a, now); e == nil {
		t.Fatal("invented runtime authority")
	}
	actions := defaultMCPActions(paths, &bytes.Buffer{})
	for _, name := range []string{"guest_list", "guest_show", "guest_revoke"} {
		args := guestArguments{ID: result.Grant.ID}
		v, e := actions.guest(name, args)
		if e != nil || v == nil {
			t.Fatal(name, v, e)
		}
		if !mcpOwnerOnlyGuestTools[name] {
			t.Fatal("owner registry missing", name)
		}
	}
	revoked, e := registry.ShowGuest(paths.Registry, result.Grant.ID, now)
	if e != nil || !revoked.Revoked {
		t.Fatal(revoked, e)
	}
	c := newGuestCmd()
	c.SetArgs([]string{"create", "photos", "--for", "never", "--public"})
	c.SetOut(&bytes.Buffer{})
	if e = c.Execute(); e == nil {
		t.Fatal("CLI accepted never")
	}
	c = newGuestCmd()
	c.SetArgs([]string{"create", "photos", "--for", "1h", "--pin"})
	c.SetIn(strings.NewReader("975310\n"))
	c.SetOut(&bytes.Buffer{})
	if e = c.Execute(); e != nil {
		t.Fatal(e)
	}
	views, e := registry.ListGuests(paths.Registry, time.Now())
	if e != nil || len(views) != 2 || !views[1].PINRequired {
		t.Fatal(views, e)
	}
	for _, pin := range []string{"1\n", strings.Repeat("1", 65), ""} {
		c = newGuestCmd()
		c.SetIn(strings.NewReader(pin))
		if _, e = guestPINInput(c); e == nil {
			t.Fatal("bad input accepted")
		}
	}
}
func TestGuestStatusAndDoctorViews(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	r, e := createGuest(paths, guestArguments{App: "photos", For: "2h", Public: true, Label: "Aunt May"}, now)
	if e != nil {
		t.Fatal(e)
	}
	reg, _, e := registry.Preflight(paths.Registry)
	if e != nil {
		t.Fatal(e)
	}
	views := activeGuestViews(reg, now)
	if len(views) != 1 {
		t.Fatal(views)
	}
	buf := &bytes.Buffer{}
	formatGuestViews(buf, views)
	if !strings.Contains(buf.String(), "Aunt May") || strings.Contains(buf.String(), reg.Guests[0].TokenHash) {
		t.Fatal(buf.String())
	}
	result := DoctorResult{Paths: DoctorPaths{RuntimeSnapshot: filepath.Join(filepath.Dir(paths.Registry), "missing")}}
	diagnoseGuests(&result, reg, now)
	codes := map[string]bool{}
	for _, f := range result.Findings {
		codes[f.Code] = true
	}
	if !codes[inspect.WarningCodeGuestExpiry] || !codes[inspect.WarningCodeGuestFunnel] {
		t.Fatal(result.Findings)
	}
	if _, e = registry.RevokeGuest(paths.Registry, r.Grant.ID, now); e != nil {
		t.Fatal(e)
	}
	reg, _, e = registry.Preflight(paths.Registry)
	if e != nil {
		t.Fatal(e)
	}
	if len(activeGuestViews(reg, now)) != 0 {
		t.Fatal("revoked link in status")
	}
	if _, e = os.Stat(paths.Registry); e != nil {
		t.Fatal(e)
	}
}

func TestGuestExplicitDisclosureAndMCPDispatch(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	proof := tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: now, UpdatedAt: now, RegistryFingerprint: currentRegistryFingerprint(paths.Registry), Services: []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", RuntimeState: "running", Endpoint: inspect.EndpointView{Kind: "https", State: "exact", Display: "https://photos.tail.test.ts.net"}}}}
	if e := tsruntime.Save(paths.Snapshot, proof); e != nil {
		t.Fatal(e)
	}
	r, e := createGuest(paths, guestArguments{App: "photos", For: "2h", Public: true, PrintLink: true, PIN: "975310"}, now)
	if e != nil || r.Link == nil || !strings.Contains(r.Message, "Enter the PIN") || !strings.HasPrefix(*r.Link, "https://photos.tail.test.ts.net/guest/") {
		t.Fatal("explicit disclosure failed", e)
	}
	token := strings.TrimPrefix(*r.Link, "https://photos.tail.test.ts.net/guest/")
	if _, reason := registry.FindGuestToken(paths.Registry, "photos", token, now); reason != "allowed" {
		t.Fatal(reason)
	}
	old := durationNowFn
	durationNowFn = func() time.Time { return now }
	t.Cleanup(func() { durationNowFn = old })
	actions := defaultMCPActions(paths, io.Discard)
	for _, call := range []struct{ name, args string }{{"guest_create", `{"app":"photos","for":"1h"}`}, {"guest_list", `{}`}, {"guest_show", `{"id":"` + r.Grant.ID + `"}`}, {"guest_revoke", `{"id":"` + r.Grant.ID + `"}`}} {
		result, e := callMCPTool(t.Context(), actions, call.name, json.RawMessage(call.args))
		if e != nil || result.IsError {
			t.Fatal(call.name, e, result)
		}
	}
	for _, call := range []struct{ name, args string }{{"guest_create", `{"app":"photos"}`}, {"guest_show", `{}`}, {"guest_revoke", `{"id":"","unexpected":true}`}} {
		result, e := callMCPTool(t.Context(), actions, call.name, json.RawMessage(call.args))
		if e != nil || !result.IsError {
			t.Fatal("malformed accepted", call.name, e)
		}
	}
	for _, args := range [][]string{{"list"}, {"show", r.Grant.ID}, {"revoke", r.Grant.ID}} {
		group := newGuestCmd()
		for _, c := range group.Commands() {
			c.Flags().Bool("json", true, "")
		}
		out := &bytes.Buffer{}
		group.SetOut(out)
		group.SetArgs(args)
		if e := group.Execute(); e != nil {
			t.Fatal(e)
		}
		var decoded output.Result
		if e := json.Unmarshal(out.Bytes(), &decoded); e != nil || !decoded.OK {
			t.Fatal("command envelope", e)
		}
	}
}
func TestGuestCompiledCLIAndStdio(t *testing.T) {
	paths := guestCommandPaths(t)
	dir := filepath.Dir(paths.Registry)
	binary := compiledTSLinkBinary(t)
	create := e2eRunBinary(t, binary, dir, "975310\n", e2eEnv(dir), "guest", "create", "photos", "--for", "2h", "--public", "--pin", "--label", "Aunt May", "--json")
	if create.ExitCode != 0 {
		t.Fatal(create.Stderr)
	}
	var envelope struct {
		OK     bool              `json:"ok"`
		Schema int               `json:"schema_version"`
		Data   guestCreateResult `json:"data"`
	}
	if e := json.Unmarshal([]byte(create.Stdout), &envelope); e != nil || !envelope.OK || envelope.Schema != 1 || envelope.Data.Link != nil || !envelope.Data.Grant.PINRequired {
		t.Fatal("binary create", e)
	}
	for _, args := range [][]string{{"guest", "list", "--json"}, {"guest", "show", envelope.Data.Grant.ID, "--json"}, {"guest", "revoke", envelope.Data.Grant.ID, "--json"}} {
		r := e2eRunBinary(t, binary, dir, "", e2eEnv(dir), args...)
		if r.ExitCode != 0 || !json.Valid([]byte(r.Stdout)) {
			t.Fatal("binary CRUD", r.Stderr)
		}
	}
	input := initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"guest_create","arguments":{"app":"photos","for":"1h","label":"MCP guest"}}}`)
	r := e2eRunBinary(t, binary, dir, input, e2eEnv(dir), "mcp")
	if r.ExitCode != 0 {
		t.Fatal(r.Stderr)
	}
	frame := mcpFrameByID(t, decodeMCPResponses(t, r.Stdout), float64(2))
	result, _ := frame["result"].(map[string]any)
	if result == nil || result["isError"] == true {
		t.Fatal("binary MCP failure", frame)
	}
	if strings.Contains(r.Stdout, "token_hash") || strings.Contains(r.Stdout, "pin_hash") || strings.Contains(r.Stdout, "975310") {
		t.Fatal("stdio secrets")
	}
}

type guestErrorReader struct{}

func (guestErrorReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("synthetic input unavailable")
}
func TestGuestCommandFailureSurfaces(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	c := newGuestCmd()
	c.SetIn(guestErrorReader{})
	if _, e := guestPINInput(c); e == nil {
		t.Fatal("input failure ignored")
	}
	c = newGuestCmd()
	c.SetArgs([]string{"create", "photos", "--for", "1h", "--pin"})
	c.SetIn(strings.NewReader(""))
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if e := c.Execute(); e == nil {
		t.Fatal("CLI PIN input failure ignored")
	}
	c = newGuestCmd()
	for _, leaf := range c.Commands() {
		leaf.Flags().Bool("json", true, "")
	}
	out := &bytes.Buffer{}
	c.SetOut(out)
	c.SetArgs([]string{"create", "photos", "--for", "1h", "--public"})
	if e := c.Execute(); e != nil {
		t.Fatal(e)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatal("create did not emit JSON")
	}
	views, e := registry.ListGuests(paths.Registry, time.Now())
	if e != nil || len(views) != 1 {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"list"}, {"show", views[0].ID}, {"revoke", views[0].ID}} {
		c = newGuestCmd()
		out.Reset()
		c.SetOut(out)
		c.SetArgs(args)
		if e := c.Execute(); e != nil || out.Len() == 0 {
			t.Fatal("human CRUD", e)
		}
	}
	for _, args := range [][]string{{"show", "missing"}, {"revoke", "missing"}} {
		c = newGuestCmd()
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if e := c.Execute(); e == nil {
			t.Fatal("missing grant command succeeded")
		}
	}
	if _, e = createGuest(paths, guestArguments{App: "photos"}, now); e == nil {
		t.Fatal("missing lifetime accepted")
	}
	cfg, e := config.ConfigPath()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(cfg, []byte(`{"durations":`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = createGuest(paths, guestArguments{App: "photos", For: "1h"}, now); e == nil {
		t.Fatal("corrupt owner policy accepted")
	}
	if e = os.WriteFile(paths.Registry, []byte(`{"guests":`), 0600); e != nil {
		t.Fatal(e)
	}
	c = newGuestCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"list"})
	if e := c.Execute(); e == nil {
		t.Fatal("invalid registry list succeeded")
	}
}
func TestGuestDoctorCurrentFunnelControl(t *testing.T) {
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	if _, e := createGuest(paths, guestArguments{App: "photos", For: "2h", Public: true}, now); e != nil {
		t.Fatal(e)
	}
	reg, _, e := registry.Preflight(paths.Registry)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(paths.PID, []byte("42\n"), 0600); e != nil {
		t.Fatal(e)
	}
	proof := tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: now, UpdatedAt: now, RegistryFingerprint: currentRegistryFingerprint(paths.Registry), Services: []tsruntime.ServiceSnapshot{{Name: "photos", RuntimeState: "running", FunnelActive: true, FunnelState: tsruntime.FunnelStateActive}}}
	for _, active := range []bool{true, false} {
		proof.Services[0].FunnelActive = active
		if e = tsruntime.Save(paths.Snapshot, proof); e != nil {
			t.Fatal(e)
		}
		result := DoctorResult{Paths: DoctorPaths{Registry: paths.Registry, PID: paths.PID, RuntimeSnapshot: paths.Snapshot}, Daemon: DoctorDaemon{Running: true, PID: 42}}
		diagnoseGuests(&result, reg, now)
		warning := false
		for _, finding := range result.Findings {
			if finding.Code == inspect.WarningCodeGuestFunnel {
				warning = true
			}
		}
		if warning == active {
			t.Fatal("Funnel diagnosis has no discrimination", active)
		}
	}
}
