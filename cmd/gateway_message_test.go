package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

func TestGatewayMessageRequiresGatewayOperation(t *testing.T) {
	for _, tt := range []struct {
		name           string
		invite, routed bool
	}{
		{"member-without-invite", false, true}, {"non-routed-invite", true, false}, {"gateway-invite-control", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newGatewayInviteFixture(t)
			if !tt.invite {
				peopleMembershipFn = func(context.Context, string, string) (tailapi.Membership, error) {
					return tailapi.MembershipMember, nil
				}
			}
			if !tt.routed {
				if err := registry.SetPortalRoute(context.Background(), f.paths.Registry, "photos", nil); err != nil {
					t.Fatal(err)
				}
				f.refresh(t)
			}
			got, err := changePeople(context.Background(), f.paths, peopleArguments{Who: "alice", Apps: []string{"photos"}, Invite: tt.invite}, false)
			if err != nil || !got.Complete {
				t.Fatal(got, err)
			}
			want := tt.invite
			if strings.Contains(got.Message, "Gateway: invitation prepared") != want {
				t.Fatalf("gateway message without an operation: %s", got.Message)
			}
			has := false
			for _, op := range got.Invites {
				has = has || op.TargetKind == registry.InviteTargetGateway
			}
			if has != want {
				t.Fatal("wrong invite operation control", got.Invites)
			}
		})
	}
}
