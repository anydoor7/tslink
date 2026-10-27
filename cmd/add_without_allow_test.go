package cmd

import (
	"context"
	"io"
	"testing"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// `tslink add NAME --proxy P` without --allow: the access it leaves behind
// depends only on whether the registry's gateway is on. These tests record
// that, so a wrapper that relies on it (the portal owner and admins reach a
// people-scoped app without a grant, see
// internal/server TestAppNodeListenerAdmitsPortalOwnerAndAdminsWithoutAGrant)
// finds it pinned.
func TestAddWithoutAllowAccessFollowsTheGatewayMode(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(t *testing.T, f *gatewayInviteFixture)
		wantScoped bool
		wantRoute  bool
		wantMode   string
	}{
		{"gateway on", func(*testing.T, *gatewayInviteFixture) {}, true, true, "people"},
		{"gateway turned off", func(t *testing.T, f *gatewayInviteFixture) {
			if err := registry.DisablePortal(f.paths.Registry); err != nil {
				t.Fatal(err)
			}
		}, false, false, "all_tailnet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			restoreShareSeams(t)
			oldEnsure, oldReg := ensureDaemonFn, registryPathFn
			t.Cleanup(func() { ensureDaemonFn, registryPathFn = oldEnsure, oldReg })
			ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
			registryPathFn = func() (string, error) { return f.paths.Registry, nil }
			tc.prepare(t, f)

			if _, err := runAddCmdOutput(t, []string{"notes"}, map[string]string{"proxy": "localhost:3001"}); err != nil {
				t.Fatal(err)
			}
			svc, err := loadPersistedService(f.paths.Registry, "notes")
			if err != nil {
				t.Fatal(err)
			}
			if svc.PeopleScoped != tc.wantScoped || (svc.PortalRoute != nil) != tc.wantRoute || len(svc.AllowedUsers) != 0 {
				t.Fatalf("notes = people_scoped %v route %v allow %v, want scoped %v route %v and no allow entries", svc.PeopleScoped, svc.PortalRoute, svc.AllowedUsers, tc.wantScoped, tc.wantRoute)
			}
			if mode := inspect.ServiceViewFor(svc).Allow.Mode; mode != tc.wantMode {
				t.Fatalf("list allow.mode = %q, want %q", mode, tc.wantMode)
			}
		})
	}
}

// An installation that predates the gateway has a registry and no portal. The
// gateway default applies to a new installation only: adding an app there
// leaves it open to the tailnet, as it was.
func TestAddWithoutAllowOnARegistryWithoutAPortal(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreShareSeams(t)
	oldEnsure, oldReg := ensureDaemonFn, registryPathFn
	t.Cleanup(func() { ensureDaemonFn, registryPathFn = oldEnsure, oldReg })
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	registryPathFn = func() (string, error) { return paths.Registry, nil }
	if _, err := runAddCmdOutput(t, []string{"notes"}, map[string]string{"proxy": "localhost:3001"}); err != nil {
		t.Fatal(err)
	}
	svc, err := loadPersistedService(paths.Registry, "notes")
	if err != nil {
		t.Fatal(err)
	}
	if reg, _ := registry.Load(paths.Registry); reg.Portal != nil {
		t.Fatalf("add created a portal in an existing registry: %+v", reg.Portal)
	}
	if svc.PeopleScoped || svc.PortalRoute != nil || len(svc.AllowedUsers) != 0 {
		t.Fatalf("notes = %+v, want an open tailnet app", svc)
	}
	if mode := inspect.ServiceViewFor(svc).Allow.Mode; mode != "all_tailnet" {
		t.Fatalf("list allow.mode = %q, want all_tailnet", mode)
	}
}
