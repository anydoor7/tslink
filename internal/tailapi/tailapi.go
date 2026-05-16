package tailapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
)

// ErrNoAPIClient means cleanup cannot use the Tailscale API because no API key is configured.
var ErrNoAPIClient = errors.New("no API client available")

// CleanupResult describes a stale-node cleanup attempt.
type CleanupResult struct {
	Matched    []string
	Deleted    []string
	Protected  []string
	Skipped    bool
	SkipReason string
}

// CleanupTarget identifies the expected TSLink-owned device identity.
type CleanupTarget struct {
	Hostname string
	Tags     []string
	DeviceID string
	NodeID   string
}

const cleanupOwnershipSkipReason = "matched tailnet devices require exact TSLink ownership proof before deletion"

// CleanupTargetForService builds the remote cleanup ownership target for a service.
func CleanupTargetForService(svc registry.Service) CleanupTarget {
	return CleanupTarget{Hostname: svc.Name, Tags: svc.Tags}
}

// CleanupTargetsForServices builds remote cleanup ownership targets for services.
func CleanupTargetsForServices(services []registry.Service) []CleanupTarget {
	targets := make([]CleanupTarget, 0, len(services))
	for _, svc := range services {
		targets = append(targets, CleanupTargetForService(svc))
	}
	return targets
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

func validateCleanupTarget(target CleanupTarget) error {
	if err := registry.ValidateName(target.Hostname); err != nil {
		return err
	}
	for _, tag := range target.Tags {
		if err := registry.ValidateTag(tag); err != nil {
			return err
		}
	}
	return nil
}

func cleanupTargetMatchesExactDevice(target CleanupTarget, deviceID, nodeID string) bool {
	if target.NodeID != "" && nodeID != "" && target.NodeID == nodeID {
		return true
	}
	if target.DeviceID != "" && deviceID != "" && target.DeviceID == deviceID {
		return true
	}
	return false
}

func deviceDeleteID(deviceID, nodeID string) string {
	if nodeID != "" {
		return nodeID
	}
	return deviceID
}

func matchingCleanupTarget(hostname, deviceID, nodeID string, targets []CleanupTarget) (CleanupTarget, bool) {
	for _, target := range targets {
		if cleanupTargetMatchesExactDevice(target, deviceID, nodeID) {
			return target, true
		}
	}
	for _, target := range targets {
		if hostname == target.Hostname {
			return target, true
		}
	}
	for _, target := range targets {
		if hostnameMatchesCleanupTarget(hostname, target.Hostname) {
			return target, true
		}
	}
	return CleanupTarget{}, false
}

// DeleteDevicesForService deletes matching devices only when TSLink ownership can be proven.
func DeleteDevicesForService(ctx context.Context, target CleanupTarget) (CleanupResult, error) {
	return CleanupStaleNodesResult(ctx, []CleanupTarget{target})
}

// CleanupStaleNodes removes stale nodes that conflict with the given service targets.
func CleanupStaleNodes(ctx context.Context, targets []CleanupTarget) error {
	_, err := CleanupStaleNodesResult(ctx, targets)
	return err
}

// CleanupStaleNodesResult removes stale nodes and returns explicit cleanup status.
func CleanupStaleNodesResult(ctx context.Context, targets []CleanupTarget) (CleanupResult, error) {
	for _, target := range targets {
		if err := validateCleanupTarget(target); err != nil {
			return CleanupResult{}, err
		}
	}
	if len(targets) == 0 {
		return CleanupResult{}, nil
	}

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

	var result CleanupResult
	for _, d := range devices {
		target, ok := matchingCleanupTarget(d.Hostname, d.ID, d.NodeID, targets)
		if !ok {
			continue
		}
		result.Matched = append(result.Matched, d.Hostname)
		if !cleanupTargetMatchesExactDevice(target, d.ID, d.NodeID) {
			result.Protected = append(result.Protected, d.Hostname)
			continue
		}
		deleteID := deviceDeleteID(d.ID, d.NodeID)
		if deleteID == "" {
			result.Protected = append(result.Protected, d.Hostname)
			continue
		}
		if err := client.Devices().Delete(ctx, deleteID); err != nil {
			return result, fmt.Errorf("delete device %s: %w", d.Hostname, err)
		}
		result.Deleted = append(result.Deleted, d.Hostname)
		slog.Info("removed tailnet node", "hostname", d.Hostname)
	}
	if len(result.Protected) > 0 {
		result.Skipped = true
		result.SkipReason = cleanupOwnershipSkipReason
	}
	return result, nil
}
