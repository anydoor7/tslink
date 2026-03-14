package tailapi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/monody0007/tslink/internal/credentials"
)

// DeleteDevicesByHostname deletes all devices matching the given hostname from the tailnet.
func DeleteDevicesByHostname(ctx context.Context, hostname string) error {
	client, err := credentials.NewTailscaleClient()
	if err != nil {
		return err
	}
	if client == nil {
		return nil // no API key, skip
	}

	devices, err := client.Devices(ctx, nil)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}

	for _, d := range devices {
		if d.Hostname == hostname || strings.HasPrefix(d.Hostname, hostname+"-") {
			if err := client.DeleteDevice(ctx, d.DeviceID); err != nil {
				return fmt.Errorf("delete device %s: %w", d.Hostname, err)
			}
			slog.Info("removed tailnet node", "hostname", d.Hostname)
		}
	}
	return nil
}

// CleanupStaleNodes removes stale nodes that conflict with the given hostnames.
func CleanupStaleNodes(ctx context.Context, hostnames []string) error {
	client, err := credentials.NewTailscaleClient()
	if err != nil {
		return err
	}
	if client == nil {
		return nil
	}

	devices, err := client.Devices(ctx, nil)
	if err != nil {
		return nil // non-fatal on serve
	}

	nameSet := make(map[string]bool, len(hostnames))
	for _, h := range hostnames {
		nameSet[h] = true
	}

	for _, d := range devices {
		// Match exact name or suffixed duplicates (e.g. "webapp-1")
		base := d.Hostname
		if idx := strings.LastIndex(base, "-"); idx > 0 {
			base = base[:idx]
		}
		if nameSet[d.Hostname] || nameSet[base] {
			_ = client.DeleteDevice(ctx, d.DeviceID)
		}
	}
	return nil
}
