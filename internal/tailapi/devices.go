package tailapi

import (
	"context"
	"sort"
	"time"

	"github.com/monody0007/tslink/internal/credentials"
	tailscale "tailscale.com/client/tailscale/v2"
)

// TailnetDevice is the read-only projection of one TSLink-tagged tailnet
// device. It deliberately omits NodeID and every other node identity value:
// deletion authorization lives only in tailapi.CleanupStaleNodesResultWithDryRun,
// which matches owned[d.NodeID] exactly, and a NodeID that never leaves this
// package cannot be copied into a caller that widens that predicate.
type TailnetDevice struct {
	Hostname           string
	Name               string
	Tags               []string
	OS                 string
	CreatedAt          *time.Time
	LastSeenAt         *time.Time
	ConnectedToControl bool
	Authorized         bool
}

// ListTSLinkDevices lists every TSLink-tagged device in the tailnet using the
// same one-GET device listing and the same TSLink tag predicate that cleanup
// adoption uses. It is strictly read-only: it issues no DELETE, records no
// ownership, and grants no deletion authority.
//
// The tailnet is shared across machines, so the returned set spans every host
// running TSLink. Deciding which rows belong to the calling machine is the
// caller's job; this function has no access to any local registry.
func ListTSLinkDevices(ctx context.Context) ([]TailnetDevice, error) {
	client, err := newTailscaleClient()
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, ErrNoAPIClient
	}
	// Same contract note as FindExactDeviceNodeID: the v2 Devices.List call is
	// one GET returning the complete devices array, with no cursor or page
	// parameter, so this is the full tailnet and not a first page.
	devices, err := client.Devices().List(ctx)
	if err != nil {
		return nil, credentials.ClassifyAPIError("list tailnet devices", err)
	}
	views := make([]TailnetDevice, 0, len(devices))
	for _, device := range devices {
		if !deviceHasTSLinkTag(device.Tags) {
			continue
		}
		views = append(views, tailnetDeviceView(device))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Hostname != views[j].Hostname {
			return views[i].Hostname < views[j].Hostname
		}
		return views[i].Name < views[j].Name
	})
	return views, nil
}

// HostnameMatchesServiceName reports whether a tailnet hostname is either the
// literal service name or one of the numeric collision variants tsnet appends
// when the plain hostname is already taken (<name>-1, <name>-2, ...). It is the
// same discovery predicate cleanup uses to decide which hostname-only matches
// to report as Protected, exported so read-only views can label a device's
// local family without reimplementing the rule.
//
// Discovery only. A true result never authorizes deletion; DELETE remains
// gated on exact NodeID ownership proof.
func HostnameMatchesServiceName(hostname, service string) bool {
	return hostnameMatchesCleanupTarget(hostname, service)
}

func tailnetDeviceView(device tailscale.Device) TailnetDevice {
	return TailnetDevice{
		Hostname:           device.Hostname,
		Name:               device.Name,
		Tags:               append([]string(nil), device.Tags...),
		OS:                 device.OS,
		CreatedAt:          optionalDeviceTime(&device.Created),
		LastSeenAt:         optionalDeviceTime(device.LastSeen),
		ConnectedToControl: device.ConnectedToControl,
		Authorized:         device.Authorized,
	}
}

// optionalDeviceTime maps both wire absences the v2 client can produce - a nil
// pointer and the zero time it decodes an empty string into - onto a single nil
// so callers never render a year-1 timestamp as a real observation.
func optionalDeviceTime(value *tailscale.Time) *time.Time {
	if value == nil || value.Time.IsZero() {
		return nil
	}
	moment := value.Time.UTC()
	return &moment
}
