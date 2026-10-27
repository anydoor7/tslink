package tailapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/anydoor7/tslink/internal/registry"
	tailscale "tailscale.com/client/tailscale/v2"
)

type Membership string

const (
	MembershipMember Membership = "member"
	MembershipGuest  Membership = "non_member"
)

var membershipClientFn = newTailscaleClient

// ResolveMembership uses the complete user directory, never online devices,
// email domains or a persisted guest bit. Unknown is an error, not a guest.
func ResolveMembership(ctx context.Context, login, controlURL string) (Membership, error) {
	login, err := registry.NormalizePerson(login)
	if err != nil || strings.HasPrefix(login, "tag:") {
		return "", registry.CodedError{Code: "usage_error", Message: "recipient must be a human login"}
	}
	if controlURL != "" && controlURL != "https://controlplane.tailscale.com" && controlURL != "https://login.tailscale.com" {
		return "", membershipUnknown(login)
	}
	client, err := membershipClientFn()
	if err != nil || client == nil {
		return "", membershipUnknown(login)
	}
	users, err := client.Users().List(ctx, nil, nil)
	if err != nil || users == nil {
		return "", membershipUnknown(login)
	}
	seen := map[string]bool{}
	result := MembershipGuest
	for _, user := range users {
		who, err := registry.NormalizePerson(user.LoginName)
		if err != nil || seen[who] || user.ID == "" || user.TailnetID == "" {
			return "", membershipUnknown(login)
		}
		seen[who] = true
		if user.Type != tailscale.UserTypeMember && user.Type != tailscale.UserTypeShared {
			return "", membershipUnknown(login)
		}
		switch user.Status {
		case tailscale.UserStatusActive, tailscale.UserStatusIdle:
		case tailscale.UserStatusNeedsApproval, tailscale.UserStatusSuspended, tailscale.UserStatusOverBillingLimit:
			if who == login {
				return "", registry.CodedError{Code: "recipient_unavailable", Message: fmt.Sprintf("%s's network account is %s; access setup is incomplete", login, user.Status)}
			}
		default:
			return "", membershipUnknown(login)
		}
		if who == login && user.Type == tailscale.UserTypeMember {
			result = MembershipMember
		}
	}
	return result, nil
}

func membershipUnknown(login string) error {
	return registry.CodedError{Code: "membership_unknown", Message: fmt.Sprintf("Could not verify %s's network membership; access setup is incomplete", login), Next: []string{"Restore API access, then retry the same access command."}}
}
