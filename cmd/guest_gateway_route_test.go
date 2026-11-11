package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

// guestGatewaySetup builds the state of a genuinely new installation through
// the production add transaction: InitializeGateway turns on the persistent
// home gateway and gives the new app its default route.
func guestGatewaySetup(t *testing.T) (sharePaths, time.Time) {
	t.Helper()
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	paths, err := configSharePaths()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	oldWait := guestURLWait
	guestURLWait = 0
	t.Cleanup(func() { guestURLWait = oldWait })
	if _, err := registry.AddWithOutcome(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}, registry.AddOptions{InitializeGateway: true, PriorInstall: func() (bool, error) { return false, nil }, PreserveUnspecified: true, Now: now}); err != nil {
		t.Fatal(err)
	}
	if svc := guestRouteService(t, paths); svc.PortalRoute == nil || svc.PortalRoute.Port != 8443 {
		t.Fatalf("precondition: fresh install add gave no default route: %+v", svc.PortalRoute)
	}
	return paths, now
}

func guestRouteService(t *testing.T, paths sharePaths) registry.Service {
	t.Helper()
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == "photos" {
			return svc
		}
	}
	t.Fatal("photos missing")
	return registry.Service{}
}

func guestRegistryBytes(t *testing.T, paths sharePaths) []byte {
	t.Helper()
	raw, err := os.ReadFile(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestGuestCreateOnAFreshInstallRemovesTheDefaultGatewayRoute(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	r, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Label: "Aunt May", Public: true, PrintLink: true}, now)
	if err != nil {
		t.Fatalf("first guest create on a fresh install: %v", err)
	}
	if r.Link == nil || !strings.HasPrefix(*r.Link, "https://"+guestSecondLinkHost+"/guest/") {
		t.Fatalf("link = %v", r.Link)
	}
	if r.RemovedGatewayRoute == nil || r.RemovedGatewayRoute.Port != 8443 || !strings.Contains(r.RemovedGatewayRoute.Message, "Removed photos from the home gateway (port 8443)") {
		t.Fatalf("removed route = %+v", r.RemovedGatewayRoute)
	}
	svc := guestRouteService(t, paths)
	if svc.PortalRoute != nil || !svc.GuestGate || !svc.Funnel || !svc.PeopleScoped {
		t.Fatalf("service after create = route %+v gate %v funnel %v scoped %v", svc.PortalRoute, svc.GuestGate, svc.Funnel, svc.PeopleScoped)
	}
	raw, _ := json.Marshal(output.NewSuccess("guest create", r))
	if !strings.Contains(string(raw), `"removed_gateway_route":{"port":8443,`) || !strings.Contains(string(raw), `"request_body_limit":{"max_body_bytes":33554432,`) {
		t.Fatalf("json = %s", raw)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	second, err := createGuest(paths, guestArguments{App: "photos", For: "1h", Public: true, PrintLink: true}, now)
	if err != nil {
		t.Fatalf("second link: %v", err)
	}
	if second.RemovedGatewayRoute != nil {
		t.Fatalf("second link reported a removal: %+v", second.RemovedGatewayRoute)
	}
	raw, _ = json.Marshal(output.NewSuccess("guest create", second))
	if !strings.Contains(string(raw), `"removed_gateway_route":null`) {
		t.Fatalf("json = %s", raw)
	}
}

func TestGuestCreateReportsTheRouteRemovalInHumanAndMCPOutput(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		paths, now := guestGatewaySetup(t)
		publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
		var out bytes.Buffer
		c := newGuestCmd()
		c.SetArgs([]string{"create", "photos", "--for", "3d", "--public"})
		c.SetOut(&out)
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Removed photos from the home gateway (port 8443): guest-gated apps cannot be routed. It stays reachable at its own URL https://"+guestSecondLinkHost+", which now restarts with its public Funnel listener; check tslink status or tslink doctor that Funnel came up.") || !strings.Contains(out.String(), "Uploads through photos are capped at 32 MiB (33554432 bytes) per request; larger uploads are refused with HTTP 413. Raise it with: tslink limits set photos --max-request-body <value>") {
			t.Fatalf("human output = %q", out.String())
		}
	})
	t.Run("mcp", func(t *testing.T) {
		paths, now := guestGatewaySetup(t)
		publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
		v, err := defaultMCPActions(paths, &bytes.Buffer{}).guest(context.Background(), "guest_create", guestArguments{App: "photos", For: "3d", Public: true})
		if err != nil {
			t.Fatal(err)
		}
		r, ok := v.(guestCreateResult)
		if !ok || r.RemovedGatewayRoute == nil || r.RemovedGatewayRoute.Port != 8443 {
			t.Fatalf("mcp result = %#v", v)
		}
		if guestRouteService(t, paths).PortalRoute != nil {
			t.Fatal("mcp create kept the route")
		}
	})
}

// The owner and admins keep every app through its own node, so their grants
// do not make the route someone else's dependency.
func TestGuestCreateRemovesARouteOnlyTheOwnerUses(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	if err := registry.BootstrapPortalOwner(context.Background(), paths.Registry, registry.DefaultPortalHostname, "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	expiry := now.Add(48 * time.Hour)
	if _, err := registry.ChangePerson(paths.Registry, "owner@example.com", []string{"photos"}, &expiry, true, false); err != nil {
		t.Fatal(err)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	r, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.RemovedGatewayRoute == nil || guestRouteService(t, paths).PortalRoute != nil {
		t.Fatalf("owner-only route kept: %+v", r.RemovedGatewayRoute)
	}
}

func TestGuestCreateRefusesARouteSomeoneElseDependsOn(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, paths sharePaths, now time.Time)
		next    string
		reason  string
	}{
		{"people grant", func(t *testing.T, paths sharePaths, now time.Time) {
			expiry := now.Add(48 * time.Hour)
			if _, err := registry.ChangePerson(paths.Registry, "alice@example.com", []string{"photos"}, &expiry, true, false); err != nil {
				t.Fatal(err)
			}
		}, "tslink portal route remove photos", "alice@example.com"},
		{"cross-origin route", func(t *testing.T, paths sharePaths, now time.Time) {
			if err := registry.SetPortalRoute(context.Background(), paths.Registry, "photos", &registry.PortalRoute{AllowCrossOrigin: true}); err != nil {
				t.Fatal(err)
			}
		}, "tslink portal route remove photos", "cross-origin"},
		{"allow-list entry", func(t *testing.T, paths sharePaths, now time.Time) {
			svc := guestRouteService(t, paths)
			svc.AllowedUsers = []string{"tag:kiosk"}
			if _, err := registry.AddWithOutcome(paths.Registry, svc, registry.AddOptions{Now: now}); err != nil {
				t.Fatal(err)
			}
		}, "tslink portal route remove photos", "allow-list"},
		{"portal-only app", func(t *testing.T, paths sharePaths, now time.Time) {
			svc := guestRouteService(t, paths)
			svc.PortalOnly = true
			if _, err := registry.AddWithOutcome(paths.Registry, svc, registry.AddOptions{Now: now}); err != nil {
				t.Fatal(err)
			}
		}, "tslink add photos --proxy <target>", "no address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paths, now := guestGatewaySetup(t)
			tc.arrange(t, paths, now)
			if guestRouteService(t, paths).PortalRoute == nil {
				t.Fatal("precondition: route missing")
			}
			publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
			before := guestRegistryBytes(t, paths)
			_, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
			var coded registry.CodedError
			if !errors.As(err, &coded) || coded.Code != "portal_route_guest_refused" {
				t.Fatalf("err = %v, want portal_route_guest_refused", err)
			}
			if !strings.Contains(coded.Message, tc.reason) || !strings.Contains(strings.Join(coded.Next, "\n"), tc.next) {
				t.Fatalf("refusal = %q next %q", coded.Message, coded.Next)
			}
			if !bytes.Equal(before, guestRegistryBytes(t, paths)) {
				t.Fatal("refused create changed the registry")
			}
		})
	}
}

// url_not_ready happens before the transaction: no grant, no gate and the
// route stays, so the owner's gateway address keeps working.
func TestGuestCreateURLNotReadyKeepsTheRoute(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	before := guestRegistryBytes(t, paths)
	_, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
		t.Fatalf("err = %v, want url_not_ready", err)
	}
	if !bytes.Equal(before, guestRegistryBytes(t, paths)) {
		t.Fatal("url_not_ready changed the registry")
	}
}

// A daemon that publishes the node shortly after `add` returned (add can
// return the gateway route URL before the app's own node is published) is
// waited for, within guestURLWait; nothing is written until the URL is exact.
func TestGuestCreateWaitsBoundedlyForTheNodeURL(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	guestURLWait = 5 * time.Second
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tsruntime.NewSnapshot(42, now, currentRegistryFingerprint(paths.Registry), now, []tsruntime.ServiceState{{Service: reg.Services[0], RuntimeHost: guestSecondLinkHost, RuntimeState: tsruntime.ServiceRuntimeRunning}})
	published := make(chan error, 1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		published <- tsruntime.Save(paths.Snapshot, snapshot)
	}()
	start := time.Now()
	r, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
	if saveErr := <-published; saveErr != nil {
		t.Fatal(saveErr)
	}
	if err != nil {
		t.Fatalf("create while the node publishes: %v", err)
	}
	if r.Link == nil || r.RemovedGatewayRoute == nil {
		t.Fatalf("result = %+v", r)
	}
	if elapsed := time.Since(start); elapsed >= guestURLWait {
		t.Fatalf("waited the whole bound: %v", elapsed)
	}
}

func TestGuestCreateURLWaitIsBounded(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	guestURLWait = 200 * time.Millisecond
	start := time.Now()
	_, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady {
		t.Fatalf("err = %v, want url_not_ready", err)
	}
	if elapsed := time.Since(start); elapsed < guestURLWait || elapsed > guestURLWait+5*time.Second {
		t.Fatalf("elapsed = %v, want about %v", elapsed, guestURLWait)
	}
	inviteIsRunningFn = func(string) bool { return false }
	guestURLWait = time.Hour
	start = time.Now()
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now); err == nil {
		t.Fatal("stopped daemon printed a link")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stopped daemon waited %v", elapsed)
	}
}

// Nothing re-adds a route to a gated app: people grants, portal route add and
// a repeated add are all refused; the add refusal names limits set.
func TestNothingRoutesAGuestGatedApp(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetPortalRoute(context.Background(), paths.Registry, "photos", &registry.PortalRoute{}); err == nil {
		t.Fatal("portal route add routed a gated app")
	} else if code, _ := registry.ErrorCode(err); code != "portal_route_guest_refused" {
		t.Fatalf("portal route add err = %v", err)
	}
	if _, err := changePeople(context.Background(), paths, peopleArguments{Who: "alice@example.com", Apps: []string{"photos"}}, false); err == nil {
		t.Fatal("people add granted a gated app")
	}
	_, err := registry.AddWithOutcome(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}, registry.AddOptions{InitializeGateway: true, PriorInstall: func() (bool, error) { return false, nil }, PreserveUnspecified: true, HumanAccess: true, Now: now})
	var coded registry.CodedError
	if !errors.As(err, &coded) || coded.Code != "conflict" || !strings.Contains(strings.Join(coded.Next, "\n"), "tslink limits set photos --max-request-body <value>") {
		t.Fatalf("repeated add on the gated app: %v (next %q)", err, coded.Next)
	}
	if svc := guestRouteService(t, paths); svc.PortalRoute != nil || !svc.GuestGate {
		t.Fatalf("after re-add: route %+v gate %v", svc.PortalRoute, svc.GuestGate)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "1h", Public: true, PrintLink: true}, now); err != nil {
		t.Fatalf("later create: %v", err)
	}
}

// The README sequence through the compiled binary, on an isolated HOME so the
// install counts as new. Without a daemon the app's own node has no URL, so
// the first guest link refuses to take the app off the gateway and changes
// nothing. Off the gateway, the gate goes up; add is refused (sticky) and
// names limits set, which changes only the limit; remove revokes the link.
func TestGuestCreateFreshInstallCompiledSequence(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	dir := t.TempDir()
	env := e2eEnv(dir, "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir())
	regPath := filepath.Join(dir, "registry.json")
	run := func(args ...string) (e2eRun, map[string]any, bool) {
		t.Helper()
		r := e2eRunBinary(t, binary, dir, "", env, append(args, "--json")...)
		e, data := e2eDecodeEnvelope(t, r, strings.Join(args, " "))
		return r, data, e.OK
	}
	if r, _, ok := run("add", "photos", "--proxy", "localhost:3000", "--preserve-host", "--health-path", "/healthz", "--request-read-timeout", "2m", "--no-daemon-install"); !ok {
		t.Fatalf("add: %s %s", r.Stdout, r.Stderr)
	}
	reg, err := registry.Load(regPath)
	if err != nil || !registry.GatewayEnabled(reg) || reg.Services[0].PortalRoute == nil {
		t.Fatalf("precondition: fresh add has no gateway route: %v", err)
	}
	before, _ := os.ReadFile(regPath)
	r, _, ok := run("guest", "create", "photos", "--for", "3d", "--label", "Aunt May", "--public")
	if ok || !strings.Contains(r.Stdout, `"code":"url_not_ready"`) || !strings.Contains(r.Stdout, "the route was kept") {
		t.Fatalf("guest create without a running node: %s", r.Stdout)
	}
	if after, _ := os.ReadFile(regPath); !bytes.Equal(before, after) {
		t.Fatal("refused guest create changed the registry")
	}
	if r, _, ok := run("portal", "route", "remove", "photos"); !ok {
		t.Fatalf("route remove: %s", r.Stdout)
	}
	if r, data, ok := run("guest", "create", "photos", "--for", "3d", "--public"); !ok || data["removed_gateway_route"] != nil {
		t.Fatalf("guest create off the gateway: %s", r.Stdout)
	}
	r, _, ok = run("add", "photos", "--proxy", "localhost:3000", "--max-request-body", "1GiB", "--no-daemon-install")
	if ok || !strings.Contains(r.Stdout, "tslink limits set photos --max-request-body") {
		t.Fatalf("add on the gated app: %s", r.Stdout)
	}
	gated, _ := registry.Load(regPath)
	if r, data, ok := run("limits", "set", "photos", "--max-request-body", "1GiB"); !ok || data["changed"] != true {
		t.Fatalf("limits set: %s", r.Stdout)
	}
	reg, err = registry.Load(regPath)
	if err != nil {
		t.Fatal(err)
	}
	want := gated.Services[0]
	want.RequestLimits = &registry.RequestLimits{MaxBody: "1GiB", ReadTimeout: "2m"}
	if !reflect.DeepEqual(reg.Services[0], want) {
		t.Fatalf("limits set changed more than the limit:\n got %+v\nwant %+v", reg.Services[0], want)
	}
	if r, _, ok := run("remove", "photos"); !ok {
		t.Fatalf("remove: %s %s", r.Stdout, r.Stderr)
	}
	reg, err = registry.Load(regPath)
	if err != nil || len(reg.Guests) != 1 || !reg.Guests[0].Revoked {
		t.Fatalf("guest grants after remove: %v %+v", err, reg.Guests)
	}
}

// A gated app keeps people enforcement on its private listener, so a grant
// given before the gate still admits its person; a new grant is refused with
// an error that says so and names the start-over.
func TestPeopleGrantOnAGuestGatedAppExplainsWhatStillWorks(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	expiry := now.Add(48 * time.Hour)
	if _, err := registry.ChangePerson(paths.Registry, "alice@example.com", []string{"photos"}, &expiry, true, false); err != nil {
		t.Fatal(err)
	}
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now); err != nil {
		t.Fatal(err)
	}
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	if allowed, _, _ := registry.PrivateAppAccessAt(reg, reg.Services[0], "alice@example.com", nil, now); !allowed {
		t.Fatal("existing grant lost on the private listener")
	}
	before := guestRegistryBytes(t, paths)
	_, err = changePeople(context.Background(), paths, peopleArguments{Who: "bob@example.com", Apps: []string{"photos"}}, false)
	var coded registry.CodedError
	if !errors.As(err, &coded) || coded.Code != "people_service_unsupported" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(coded.Message, "people grants given before the gate keep working") || !strings.Contains(strings.Join(coded.Next, "\n"), "tslink remove photos") {
		t.Fatalf("refusal = %q next %q", coded.Message, coded.Next)
	}
	if !bytes.Equal(before, guestRegistryBytes(t, paths)) {
		t.Fatal("refused grant changed the registry")
	}
}

// A recipe's request limits become the new service's default; explicit
// request-limit flags replace them.
func TestRecipeRequestLimitsAreTheServiceDefault(t *testing.T) {
	old := recipeLookupFn
	t.Cleanup(func() { recipeLookupFn = old })
	recipeLookupFn = func(id string) (recipes.Recipe, bool) {
		return recipes.Recipe{ID: id, RecommendedName: "uploads", DefaultTarget: "http://127.0.0.1:3923", RequestLimits: &registry.RequestLimits{MaxBody: "4GiB", ReadTimeout: "2m"}}, true
	}
	_, svc, err := recipeService(recipeRequest{RecipeID: "upload-app"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.RequestLimits == nil || svc.RequestLimits.MaxBody != "4GiB" || svc.RequestLimits.ReadTimeout != "2m" {
		t.Fatalf("recipe limits not applied: %+v", svc.RequestLimits)
	}
	_, svc, err = recipeService(recipeRequest{RecipeID: "upload-app", RequestLimits: &registry.RequestLimits{MaxBody: "1GiB"}})
	if err != nil || svc.RequestLimits == nil || svc.RequestLimits.MaxBody != "1GiB" || svc.RequestLimits.ReadTimeout != "" {
		t.Fatalf("explicit limits did not replace the recipe's: %+v %v", svc.RequestLimits, err)
	}
}

// guestAuthorityRuntime gives the authority fixture a running daemon and an
// exact runtime snapshot, so the owner control of guest_create can remove
// the route.
func guestAuthorityRuntime(t *testing.T, paths sharePaths) {
	t.Helper()
	now := time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)
	restoreInviteCommandSeams(t)
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 42, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return now, nil }
	oldWait := guestURLWait
	guestURLWait = 0
	t.Cleanup(func() { guestURLWait = oldWait })
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	var states []tsruntime.ServiceState
	for _, svc := range reg.Services {
		states = append(states, tsruntime.ServiceState{Service: svc, RuntimeHost: svc.Name + ".tail-guest.ts.net", RuntimeState: tsruntime.ServiceRuntimeRunning})
	}
	if err := tsruntime.Save(paths.Snapshot, tsruntime.NewSnapshot(42, now, currentRegistryFingerprint(paths.Registry), now, states)); err != nil {
		t.Fatal(err)
	}
}

// m5: without evidence that the app's own node runs with an exact URL, the
// first link does not take the app off the gateway, with or without
// --print-link, and nothing changes.
func TestGuestCreateKeepsTheRouteWithoutAConfirmedNode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		running bool
	}{{"daemon stopped", false}, {"node not published", true}} {
		t.Run(tc.name, func(t *testing.T) {
			paths, now := guestGatewaySetup(t)
			inviteIsRunningFn = func(string) bool { return tc.running }
			before := guestRegistryBytes(t, paths)
			_, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now)
			var coded registry.CodedError
			if !errors.As(err, &coded) || coded.Code != registry.CodeURLNotReady || !strings.Contains(coded.Message, "needs photos's own node running with an exact URL before it can take the app off the home gateway") || !strings.Contains(coded.Message, "the route was kept") {
				t.Fatalf("err = %v next %q", err, coded.Next)
			}
			if !bytes.Equal(before, guestRegistryBytes(t, paths)) {
				t.Fatal("refused create changed the registry")
			}
		})
	}
	// R2-7: evidence for an earlier service of the same name does not count.
	{
		paths, now := guestGatewaySetup(t)
		_, _, err := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{App: "photos", Value: "3d", PublicAck: true, Now: now, AllowRouteRemoval: true, EvidenceCreatedAt: guestRouteService(t, paths).CreatedAt.Add(-time.Second)})
		if code, _ := registry.ErrorCode(err); code != registry.CodeURLNotReady || guestRouteService(t, paths).PortalRoute == nil {
			t.Fatalf("stale evidence: %v", err)
		}
	}
	// The registry layer refuses on its own when the caller has no evidence.
	paths, now := guestGatewaySetup(t)
	_, _, err := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{App: "photos", Value: "3d", PublicAck: true, Now: now})
	if code, _ := registry.ErrorCode(err); code != registry.CodeURLNotReady {
		t.Fatalf("registry create without evidence: %v", err)
	}
	if guestRouteService(t, paths).PortalRoute == nil {
		t.Fatal("registry create removed the route without evidence")
	}
}

// m6: an admin who is a device-invited guest, or holds a gateway invite,
// reaches the app only through the gateway, so they count as a dependent.
func TestGuestCreateCountsAGatewayOnlyAdminAsADependent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(p *registry.Person)
	}{
		{"device-invited guest", func(p *registry.Person) { p.Guest = true }},
		{"gateway invite", func(p *registry.Person) {
			p.Invites = []registry.PersonInvite{{TargetKind: registry.InviteTargetGateway, Hostname: "home", NodeID: "node1", ID: "1001", State: registry.PersonInviteAccepted}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, now := guestGatewaySetup(t)
			reg, err := registry.Load(paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			reg.Portal.Owner, reg.Portal.Admins = "owner@example.com", []string{"partner@other.example"}
			expiry := now.Add(48 * time.Hour)
			p := registry.Person{Login: "partner@other.example", Grants: []registry.PersonGrant{{App: "photos", ExpiresAt: &expiry}}}
			tc.mutate(&p)
			reg.People = []registry.Person{p}
			reg.Services[0].PeopleScoped = true
			data, err := json.Marshal(reg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.Registry, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.Load(paths.Registry); err != nil {
				t.Fatalf("fixture registry: %v", err)
			}
			publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
			_, err = createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now)
			if code, _ := registry.ErrorCode(err); code != "portal_route_guest_refused" || !strings.Contains(err.Error(), "partner@other.example") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// m2: no hint, note or refusal repeats a proxy target, which can carry
// backend credentials.
func TestGuestAndPeopleHintsNeverRepeatTheProxyTarget(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	const secret = "s3cretpw"
	svc := registry.Service{Name: "creds", Type: registry.TypeProxy, Target: "http://svcuser:" + secret + "@127.0.0.1:3999"}
	if _, err := registry.Add(paths.Registry, svc); err != nil {
		t.Fatal(err)
	}
	r, err := createGuest(paths, guestArguments{App: "creds", For: "3d", Public: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	_, peopleErr := changePeople(context.Background(), paths, peopleArguments{Who: "bob@example.com", Apps: []string{"creds"}}, false)
	var coded registry.CodedError
	errors.As(peopleErr, &coded)
	portalOnly := registry.GuestRouteRemovable(&registry.Registry{}, registry.Service{Name: "creds", Type: registry.TypeProxy, Target: svc.Target, PortalOnly: true, PortalRoute: &registry.PortalRoute{Port: 8443}})
	for name, text := range map[string]string{"guest create": string(raw), "people refusal": coded.Message + strings.Join(coded.Next, " "), "portal-only refusal": fmt.Sprint(portalOnly, portalOnly.(registry.CodedError).Next)} {
		if strings.Contains(text, secret) || strings.Contains(text, "svcuser") {
			t.Fatalf("%s repeats the target: %s", name, text)
		}
	}
}

// corruptAuditJournal makes every later receipt write fail.
func corruptAuditJournal(t *testing.T, regPath string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(filepath.Dir(regPath), "mcp-audit.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// R2-2: a receipt that cannot be written after remove committed is a warning:
// device cleanup still runs and the command reports the removal.
func TestRemoveStillCleansUpWhenTheRevocationReceiptFails(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now); err != nil {
		t.Fatal(err)
	}
	corruptAuditJournal(t, paths.Registry)
	oldDelete := deleteDevicesFn
	t.Cleanup(func() { deleteDevicesFn = oldDelete })
	cleaned := 0
	deleteDevicesFn = func(context.Context, tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		cleaned++
		return tailapi.CleanupResult{}, tailapi.ErrNoAPIClient
	}
	r, err := removeServiceResult(paths.Registry, filepath.Join(filepath.Dir(paths.Registry), "node-ownership.json"), "photos")
	if err != nil {
		t.Fatalf("remove after commit reported failure: %v", err)
	}
	if !r.Removed || cleaned != 1 || !strings.Contains(r.AuditWarning, "audit receipt unavailable") {
		t.Fatalf("result %+v, cleanup calls %d", r, cleaned)
	}
}

// R2-2: guest create that revoked an orphan keeps its token when the receipt
// cannot be written.
func TestGuestCreateKeepsItsLinkWhenAnOrphanReceiptFails(t *testing.T) {
	paths, now := guestSecondLinkSetup(t)
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Remove(paths.Registry, "photos"); err != nil {
		t.Fatal(err)
	}
	// As an older release left it: the removed app's link unrevoked.
	raw, _ := os.ReadFile(paths.Registry)
	raw = regexp.MustCompile(`"revoked":\s*true`).ReplaceAll(raw, []byte(`"revoked": false`))
	if err := os.WriteFile(paths.Registry, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	corruptAuditJournal(t, paths.Registry)
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	r, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now)
	if err != nil {
		t.Fatalf("committed create reported failure: %v", err)
	}
	if r.Link == nil || !strings.Contains(r.AuditWarning, "audit receipt unavailable") {
		t.Fatalf("result %+v", r)
	}
	if _, reason := registry.FindGuestToken(paths.Registry, "photos", strings.TrimPrefix(*r.Link, "https://"+guestSecondLinkHost+"/guest/"), now); reason != "allowed" {
		t.Fatalf("returned link = %s", reason)
	}
}

// R2-6: an admin who reaches TSLink only through the gateway depends on the
// route even without a grant for this app (admins open every app).
func TestGuestCreateCountsAGatewayOnlyAdminWithoutAGrant(t *testing.T) {
	paths, now := guestGatewaySetup(t)
	reg, err := registry.Load(paths.Registry)
	if err != nil {
		t.Fatal(err)
	}
	reg.Portal.Owner, reg.Portal.Admins = "owner@example.com", []string{"partner@other.example"}
	reg.People = []registry.Person{{Login: "partner@other.example", Guest: true, Grants: []registry.PersonGrant{}}}
	data, _ := json.Marshal(reg)
	if err := os.WriteFile(paths.Registry, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load(paths.Registry); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	publishGuestRuntime(t, paths, now, tsruntime.ServiceRuntimeRunning, guestSecondLinkHost)
	_, err = createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true}, now)
	if code, _ := registry.ErrorCode(err); code != "portal_route_guest_refused" || !strings.Contains(err.Error(), "partner@other.example") {
		t.Fatalf("err = %v", err)
	}
}
