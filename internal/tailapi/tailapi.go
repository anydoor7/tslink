package tailapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/credentials"
)

// ErrNoAPIClient means cleanup cannot use the Tailscale API because no API key is configured.
var ErrNoAPIClient = errors.New("no API client available")

// CleanupResult describes a stale-node cleanup attempt.
type CleanupResult struct {
	Matched    []string
	Deleted    []string
	Skipped    bool
	SkipReason string
}

func hostnameMatchesCleanupTarget(hostname, target string) bool {
	if hostname == target {
		return true
	}
	suffix, ok := strings.CutPrefix(hostname, target+"-")
	if !ok || suffix == "" {
		return false
	}
	n, err := strconv.Atoi(suffix)
	return err == nil && n > 0 && strconv.Itoa(n) == suffix
}

// DeleteDevicesByHostname deletes all devices matching the given hostname from the tailnet.
func DeleteDevicesByHostname(ctx context.Context, hostname string) error {
	client, err := credentials.NewTailscaleClient()
	if err != nil {
		return err
	}
	if client == nil {
		return ErrNoAPIClient
	}

	devices, err := client.Devices().List(ctx)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}

	for _, d := range devices {
		if hostnameMatchesCleanupTarget(d.Hostname, hostname) {
			if err := client.Devices().Delete(ctx, d.ID); err != nil {
				return fmt.Errorf("delete device %s: %w", d.Hostname, err)
			}
			slog.Info("removed tailnet node", "hostname", d.Hostname)
		}
	}
	return nil
}

// CleanupStaleNodes removes stale nodes that conflict with the given hostnames.
func CleanupStaleNodes(ctx context.Context, hostnames []string) error {
	_, err := CleanupStaleNodesResult(ctx, hostnames)
	return err
}

// CleanupStaleNodesResult removes stale nodes and returns explicit cleanup status.
func CleanupStaleNodesResult(ctx context.Context, hostnames []string) (CleanupResult, error) {
	client, err := credentials.NewTailscaleClient()
	if err != nil {
		return CleanupResult{}, err
	}
	if client == nil {
		return CleanupResult{Skipped: true, SkipReason: ErrNoAPIClient.Error()}, nil
	}

	devices, err := client.Devices().List(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("list devices: %w", err)
	}

	nameSet := make(map[string]struct{}, len(hostnames))
	for _, h := range hostnames {
		nameSet[h] = struct{}{}
	}

	var result CleanupResult
	for _, d := range devices {
		for hostname := range nameSet {
			if !hostnameMatchesCleanupTarget(d.Hostname, hostname) {
				continue
			}
			result.Matched = append(result.Matched, d.Hostname)
			if err := client.Devices().Delete(ctx, d.ID); err != nil {
				return result, fmt.Errorf("delete device %s: %w", d.Hostname, err)
			}
			result.Deleted = append(result.Deleted, d.Hostname)
			break
		}
	}
	return result, nil
}
