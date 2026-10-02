package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/spf13/pflag"
)

func runDurationRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	reset := func() {
		for _, path := range [][]string{{"extend"}, {"people", "add"}, {"people", "update"}} {
			c, _, err := rootCmd.Find(path)
			if err != nil {
				t.Fatal(err)
			}
			c.Flags().VisitAll(func(f *pflag.Flag) {
				var err error
				if v, ok := f.Value.(pflag.SliceValue); ok && f.DefValue == "[]" {
					err = v.Replace(nil)
				} else {
					err = f.Value.Set(f.DefValue)
				}
				if err != nil {
					t.Fatal(err)
				}
				f.Changed = false
			})
		}
	}
	reset()
	defer reset()
	return runRecipeRoot(t, args...)
}

func durationCommandPaths(t *testing.T) (sharePaths, time.Time) {
	t.Helper()
	paths := peopleTestPaths(t)
	now := peopleNowFn()
	deadline := now.Add(8 * time.Hour)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	oldClock, oldHook := durationNowFn, durationChangedFn
	t.Cleanup(func() { durationNowFn, durationChangedFn = oldClock, oldHook })
	durationNowFn = func() time.Time { return now }
	return paths, now
}

func TestExtendCommandMCPAndClockCapture(t *testing.T) {
	paths, now := durationCommandPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	var events []registry.DurationChange
	durationChangedFn = func(e registry.DurationChange) {
		reg, err := registry.Load(paths.Registry)
		if err != nil || len(reg.Services) != 3 {
			t.Fatalf("hook before save: %v", err)
		}
		events = append(events, e)
	}
	for _, args := range [][]string{
		{"extend", "photos", "--person", "alice", "--for", "90m"},
		{"extend", "public", "--until", "2030-01-01T03:00:00Z"},
		{"extend", "photos", "--person", "alice", "--for", "never", "--ack-never"},
	} {
		raw, err := runDurationRoot(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			OK      bool                    `json:"ok"`
			Version int                     `json:"schema_version"`
			Command string                  `json:"command"`
			Data    registry.DurationChange `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil || !envelope.OK || envelope.Version != 1 || envelope.Command != "extend" || !envelope.Data.ChangedAt.Equal(now) {
			t.Fatalf("%s %v", raw, err)
		}
	}
	actions := defaultMCPActions(paths, io.Discard)
	// Clocks are captured before a request worker is created; later seam changes
	// cannot change the handler's operation time, even decades from wall time.
	durationNowFn = func() time.Time { return now.Add(365 * 24 * time.Hour) }
	res, err := callMCPTool(context.Background(), actions, "extend", json.RawMessage(`{"service":"public","for":"1h"}`))
	if err != nil || res.IsError {
		t.Fatalf("%+v %v", res, err)
	}
	structured := mcpResultStructured(t, res)
	validateAgainstToolOutputSchema(t, "extend", structured)
	if structured["changed_at"] != now.Format(time.RFC3339) || structured["expires_at"] != now.Add(time.Hour).Format(time.RFC3339) {
		t.Fatalf("clock not captured: %+v", structured)
	}
	if len(events) != 4 {
		t.Fatalf("post-save events=%d", len(events))
	}
	before, _ := os.ReadFile(paths.Registry)
	for _, raw := range []string{`{"service":"public","for":"7d1s"}`, `{"service":"public","for":"never","ack_never":true}`, `{"service":"photos","who":"alice","for":"never"}`, `{"service":"public","for":"1h","until":"2030-01-02"}`, `{"service":"public"}`, `{"service":"public","surprise":true}`, `{}`, `{"service":"public","for":123}`, `{"service":"public","who":"","for":"1h"}`, `{"service":"public","who":null,"for":"1h"}`, `{"service":"public","who":123,"for":"1h"}`} {
		res, err := callMCPTool(context.Background(), actions, "extend", json.RawMessage(raw))
		if err != nil || !res.IsError || res.StructuredContent != nil {
			t.Fatalf("%s: %+v %v", raw, res, err)
		}
	}
	after, _ := os.ReadFile(paths.Registry)
	if string(before) != string(after) || len(events) != 4 {
		t.Fatal("refusal wrote data/emitted event")
	}
	for _, args := range [][]string{{"extend", "public"}, {"extend", "public", "--person", ""}, {"extend", "public", "--for", "1h", "--until", "2030-01-02"}} {
		if _, err := runDurationRoot(t, args...); err == nil {
			t.Fatal("invalid CLI accepted", args)
		}
	}
	bad := errors.New("path unavailable")
	inviteRegistryPathFn = func() (string, error) { return "", bad }
	if _, err := runDurationRoot(t, "extend", "public", "--for", "1h"); !errors.Is(err, bad) {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Registry, []byte("partial JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := extendLifetime(paths.Registry, extendArguments{Service: "public", For: ptrString("1h")}, now); err == nil || len(events) != 4 {
		t.Fatal("failed save emitted event", err)
	}
}

func TestPeopleUntilAndDurationPolicyConfig(t *testing.T) {
	paths, now := durationCommandPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	for _, args := range [][]string{{"people", "update", "alice", "--until", "2030-01-02T03:00:00Z", "--json"}, {"people", "update", "alice", "--for", "until 2030-01-02T04:00:00Z", "--json"}} {
		if _, err := runDurationRoot(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runDurationRoot(t, "people", "update", "alice", "--for", "1h", "--until", "2030-01-02"); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatal(err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", For: ptrString("never"), Invite: true, AckNever: true}, true); err == nil {
		t.Fatal("guest never admitted")
	}
	if err := config.SaveGlobalConfig(config.GlobalConfig{Durations: &config.DurationPolicyConfig{PublicMax: "14d"}}); err != nil {
		t.Fatal(err)
	}
	expiry, err := resolveFunnelExpiry(true, "8d", true, now)
	if err != nil || !expiry.Equal(now.Add(8*24*time.Hour)) {
		t.Fatalf("configured cap ignored: %v %v", expiry, err)
	}
	if _, err := extendLifetime(paths.Registry, extendArguments{Service: "public", For: ptrString("8d")}, now); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveGlobalConfig(config.GlobalConfig{Durations: &config.DurationPolicyConfig{PublicMax: "3d"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFunnelExpiry(true, "7d", true, now); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal(err)
	}
	until := "until 2030-01-01T01:00:00Z"
	expiry, err = resolveFunnelExpiry(true, until, true, now)
	if err != nil || !expiry.Equal(now.Add(time.Hour)) {
		t.Fatalf("absolute Funnel %v %v", expiry, err)
	}
	path, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"durations":{"public_max":"never"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFunnelExpiry(true, "1h", true, now); err == nil {
		t.Fatal("invalid policy ignored")
	}
	if _, err := extendLifetime(paths.Registry, extendArguments{Service: "public", For: ptrString("1h")}, now); err == nil {
		t.Fatal("invalid extend policy ignored")
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", For: ptrString("1h")}, true); err == nil {
		t.Fatal("invalid people policy ignored")
	}
	for _, tool := range []string{"share", "add", "recipe_plan", "recipe_apply", "people_add", "people_update", "extend"} {
		schema := mcpToolByName(t, tool).InputSchema["properties"].(map[string]any)
		key := "funnel_ttl"
		if strings.HasPrefix(tool, "people") || tool == "extend" {
			key = "for"
		}
		entry := schema[key].(map[string]any)
		if !strings.Contains(entry["description"].(string), duration.Suggestions) || len(entry["examples"].([]string)) < 5 {
			t.Fatalf("missing suggestions %s", tool)
		}
	}
}

func TestCompiledExtendAlwaysUsesStableJSON(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	dir := t.TempDir()
	path := dir + "/registry.json"
	fixture := `{"schema_version":2,"services":[{"name":"photos","type":"proxy","target":"http://localhost:3000","people_scoped":true}],"people":[{"login":"alice","grants":[{"app":"photos","expires_at":"2000-01-01T00:00:00Z","expired":true}]}]}`
	if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	run := e2eRunBinary(t, binary, dir, "", e2eEnv(dir), "extend", "photos", "--person", "alice", "--for", "1h")
	frame, _ := e2eDecodeEnvelope(t, run, "extend")
	if run.ExitCode != 4 || frame.OK || frame.Command != "extend" || frame.SchemaVersion != 1 || frame.Error.Code != "conflict" {
		t.Fatalf("expired JSON: %+v %+v", run, frame)
	}
	before, _ := os.ReadFile(path)
	if string(before) != fixture {
		t.Fatal("expired refusal wrote registry")
	}
	run = e2eRunBinary(t, binary, dir, "", e2eEnv(dir), "extend", "photos", "--person", "alice", "--for", "1h", "--regrant")
	frame, _ = e2eDecodeEnvelope(t, run, "extend")
	var result registry.DurationChange
	wire, _ := json.Marshal(frame.Data)
	if err := json.Unmarshal(wire, &result); err != nil {
		t.Fatal(err)
	}
	if run.ExitCode != 0 || !frame.OK || !result.Regranted || !result.ExpiresAt.Equal(result.ChangedAt.Add(time.Hour)) {
		t.Fatalf("regrant JSON: %+v %+v", run, result)
	}
	until := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	run = e2eRunBinary(t, binary, dir, "", e2eEnv(dir), "extend", "photos", "--person", "alice", "--until", until.Format(time.RFC3339))
	frame, _ = e2eDecodeEnvelope(t, run, "extend")
	wire, _ = json.Marshal(frame.Data)
	if err := json.Unmarshal(wire, &result); err != nil {
		t.Fatal(err)
	}
	if run.ExitCode != 0 || !result.ExpiresAt.Equal(until) || result.Regranted {
		t.Fatalf("absolute JSON: %+v %+v", run, result)
	}
	for _, args := range [][]string{{"extend"}, {"extend", "photos", "--for", "never", "--person", "alice"}, {"extend", "photos", "--unknown"}} {
		run = e2eRunBinary(t, binary, dir, "", e2eEnv(dir), args...)
		frame, _ = e2eDecodeEnvelope(t, run, "extend")
		if run.ExitCode != 2 || frame.OK || frame.Error.Code != "usage_error" {
			t.Fatalf("usage JSON: %+v %+v", run, frame)
		}
	}
}

func TestFirstGuestInvitationRuntimeRefusesPermanentControlPasses(t *testing.T) {
	paths, now := durationCommandPaths(t)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	calls := 0
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, _ string, print, multi, exit bool) (tailapi.Invite, error) {
		calls++
		return tailapi.Invite{ID: "100", Kind: "device", Service: target.Service, InviteURL: "https://login.tailscale.com/admin/invite/FAKE-duration-control"}, nil
	}
	if _, err := registry.ChangePerson(paths.Registry, "alice", nil, nil, true, true); err != nil {
		t.Fatal(err)
	}
	reviewRefreshProof(t, paths)
	before, _ := os.ReadFile(paths.Registry)
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true}, true); err == nil || !strings.Contains(err.Error(), "first guest invitation") {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(paths.Registry)
	if string(before) != string(after) || calls != 0 {
		t.Fatal("rejected guest transition wrote/sent")
	}
	// Same runtime chain, finite replacement: reaches the fake invitation action.
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, For: ptrString("3d")}, true)
	if err != nil || !result.Complete || calls != 1 || !result.Person.Grants[0].ExpiresAt.Equal(now.Add(72*time.Hour)) {
		t.Fatalf("finite control: %+v %v calls=%d", result, err, calls)
	}
}
