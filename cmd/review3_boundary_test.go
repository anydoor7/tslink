package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func r3ListingOverlay(t *testing.T, endpoint string, code int, body string) {
	t.Helper()
	base, err := url.Parse(os.Getenv(tailapi.APIBaseURLEnv))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(base)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, endpoint) {
			w.Header().Set("Content-Type", "application/json")
			if code == http.StatusPartialContent {
				w.Header().Set("Content-Range", "bytes 0-13/200")
			}
			w.WriteHeader(code)
			io.WriteString(w, body)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(tailapi.APIBaseURLEnv, srv.URL)
}

func TestReview3ReplacementRequiresCompleteListing(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"live-control", 200, `[{"id":"1001","deviceId":123,"accepted":false}]`},
		// Root null is not here: the API answers it for a device with no invites,
		// see TestReview3NullDeviceInvitesIsAbsenceEvidence.
		{"blank", 200, ``}, {"no-content", 204, ``}, {"partial-array", 206, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := peopleTestPaths(t)
			api := reviewPeopleAPI(t, paths)
			first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
			if err != nil || !first.Complete || first.Invites[0].ID != "1001" {
				t.Fatal("create control", first, err)
			}
			prior, err := readPerson(paths.Registry, "alice")
			if err != nil {
				t.Fatal(err)
			}
			r3ListingOverlay(t, "/device-invites", tc.code, tc.body)
			reviewRefreshProof(t, paths)
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": "1001"}}, true)
			current, e := readPerson(paths.Registry, "alice")
			if e != nil {
				t.Fatal(e)
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			t.Logf("result=%+v error=%v posts=%v live=%v ledger=%+v", result, err, api.posts, api.pending, current.Invites)
			if api.posts["photos"] != 1 || len(api.pending) != 1 || !reflect.DeepEqual(prior, current) || (err == nil && result.Complete) {
				t.Error("incomplete listing retired a live invite or permitted a duplicate POST")
			}
		})
	}
}

func TestReview3CleanupRequiresCompleteListing(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, body string
		code                 int
	}{
		// Root null is not here: see TestReview3NullDeviceInvitesIsAbsenceEvidence.
		{"blank-invites", "/device-invites", "", 200},
		{"partial-devices", "/devices", `{"devices":[]}`, 206},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := peopleTestPaths(t)
			api := reviewPeopleAPI(t, paths)
			first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
			if err != nil || !first.Complete {
				t.Fatal("create control", first, err)
			}
			r3ListingOverlay(t, tc.endpoint, tc.code, tc.body)
			result, err := removePeople(paths.Registry, "alice")
			api.mu.Lock()
			defer api.mu.Unlock()
			t.Logf("result=%+v error=%v live=%v deletes=%d", result, err, api.pending, api.deletes)
			if err == nil && result.Complete {
				t.Error("incomplete listing falsely completed cleanup with live invite remaining")
			}
			p, e := readPerson(paths.Registry, "alice")
			if e != nil {
				t.Fatal(e)
			}
			if !p.Revoked {
				t.Fatal("local denial missing")
			}
			if registry.PersonInviteTerminal(p.Invites[0]) {
				t.Errorf("false terminal %s", p.Invites[0].State)
			}
		})
	}
}

func TestReview3ReplacementPositiveControl(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	api.mu.Lock()
	delete(api.pending, "1001")
	api.mu.Unlock()
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": "1001"}}, true)
	if err != nil || !result.Complete || result.Invites[0].ID != "1002" {
		t.Fatal(result, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if fmt.Sprint(api.posts["photos"]) != "2" || len(api.pending) != 1 {
		t.Fatal(api.posts, api.pending)
	}
}

func TestReview3BadReplacementOrphansOldInvite(t *testing.T) {
	paths := peopleTestPaths(t)
	api := reviewPeopleAPI(t, paths)
	first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
	if err != nil || !first.Complete {
		t.Fatal(first, err)
	}
	goodAPI := os.Getenv(tailapi.APIBaseURLEnv)
	// A blank 200 body is the bad listing: root null is a valid empty one.
	r3ListingOverlay(t, "/device-invites", http.StatusOK, "")
	reviewRefreshProof(t, paths)
	result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": "1001"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(tailapi.APIBaseURLEnv, goodAPI)
	removed, err := removePeople(paths.Registry, "alice")
	if err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	t.Logf("replacement complete=%v removed=%+v posts=%v remaining=%v DELETEs=%d", result.Complete, removed, api.posts, api.pending, api.deletes)
	if removed.Complete && len(api.pending) != 0 {
		t.Error("successful later cleanup skipped live old attempt forever")
	}
}

// A device with no invites is answered with root null, not [] (observed
// 2026-10-20), so null is the same proof of absence as []. Every people flow
// that waits for that proof must end identically for both encodings; the []
// run is the control that keeps "both fail the same way" from passing.
func TestReview3NullDeviceInvitesIsAbsenceEvidence(t *testing.T) {
	type outcome struct {
		complete bool
		invites  []registry.PersonInvite
		posts    int
		deletes  int
		live     int
	}
	finish := func(t *testing.T, paths sharePaths, api *reviewInviteAPI, complete bool) outcome {
		t.Helper()
		p, err := readPerson(paths.Registry, "alice")
		if err != nil {
			t.Fatal(err)
		}
		api.mu.Lock()
		defer api.mu.Unlock()
		return outcome{complete, p.Invites, api.posts["photos"], api.deletes, len(api.pending)}
	}
	created := func(t *testing.T, body string) (sharePaths, *reviewInviteAPI) {
		t.Helper()
		paths := peopleTestPaths(t)
		api := reviewPeopleAPI(t, paths)
		first, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
		if err != nil || !first.Complete || first.Invites[0].ID != "1001" {
			t.Fatal("create control", first, err)
		}
		api.mu.Lock()
		delete(api.pending, "1001")
		api.mu.Unlock()
		r3ListingOverlay(t, "/device-invites", http.StatusOK, body)
		reviewRefreshProof(t, paths)
		return paths, api
	}
	flows := map[string]func(t *testing.T, body string) outcome{
		"replace": func(t *testing.T, body string) outcome {
			paths, api := created(t, body)
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Replace: map[string]string{"photos": "1001"}}, true)
			if err != nil {
				t.Fatal(err)
			}
			return finish(t, paths, api, result.Complete)
		},
		"cleanup": func(t *testing.T, body string) outcome {
			paths, api := created(t, body)
			result, err := removePeople(paths.Registry, "alice")
			if err != nil {
				t.Fatal(err)
			}
			return finish(t, paths, api, result.Complete)
		},
		"none-update": func(t *testing.T, body string) outcome {
			paths, api := created(t, body)
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{"photos": "none"}}, true)
			if err != nil {
				t.Fatal(err)
			}
			return finish(t, paths, api, result.Complete)
		},
		"none-remove": func(t *testing.T, body string) outcome {
			paths, api := created(t, body)
			result, err := removePeopleContext(context.Background(), paths.Registry, "alice", map[string]string{"photos": "none"})
			if err != nil {
				t.Fatal(err)
			}
			return finish(t, paths, api, result.Complete)
		},
	}
	for name, flow := range flows {
		t.Run(name, func(t *testing.T) {
			empty := flow(t, "[]")
			if !empty.complete {
				t.Fatalf("control: an empty array did not complete: %+v", empty)
			}
			if got := flow(t, "null"); !reflect.DeepEqual(got, empty) {
				t.Errorf("null listing = %+v, want the same as the empty array %+v", got, empty)
			}
		})
	}
}
