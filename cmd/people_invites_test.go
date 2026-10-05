package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func TestPeopleRemovalPartialCleanupAndRetry(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	added, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || !added.Complete {
		t.Fatal(added, err)
	}
	api.mu.Lock()
	api.failDelete = "1001"
	api.deleteCheck = func() {
		p, e := readPerson(paths.Registry, "alice")
		if e != nil || !p.Revoked || len(p.Grants) != 0 {
			t.Error("DELETE precedes local denial", p, e)
		}
	}
	api.mu.Unlock()
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil || !removed.Revoked || removed.Complete || len(removed.Cleanup) != 2 {
		t.Fatal(removed, err)
	}
	api.mu.Lock()
	if len(api.pending) != 1 || api.deletes != 1 {
		t.Error("partial cleanup control", api.pending, api.deletes)
	}
	api.failDelete = ""
	api.mu.Unlock()
	for _, isJSON := range []bool{false, true} {
		var b bytes.Buffer
		writePeopleResult(&b, "people remove", removed, isJSON)
		if isJSON {
			if !strings.Contains(b.String(), `"complete":false`) || !strings.Contains(b.String(), `"cleanup"`) {
				t.Fatal(b.String())
			}
		} else if !strings.Contains(b.String(), "Remote cleanup is deferred or incomplete") {
			t.Fatal(b.String())
		}
	}
	// Cleanup uses persisted node proof even with no PID/snapshot files.
	if err := os.Remove(paths.Snapshot); err != nil {
		t.Fatal(err)
	}
	retried, err := removePeople(paths.Registry, "alice")
	if err != nil || !retried.Complete || retried.Removed {
		t.Fatal(retried, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.pending) != 0 || api.deletes != 2 {
		t.Fatal("cleanup retry repeated finished work", api.pending, api.deletes)
	}
}

func TestPeopleRemovalWithoutTokenAndAcceptedShares(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	added, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || !added.Complete {
		t.Fatal(added, err)
	}
	savedURL := os.Getenv(tailapi.APIBaseURLEnv)
	t.Setenv(tailapi.APIBaseURLEnv, "")
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	t.Setenv("TSLINK_API_KEY", "")
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil || !removed.Revoked || removed.Complete {
		t.Fatal(removed, err)
	}
	for _, v := range removed.Cleanup {
		if v.Code != registry.CodeInviteAPIKeyRequired || v.ID == "" {
			t.Fatal("deferred association missing", v)
		}
	}
	p, e := readPerson(paths.Registry, "alice")
	if e != nil || !p.Revoked || len(p.Invites) != 2 {
		t.Fatal(p, e)
	}
	t.Setenv(tailapi.APIBaseURLEnv, savedURL)
	t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-review>")
	api.mu.Lock()
	api.accepted["1001"] = true
	api.mu.Unlock()
	removed, err = removePeople(paths.Registry, "alice")
	if err != nil || !removed.Complete {
		t.Fatal(removed, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.deletes != 1 || len(api.pending) != 1 || api.pending["1001"] != "photos" {
		t.Fatal("accepted share was deleted", api.pending, api.deletes)
	}
	if removed.Cleanup[1].State != registry.PersonInviteAccepted {
		t.Fatal(removed)
	}
}

func TestPeopleUnknownPostReconciliationAndCrashRecovery(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	api.dropOnce = "photos"
	args := peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}
	first, err := changePeople(context.Background(), paths, args, false)
	if err != nil || first.Complete || first.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal(first, err)
	}
	reviewRefreshProof(t, paths)
	args.Apps = nil
	retry, err := changePeople(context.Background(), paths, args, true)
	if err != nil || retry.Complete || retry.Invites[0].Code != "invite_reconciliation_required" || len(retry.Invites[0].ReconcileIDs) != 1 {
		t.Fatal(retry, err)
	}
	args.Reconcile = map[string]string{"photos": "none"}
	refused, err := changePeople(context.Background(), paths, args, true)
	if err != nil || refused.Complete || refused.Invites[0].Code != "invite_reconciliation_required" {
		t.Fatal(refused, err)
	}
	args.Reconcile = map[string]string{"photos": "1001"}
	args.PrintLinks = true
	resolved, err := changePeople(context.Background(), paths, args, true)
	if err != nil || !resolved.Complete || resolved.Invites[0].ID != "1001" || !strings.Contains(resolved.Invites[0].InviteURL, "REVIEW-FAKE-1001") {
		t.Fatal(resolved, err)
	}
	api.mu.Lock()
	if api.posts["photos"] != 1 {
		t.Error("reconciliation duplicated POST", api.posts)
	}
	api.mu.Unlock()
	data, e := os.ReadFile(paths.Registry)
	if e != nil || bytes.Contains(data, []byte("REVIEW-FAKE")) || bytes.Contains(data, []byte("tskey-api")) {
		t.Fatal("secret persisted", e)
	}
	// Simulate process death after sending was durably saved, before any result.
	p, e := readPerson(paths.Registry, "alice")
	if e != nil {
		t.Fatal(e)
	}
	op := p.Invites[0]
	op.ID = ""
	op.State = registry.PersonInviteSending
	// Crash fixture rewinds the disk directly; the guarded Store API now
	// correctly refuses erasing a recorded successful ID.
	reg, _, e := registry.Preflight(paths.Registry)
	if e != nil {
		t.Fatal(e)
	}
	reg.People[0].Invites[0] = op
	fixture, e := json.Marshal(reg)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(paths.Registry, fixture, 0600); e != nil {
		t.Fatal(e)
	}
	args.Reconcile = nil
	args.PrintLinks = false
	retry, err = changePeople(context.Background(), paths, args, true)
	if err != nil || retry.Complete || retry.Invites[0].Code != "invite_reconciliation_required" {
		t.Fatal("sending was retried after restart", retry, err)
	}
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil || removed.Complete || !removed.Revoked {
		t.Fatal(removed, err)
	}
	removed, err = removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "1001"})
	if err != nil || !removed.Complete || removed.Cleanup[0].ID != "1001" {
		t.Fatal(removed, err)
	}
}

func TestPeopleRemovalDuringStalledPostAndConcurrentRetry(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	entered, release := make(chan struct{}), make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
			return
		}
		if r.Method == "POST" {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, `[{"id":"1001","deviceId":123,"inviteUrl":"https://login.tailscale.com/admin/invite/FAKE"}]`)
			return
		}
		t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	done := make(chan PeopleResult, 1)
	go func() {
		v, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
		if e != nil {
			t.Error(e)
		}
		done <- v
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("POST not reached")
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	reviewRefreshProof(t, paths)
	retry, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true}, true)
	if e != nil || retry.Complete || retry.Invites[0].Code != "people_invite_busy" {
		t.Fatal(retry, e)
	}
	removed, e := removePeople(paths.Registry, "alice")
	if e != nil || !removed.Revoked || removed.Complete || removed.Cleanup[0].Code != "people_invite_busy" {
		t.Fatal(removed, e)
	}
	p, e := readPerson(paths.Registry, "alice")
	if e != nil || !p.Revoked || len(p.Grants) != 0 {
		t.Fatal("local denial blocked by POST", p, e)
	}
	close(release)
	select {
	case result := <-done:
		if !result.Person.Revoked {
			t.Fatal("stale local grant in create result", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stalled POST did not join")
	}
	p, e = readPerson(paths.Registry, "alice")
	if e != nil || p.Invites[0].ID != "1001" || !p.Revoked {
		t.Fatal("concurrent completion lost cleanup association", p, e)
	}
}

func TestPeopleInviteContextCancellationBeforeAndAfterPOST(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	entered := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result PeopleResult
	var e error
	go func() {
		defer close(done)
		result, e = changePeople(ctx, paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("cancelled invitation operation did not finish")
		}
	}()
	// Reach POST before cancellation; load must not move the test into the
	// distinct pre-POST failure path.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("invitation operation did not reach POST")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled POST did not finish")
	}
	if e != nil || result.Complete || result.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal(result, e)
	}
	p, e := readPerson(paths.Registry, "alice")
	if e != nil || p.Invites[0].State != registry.PersonInviteUnknown {
		t.Fatal("cancelled POST outcome not durable", p, e)
	}
	reviewRefreshProof(t, paths)
	before, stop := context.WithCancel(context.Background())
	stop()
	result, e = changePeople(before, paths, peopleArguments{Who: "bob", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || result.Complete || result.Invites[0].State != registry.PersonInvitePending {
		t.Fatal("cancelled pre-POST operation must be retryable", result, e)
	}
}

func TestPeopleAccessExplainPeopleOverrideRedactionAndMCP(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, e := changePeople(context.Background(), paths, peopleArguments{Who: "private-alice@example.com", Apps: []string{"photos"}}, false); e != nil {
		t.Fatal(e)
	}
	for _, app := range []string{"photos", "finance"} {
		v, e := accessExplainResultForPath(paths.Registry, app)
		if e != nil || v.TSLinkLocalEnforcement.Kind != "http_people" || v.TSLinkLocalEnforcement.People.KnownLogins != 1 {
			t.Fatal(v, e)
		}
		var human bytes.Buffer
		formatAccessExplain(v, &human)
		if !strings.Contains(human.String(), "people list") || !strings.Contains(human.String(), "Known untagged") || strings.Contains(human.String(), "private-alice") {
			t.Fatal(human.String())
		}
		res, e := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "access_explain", json.RawMessage(`{"service":"`+app+`"}`))
		if e != nil || res.IsError {
			t.Fatal(res, e)
		}
		b, _ := json.Marshal(res)
		if !bytes.Contains(b, []byte("http_people")) || bytes.Contains(b, []byte("private-alice")) {
			t.Fatal(string(b))
		}
	}
	if _, e := removePeople(paths.Registry, "private-alice@example.com"); e != nil {
		t.Fatal(e)
	}
	v, e := accessExplainResultForPath(paths.Registry, "photos")
	if e != nil || v.TSLinkLocalEnforcement.People.Tombstones != 1 || v.TSLinkLocalEnforcement.People.Grants != 0 {
		t.Fatal(v, e)
	}
}

func TestPeopleInviteResultSaveFailureRequiresReconciliation(t *testing.T) {
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	var sending []byte
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices") {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
			return
		}
		if r.Method == "GET" {
			io.WriteString(w, `[{"id":"1001","deviceId":123}]`)
			return
		}
		if r.Method == "POST" {
			var e error
			sending, e = os.ReadFile(paths.Registry)
			if e != nil {
				t.Error(e)
			}
			if !bytes.Contains(sending, []byte(`"state": "sending"`)) {
				t.Error("POST was not preceded by durable sending state")
			}
			if e := os.WriteFile(paths.Registry, []byte("partial registry body"), 0600); e != nil {
				t.Error(e)
			}
			io.WriteString(w, `[{"id":"1001","deviceId":123,"inviteUrl":"https://login.tailscale.com/admin/invite/FAKE"}]`)
			return
		}
		t.Errorf("unexpected route %s", r.Method)
	}))
	defer api.Close()
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || first.Complete || first.Invites[0].Code != "invite_state_failed" || first.Invites[0].ID != "1001" {
		t.Fatal("failed result save was hidden", first, e)
	}
	if e := os.WriteFile(paths.Registry, sending, 0600); e != nil {
		t.Fatal(e)
	}
	reviewRefreshProof(t, paths)
	retried, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true}, true)
	if e != nil || retried.Complete || retried.Invites[0].Code != "invite_reconciliation_required" {
		t.Fatal("restored unfinished operation POSTed again", retried, e)
	}
}

func TestPeopleInviteStateReadRejectsDirectoryAndMissing(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, e := readPerson(paths.Registry, "absent"); e == nil || !strings.Contains(e.Error(), "person not found") {
		t.Fatal(e)
	}
	if _, e := readPerson(t.TempDir(), "alice"); e == nil || !strings.Contains(e.Error(), "regular file") {
		t.Fatal(e)
	}
	if e := os.Remove(paths.Registry); e != nil {
		t.Fatal(e)
	}
	if _, e := readPerson(paths.Registry, "alice"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestPeopleUnknownInviteCannotAdoptAnotherPersonsID(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	api.dropOnce = "photos"
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || first.Complete {
		t.Fatal(first, e)
	}
	if _, e := registry.ChangePerson(paths.Registry, "bob", []string{"photos"}, nil, false, false); e != nil {
		t.Fatal(e)
	}
	if e := registry.SavePersonInvite(paths.Registry, "bob", registry.PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", ID: "1001", State: registry.PersonInviteComplete}); e != nil {
		t.Fatal(e)
	}
	reviewRefreshProof(t, paths)
	attempt, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "1001"}}, true)
	if e != nil || attempt.Complete || attempt.Invites[0].Code != "invite_reconciliation_conflict" {
		t.Fatal("another person's ID was adopted", attempt, e)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts["photos"] != 1 {
		t.Fatal("conflicting resolution sent a new POST", api.posts)
	}
}

func TestPeopleInviteFileFailuresAndRefusals(t *testing.T) {
	for _, tc := range []string{"missing", "inactive", "missing-node", "bad-registry-lock", "bad-operation-lock", "cleanup-lock", "cancel-save", "unneeded-reconcile"} {
		t.Run(tc, func(t *testing.T) {
			paths := peopleTestPaths(t)
			p, e := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false)
			if e != nil {
				t.Fatal(e)
			}
			target := tailapi.DeviceTarget{Service: "photos", Hostname: "photos", NodeID: "n1"}
			switch tc {
			case "missing":
				if e := os.Remove(paths.Registry); e != nil {
					t.Fatal(e)
				}
				v := resumePeopleInvite(context.Background(), paths.Registry, "alice", target, peopleArguments{})
				if v.Code != "invite_state_failed" {
					t.Fatal(v)
				}
				result := PeopleRemoveResult{Login: "alice", Complete: true}
				cleanupPeopleInvites(context.Background(), paths.Registry, &result, nil)
				if result.Complete || result.Cleanup[0].Code != "invite_state_failed" {
					t.Fatal(result)
				}
			case "inactive":
				if _, e := registry.RemovePerson(paths.Registry, "alice"); e != nil {
					t.Fatal(e)
				}
				v := resumePeopleInvite(context.Background(), paths.Registry, "alice", target, peopleArguments{})
				if v.Code != "person_grant_inactive" {
					t.Fatal(v)
				}
			case "missing-node":
				target.NodeID = ""
				v := resumePeopleInvite(context.Background(), paths.Registry, "alice", target, peopleArguments{})
				if v.Code != registry.CodeInviteOwnershipUnproven {
					t.Fatal(v)
				}
			case "bad-registry-lock":
				if e := os.Remove(paths.Registry + ".lock"); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(paths.Registry+".lock", 0700); e != nil {
					t.Fatal(e)
				}
				v := resumePeopleInvite(context.Background(), paths.Registry, "alice", target, peopleArguments{})
				if v.Code != "invite_state_failed" {
					t.Fatal(v)
				}
			case "bad-operation-lock":
				if e := os.Mkdir(paths.Registry+".people-invites.lock", 0700); e != nil {
					t.Fatal(e)
				}
				result := PeopleResult{Person: peopleView(p, nil, peopleNowFn()), Complete: true}
				resumePeopleInvites(context.Background(), paths.Registry, p, map[string]tailapi.DeviceTarget{"photos": target}, peopleArguments{}, &result)
				if result.Complete || result.Invites[0].Code != "invite_state_failed" {
					t.Fatal(result)
				}
			case "cleanup-lock", "cancel-save":
				op := registry.PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: registry.PersonInvitePending}
				if e := registry.SavePersonInvite(paths.Registry, "alice", op); e != nil {
					t.Fatal(e)
				}
				if _, e := registry.RemovePerson(paths.Registry, "alice"); e != nil {
					t.Fatal(e)
				}
				lock := paths.Registry + ".people-invites.lock"
				if tc == "cancel-save" {
					lock = paths.Registry + ".lock"
					if e := os.Remove(lock); e != nil {
						t.Fatal(e)
					}
				}
				if e := os.Mkdir(lock, 0700); e != nil {
					t.Fatal(e)
				}
				result := PeopleRemoveResult{Login: "alice", Revoked: true, Complete: true}
				cleanupPeopleInvites(context.Background(), paths.Registry, &result, nil)
				if result.Complete || result.Cleanup[0].Code != "invite_state_failed" {
					t.Fatal(result)
				}
			case "unneeded-reconcile":
				v := resumePeopleInvite(context.Background(), paths.Registry, "alice", target, peopleArguments{Reconcile: map[string]string{"photos": "1001"}})
				if v.Code != "invite_reconciliation_required" {
					t.Fatal(v)
				}
			}
		})
	}
}

func TestPeopleFinishedInvitationReuseAndUnavailableLinks(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	first, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if e != nil || !first.Complete {
		t.Fatal(first, e)
	}
	reviewRefreshProof(t, paths)
	reused, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, PrintLinks: true}, true)
	if e != nil || !reused.Complete || reused.Invites[0].ID != first.Invites[0].ID || reused.Invites[0].InviteURL == "" {
		t.Fatal(reused, e)
	}
	api.mu.Lock()
	delete(api.pending, "1001")
	api.mu.Unlock()
	missing, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, PrintLinks: true}, true)
	if e != nil || missing.Complete || missing.Invites[0].Code != "invite_link_unavailable" {
		t.Fatal(missing, e)
	}
	t.Setenv("TSLINK_API_KEY", "")
	failed, e := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, PrintLinks: true}, true)
	if e != nil || failed.Complete || failed.Invites[0].Code != "invite_reconciliation_failed" {
		t.Fatal(failed, e)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.posts["photos"] != 1 {
		t.Fatal("completed invitation re-created", api.posts)
	}
}

func TestPeopleCleanupAssociationAndResultSaveFailures(t *testing.T) {
	for _, mode := range []string{"association-save", "association-read", "result-save"} {
		t.Run(mode, func(t *testing.T) {
			paths := peopleTestPaths(t)
			if _, e := registry.ChangePerson(paths.Registry, "alice", []string{"photos"}, nil, false, false); e != nil {
				t.Fatal(e)
			}
			op := registry.PersonInvite{App: "photos", Hostname: "photos", NodeID: "n1", State: registry.PersonInviteUnknown}
			if mode == "result-save" {
				op.State = registry.PersonInviteComplete
				op.ID = "1001"
			}
			if e := registry.SavePersonInvite(paths.Registry, "alice", op); e != nil {
				t.Fatal(e)
			}
			breakLock := func() {
				if e := os.Remove(paths.Registry + ".lock"); e != nil {
					t.Error(e)
				}
				if e := os.Mkdir(paths.Registry+".lock", 0700); e != nil {
					t.Error(e)
				}
			}
			deletes := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
					breakLock()
					io.WriteString(w, `{}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/devices") {
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos"}]}`)
					return
				}
				if mode == "association-save" {
					breakLock()
				}
				if mode == "association-read" {
					if e := os.WriteFile(paths.Registry, []byte("broken concurrent writer"), 0600); e != nil {
						t.Error(e)
					}
				}
				io.WriteString(w, `[{"id":"1001"}]`)
			}))
			defer api.Close()
			t.Setenv(tailapi.APIBaseURLEnv, api.URL)
			t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-test>")
			result, e := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "1001"})
			if e != nil || !result.Revoked || result.Complete || len(result.Cleanup) != 1 {
				t.Fatal(result, e)
			}
			wantCode := "invite_state_failed"
			if mode == "association-read" {
				wantCode = "usage_error"
			}
			if result.Cleanup[0].Code != wantCode {
				t.Fatal(result)
			}
			wantDeletes := 0
			if mode == "result-save" {
				wantDeletes = 1
			}
			if deletes != wantDeletes {
				t.Fatal("DELETE crossed failed association persistence", deletes)
			}
		})
	}
}
