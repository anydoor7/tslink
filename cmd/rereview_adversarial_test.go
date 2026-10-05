package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// Separate process, production state transitions, and immediate process death.
// The parent owns the server and registry, so neither disappears with the child.
func TestReReviewCrashHelper(t *testing.T) {
	mode := os.Getenv("RR_CRASH_MODE")
	if mode == "" {
		return
	}
	dir := os.Getenv("RR_CRASH_DIR")
	paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "pid"), Snapshot: filepath.Join(dir, "runtime.json")}
	t.Setenv(config.ConfigDirEnv, dir)
	t.Setenv(tailapi.APIBaseURLEnv, os.Getenv("RR_CRASH_API"))
	t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-review2>")
	restoreInviteCommandSeams(t)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	peopleNowFn = func() time.Time { return now }
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || len(result.Invites) != 1 {
		t.Fatal(result, err)
	}
	fmt.Printf("DURABLE:%s\n", result.Invites[0].State)
	os.Exit(73)
}

func TestReReviewCrashStateMatrix(t *testing.T) {
	for _, mode := range []string{"pending", "sending-before-post", "sending-after-post", "sending-after-post-remove", "unknown", "complete"} {
		t.Run(mode, func(t *testing.T) {
			paths := peopleTestPaths(t)
			// Install standard clock/PID seams for parent recovery.
			reviewPeopleAPI(t, paths)
			var mu sync.Mutex
			posts := 0
			pending := map[string]bool{}
			reached := make(chan struct{}, 1)
			blocked := true
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				block := blocked
				mu.Unlock()
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices") {
					if block && mode == "pending" {
						http.Error(w, "refused before POST", http.StatusServiceUnavailable)
						return
					}
					if block && mode == "sending-before-post" {
						reached <- struct{}{}
						<-r.Context().Done()
						return
					}
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
					return
				}
				if r.Method == "POST" {
					var body []map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body) != 1 || body[0]["email"] != nil {
						t.Error("link mode sent email", body)
					}
					mu.Lock()
					posts++
					id := fmt.Sprint(1000 + posts)
					pending[id] = true
					mu.Unlock()
					if block && strings.HasPrefix(mode, "sending-after-post") {
						reached <- struct{}{}
						<-r.Context().Done()
						return
					}
					if block && mode == "unknown" {
						io.WriteString(w, "[")
						return
					}
					fmt.Fprintf(w, `[{"id":%q,"inviteUrl":"https://login.tailscale.com/admin/invite/FAKE-REVIEW2"}]`, id)
					return
				}
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/device-invites") {
					mu.Lock()
					defer mu.Unlock()
					data := []map[string]any{}
					for id := range pending {
						data = append(data, map[string]any{"id": id, "accepted": false, "inviteUrl": "https://login.tailscale.com/admin/invite/FAKE-REVIEW2"})
					}
					json.NewEncoder(w).Encode(data)
					return
				}
				if r.Method == "DELETE" {
					mu.Lock()
					delete(pending, strings.TrimPrefix(r.URL.Path, "/api/v2/device-invites/"))
					mu.Unlock()
					io.WriteString(w, `{}`)
					return
				}
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				http.Error(w, "bad", 500)
			}))
			defer api.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReReviewCrashHelper$", "-test.count=1")
			child.Env = append(os.Environ(), "RR_CRASH_MODE="+mode, "RR_CRASH_DIR="+filepath.Dir(paths.Registry), "RR_CRASH_API="+api.URL)
			var log bytes.Buffer
			child.Stdout = &log
			child.Stderr = &log
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "sending-") {
				select {
				case <-reached:
				case <-ctx.Done():
					_ = child.Process.Kill()
					t.Fatal("child never reached boundary", log.String())
				}
				p, e := readPerson(paths.Registry, "alice")
				if e != nil || len(p.Invites) != 1 || p.Invites[0].State != registry.PersonInviteSending {
					t.Fatal(p, e)
				}
				if mode == "sending-after-post-remove" {
					removal := make(chan struct {
						result PeopleRemoveResult
						err    error
					}, 1)
					go func() {
						result, err := removePeople(paths.Registry, "alice")
						removal <- struct {
							result PeopleRemoveResult
							err    error
						}{result, err}
					}()
					var removed PeopleRemoveResult
					select {
					case got := <-removal:
						removed, e = got.result, got.err
					case <-time.After(5 * time.Second):
						_ = child.Process.Kill()
						t.Fatal("local remove blocked on child POST")
					}
					if e != nil || !removed.Revoked || removed.Complete || removed.Cleanup[0].Code != "people_invite_busy" {
						t.Fatal(removed, e)
					}

					t.Logf("cross-process in-flight remove: %+v", removed)
				}
				if e := child.Process.Kill(); e != nil {
					t.Fatal(e)
				}
			}
			err := child.Wait()
			if err == nil {
				t.Fatal("child did not crash")
			}
			p, e := readPerson(paths.Registry, "alice")
			if e != nil || len(p.Invites) != 1 {
				t.Fatal(p, e, log.String())
			}
			want := mode
			if strings.HasPrefix(mode, "sending-") {
				want = registry.PersonInviteSending
			}
			if p.Invites[0].State != want {
				t.Fatalf("durable=%+v want=%s child=%s", p.Invites, want, log.String())
			}
			mu.Lock()
			before := posts
			blocked = false
			mu.Unlock()
			t.Setenv(tailapi.APIBaseURLEnv, api.URL)
			reviewRefreshProof(t, paths)
			if mode == "sending-after-post-remove" {
				removed, e := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "1001"})
				if e != nil || !removed.Complete || !removed.Revoked {
					t.Fatal(removed, e)
				}
				mu.Lock()
				final := posts
				remaining := len(pending)
				mu.Unlock()
				if final != 1 || remaining != 0 {
					t.Fatal("late invite escaped cleanup", final, remaining)
				}
				t.Logf("after child kill: cleanup=%+v posts=%d pending=%d", removed, final, remaining)
				return
			}
			result, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true}, true)
			if e != nil {
				t.Fatal(result, e)
			}
			mu.Lock()
			after := posts
			mu.Unlock()
			if want == "pending" {
				if !result.Complete || after != before+1 {
					t.Fatal("pending did not resume", result, after, before)
				}
			} else {
				if after != before {
					t.Fatal("recovery duplicated POST", before, after)
				}
				if want == "complete" {
					if !result.Complete {
						t.Fatal(result)
					}
				} else {
					if result.Complete || result.Invites[0].Code != "invite_reconciliation_required" {
						t.Fatal(result)
					}
					id := "1001"
					if before == 0 {
						id = "none"
					}
					result, e = changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": id}}, true)
					if e != nil || !result.Complete {
						t.Fatal("explicit recovery failed", result, e)
					}
				}
			}
			mu.Lock()
			final := posts
			mu.Unlock()
			if final != 1 {
				t.Fatal("expected exactly one controlled create after recovery", final)
			}
			t.Logf("state=%s crash=%v posts(before/retry/final)=%d/%d/%d result=%+v", want, err, before, after, final, result.Invites)
		})
	}
}

func TestReReviewAmbiguousLinkCandidates(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	api.dropOnce = "photos"
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || first.Complete {
		t.Fatal(first, e)
	}
	api.mu.Lock()
	api.pending["9999"] = "photos"
	api.mu.Unlock()
	reviewRefreshProof(t, paths)
	for _, id := range []string{"", "none", "9998"} {
		reconcile := map[string]string{}
		if id != "" {
			reconcile["photos"] = id
		}
		r, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: reconcile}, true)
		if e != nil || r.Complete || r.Invites[0].Code != "invite_reconciliation_required" || len(r.Invites[0].ReconcileIDs) != 2 {
			t.Fatal(r, e)
		}
	}
	r, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "1001"}}, true)
	if e != nil || !r.Complete {
		t.Fatal(r, e)
	}
	removed, e := removePeople(paths.Registry, "alice")
	if e != nil || !removed.Complete {
		t.Fatal(removed, e)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.pending) != 1 || api.pending["9999"] != "photos" || api.posts["photos"] != 1 {
		t.Fatal(api.pending, api.posts)
	}
	t.Log("ambiguous candidates never adopted automatically; explicit selected link cleaned; unrelated link retained")
}

func TestReReviewReAddLifecycleHistory(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || !first.Complete {
		t.Fatal(first, e)
	}
	removed, e := removePeople(paths.Registry, "alice")
	if e != nil || !removed.Complete {
		t.Fatal(removed, e)
	}
	reviewRefreshProof(t, paths)
	second, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || !second.Complete || second.Invites[0].ID == first.Invites[0].ID {
		t.Fatal(second, e)
	}
	p, e := readPerson(paths.Registry, "alice")
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("re-add first id=%s second id=%s ledger=%+v", first.Invites[0].ID, second.Invites[0].ID, p.Invites)
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.pending) != 1 {
		t.Fatal(api.pending)
	}
	if len(p.Invites) != 2 {
		t.Errorf("terminal invitation history overwritten when re-added person is invited again: %+v", p.Invites)
	}
}

func TestReReviewExpiredInviteRecovery(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	duration := "7d"
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, For: &duration, Invite: true}, false)
	if e != nil || !first.Complete {
		t.Fatal(first, e)
	}
	api.mu.Lock()
	delete(api.pending, first.Invites[0].ID)
	api.mu.Unlock()
	reviewRefreshProof(t, paths)
	r, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, PrintLinks: true}, true)
	if e != nil || r.Complete || r.Invites[0].Code != "invite_link_unavailable" {
		t.Fatal(r, e)
	}
	recovered, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, PrintLinks: true, Reconcile: map[string]string{"photos": "none"}}, true)
	t.Logf("explicit verified absence recovery complete=%t invites=%+v err=%v", recovered.Complete, recovered.Invites, e)
	if e != nil || !recovered.Complete {
		t.Error("confirmed absent completed invite has no non-destructive recovery", recovered.Invites, e)
	}
	if !reflect.DeepEqual(first.Person.Grants, recovered.Person.Grants) {
		t.Fatal("replacement altered deadline/grants", first.Person.Grants, recovered.Person.Grants)
	}
	if len(recovered.Person.Invites) != 2 || recovered.Person.Invites[0].ID != first.Invites[0].ID || recovered.Person.Invites[0].State != registry.PersonInviteReplaced {
		t.Fatal("old attempt evidence lost", recovered.Person.Invites)
	}
}

func TestReReviewPeopleInputErrorTaxonomy(t *testing.T) {
	paths := peopleTestPaths(t)
	for _, args := range []peopleArguments{{Who: "bad login", Apps: []string{"photos"}}, {Who: "alice", Apps: []string{"missing"}}} {
		_, e := changePeople(context.Background(), paths, args, false)
		if e == nil {
			t.Fatal("bad input accepted")
		}
		envelope := output.NewFailureForError("people add", e)
		t.Logf("who=%q apps=%q exit=%d stable=%s error=%s", args.Who, args.Apps, envelope.Code, envelope.Error.Code, envelope.Error.Message)
		if envelope.Error.Code == "internal_error" {
			t.Error("expected input failure categorized as unexpected internal failure")
		}
	}
}

func TestReReviewDeletedNodeReAdd(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || !first.Complete {
		t.Fatal(first, e)
	}
	if _, e := registry.Remove(paths.Registry, "photos"); e != nil {
		t.Fatal(e)
	}
	// Simulate the ordinary successful device deletion performed by app removal.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n2","hostname":"finance"}]}`)
			return
		}
		t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", 500)
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	removed, e := removePeople(paths.Registry, "alice")
	if e != nil || !removed.Revoked {
		t.Fatal(removed, e)
	}
	retry, e := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "none"})
	if e != nil {
		t.Fatal(retry, e)
	}
	if !removed.Complete || !retry.Complete {
		t.Fatal("confirmed deleted target did not finish", removed, retry)
	}
	p, e := readPerson(paths.Registry, "alice")
	if e != nil || len(p.Invites) != 1 || p.Invites[0].ID != first.Invites[0].ID || p.Invites[0].NodeID != "n1" || p.Invites[0].State != registry.PersonInviteTargetGone {
		t.Fatal("deleted node evidence lost", p, e)
	}
	second, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"finance"}}, false)
	t.Logf("remove=%+v retry=%+v readd=%+v err=%v", removed, retry, second, e)
	if e != nil {
		t.Error("deleted remote node leaves permanent cleanup barrier for re-add", e)
	}
}

func TestReReviewManifestRemoveFields(t *testing.T) {
	fields := commandJSONResultFields("tslink people remove")
	t.Logf("manifest fields=%+v", fields)
	for _, name := range []string{"complete", "cleanup"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("manifest omits mandatory removal result field %s", name)
		}
	}
}

func TestReReviewMCPContracts(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	api.dropOnce = "photos"
	actions := defaultMCPActions(paths, io.Discard)
	rows := []map[string]any{}
	calls := []struct{ name, args string }{
		{"people_add", `{"who":"alice","apps":["photos"],"invite":true}`},
		{"people_list", `{}`},
		{"access_explain", `{"service":"photos"}`},
		{"people_update", `{"who":"alice","invite":true}`},
		{"people_remove", `{"who":"alice"}`},
		{"people_remove", `{"who":"alice","reconcile_invites":{"photos":"1001"}}`},
	}
	for _, c := range calls {
		reviewRefreshProof(t, paths)
		res, e := callMCPTool(context.Background(), actions, c.name, json.RawMessage(c.args))
		if e != nil || res.IsError {
			t.Fatal(c.name, res, e)
		}
		payload := mcpResultStructured(t, res)
		validateAgainstToolOutputSchema(t, c.name, payload)
		rows = append(rows, map[string]any{"tool": c.name, "input": json.RawMessage(c.args), "input_schema": mcpToolByName(t, c.name).InputSchema, "payload": payload, "output_schema": mcpToolByName(t, c.name).OutputSchema, "annotations": mcpToolHints[c.name]})
	}
	hints := mcpToolHints["people_remove"]
	if hints.OpenWorldHint == nil || !*hints.OpenWorldHint || !hints.IdempotentHint || hints.ReadOnlyHint {
		t.Fatal("wrong removal annotations", hints)
	}
	for _, c := range []struct{ name, args string }{{"people_add", `{"who":"alice","apps":["photos"],"multi_use":true}`}, {"people_update", `{"who":"alice","invite":false}`}} {
		res, e := callMCPTool(context.Background(), actions, c.name, json.RawMessage(c.args))
		if e != nil || !res.IsError {
			t.Fatal("invalid input accepted", c, res, e)
		}
	}
	data, e := json.Marshal(rows)
	if e != nil {
		t.Fatal(e)
	}
	t.Log("RR_MCP_CONTRACT=" + string(data))
}

func TestPeopleExplicitReplaceCLIAndMCP(t *testing.T) {
	for _, kind := range []string{"cli", "mcp"} {
		t.Run(kind, func(t *testing.T) {
			paths := peopleTestPaths(t)
			api := reviewPeopleAPI(t, paths)
			duration := "7d"
			first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, For: &duration, Invite: true}, false)
			if err != nil || !first.Complete {
				t.Fatal(first, err)
			}
			id := ""
			for _, inv := range first.Invites {
				if inv.App == "photos" {
					id = inv.ID
				}
			}
			// Grants use independent deadlines: test preservation of the complete set.
			reg, _, err := registry.Preflight(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			deadline := peopleNowFn().Add(2 * time.Hour)
			reg.People[0].Grants[1].ExpiresAt = &deadline
			data, err := json.Marshal(reg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.Registry, data, 0600); err != nil {
				t.Fatal(err)
			}
			before := reg.People[0].Grants
			api.mu.Lock()
			delete(api.pending, id)
			api.mu.Unlock()
			for retry := 0; retry < 2; retry++ {
				reviewRefreshProof(t, paths)
				if kind == "cli" {
					inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
					invitePIDPathFn = func() (string, error) { return paths.PID, nil }
					inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
					c := newPeopleCmd()
					c.SetOut(io.Discard)
					c.SetErr(io.Discard)
					c.SetArgs([]string{"update", "alice", "--invite", "--replace-invite", "photos=" + id})
					if err := c.Execute(); err != nil {
						t.Fatal(err)
					}
				} else {
					res, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "people_update", json.RawMessage(fmt.Sprintf(`{"who":"alice","invite":true,"replace_invites":{"photos":%q}}`, id)))
					if err != nil || res.IsError {
						t.Fatal(res, err)
					}
					payload := mcpResultStructured(t, res)
					validateAgainstToolOutputSchema(t, "people_update", payload)
					if payload["complete"] != true {
						t.Fatal(payload)
					}
				}
			}
			current, err := readPerson(paths.Registry, "alice")
			if err != nil || !reflect.DeepEqual(current.Grants, before) || len(current.Invites) != 3 {
				t.Fatal(current, err)
			}
			if current.Invites[1].State != registry.PersonInviteReplaced || current.Invites[1].ID != id || current.Invites[1].NodeID != "n1" || current.Invites[2].Attempt != 1 {
				t.Fatal("replacement evidence lost", current)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.posts["photos"] != 2 || api.posts["finance"] != 1 || api.deletes != 0 {
				t.Fatal("replacement retry duplicated or changed other app", api.posts, api.deletes)
			}
		})
	}
}

func TestPeopleReplaceRefusesLiveOrWrongAttempt(t *testing.T) {
	for _, kind := range []string{"pending", "accepted", "wrong-id", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			paths := peopleTestPaths(t)
			api := reviewPeopleAPI(t, paths)
			if kind == "unknown" {
				api.dropOnce = "photos"
			}
			first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
			if err != nil {
				t.Fatal(first, err)
			}
			id := "1001"
			if kind == "wrong-id" {
				id = "9999"
			}
			if kind == "accepted" {
				api.mu.Lock()
				api.accepted["1001"] = true
				api.mu.Unlock()
			}
			reviewRefreshProof(t, paths)
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": id}}, true)
			if err != nil || result.Complete || result.Invites[0].Code != "conflict" {
				t.Fatal(result, err)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.posts["photos"] != 1 || api.deletes != 0 {
				t.Fatal("unsafe replacement mutated remote", api.posts, api.deletes)
			}
		})
	}
}

func TestPeopleReplaceValidationAndTaxonomy(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false); err != nil {
		t.Fatal(err)
	}
	for _, args := range []peopleArguments{
		{Who: "alice", Replace: map[string]string{"photos": "1001"}},
		{Who: "alice", Invite: true, Apps: []string{"photos"}, Replace: map[string]string{"photos": "1001"}},
		{Who: "alice", Invite: true, Replace: map[string]string{"photos": "none"}},
		{Who: "alice", Invite: true, Replace: map[string]string{"bad/": "1001"}},
		{Who: "alice", Invite: true, Replace: map[string]string{"photos": "1001"}, Reconcile: map[string]string{"photos": "none"}},
	} {
		reviewRefreshProof(t, paths)
		_, err := changePeople(context.Background(), paths, args, true)
		if err == nil || output.NewFailureForError("people update", err).Code != output.ExitUsage {
			t.Fatal("replacement validation did not report usage", args, err)
		}
	}
	cases := []struct {
		tool string
		args string
		code string
		exit int
	}{
		{"people_add", `{"who":"bad login","apps":["photos"]}`, "usage_error", 2},
		{"people_add", `{"who":"bob","apps":["missing"]}`, "not_found", 5},
		{"people_update", `{"who":"missing","for":"never","ack_never":true}`, "not_found", 5},
		{"people_add", `{"who":"alice","apps":["photos"]}`, "conflict", 4},
		{"people_remove", `{"who":"bad login"}`, "usage_error", 2},
	}
	for _, tc := range cases {
		res, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), tc.tool, json.RawMessage(tc.args))
		if err != nil || !res.IsError {
			t.Fatal(tc, res, err)
		}
		failure := decodeMCPFailureText(t, mcpResultText(t, res))
		if failure["code"] != tc.code {
			t.Fatal(tc, failure)
		}
		var args peopleArguments
		if err := json.Unmarshal([]byte(tc.args), &args); err != nil {
			t.Fatal(err)
		}
		var cliErr error
		if tc.tool == "people_remove" {
			_, cliErr = removePeople(paths.Registry, args.Who)
		} else {
			_, cliErr = changePeople(context.Background(), paths, args, tc.tool == "people_update")
		}
		envelope := output.NewFailureForError("people", cliErr)
		if envelope.Code != tc.exit || envelope.Error.Code != tc.code {
			t.Fatal(tc, envelope)
		}
	}
}

func TestPeopleReplacementUnknownSuccessorRequiresReconciliation(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	id := first.Invites[0].ID
	api.mu.Lock()
	delete(api.pending, id)
	api.dropOnce = "photos"
	api.mu.Unlock()
	args := peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": id}}
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, args, true)
	if err != nil || result.Complete || result.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal(result, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || len(p.Invites) != 2 || p.Invites[0].ID != id || p.Invites[0].State != registry.PersonInviteReplaced {
		t.Fatal(p, err)
	}
	reviewRefreshProof(t, paths)
	result, err = changePeople(context.Background(), paths, args, true)
	if err != nil || result.Complete || result.Invites[0].Code != "invite_reconciliation_required" {
		t.Fatal("replacement retry bypassed ambiguity", result, err)
	}
	args.Replace = nil
	args.Reconcile = map[string]string{"photos": "1002"}
	reviewRefreshProof(t, paths)
	result, err = changePeople(context.Background(), paths, args, true)
	if err != nil || !result.Complete || result.Invites[0].ID != "1002" {
		t.Fatal(result, err)
	}
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil || !removed.Complete || removed.Cleanup[0].State != registry.PersonInviteReplaced || removed.Cleanup[1].State != registry.PersonInviteRevoked {
		t.Fatal(removed, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts["photos"] != 2 || len(api.pending) != 0 {
		t.Fatal("successor duplicated or escaped cleanup", api.posts, api.pending)
	}
}

func TestPeopleReplacementListingFailuresPreserveAttempt(t *testing.T) {
	for _, kind := range []string{"transient", "partial", "ownership", "retirement-write"} {
		t.Run(kind, func(t *testing.T) {
			paths := peopleTestPaths(t)
			reviewPeopleAPI(t, paths)
			first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
			if err != nil || !first.Complete {
				t.Fatal(first, err)
			}
			prior, err := readPerson(paths.Registry, "alice")
			if err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("replacement failure caused mutation %s", r.Method)
					http.Error(w, "bad", 500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/devices") {
					if kind == "ownership" {
						io.WriteString(w, `{"devices":[{"nodeId":"foreign","hostname":"photos"}]}`)
					} else {
						io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
					}
					return
				}
				if kind == "transient" {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				if kind == "partial" {
					io.WriteString(w, "[")
					return
				}
				if kind == "retirement-write" {
					if err := os.Remove(paths.Registry + ".lock"); err != nil {
						t.Error(err)
					}
					if err := os.Mkdir(paths.Registry+".lock", 0700); err != nil {
						t.Error(err)
					}
				}
				io.WriteString(w, "[]")
			}))
			defer api.Close()
			t.Setenv(tailapi.APIBaseURLEnv, api.URL)
			reviewRefreshProof(t, paths)
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": first.Invites[0].ID}}, true)
			if err != nil || result.Complete || result.Invites[0].Code == "" {
				t.Fatal(result, err)
			}
			current, err := readPerson(paths.Registry, "alice")
			if err != nil || !reflect.DeepEqual(prior, current) {
				t.Fatal("failed proof rewrote operation/grants", prior, current, err)
			}
		})
	}
}

func TestPeopleReplacementConcurrentRemovalOverRealHTTP(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var mu sync.Mutex
	posts := 0
	pending := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
			return
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/device-invites") {
			mu.Lock()
			defer mu.Unlock()
			if pending {
				io.WriteString(w, `[{"id":"1002"}]`)
			} else {
				io.WriteString(w, "[]")
			}
			return
		}
		if r.Method == "POST" {
			p, err := readPerson(paths.Registry, "alice")
			if err != nil || len(p.Invites) != 2 || p.Invites[0].State != registry.PersonInviteReplaced || p.Invites[1].State != registry.PersonInviteSending {
				t.Error("POST preceded durable retirement/sending evidence", p, err)
			}
			mu.Lock()
			posts++
			pending = true
			mu.Unlock()
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, `[{"id":"1002","inviteUrl":"https://login.tailscale.com/admin/invite/FAKE-REPLACEMENT"}]`)
			return
		}
		if r.Method == "DELETE" {
			mu.Lock()
			pending = false
			mu.Unlock()
			io.WriteString(w, "{}")
			return
		}
		t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		api.Close()
	}()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	reviewRefreshProof(t, paths)
	done := make(chan PeopleResult, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": first.Invites[0].ID}}, true)
		done <- result
		errs <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement never reached POST")
	}
	removal := make(chan struct {
		result PeopleRemoveResult
		err    error
	}, 1)
	go func() {
		result, err := removePeople(paths.Registry, "alice")
		removal <- struct {
			result PeopleRemoveResult
			err    error
		}{result, err}
	}()
	var removed PeopleRemoveResult
	select {
	case got := <-removal:
		removed, err = got.result, got.err
	case <-time.After(5 * time.Second):
		t.Fatal("removal blocked behind an unreleased POST")
	}
	if err != nil || !removed.Revoked || removed.Complete || removed.Cleanup[0].Code != "people_invite_busy" {
		t.Fatal("removal blocked behind replacement", removed, err)
	}
	close(release)
	result := <-done
	err = <-errs
	if err != nil || !result.Person.Revoked || result.Complete {
		t.Fatal(result, err)
	}
	removed, err = removePeople(paths.Registry, "alice")
	if err != nil || !removed.Complete || !removed.Revoked {
		t.Fatal(removed, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || len(p.Invites) != 2 || p.Invites[0].State != registry.PersonInviteReplaced || p.Invites[1].State != registry.PersonInviteRevoked {
		t.Fatal(p, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 1 || pending {
		t.Fatal("replacement successor escaped cleanup", posts, pending)
	}
}

func TestPeopleDeletedTargetRetiresUnknownAttempt(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	api.dropOnce = "photos"
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || first.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal(first, err)
	}
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/devices") {
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		}
		io.WriteString(w, `{"devices":[]}`)
	}))
	defer empty.Close()
	t.Setenv(tailapi.APIBaseURLEnv, empty.URL)
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil || !removed.Complete || removed.Cleanup[0].State != registry.PersonInviteTargetGone {
		t.Fatal(removed, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || p.Invites[0].NodeID != "n1" || p.Invites[0].State != registry.PersonInviteTargetGone {
		t.Fatal(p, err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"finance"}}, false); err != nil {
		t.Fatal("unknown dead target blocked re-add", err)
	}
}

func TestPeopleReplaceDeadlineBetweenRetirementAndPending(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	duration := "1h"
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, For: &duration, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	api.mu.Lock()
	delete(api.pending, first.Invites[0].ID)
	api.mu.Unlock()
	base := peopleNowFn()
	peopleNowFn = func() time.Time {
		p, err := readPerson(paths.Registry, "alice")
		if err == nil && len(p.Invites) > 0 && p.Invites[0].State == registry.PersonInviteReplaced {
			return base.Add(time.Hour)
		}
		return base
	}
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": first.Invites[0].ID}}, true)
	if err != nil || result.Complete || result.Invites[0].Code != "conflict" {
		t.Fatal("expired grant sent successor", result, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || len(p.Invites) != 1 || p.Invites[0].State != registry.PersonInviteReplaced || p.Grants[0].ExpiresAt == nil {
		t.Fatal("retirement/deadline lost", p, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts["photos"] != 1 {
		t.Fatal("deadline failure caused POST", api.posts)
	}
}

func TestPeopleReconcileSaveRefusesConcurrentRevocation(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	op := registry.PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: registry.PersonInviteUnknown}
	if err := registry.SavePersonInvite(paths.Registry, "alice", op); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("revocation allowed remote mutation")
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
		} else {
			if _, err := registry.RemovePerson(paths.Registry, "alice"); err != nil {
				t.Error(err)
			}
			io.WriteString(w, "[]")
		}
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "none"}}, true)
	if err != nil || result.Complete || !result.Person.Revoked || result.Invites[0].Code != "conflict" {
		t.Fatal(result, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || p.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal(p, err)
	}
}

func TestPeopleUnknownPostStateWriteFailureKeepsSending(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected call %s", r.Method)
			return
		}
		if err := os.Remove(paths.Registry + ".lock"); err != nil {
			t.Error(err)
		}
		if err := os.Mkdir(paths.Registry+".lock", 0700); err != nil {
			t.Error(err)
		}
		io.WriteString(w, "[")
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || result.Complete || result.Invites[0].Code != "invite_state_failed" {
		t.Fatal(result, err)
	}
	p, err := readPerson(paths.Registry, "alice")
	if err != nil || p.Invites[0].State != registry.PersonInviteSending {
		t.Fatal("indeterminate POST lost durable barrier", p, err)
	}
}

func TestPeopleRecoveryCLIParsingAndInactiveReplace(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	for _, args := range [][]string{
		{"update", "alice", "--invite", "--replace-invite", "photos"},
		{"update", "alice", "--invite", "--replace-invite", "photos=1001", "--replace-invite", "photos=1001"},
		{"update", "alice", "--invite", "--reconcile-invite", "photos"},
		{"remove", "alice", "--reconcile-invite", "photos"},
	} {
		c := newPeopleCmd()
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		c.SetArgs(args)
		if err := c.Execute(); err == nil {
			t.Fatal("invalid recovery flag accepted", args)
		}
	}
	reviewRefreshProof(t, paths)
	c := newPeopleCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"update", "alice", "--invite", "--reconcile-invite", "photos=1001"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	var human bytes.Buffer
	listed, err := listPeople(paths)
	if err != nil {
		t.Fatal(err)
	}
	writePeopleResult(&human, "people list", listed, false)
	if !strings.Contains(human.String(), "invite photos id=1001 state=complete") {
		t.Fatal(human.String())
	}
	c = newPeopleCmd()
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.SetArgs([]string{"remove", "alice", "--reconcile-invite", "photos=1001"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	reviewRefreshProof(t, paths)
	for _, args := range []peopleArguments{
		{Who: "alice", Invite: true, Reconcile: map[string]string{"bad/": "1001"}},
		{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "../"}},
		{Who: "alice", Reconcile: map[string]string{"photos": "none"}},
	} {
		if _, err := changePeople(context.Background(), paths, args, true); err == nil {
			t.Fatal("invalid reconciliation accepted", args)
		}
	}
	if _, err := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false); err != nil {
		t.Fatal(err)
	}
	reviewRefreshProof(t, paths)
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"finance": "1001"}}, true); err == nil {
		t.Fatal("replacement without selected grant accepted")
	}
	// An unknown send with an explicitly verified empty list can terminate removal.
	op := registry.PersonInvite{App: "finance", Hostname: "finance", NodeID: "n2", State: registry.PersonInviteUnknown}
	if err := registry.SavePersonInvite(paths.Registry, "alice", op); err != nil {
		t.Fatal(err)
	}
	removed, err := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"finance": "none"})
	if err != nil || !removed.Complete || removed.Cleanup[len(removed.Cleanup)-1].State != registry.PersonInviteCancelled {
		t.Fatal(removed, err)
	}
}
