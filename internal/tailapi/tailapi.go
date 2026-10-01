package tailapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/registry"
)

// ErrNoAPIClient means cleanup cannot use the Tailscale API because no API key is configured.
var ErrNoAPIClient = errors.New("no API client available")

// CleanupResult describes a stale-node cleanup attempt.
type CleanupResult struct {
	Matched              []string
	WouldDelete          []string
	Deleted              []string
	Protected            []string
	Skipped              bool
	SkipReason           string
	ResolvedOwnershipIDs []string
}

// CleanupTarget identifies the expected TSLink-owned device identity.
type CleanupTarget struct {
	Hostname string
	Tags     []string
	NodeIDs  []string
}

const cleanupOwnershipSkipReason = "matched tailnet devices require exact TSLink ownership proof before deletion"

// FindExactDeviceNodeID performs the read-only lookup used by cleanup adoption.
// It deliberately uses literal hostname equality and requires a TSLink tag:
// suffix, prefix, wildcard, regex, foreign-tagged, and first-match selection are
// never accepted for migration proof.
func FindExactDeviceNodeID(ctx context.Context, hostname string) (string, int, error) {
	if err := registry.ValidateName(hostname); err != nil {
		return "", 0, err
	}
	client, err := newTailscaleClient()
	if err != nil {
		return "", 0, err
	}
	if client == nil {
		return "", 0, ErrNoAPIClient
	}
	// The tailscale v2 Devices.List contract is one GET returning the complete
	// devices array; it has no cursor, page, or limit parameter. The cardinality
	// below therefore counts the full response, not one page.
	devices, err := client.Devices().List(ctx)
	if err != nil {
		return "", 0, credentials.ClassifyAPIError("list devices for adoption", err)
	}
	hostnameMatches := 0
	var taggedNodeIDs []string
	for _, device := range devices {
		if device.Hostname != hostname {
			continue
		}
		hostnameMatches++
		if deviceHasTSLinkTag(device.Tags) {
			taggedNodeIDs = append(taggedNodeIDs, device.NodeID)
		}
	}
	matches := len(taggedNodeIDs)
	if hostnameMatches > 0 && matches == 0 {
		configuredDefaultTag := config.GetDefaultTag()
		return "", 0, registry.CodedError{
			Code:        "conflict",
			Message:     fmt.Sprintf("matched %d devices, 0 of them TSLink-tagged", hostnameMatches),
			Next:        []string{fmt.Sprintf("verify the device carries %s, tag:tslink-funnel, or another tag:tslink-* tag", configuredDefaultTag), "tslink cleanup --help"},
			MessageOnly: true,
		}
	}
	if matches != 1 {
		return "", matches, nil
	}
	nodeID := taggedNodeIDs[0]
	if strings.TrimSpace(nodeID) == "" || len(nodeID) > 256 {
		return "", matches, errors.New("matched device has an invalid node identity")
	}
	return nodeID, matches, nil
}

func deviceHasTSLinkTag(tags []string) bool {
	configuredDefaultTag := config.GetDefaultTag()
	for _, tag := range tags {
		if tag == DefaultTag || tag == configuredDefaultTag || tag == registry.FunnelTag || strings.HasPrefix(tag, "tag:tslink-") {
			return true
		}
	}
	return false
}

// CleanupTargetForService builds the remote cleanup ownership target for a service.
func CleanupTargetForService(svc registry.Service) CleanupTarget {
	return CleanupTarget{Hostname: svc.Name, Tags: svc.Tags}
}

// CleanupTargetForOwnedService attaches durable exact NodeID proof. Hostname
// remains discovery-only and can never authorize DELETE.
func CleanupTargetForOwnedService(svc registry.Service, nodeIDs []string) CleanupTarget {
	target := CleanupTargetForService(svc)
	target.NodeIDs = append([]string(nil), nodeIDs...)
	return target
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
	for _, nodeID := range target.NodeIDs {
		if strings.TrimSpace(nodeID) == "" || len(nodeID) > 256 {
			return errors.New("cleanup target contains an invalid node ID")
		}
	}
	return nil
}

func matchingCleanupTarget(hostname string, targets []CleanupTarget) (CleanupTarget, bool) {
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

// DeleteDevicesForService deletes exact recorded NodeIDs and reports every
// hostname-only match as protected.
func DeleteDevicesForService(ctx context.Context, target CleanupTarget) (CleanupResult, error) {
	return CleanupStaleNodesResult(ctx, []CleanupTarget{target})
}

// CleanupStaleNodes reports stale nodes that conflict with the given service targets.
func CleanupStaleNodes(ctx context.Context, targets []CleanupTarget) error {
	_, err := CleanupStaleNodesResult(ctx, targets)
	return err
}

// CleanupStaleNodesResult reconciles exact ownership and may issue DELETE only
// for a device whose NodeID appears in a CleanupTarget.
func CleanupStaleNodesResult(ctx context.Context, targets []CleanupTarget) (CleanupResult, error) {
	return CleanupStaleNodesResultWithDryRun(ctx, targets, false)
}

// CleanupStaleNodesResultWithDryRun reconciles devices using exact NodeID
// ownership. Hostname matches without exact proof remain Protected.
func CleanupStaleNodesResultWithDryRun(ctx context.Context, targets []CleanupTarget, dryRun bool) (CleanupResult, error) {
	for _, target := range targets {
		if err := validateCleanupTarget(target); err != nil {
			return CleanupResult{}, err
		}
	}
	if len(targets) == 0 {
		return CleanupResult{}, nil
	}

	client, err := newTailscaleClient()
	if err != nil {
		return CleanupResult{}, err
	}
	if client == nil {
		return CleanupResult{Skipped: true, SkipReason: ErrNoAPIClient.Error()}, nil
	}

	devices, err := client.Devices().List(ctx)
	if err != nil {
		return CleanupResult{}, credentials.ClassifyAPIError("list devices", err)
	}

	owned := make(map[string]CleanupTarget)
	for _, target := range targets {
		for _, nodeID := range target.NodeIDs {
			if _, duplicate := owned[nodeID]; duplicate {
				return CleanupResult{}, errors.New("duplicate node ownership proof")
			}
			owned[nodeID] = target
		}
	}
	seenOwned := make(map[string]struct{}, len(owned))
	var result CleanupResult
	for _, d := range devices {
		if _, exact := owned[d.NodeID]; exact {
			seenOwned[d.NodeID] = struct{}{}
			result.Matched = append(result.Matched, d.Hostname)
			if dryRun {
				result.WouldDelete = append(result.WouldDelete, d.Hostname)
				continue
			}
			if err := client.Devices().Delete(ctx, d.NodeID); err != nil {
				return result, errors.New("delete TSLink-owned device failed")
			}
			result.Deleted = append(result.Deleted, d.Hostname)
			result.ResolvedOwnershipIDs = append(result.ResolvedOwnershipIDs, d.NodeID)
			continue
		}
		_, ok := matchingCleanupTarget(d.Hostname, targets)
		if !ok {
			continue
		}
		result.Matched = append(result.Matched, d.Hostname)
		result.Protected = append(result.Protected, d.Hostname)
	}
	if !dryRun {
		for nodeID := range owned {
			if _, found := seenOwned[nodeID]; !found {
				result.ResolvedOwnershipIDs = append(result.ResolvedOwnershipIDs, nodeID)
			}
		}
	}
	if len(result.Protected) > 0 {
		result.Skipped = true
		result.SkipReason = cleanupOwnershipSkipReason
	}
	return result, nil
}
