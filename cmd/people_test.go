package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	"github.com/anydoor7/tslink/internal/tailapi"
)

func peopleTestPaths(t *testing.T) sharePaths {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
	for _, name := range []string{"photos", "finance"} {
		if _, err := registry.Add(paths.Registry, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
	}
	old := peopleNowFn
	t.Cleanup(func() { peopleNowFn = old })
	peopleNowFn = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	return paths
}

func TestPeopleCommandAndMCPRoundTrip(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
		t.Fatal("no-token grant attempted an invitation")
		return tailapi.Invite{}, nil
	}
	group := newPeopleCmd()
	group.Flags().Bool("json", true, "")
	for _, c := range group.Commands() {
		c.Flags().Bool("json", true, "")
	}
	var out bytes.Buffer
	group.SetOut(&out)
	group.SetArgs([]string{"add", "alice@example.com", "--apps", "photos,finance", "--for", "7d"})
	if err := group.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope output.Result
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.SchemaVersion != 1 || envelope.Command != "people add" {
		t.Fatal(out.String())
	}
	data, _ := json.Marshal(envelope.Data)
	var added PeopleResult
	if err := json.Unmarshal(data, &added); err != nil {
		t.Fatal(err)
	}
	if len(added.Person.Grants) != 2 || !strings.Contains(added.Message, "Install Tailscale") || !strings.Contains(added.InviteRequirement, "user-owned") {
		t.Fatal(added)
	}
	before, _ := os.ReadFile(paths.Registry)
	tools := defaultMCPActions(paths, io.Discard)
	listed, err := callMCPTool(context.Background(), tools, "people_list", json.RawMessage(`{}`))
	if err != nil || listed.IsError {
		t.Fatal(listed, err)
	}
	after, _ := os.ReadFile(paths.Registry)
	if string(before) != string(after) {
		t.Fatal("read-only list wrote registry")
	}
	updated, err := callMCPTool(context.Background(), tools, "people_update", json.RawMessage(`{"who":"alice@example.com","apps":["photos"],"for":"never"}`))
	if err != nil || updated.IsError {
		t.Fatal(updated, err)
	}
	reg, _ := registry.Load(paths.Registry)
	if len(reg.People[0].Grants) != 1 || reg.People[0].Grants[0].ExpiresAt != nil {
		t.Fatal("MCP update wrong")
	}
	removed, err := callMCPTool(context.Background(), tools, "people_remove", json.RawMessage(`{"who":"alice@example.com"}`))
	if err != nil || removed.IsError {
		t.Fatal(removed, err)
	}
	reg, _ = registry.Load(paths.Registry)
	if !reg.People[0].Revoked {
		t.Fatal("MCP remove failed")
	}
	for _, call := range []struct{ name, args string }{{"people_add", `{"who":"bob","apps":["photos"]}`}, {"people_add", `{"who":"bob","allow_exit_node":true}`}, {"people_list", `{"surprise":true}`}, {"people_remove", `{}`}, {"people_update", `{"who":"bob"}`}} {
		res, err := callMCPTool(context.Background(), tools, call.name, json.RawMessage(call.args))
		if err != nil {
			t.Fatal(err)
		}
		if call.args == `{"who":"bob","apps":["photos"]}` {
			if res.IsError {
				t.Fatal(res)
			}
		} else if !res.IsError {
			t.Fatalf("accepted invalid arguments %s", call.args)
		}
	}
}

func TestPeopleBundledInvitesUseFakeRESTAndMaskLinks(t *testing.T) {
	for _, printLinks := range []bool{false, true} {
		t.Run(map[bool]string{false: "masked", true: "explicit"}[printLinks], func(t *testing.T) {
			paths := peopleTestPaths(t)
			restoreInviteCommandSeams(t)
			started := peopleNowFn()
			inviteIsRunningFn = func(string) bool { return true }
			inviteReadPIDFn = func(string) (int, error) { return 42, nil }
			invitePIDModTimeFn = func(string) (time.Time, error) { return started, nil }
			proof := tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: started, UpdatedAt: started, RegistryFingerprint: currentRegistryFingerprint(paths.Registry), Services: []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", NodeID: "n1", RuntimeState: "running"}, {Name: "finance", Type: "proxy", NodeID: "n2", RuntimeState: "running"}}}
			if err := tsruntime.Save(paths.Snapshot, proof); err != nil {
				t.Fatal(err)
			}
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/api/v2/tailnet/-/devices" {
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos","name":"photos.tail.test.ts.net","isExternal":false},{"nodeId":"n2","hostname":"finance","name":"finance.tail.test.ts.net","isExternal":false}]}`)
					return
				}
				if r.Method == "POST" && (r.URL.Path == "/api/v2/device/n1/device-invites" || r.URL.Path == "/api/v2/device/n2/device-invites") {
					var body []map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 1 || body[0]["email"] != nil || body[0]["multiUse"] == true || body[0]["allowExitNode"] == true {
						t.Errorf("unexpected email/elevation: %+v", body)
					}
					calls++
					io.WriteString(w, `[{"id":"100","deviceId":123,"inviteUrl":"https://login.tailscale.com/admin/invite/FAKE-BEARER"}]`)
					return
				}
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer api.Close()
			t.Setenv(tailapi.APIBaseURLEnv, api.URL)
			t.Setenv("TSLINK_API_KEY", "tskey-api-FAKE-people-test")
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice@example.com", Apps: []string{"photos", "finance"}, Invite: true, PrintLinks: printLinks}, false)
			if err != nil || !result.Complete || calls != 2 {
				t.Fatal(result, err, calls)
			}
			var out bytes.Buffer
			writePeopleResult(&out, "people add", result, true)
			if strings.Contains(out.String(), "FAKE-BEARER") != printLinks {
				t.Fatal("masking failed", out.String())
			}
			if strings.Contains(out.String(), "tskey-api-FAKE-people-test") {
				t.Fatal("credential in result")
			}
			if printLinks && !strings.Contains(result.Message, "accept https://login.tailscale.com/admin/invite/FAKE-BEARER") {
				t.Fatal("guide missing link")
			}
		})
	}
}

func TestPeoplePartialInviteFailureKeepsLocalGrants(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	now := peopleNowFn()
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	reviewRefreshProof(t, paths)
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, _ string, print, multi, exit bool) (tailapi.Invite, error) {
		if !print || multi || exit {
			t.Fatal("email or elevated invite")
		}
		if target.Service == "finance" {
			return tailapi.Invite{}, registry.CodedError{Code: registry.CodeInviteAPIKeyRequired, Message: "no token"}
		}
		return tailapi.Invite{ID: "100", Kind: "device", Service: target.Service, InviteURL: "https://login.tailscale.com/admin/invite/FAKE"}, nil
	}
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || result.Complete || len(result.Invites) != 2 || result.Invites[0].Code != registry.CodeInviteAPIKeyRequired || result.Invites[1].InviteURL != "" {
		t.Fatal(result, err)
	}
	reg, _ := registry.Load(paths.Registry)
	if len(reg.People[0].Grants) != 2 {
		t.Fatal("partial invite lost local grants")
	}
	var human bytes.Buffer
	writePeopleResult(&human, "people add", result, false)
	if !strings.Contains(human.String(), "Local access saved") {
		t.Fatal(human.String())
	}
	inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
		return tailapi.Invite{}, errors.New("opaque backend error")
	}
	reviewRefreshProof(t, paths)
	result, err = changePeople(context.Background(), paths, peopleArguments{Who: "bob", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || result.Invites[0].Code != "invite_failed" {
		t.Fatal(result, err)
	}
}

func TestPeopleURLProofAndHumanOutput(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	started := peopleNowFn()
	invitePIDModTimeFn = func(string) (time.Time, error) { return started, nil }
	fingerprint := currentRegistryFingerprint(paths.Registry)
	snapshot := tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: started, UpdatedAt: started, RegistryFingerprint: fingerprint, Services: []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", RuntimeState: "running", Endpoint: inspect.EndpointView{Kind: "https", State: "exact", Display: "https://photos.tail.test.ts.net"}}}}
	if err := tsruntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	urls := peopleURLs(paths)
	if urls["photos"] != "https://photos.tail.test.ts.net" {
		t.Fatal(urls)
	}
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false)
	if err != nil || !strings.Contains(result.Message, "Open and bookmark https://photos.tail.test.ts.net") {
		t.Fatal(result, err)
	}
	if urls := peopleURLs(paths); len(urls) != 0 {
		t.Fatal("stale proof returned URL", urls)
	}
	for _, data := range []any{result, PeopleListResult{People: []PeopleView{result.Person}}, PeopleListResult{People: []PeopleView{}}, PeopleRemoveResult{Login: "alice", Revoked: true}} {
		var b bytes.Buffer
		writePeopleResult(&b, "test", data, false)
		if b.Len() == 0 {
			t.Fatal("empty human output")
		}
	}
}

func TestPeopleInputRefusalsAndReadOnlyMissing(t *testing.T) {
	paths := peopleTestPaths(t)
	for _, args := range []peopleArguments{{Who: "alice"}, {Who: "alice", Apps: []string{"photos"}, PrintLinks: true}, {Who: "alice", Apps: []string{}}, {Who: "alice", Apps: []string{"photos"}, For: ptrString("0h")}} {
		if _, err := changePeople(context.Background(), paths, args, false); err == nil {
			t.Fatal("invalid add accepted", args)
		}
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice"}, true); err == nil || !strings.Contains(err.Error(), "update requires") {
		t.Fatal(err)
	}
	if _, err := removePeople(paths.Registry, "bad login"); err == nil {
		t.Fatal("malformed login accepted")
	}
	if _, err := listPeople(sharePaths{Registry: filepath.Join(t.TempDir(), "missing")}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Registry, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := listPeople(paths); err == nil {
		t.Fatal("bad registry accepted")
	}
}

func ptrString(s string) *string { return &s }

func TestPeopleCLISuccessAndFailurePaths(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	run := func(args ...string) error {
		group := newPeopleCmd()
		var b bytes.Buffer
		group.SetOut(&b)
		group.SetErr(&b)
		group.SetArgs(args)
		return group.Execute()
	}
	for _, args := range [][]string{{"add", "alice", "--apps", "photos", "--for", "7d"}, {"list"}, {"update", "alice", "--apps", "photos,finance", "--for", "never"}, {"remove", "alice"}} {
		if err := run(args...); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, args := range [][]string{{"add", "alice", "--apps", "missing"}, {"update", "missing", "--for", "never"}, {"remove", "bad login"}} {
		if err := run(args...); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
	if err := os.WriteFile(paths.Registry, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run("list"); err == nil {
		t.Fatal("list accepted corrupt file")
	}
	bad := errors.New("path unavailable")
	inviteRegistryPathFn = func() (string, error) { return "", bad }
	for _, args := range [][]string{{"add", "alice", "--apps", "photos"}, {"update", "alice", "--for", "never"}, {"list"}, {"remove", "alice"}} {
		if err := run(args...); !errors.Is(err, bad) {
			t.Fatal("wrong path error", err)
		}
	}
	// Invalid global config path on remove is a real file-system write refusal.
	t.Setenv(config.ConfigDirEnv, filepath.Join(t.TempDir(), "not-a-dir"))
	inviteRegistryPathFn = config.RegistryPath
	p := os.Getenv(config.ConfigDirEnv)
	if err := os.WriteFile(p, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run("remove", "alice"); err == nil {
		t.Fatal("remove wrote through a non-directory")
	}
}

func TestPeopleRemainingViewAndValidationPaths(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	args := peopleArguments{Who: "alice", Apps: []string{}}
	if _, err := changePeople(context.Background(), paths, args, true); err == nil || !strings.Contains(err.Error(), "apps must not") {
		t.Fatal(err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "bad login", Apps: []string{"photos"}}, false); err == nil {
		t.Fatal("bad login accepted")
	}
	if _, err := changePeople(context.Background(), sharePaths{Registry: filepath.Join(t.TempDir(), "bad")}, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false); err == nil {
		t.Fatal("invite with no app accepted")
	}
	if err := os.WriteFile(paths.Registry, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false); err == nil {
		t.Fatal("bad registry invitation admitted")
	}
	if err := os.WriteFile(paths.Registry, []byte(`{"schema_version":1,"services":[{"name":"bad","type":"proxy","typo":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := listPeople(paths); err == nil {
		t.Fatal("invalid entry accepted")
	}
	expires := peopleNowFn()
	v := PeopleView{Login: "alice", Grants: []PeopleGrantView{{PersonGrant: registry.PersonGrant{App: "photos", ExpiresAt: &expires}, Active: false}}}
	if message := peopleMessage(v, nil, false, false); !strings.Contains(message, "access has ended") {
		t.Fatal(message)
	}
	var human bytes.Buffer
	writePeopleResult(&human, "people list", PeopleListResult{People: []PeopleView{v}}, false)
	if !strings.Contains(human.String(), "2030-01-01T00:00:00Z") {
		t.Fatal(human.String())
	}
	// Cover missing process proof with a valid snapshot, independently of stale
	// registry proof already checked by TestPeopleURLProofAndHumanOutput.
	proof := tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: expires, UpdatedAt: expires, Services: []tsruntime.ServiceSnapshot{}}
	if err := tsruntime.Save(paths.Snapshot, proof); err != nil {
		t.Fatal(err)
	}
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 0, errors.New("missing PID") }
	if urls := peopleURLs(paths); len(urls) != 0 {
		t.Fatal(urls)
	}
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return time.Time{}, errors.New("missing PID metadata") }
	if urls := peopleURLs(paths); len(urls) != 0 {
		t.Fatal(urls)
	}
}

func TestPeopleListPreservesFilePermissions(t *testing.T) {
	if !registryModeObservable {
		t.Skip("Windows does not expose Unix file permission convergence")
	}
	paths := peopleTestPaths(t)
	if err := os.Chmod(paths.Registry, 0640); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(paths.Registry)
	result, err := listPeople(paths)
	if err != nil || len(result.People) != 0 {
		t.Fatal(result, err)
	}
	after, _ := os.ReadFile(paths.Registry)
	stat, err := os.Stat(paths.Registry)
	if err != nil || stat.Mode().Perm() != 0640 || string(before) != string(after) {
		t.Fatal("read-only people list changed content or mode", err)
	}
}
