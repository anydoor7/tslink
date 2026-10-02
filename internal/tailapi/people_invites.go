package tailapi

import (
	"context"
	"fmt"
)

// DeviceInviteOutcomeUnknown means POST was attempted; even an HTTP refusal
// or unreadable response does not prove the remote side effect absent.
type DeviceInviteOutcomeUnknown struct{ Err error }

func (e *DeviceInviteOutcomeUnknown) Error() string { return e.Err.Error() }
func (e *DeviceInviteOutcomeUnknown) Unwrap() error { return e.Err }

// ListDeviceInvitesForTarget needs no running snapshot: the durable node ID
// and hostname must still match a currently owned remote device.
func ListDeviceInvitesForTarget(ctx context.Context, target DeviceTarget) ([]Invite, error) {
	client, err := inviteClientFn()
	if err != nil {
		return nil, err
	}
	return listPeopleDeviceInvites(ctx, client, target)
}

func listPeopleDeviceInvites(ctx context.Context, client inviteAPI, target DeviceTarget) ([]Invite, error) {
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return nil, inviteAPIError("list devices for people invite proof", err)
	}
	device, err := resolveOwnedDevice(devices, target)
	if err != nil {
		return nil, err
	}
	responses, err := client.ListDeviceInvites(ctx, device.NodeID)
	if err != nil {
		return nil, inviteAPIError("list device invites for people reconciliation", err)
	}
	invites := make([]Invite, 0, len(responses))
	for _, response := range responses {
		if err := ValidateInviteID(response.ID); err != nil {
			return nil, err
		}
		invites = append(invites, deviceInvite(response, target.Service, response.Email, response.Email != ""))
	}
	return invites, nil
}

// RevokePendingDeviceInvite only deletes the recorded ID on its proven node.
// Accepted shares are reported separately. Absence is idempotent success.
func RevokePendingDeviceInvite(ctx context.Context, target DeviceTarget, id string) (string, error) {
	if err := ValidateInviteID(id); err != nil {
		return "", err
	}
	client, err := inviteClientFn()
	if err != nil {
		return "", err
	}
	invites, err := listPeopleDeviceInvites(ctx, client, target)
	if err != nil {
		return "", err
	}
	for _, inv := range invites {
		if inv.ID != id {
			continue
		}
		if inv.Accepted {
			return "accepted", nil
		}
		if err := client.DeleteDeviceInvite(ctx, id); err != nil {
			return "", inviteAPIError(fmt.Sprintf("revoke pending device invite %q", id), err)
		}
		return "revoked", nil
	}
	return "revoked", nil
}
