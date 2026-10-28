package cmd

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/registry"
	"reflect"
	"testing"
)

func TestGuestGateDoesNotBlockPeopleAdapters(t *testing.T) {
	for _, member := range []bool{true, false} {
		for _, gate := range []bool{false, true} {
			for _, adapter := range []string{"add", "people-add"} {
				t.Run(fmt.Sprintf("member=%v/gate=%v/%s", member, gate, adapter), func(t *testing.T) {
					h := newAddAllowHarness(t, member)
					h.f.login = "a@example.invalid"
					svc := registry.Service{Name: "drop-w2", Type: registry.TypeProxy, Target: "http://localhost:3003", Funnel: true, PublicAck: true, GuestGate: gate}
					if _, err := registry.Add(h.f.paths.Registry, svc); err != nil {
						t.Fatal(err)
					}
					before, err := loadPersistedService(h.f.paths.Registry, "drop-w2")
					if err != nil {
						t.Fatal(err)
					}
					never := "never"
					app := "photos"
					if adapter == "add" {
						app = "canary"
						_, err = runAddCmdOutput(t, []string{app}, map[string]string{"dir": t.TempDir(), "allow": h.f.login, "no-daemon-install": "true"})
					} else {
						_, err = changePeople(context.Background(), h.f.paths, peopleArguments{Who: h.f.login, Apps: []string{app}, For: &never, AckNever: true}, false)
					}
					if err != nil {
						t.Fatal(err)
					}
					after, err := loadPersistedService(h.f.paths.Registry, "drop-w2")
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatal("unrelated Funnel configuration changed")
					}
					person, err := readPerson(h.f.paths.Registry, h.f.login)
					if err != nil {
						t.Fatal(err)
					}
					if !registry.PersonGrantActiveAt(person, app, peopleNowFn()) {
						t.Fatal("requested grant missing")
					}
					for _, g := range person.Grants {
						if g.App == "drop-w2" {
							t.Fatal("unrequested grant created")
						}
						if g.App == app && g.ExpiresAt != nil {
							t.Fatal("permanent grant changed")
						}
					}
					posts, _ := h.f.counts()
					want := 0
					if !member {
						want = 1
					}
					if posts != want {
						t.Fatalf("invitation posts=%d want=%d", posts, want)
					}
				})
			}
		}
	}
}
