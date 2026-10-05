package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

type reviewInviteAPI struct {
	mu          sync.Mutex
	pending     map[string]string
	posts       map[string]int
	deletes     int
	failPhotos  bool
	dropOnce    string
	failDelete  string
	accepted    map[string]bool
	deleteCheck func()
}

func reviewPeopleAPI(t *testing.T, paths sharePaths) *reviewInviteAPI {
	t.Helper()
	restoreInviteCommandSeams(t)
	now := peopleNowFn()
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	a := &reviewInviteAPI{pending: map[string]string{}, posts: map[string]int{}, accepted: map[string]bool{}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/api/v2/tailnet/-/devices" {
			io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos","name":"photos.tail.test.ts.net","isExternal":false},{"nodeId":"n2","hostname":"finance","name":"finance.tail.test.ts.net","isExternal":false}]}`)
			return
		}
		for _, app := range []struct{ name, node string }{{"photos", "n1"}, {"finance", "n2"}} {
			if r.URL.Path != "/api/v2/device/"+app.node+"/device-invites" {
				continue
			}
			if r.Method == "POST" {
				if a.failPhotos && app.name == "photos" {
					http.Error(w, `{"message":"fixture failure"}`, 500)
					return
				}
				a.posts[app.name]++
				base := 1000
				if app.name == "finance" {
					base = 2000
				}
				id := fmt.Sprintf("%d", base+a.posts[app.name])
				a.pending[id] = app.name
				if a.dropOnce == app.name {
					a.dropOnce = ""
					io.WriteString(w, "[")
					return
				}
				json.NewEncoder(w).Encode([]map[string]any{{"id": id, "deviceId": 123, "inviteUrl": "https://login.tailscale.com/admin/invite/REVIEW-FAKE-" + id, "accepted": false}})
				return
			}
			if r.Method == "GET" {
				v := []map[string]any{}
				for id, name := range a.pending {
					if name == app.name {
						v = append(v, map[string]any{"id": id, "deviceId": 123, "accepted": a.accepted[id], "inviteUrl": "https://login.tailscale.com/admin/invite/REVIEW-FAKE-" + id})
					}
				}
				json.NewEncoder(w).Encode(v)
				return
			}
		}
		if r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/v2/device-invites/") {
			if a.deleteCheck != nil {
				a.deleteCheck()
			}
			id := strings.TrimPrefix(r.URL.Path, "/api/v2/device-invites/")
			if id == a.failDelete {
				http.Error(w, `{"message":"deletion failed"}`, http.StatusServiceUnavailable)
				return
			}
			a.deletes++
			delete(a.pending, id)
			io.WriteString(w, `{}`)
			return
		}
		t.Errorf("unexpected fake API route: %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(api.Close)
	t.Setenv(tailapi.APIBaseURLEnv, api.URL)
	t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-review>")
	reviewRefreshProof(t, paths)
	return a
}
func reviewRefreshProof(t *testing.T, paths sharePaths) {
	t.Helper()
	now := peopleNowFn()
	err := tsruntime.Save(paths.Snapshot, tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: now, UpdatedAt: now, RegistryFingerprint: currentRegistryFingerprint(paths.Registry), Services: []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", NodeID: "n1", RuntimeState: "running"}, {Name: "finance", Type: "proxy", NodeID: "n2", RuntimeState: "running"}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestReviewRemoveRevokesPendingInvites(t *testing.T) {
	paths := peopleTestPaths(t)
	a := reviewPeopleAPI(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice@example.com", Apps: []string{"photos", "finance"}, Invite: true}, false)
	if err != nil || !result.Complete || len(result.Invites) != 2 {
		t.Fatal("positive create control failed", result, err)
	}
	a.mu.Lock()
	before := len(a.pending)
	a.mu.Unlock()
	if before != 2 {
		t.Fatal("positive pending control", before)
	}
	revoked, err := removePeople(paths.Registry, "alice@example.com")
	if err != nil || !revoked.Revoked {
		t.Fatal(revoked, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t.Logf("created=%d remaining=%d DELETE calls=%d local revoked=%t", before, len(a.pending), a.deletes, revoked.Revoked)
	if len(a.pending) != 0 {
		t.Errorf("pending invitation links survive people remove: %d", len(a.pending))
	}
}
func TestReviewBundleRetryAvoidsDuplicateSuccesses(t *testing.T) {
	paths := peopleTestPaths(t)
	a := reviewPeopleAPI(t, paths)
	a.failPhotos = true
	args := peopleArguments{Who: "alice@example.com", Apps: []string{"photos", "finance"}, Invite: true}
	result, err := changePeople(context.Background(), paths, args, false)
	if err != nil || result.Complete || len(result.Invites) != 2 {
		t.Fatal("partial-failure control", result, err)
	}
	a.mu.Lock()
	if a.posts["finance"] != 1 || a.posts["photos"] != 0 {
		t.Fatal("unexpected original posts", a.posts)
	}
	a.failPhotos = false
	a.mu.Unlock()
	reviewRefreshProof(t, paths)
	// The 500 outcome is unknown. Owner explicitly verifies the empty photos list.
	if err := json.Unmarshal([]byte(`{"reconcile_invites":{"photos":"none"}}`), &args); err != nil {
		t.Fatal(err)
	}
	result, err = changePeople(context.Background(), paths, args, true)
	if err != nil || !result.Complete {
		t.Fatal("retry failed", result, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t.Logf("retry result complete=%t; successful creates photos=%d finance=%d; pending=%d", result.Complete, a.posts["photos"], a.posts["finance"], len(a.pending))
	if a.posts["finance"] != 1 {
		t.Errorf("already successful app invited again: finance creates=%d", a.posts["finance"])
	}
}

func TestReviewAmbiguousCreateRecoveryAvoidsDuplicate(t *testing.T) {
	paths := peopleTestPaths(t)
	a := reviewPeopleAPI(t, paths)
	a.dropOnce = "photos"
	args := peopleArguments{Who: "alice@example.com", Apps: []string{"photos", "finance"}, Invite: true}
	result, err := changePeople(context.Background(), paths, args, false)
	if err != nil || result.Complete || result.Invites[0].ID == "" || result.Invites[1].Code == "" {
		t.Fatal("ambiguous response control", result, err)
	}
	a.mu.Lock()
	before := len(a.pending)
	a.mu.Unlock()
	if before != 2 {
		t.Fatal("POST did not create both", before)
	}
	// Recovery now uses the durable people operation, not standalone invite device.
	reviewRefreshProof(t, paths)
	result, err = changePeople(context.Background(), paths, args, true)
	if err != nil {
		t.Fatal("retry failed", result, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	t.Logf("initial complete=%t failed-app code=%s id=%q; documented per-app retry creates photos=%d finance=%d", result.Complete, result.Invites[1].Code, result.Invites[1].ID, a.posts["photos"], a.posts["finance"])
	if a.posts["photos"] != 1 {
		t.Errorf("retry of app reported as failed duplicates committed invitation: %d pending photos invites", a.posts["photos"])
	}
}

func TestReviewReadOnlyPeopleListWithRunningSnapshot(t *testing.T) {
	if !registryModeObservable {
		t.Skip("requires Unix file mode semantics")
	}
	paths := peopleTestPaths(t)
	reviewPeopleAPI(t, paths)
	// Snapshot proof is prepared before changing the mode. No remote API is used.
	if err := os.Chmod(paths.Registry, 0640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(paths.Registry)
	if err != nil || before.Mode().Perm() != 0640 {
		t.Fatal("mode positive control", err)
	}
	result, err := listPeople(paths)
	if err != nil || len(result.People) != 0 {
		t.Fatal(result, err)
	}
	after, err := os.Stat(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("registry mode before=%04o after=%04o", before.Mode().Perm(), after.Mode().Perm())
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Errorf("readOnly people list changed registry permissions")
	}
}

func TestReviewAccessExplanationReflectsPeopleScope(t *testing.T) {
	paths := peopleTestPaths(t)
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice@example.com", Apps: []string{"photos"}}, false); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	svc := reg.Services[0]
	if svc.Name != "photos" || !svc.PeopleScoped {
		t.Fatal("scope positive control", svc)
	}
	allowed, authoritative := registry.PeopleAccessAt(reg, svc, "stranger@example.com", nil, peopleNowFn())
	if allowed || !authoritative {
		t.Fatal("request policy positive control", allowed, authoritative)
	}
	view, err := accessExplainResultForPath(paths.Registry, "photos")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual stranger denied=%t; diagnostic kind=%s applies=%t allow_mode=%s", !allowed, view.TSLinkLocalEnforcement.Kind, view.TSLinkLocalEnforcement.Applies, view.TSLinkLocalEnforcement.AllowList.Mode)
	if !view.TSLinkLocalEnforcement.Applies {
		t.Error("access explain denies existence of enforced people policy")
	}
}
