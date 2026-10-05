package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// Inject an ordinary concurrent extend at the clock seam immediately after
// the first guest grant commit, before the first invite-history commit.
// The injected clock is a deterministic scheduling seam; no sleeps are used.
func TestGuestPolicyAppliesDuringFirstInvitation(t *testing.T) {
	for _, name := range []string{"first_invitation_without_concurrent_extension", "first_invitation_rejects_concurrent_permanent_extension", "first_invitation_rejects_concurrent_permanent_people_update", "grant_change_before_send_fails_closed", "partial_remote_response_keeps_guest_classification"} {
		t.Run(name, func(t *testing.T) {
			paths, now := durationCommandPaths(t)
			restoreInviteCommandSeams(t)
			inviteIsRunningFn = func(string) bool { return true }
			inviteReadPIDFn = func(string) (int, error) { return 42, nil }
			invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
			if err := tsruntime.Save(paths.Snapshot, tsruntime.Snapshot{SchemaVersion: 1, DaemonPID: 42, DaemonStartedAt: now, UpdatedAt: now, RegistryFingerprint: currentRegistryFingerprint(paths.Registry), Services: []tsruntime.ServiceSnapshot{{Name: "photos", Type: "proxy", NodeID: "n1", RuntimeState: "running"}, {Name: "finance", Type: "proxy", NodeID: "n2", RuntimeState: "running"}}}); err != nil {
				t.Fatal(err)
			}
			var posts atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/api/v2/tailnet/-/devices" {
					io.WriteString(w, `{"devices":[{"nodeId":"n1","hostname":"photos","name":"photos.tail.test.ts.net","isExternal":false}]}`)
					return
				}
				if r.Method == "POST" && r.URL.Path == "/api/v2/device/n1/device-invites" {
					posts.Add(1)
					if name == "partial_remote_response_keeps_guest_classification" {
						io.WriteString(w, `[{"id":`)
						return
					}
					io.WriteString(w, `[{"id":"100","deviceId":123,"inviteUrl":"https://login.tailscale.com/admin/invite/FAKE-review"}]`)
					return
				}
				t.Errorf("unexpected REST %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer api.Close()
			t.Setenv(tailapi.APIBaseURLEnv, api.URL)
			t.Setenv("TSLINK_API_KEY", "tskey-api-<test-only-FAKE-review>")
			oldClock := peopleNowFn
			defer func() { peopleNowFn = oldClock }()
			triggered := false
			peopleNowFn = func() time.Time {
				if !triggered && name != "first_invitation_without_concurrent_extension" && name != "partial_remote_response_keeps_guest_classification" {
					triggered = true
					done := make(chan error, 1)
					go func() {
						var err error
						if name == "first_invitation_rejects_concurrent_permanent_people_update" {
							_, err = changePeople(context.Background(), paths, peopleArguments{Who: "alice", For: ptrString("never"), AckNever: true, Now: now}, true)
						} else {
							value := "never"
							if name == "grant_change_before_send_fails_closed" {
								value = "4d"
							}
							_, err = extendLifetime(paths.Registry, extendArguments{Service: "photos", Who: "alice", For: ptrString(value), AckNever: true}, now)
						}
						done <- err
					}()
					concurrentErr := <-done
					if name == "grant_change_before_send_fails_closed" {
						if concurrentErr != nil {
							t.Fatal(concurrentErr)
						}
					} else if concurrentErr == nil || !strings.Contains(concurrentErr.Error(), "only for tailnet-member") {
						t.Errorf("concurrent permanent change must reject guest policy: %v", concurrentErr)
					}
				}
				return now
			}
			result, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice", For: ptrString("3d"), Invite: true, Now: now}, true)
			if err != nil {
				t.Fatal(err)
			}
			expectComplete := name != "grant_change_before_send_fails_closed" && name != "partial_remote_response_keeps_guest_classification"
			if result.Complete != expectComplete {
				t.Fatalf("runtime chain: error=%v complete=%v posts=%d", err, result.Complete, posts.Load())
			}
			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			p := reg.People[0]
			if !registry.PersonIsGuest(p) || !p.Guest {
				t.Fatal("guest classification was not durably committed", p)
			}
			if name == "grant_change_before_send_fails_closed" {
				if posts.Load() != 0 || len(result.Invites) != 1 || result.Invites[0].Code != "conflict" || len(p.Invites) != 0 {
					t.Fatalf("changed grant sent invite: result=%+v posts=%d", result, posts.Load())
				}
			} else if name == "partial_remote_response_keeps_guest_classification" {
				if posts.Load() != 1 || len(p.Invites) != 1 || p.Invites[0].State != registry.PersonInviteUnknown {
					t.Fatalf("ambiguous outcome lost: %+v posts=%d", p, posts.Load())
				}
			} else if posts.Load() != 1 || len(p.Invites) != 1 || p.Invites[0].State != registry.PersonInviteComplete {
				t.Fatalf("no completed invite: %+v", p.Invites)
			}
			t.Logf("case=%s posts=%d history=%d permanent=%v active_after_8d=%v", name, posts.Load(), len(p.Invites), p.Grants[0].ExpiresAt == nil, registry.PersonGrantActiveAt(p, "photos", now.Add(8*24*time.Hour)))
			if p.Grants[0].ExpiresAt == nil || registry.PersonGrantActiveAt(p, "photos", now.Add(8*24*time.Hour)) {
				t.Fatal("guest policy bypass: successful first invitation has permanent grant")
			}
		})
	}
}
