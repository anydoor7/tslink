package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

const guestSecondLinkHost = "photos.tail-guest.ts.net"

// publishGuestRuntime stands in for the daemon republishing runtime.json after
// a registry change. It builds the snapshot with the production builder from
// the current registry, so the endpoint kind is whatever the runtime would
// report (public_https once the guest Funnel gate exists), never a hand-picked
// fixture value.
func publishGuestRuntime(t *testing.T, paths sharePaths, now time.Time, runtimeState, host string) inspect.EndpointView {
	t.Helper()
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	var states []tsruntime.ServiceState
	for _, svc := range reg.Services {
		states = append(states, tsruntime.ServiceState{Service: svc, RuntimeHost: host, RuntimeState: runtimeState})
	}
	snapshot := tsruntime.NewSnapshot(42, now, currentRegistryFingerprint(paths.Registry), now, states)
	if err := tsruntime.Save(paths.Snapshot, snapshot); err != nil {
		t.Fatal(err)
	}
	for _, svc := range snapshot.Services {
		if svc.Name == "photos" {
			return svc.Endpoint
		}
	}
	t.Fatal("photos missing from snapshot")
	return inspect.EndpointView{}
}

func guestSecondLinkSetup(t *testing.T) (sharePaths, time.Time) {
	t.Helper()
	paths := guestCommandPaths(t)
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	return paths, now
}

func createPrintedGuest(t *testing.T, paths sharePaths, now time.Time) (registry.GuestView, string) {
	t.Helper()
	r, err := createGuest(paths, guestArguments{App: "photos", For: "2h", Public: true, PrintLink: true}, now)
	if err != nil {
		t.Fatalf("guest create --print-link: %v", err)
	}
	prefix := "https://" + guestSecondLinkHost + "/guest/"
	if r.Link == nil || !strings.HasPrefix(*r.Link, prefix) {
		t.Fatalf("link = %v, want prefix %s", r.Link, prefix)
	}
	return r.Grant, strings.TrimPrefix(*r.Link, prefix)
}

func TestGuestCreateFirstLinkUsesTailnetHTTPSEndpoint(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost); ep.Kind != inspect.EndpointKindHTTPS || ep.State != inspect.EndpointStateExact {
		t.Fatalf("precondition: endpoint before the gate = %+v, want exact https", ep)
	}
	grant, token := createPrintedGuest(t, paths, now)
	if _, reason := registry.FindGuestToken(paths.Registry, "photos", token, now); reason != "allowed" {
		t.Fatalf("first token %s: %s", grant.ID, reason)
	}
}

func TestGuestCreateAfterRevokeWhileGateIsUp(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	first, _ := createPrintedGuest(t, paths, now)
	if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost); ep.Kind != inspect.EndpointKindPublicHTTPS || ep.State != inspect.EndpointStateExact {
		t.Fatalf("precondition: endpoint with the gate up = %+v, want exact public_https", ep)
	}
	if _, err := registry.RevokeGuest(paths.Registry, first.ID, now); err != nil {
		t.Fatal(err)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	_, token := createPrintedGuest(t, paths, now)
	if _, reason := registry.FindGuestToken(paths.Registry, "photos", token, now); reason != "allowed" {
		t.Fatalf("replacement token: %s", reason)
	}
}

func TestGuestCreateTwoConcurrentLinksOnOneService(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	_, firstToken := createPrintedGuest(t, paths, now)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	_, secondToken := createPrintedGuest(t, paths, now)
	if firstToken == secondToken {
		t.Fatal("two links share one token")
	}
	for _, token := range []string{firstToken, secondToken} {
		if _, reason := registry.FindGuestToken(paths.Registry, "photos", token, now); reason != "allowed" {
			t.Fatalf("token %q: %s", token[:4], reason)
		}
	}
}

// The second-link fix must not turn url_not_ready into "anything goes": an
// endpoint without exact runtime evidence still refuses to print a link, and
// refuses before any grant is written.
func TestGuestCreateStillRefusesAnEndpointThatIsNotReady(t *testing.T) {
	cases := []struct {
		name    string
		publish func(t *testing.T, paths sharePaths, now time.Time)
	}{
		{"failed node with the gate up", func(t *testing.T, paths sharePaths, now time.Time) {
			publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeFailed, guestSecondLinkHost)
		}},
		{"no runtime host yet", func(t *testing.T, paths sharePaths, now time.Time) {
			if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, ""); ep.State == inspect.EndpointStateExact {
				t.Fatalf("precondition: endpoint without a host is exact: %+v", ep)
			}
		}},
		{"snapshot from before the gate", func(t *testing.T, paths sharePaths, now time.Time) {
			// The runtime has not republished since the first create changed
			// the registry, so the snapshot fingerprint is stale.
			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			if !reg.Services[0].GuestGate {
				t.Fatal("precondition: first create did not raise the gate")
			}
			stale := tsruntime.NewSnapshot(42, now, "stale-fingerprint", now, []tsruntime.ServiceState{{Service: reg.Services[0], RuntimeHost: guestSecondLinkHost, RuntimeState: tsruntime.ServiceRuntimeRunning}})
			if err := tsruntime.Save(paths.Snapshot, stale); err != nil {
				t.Fatal(err)
			}
		}},
		{"daemon not running", func(t *testing.T, paths sharePaths, now time.Time) {
			publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
			inviteIsRunningFn = func(string) bool { return false }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, now := guestSecondLinkSetup(t)
			publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
			createPrintedGuest(t, paths, now)
			tc.publish(t, paths, now)
			before, err := registry.ListGuests(paths.Registry, now)
			if err != nil {
				t.Fatal(err)
			}
			_, err = createGuest(paths, guestArguments{App: "photos", For: "2h", Public: true, PrintLink: true}, now)
			if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
				t.Fatalf("err = %v, want url_not_ready", err)
			}
			after, err := registry.ListGuests(paths.Registry, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("refused create wrote a grant: %d -> %d", len(before), len(after))
			}
		})
	}
}

// People grants on a guest-gated app report the node's URL too: Funnel changes
// who can reach the node, not its address.
func TestPeopleURLsIncludeAGuestGatedApp(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	createPrintedGuest(t, paths, now)
	if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost); ep.Kind != inspect.EndpointKindPublicHTTPS {
		t.Fatalf("precondition: kind = %q", ep.Kind)
	}
	if got := peopleURLs(paths)["photos"]; got != "https://"+guestSecondLinkHost {
		t.Fatalf("people URL = %q", got)
	}
}
