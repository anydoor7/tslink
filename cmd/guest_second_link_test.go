package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/errcode"
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

// A person granted an app before its guest gate was raised still sees the
// app's URL in people views: Funnel changes who can reach the node, not its
// address. (people add/update refuse a gated app outright with
// people_service_unsupported, so views of existing grants are the reachable
// people effect.)
func TestPeopleListShowsTheURLOfAGrantWhoseAppBecameGuestGated(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	oldClock := peopleNowFn
	peopleNowFn = func() time.Time { return now }
	t.Cleanup(func() { peopleNowFn = oldClock })
	expiry := now.Add(48 * time.Hour)
	if _, err := registry.ChangePerson(paths.Registry, "alice@example.com", []string{"photos"}, &expiry, true, false); err != nil {
		t.Fatal(err)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	createPrintedGuest(t, paths, now)
	if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost); ep.Kind != inspect.EndpointKindPublicHTTPS {
		t.Fatalf("precondition: kind = %q", ep.Kind)
	}
	list, err := listPeople(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.People) != 1 || len(list.People[0].Grants) != 1 {
		t.Fatalf("people = %+v", list.People)
	}
	if got := list.People[0].Grants[0].URL; got != "https://"+guestSecondLinkHost {
		t.Fatalf("grant URL = %q", got)
	}
}

// Pins the staleness window (review r1 F2): a create that raises the Funnel
// deadline changes the registry fingerprint, so a second --print-link issued
// before the daemon republishes fails with url_not_ready, saves nothing, and
// says how to recover. After the republish the same create succeeds. A create
// that does not extend the deadline leaves the fingerprint alone and needs no
// republish.
func TestGuestCreateBeforeTheDaemonRepublishesFailsWithARerunHint(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	createPrintedGuest(t, paths, now) // 2h: raises the gate and the deadline
	_, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
	var coded registry.CodedError
	if !errors.As(err, &coded) || coded.Code != registry.CodeURLNotReady {
		t.Fatalf("err = %v, want url_not_ready", err)
	}
	next := strings.Join(coded.Next, "\n")
	if !strings.Contains(next, "tslink url photos --wait=30s") || !strings.Contains(next, "rerun this guest create") {
		t.Fatalf("next = %q", next)
	}
	if guests, err := registry.ListGuests(paths.Registry, now); err != nil || len(guests) != 1 {
		t.Fatalf("refused create saved a grant: %d, %v", len(guests), err)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	createPrintedGuest(t, paths, now) // 2h: deadline unchanged, fingerprint unchanged
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "1h", Public: true, PrintLink: true}, now); err != nil {
		t.Fatalf("create within the existing deadline without a republish: %v", err)
	}
}

// An open (ungated) Funnel app reaches CreateGuest's conflict instead of a
// misleading url_not_ready, and nothing is saved (review r1 F5).
func TestGuestCreateOnAnOpenFunnelAppIsAConflict(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "open", Type: registry.TypeProxy, Target: "http://127.0.0.1:3001", Funnel: true, PublicAck: true}); err != nil {
		t.Fatal(err)
	}
	if ep := publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost); ep.Kind != inspect.EndpointKindHTTPS {
		t.Fatalf("precondition: photos kind = %q", ep.Kind)
	}
	if got := peopleURLs(paths)["open"]; got != "https://"+guestSecondLinkHost {
		t.Fatalf("precondition: open app has no exact URL: %q", got)
	}
	_, err := createGuest(paths, guestArguments{App: "open", For: "2h", Public: true, PrintLink: true}, now)
	if code, ok := registry.ErrorCode(err); !ok || code != errcode.Conflict {
		t.Fatalf("err = %v, want conflict", err)
	}
	if guests, err := registry.ListGuests(paths.Registry, now); err != nil || len(guests) != 0 {
		t.Fatalf("conflict saved a grant: %d, %v", len(guests), err)
	}
}
