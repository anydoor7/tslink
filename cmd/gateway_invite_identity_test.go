package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func TestGatewayInviteIdentityReplacement(t *testing.T) {
	for _, state := range []string{registry.PersonInviteComplete, registry.PersonInviteAccepted, registry.PersonInvitePending, registry.PersonInviteSending, registry.PersonInviteUnknown} {
		t.Run(state, func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: true}, false)
			if err != nil || !got.Complete {
				t.Fatal(got, err)
			}
			p, err := readPerson(f.paths.Registry, "alice")
			if err != nil {
				t.Fatal(err)
			}
			// Seed the historical send state without replaying remote side effects.
			reg, err := registry.Load(f.paths.Registry)
			if err != nil {
				t.Fatal(err)
			}
			op := p.Invites[0]
			op.State = state
			if state != registry.PersonInviteComplete && state != registry.PersonInviteAccepted {
				op.ID = ""
			}
			for i := range reg.People {
				if reg.People[i].Login == "alice" {
					reg.People[i].Invites[0] = op
				}
			}
			writeGatewayInviteRegistry(t, f.paths.Registry, reg)
			now := peopleNowFn()
			ledger := filepath.Join(filepath.Dir(f.paths.Registry), "portal-nodes", "home", "ownership.json")
			if _, err := tsruntime.TryUpdateOwnership(ledger, func(l *tsruntime.OwnershipLedger) (bool, error) {
				for i := range l.Nodes {
					l.Nodes[i].RetiredAt = &now
				}
				l.Nodes = append(l.Nodes, tsruntime.OwnedNode{ServiceName: "gateway", NodeID: "gateway-node-2", RecordedAt: now})
				return true, nil
			}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.nodeID = "gateway-node-2"
			f.mu.Unlock()
			f.refresh(t)
			args := peopleArguments{Who: "alice", Invite: true}
			got, err = changePeople(context.Background(), f.paths, args, true)
			if err != nil || got.Complete || len(got.Invites) != 1 || got.Invites[0].Code != "gateway_invite_stale" {
				t.Fatalf("stale invite not reported: %+v %v", got, err)
			}
			if posts, deletes := f.counts(); posts != 1 || deletes != 0 {
				t.Fatal("implicit identity replacement sent/revoked invite", posts, deletes)
			}
			if err := gatewayInviteProof(f.paths.Registry, tailapi.DeviceTarget{Kind: "gateway", Hostname: "home", NodeID: "gateway-node"}); err == nil {
				t.Fatal("retired node still proves invite ownership")
			}
			// Wrong explicit IDs must not retire the old operation.
			f.refresh(t)
			args.Replace = map[string]string{gatewayInviteKey: "9999"}
			got, err = changePeople(context.Background(), f.paths, args, true)
			if err != nil || got.Complete || got.Invites[0].Code != "gateway_invite_stale" {
				t.Fatal(got, err)
			}
			f.refresh(t)
			args.Replace = nil
			if op.ID != "" {
				args.Replace = map[string]string{gatewayInviteKey: op.ID}
			} else {
				args.Reconcile = map[string]string{gatewayInviteKey: "none"}
			}
			got, err = changePeople(context.Background(), f.paths, args, true)
			if err != nil || !got.Complete || got.Invites[0].ID != "1002" {
				t.Fatalf("explicit recovery: %+v %v", got, err)
			}
			p, err = readPerson(f.paths.Registry, "alice")
			if err != nil || len(p.Invites) != 2 || p.Invites[0].State != registry.PersonInviteTargetGone || p.Invites[0].NodeID != "gateway-node" || p.Invites[1].NodeID != "gateway-node-2" {
				t.Fatalf("recovery history: %+v %v", p, err)
			}
			if posts, deletes := f.counts(); posts != 2 || deletes != 0 {
				t.Fatal(posts, deletes)
			}
			if op.ID != "" {
				f.refresh(t)
				got, err = changePeople(context.Background(), f.paths, args, true)
				if err != nil || !got.Complete {
					t.Fatal("replacement retry", got, err)
				}
				if posts, _ := f.counts(); posts != 2 {
					t.Fatal("replacement retry sent another invitation", posts)
				}
			}
		})
	}
}

func TestGatewayInviteProofRecheckedBeforePost(t *testing.T) {
	f := newGatewayInviteFixture(t)
	got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}}, false)
	if err != nil || !got.Complete {
		t.Fatal(got, err)
	}
	op := registry.PersonInvite{TargetKind: "gateway", Hostname: "home", NodeID: "gateway-node", State: registry.PersonInviteUnknown}
	if err := registry.SavePersonInvite(f.paths.Registry, "alice", op); err != nil {
		t.Fatal(err)
	}
	retired := false
	f.beforeList = func() {
		now := peopleNowFn()
		path := filepath.Join(filepath.Dir(f.paths.Registry), "portal-nodes", "home", "ownership.json")
		if _, err := tsruntime.TryUpdateOwnership(path, func(l *tsruntime.OwnershipLedger) (bool, error) {
			for i := range l.Nodes {
				l.Nodes[i].RetiredAt = &now
			}
			return true, nil
		}); err != nil {
			t.Error(err)
		}
		retired = true
	}
	f.refresh(t)
	got, err = changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Invite: true, Reconcile: map[string]string{gatewayInviteKey: "none"}}, true)
	if err != nil || got.Complete || len(got.Invites) != 1 || got.Invites[0].Code != registry.CodeInviteOwnershipUnproven {
		t.Fatalf("second ownership proof not enforced: %+v %v", got, err)
	}
	if !retired {
		t.Fatal("reconciliation did not reach ownership change seam")
	}
	if posts, _ := f.counts(); posts != 0 {
		t.Fatal("POST after ownership retired", posts)
	}
}

func writeGatewayInviteRegistry(t *testing.T, path string, reg *registry.Registry) {
	t.Helper()
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
