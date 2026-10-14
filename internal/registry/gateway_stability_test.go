package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// re-adding an existing route with port 0 keeps its port even
// when a lower port became free.
func TestGatewayRouteReAddKeepsPort(t *testing.T) {
	path, _ := gatewayRegistry(t)
	for _, name := range []string{"a", "b"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000"}); err != nil {
			t.Fatal(err)
		}
		if err := SetPortalRoute(context.Background(), path, name, &PortalRoute{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetPortalRoute(context.Background(), path, "a", nil); err != nil {
		t.Fatal(err)
	}
	// Toggle the opt-out on b, as `portal route add b --allow-cross-origin` would.
	if err := SetPortalRoute(context.Background(), path, "b", &PortalRoute{AllowCrossOrigin: true}); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range reg.Services {
		if s.Name == "b" && s.PortalRoute.Port != 8444 {
			t.Fatalf("b moved from 8444 to %d", s.PortalRoute.Port)
		}
	}
}

func TestGatewayServiceFieldsFollowExistingFields(t *testing.T) {
	data, err := json.Marshal(Service{Name: "app", Type: TypeProxy, Target: "http://localhost:9", PortalOnly: true, PortalRoute: &PortalRoute{Port: 8443}, Upstream: "host"})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(data, []byte(`"name"`)) > bytes.Index(data, []byte(`"portal_route"`)) || bytes.Index(data, []byte(`"created_at"`)) > bytes.Index(data, []byte(`"upstream"`)) {
		t.Fatalf("new fields precede existing fields: %s", data)
	}
}

func TestGatewayInviteLiveStateUniqueness(t *testing.T) {
	for _, state := range []string{PersonInvitePending, PersonInviteSending, PersonInviteUnknown, PersonInviteComplete, PersonInviteAccepted, PersonInviteRevoked, PersonInviteCancelled, PersonInviteTargetGone, PersonInviteReplaced} {
		t.Run(state, func(t *testing.T) {
			old := PersonInvite{TargetKind: InviteTargetGateway, Hostname: "home", NodeID: "node1", State: state, ID: "1001"}
			next := PersonInvite{TargetKind: InviteTargetGateway, Hostname: "home", NodeID: "node2", State: PersonInvitePending, Attempt: 1}
			if err := validatePersonInvites([]PersonInvite{old}); err != nil {
				t.Fatal("state control", err)
			}
			err := validatePersonInvites([]PersonInvite{old, next})
			live := state == PersonInvitePending || state == PersonInviteSending || state == PersonInviteUnknown || state == PersonInviteComplete || state == PersonInviteAccepted
			if (err != nil) != live {
				t.Fatalf("state=%s permits duplicate gateway=%t", state, err == nil)
			}
		})
	}
	for _, kind := range []string{"pretend", "gateway"} {
		op := PersonInvite{TargetKind: kind, App: "photos", Hostname: "home", NodeID: "node", State: PersonInvitePending}
		if validatePersonInvites([]PersonInvite{op}) == nil {
			t.Fatal("invalid gateway target kind/app pair accepted", op)
		}
	}
}

func TestGatewayInviteHistoryKeepsSchemaGuard(t *testing.T) {
	reg := &Registry{People: []Person{{Login: "alice", Invites: []PersonInvite{{TargetKind: InviteTargetGateway, Hostname: "home", NodeID: "node", State: PersonInviteCancelled}}}}}
	if !hasGatewayFields(reg) {
		t.Fatal("gateway history lost downgrade guard")
	}
	reg.People[0].Invites[0] = PersonInvite{App: "photos", Hostname: "photos", NodeID: "app-node", State: PersonInviteCancelled}
	if hasGatewayFields(reg) {
		t.Fatal("legacy app invitation changed schema")
	}
}
