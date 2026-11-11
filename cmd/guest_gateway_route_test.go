package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
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
		guestGatewaySetup(t)
		var out bytes.Buffer
		c := newGuestCmd()
		c.SetArgs([]string{"create", "photos", "--for", "3d", "--public"})
		c.SetOut(&out)
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "Removed photos from the home gateway (port 8443): guest-gated apps cannot be routed. You still reach it at its own URL (tslink url photos).") || !strings.Contains(out.String(), "Uploads through photos are capped at 32 MiB (33554432 bytes) per request; larger uploads are refused with HTTP 413. Raise it with: tslink add photos --proxy http://127.0.0.1:3000 --max-request-body <value>") {
			t.Fatalf("human output = %q", out.String())
		}
	})
	t.Run("mcp", func(t *testing.T) {
		paths, _ := guestGatewaySetup(t)
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
		}, "tslink add photos --proxy http://127.0.0.1:3000", "no address"},
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
	if elapsed := time.Since(start); elapsed < guestURLWait || elapsed > guestURLWait+2*time.Second {
		t.Fatalf("elapsed = %v, want about %v", elapsed, guestURLWait)
	}
	inviteIsRunningFn = func(string) bool { return false }
	guestURLWait = time.Hour
	start = time.Now()
	if _, err := createGuest(paths, guestArguments{App: "photos", For: "3d", Public: true, PrintLink: true}, now); err == nil {
		t.Fatal("stopped daemon printed a link")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stopped daemon waited %v", elapsed)
	}
}

// Nothing re-adds a route to a gated app: people grants and portal route add
// are refused, and a repeated add keeps the app off the gateway.
func TestNothingRoutesAGuestGatedApp(t *testing.T) {
	paths, now := guestGatewaySetup(t)
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
	if _, err := registry.AddWithOutcome(paths.Registry, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}, registry.AddOptions{InitializeGateway: true, PriorInstall: func() (bool, error) { return false, nil }, PreserveUnspecified: true, HumanAccess: true, Now: now}); err != nil {
		t.Fatalf("repeated add on the gated app: %v", err)
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
// install counts as new: add, then guest create, succeeds in one run each.
func TestGuestCreateFreshInstallCompiledSequence(t *testing.T) {
	binary := compiledTSLinkBinary(t)
	dir := t.TempDir()
	env := e2eEnv(dir, "HOME="+t.TempDir(), "XDG_CONFIG_HOME="+t.TempDir())
	if r := e2eRunBinary(t, binary, dir, "", env, "add", "photos", "--proxy", "localhost:3000", "--no-daemon-install", "--json"); r.ExitCode != 0 {
		t.Fatalf("add: %s %s", r.Stdout, r.Stderr)
	}
	reg, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || !registry.GatewayEnabled(reg) || reg.Services[0].PortalRoute == nil {
		t.Fatalf("precondition: fresh add has no gateway route: %v", err)
	}
	r := e2eRunBinary(t, binary, dir, "", env, "guest", "create", "photos", "--for", "3d", "--label", "Aunt May", "--public", "--json")
	e, data := e2eDecodeEnvelope(t, r, "guest create")
	if !e.OK || r.ExitCode != 0 {
		t.Fatalf("guest create: %s %s", r.Stdout, r.Stderr)
	}
	removed, _ := data["removed_gateway_route"].(map[string]any)
	if removed == nil || removed["port"] != float64(8443) {
		t.Fatalf("removed_gateway_route = %v", data["removed_gateway_route"])
	}
	reg, err = registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || reg.Services[0].PortalRoute != nil || !reg.Services[0].GuestGate || len(reg.Guests) != 1 {
		t.Fatalf("registry after create: %v %+v", err, reg.Services)
	}
	// Raising the request-body limit on the gated app is an ordinary add.
	if r := e2eRunBinary(t, binary, dir, "", env, "add", "photos", "--proxy", "localhost:3000", "--max-request-body", "1GiB", "--no-daemon-install", "--json"); r.ExitCode != 0 {
		t.Fatalf("limit change on the gated app: %s %s", r.Stdout, r.Stderr)
	}
	reg, err = registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || reg.Services[0].PortalRoute != nil || !reg.Services[0].GuestGate || reg.Services[0].RequestLimits == nil || reg.Services[0].RequestLimits.MaxBody != "1GiB" {
		t.Fatalf("registry after limit change: %v %+v", err, reg.Services)
	}
	// remove ends the links; the re-added app starts with no live guest link.
	if r := e2eRunBinary(t, binary, dir, "", env, "remove", "photos", "--json"); r.ExitCode != 0 {
		t.Fatalf("remove: %s %s", r.Stdout, r.Stderr)
	}
	reg, err = registry.Load(filepath.Join(dir, "registry.json"))
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
