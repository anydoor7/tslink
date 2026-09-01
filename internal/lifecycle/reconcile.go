package lifecycle

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

const (
	ACLNotRequested = "not_requested"
	ACLStillInUse   = "still_in_use"
	ACLWouldDelete  = "would_delete_if_canonical"
	ACLDeleted      = "deleted"
	ACLSkipped      = "skipped"

	cleanupUnavailableReason   = "device cleanup could not be completed; see warnings"
	ownershipUnavailableReason = "ownership ledger unavailable; device deletion disabled"
	emptyRegistryReason        = "registry has zero services while ownership ledger contains records; device deletion disabled"
	partialRegistryReason      = "registry appears incomplete: orphan ownership records exceed registered services and at least 3 remote deletions would result; device deletion disabled; restore registry.json or re-establish reviewed TSLink ownership with tslink cleanup --adopt <hostname> --force --dry-run=false"
)

type Options struct {
	RegistryPath   string
	OwnershipPath  string
	Now            time.Time
	DryRun         bool
	ManageACL      bool
	CheckUnusedACL bool
}

type AdoptionResult struct {
	ServiceName string `json:"service_name"`
	Matches     int    `json:"matches"`
	Written     bool   `json:"written"`
}

// Result intentionally contains service/hostname labels but never NodeIDs.
type Result struct {
	DryRun               bool            `json:"dry_run"`
	RegistryChanged      bool            `json:"registry_changed"`
	ExpiredFunnels       []string        `json:"expired_funnels"`
	DevicesMatched       []string        `json:"devices_matched"`
	DevicesWouldDelete   []string        `json:"devices_would_delete"`
	DevicesDeleted       []string        `json:"devices_deleted"`
	DevicesProtected     []string        `json:"devices_protected"`
	DevicesAdopted       []string        `json:"devices_adopted,omitempty"`
	Adoption             *AdoptionResult `json:"adoption,omitempty"`
	DeviceCleanupSkipped bool            `json:"device_cleanup_skipped"`
	DeviceSkipReason     string          `json:"device_skip_reason,omitempty"`
	ACLAction            string          `json:"acl_action"`
	Warnings             []string        `json:"warnings,omitempty"`
}

var (
	cleanupDevicesFn = tailapi.CleanupStaleNodesResultWithDryRun
	deleteTagFn      = tailapi.DeleteTag
)

// Reconcile is the single lifecycle implementation used by cleanup, serve
// startup, and the serve wall-clock ticker.
func Reconcile(ctx context.Context, options Options) (Result, error) {
	if options.Now.IsZero() {
		options.Now = time.Now()
	}
	result := Result{
		DryRun:             options.DryRun,
		ExpiredFunnels:     []string{},
		DevicesMatched:     []string{},
		DevicesWouldDelete: []string{},
		DevicesDeleted:     []string{},
		DevicesProtected:   []string{},
		ACLAction:          ACLNotRequested,
	}

	expired, err := registry.DowngradeExpiredFunnels(options.RegistryPath, options.Now, options.DryRun)
	if err != nil {
		return Result{}, fmt.Errorf("downgrade expired Funnel services: %w", err)
	}
	for _, svc := range expired {
		result.ExpiredFunnels = append(result.ExpiredFunnels, svc.Name)
	}
	result.RegistryChanged = !options.DryRun && len(expired) > 0

	reg, err := registry.Load(options.RegistryPath)
	if err != nil {
		return Result{}, err
	}
	active := make(map[string]struct{}, len(reg.Services))
	activeFunnels := 0
	for _, svc := range reg.Services {
		active[svc.Name] = struct{}{}
		effective := registry.EffectiveServiceAt(svc, options.Now)
		if effective.Funnel {
			activeFunnels++
		}
	}

	ledger, err := tsruntime.LoadOwnership(options.OwnershipPath)
	if err != nil {
		result.DeviceCleanupSkipped = true
		result.DeviceSkipReason = ownershipUnavailableReason
		result.Warnings = append(result.Warnings, err.Error())
	}
	orphanIDs := make(map[string][]string)
	deletionEnabled := err == nil
	if deletionEnabled {
		for _, node := range ledger.Nodes {
			if _, registered := active[node.ServiceName]; registered {
				continue
			}
			orphanIDs[node.ServiceName] = append(orphanIDs[node.ServiceName], node.NodeID)
		}
	}
	if deletionEnabled && len(reg.Services) == 0 && len(ledger.Nodes) > 0 {
		deletionEnabled = false
		result.DeviceCleanupSkipped = true
		result.DeviceSkipReason = emptyRegistryReason
		result.Warnings = append(result.Warnings, emptyRegistryReason)
	}
	if deletionEnabled && len(orphanIDs) > len(reg.Services) && len(orphanIDs) >= 3 {
		deletionEnabled = false
		result.DeviceCleanupSkipped = true
		result.DeviceSkipReason = partialRegistryReason
		result.Warnings = append(result.Warnings, partialRegistryReason)
	}
	if !deletionEnabled {
		orphanIDs = make(map[string][]string)
	}
	serviceNames := make([]string, 0, len(orphanIDs))
	for name := range orphanIDs {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	targets := make([]tailapi.CleanupTarget, 0, len(serviceNames))
	for _, name := range serviceNames {
		targets = append(targets, tailapi.CleanupTarget{
			Hostname: name,
			NodeIDs:  append([]string(nil), orphanIDs[name]...),
		})
	}
	if len(targets) > 0 {
		cleanup, cleanupErr := cleanupDevicesFn(ctx, targets, options.DryRun)
		result.DevicesMatched = append(result.DevicesMatched, cleanup.Matched...)
		result.DevicesWouldDelete = append(result.DevicesWouldDelete, cleanup.WouldDelete...)
		result.DevicesDeleted = append(result.DevicesDeleted, cleanup.Deleted...)
		result.DevicesProtected = append(result.DevicesProtected, cleanup.Protected...)
		result.DeviceCleanupSkipped = cleanup.Skipped
		result.DeviceSkipReason = cleanup.SkipReason
		if cleanupErr != nil {
			result.DeviceCleanupSkipped = true
			if result.DeviceSkipReason == "" {
				result.DeviceSkipReason = cleanupUnavailableReason
			}
			result.Warnings = append(result.Warnings, cleanupErr.Error())
		} else if !options.DryRun && len(cleanup.ResolvedOwnershipIDs) > 0 {
			if err := tsruntime.RemoveOwnedNodeIDs(options.OwnershipPath, cleanup.ResolvedOwnershipIDs); err != nil {
				return Result{}, fmt.Errorf("update node ownership ledger: %w", err)
			}
		}
	}

	if options.ManageACL {
		switch {
		case activeFunnels > 0:
			result.ACLAction = ACLStillInUse
		case options.DryRun:
			result.ACLAction = ACLWouldDelete
		case len(expired) > 0 || options.CheckUnusedACL:
			if err := deleteTagFn(ctx, registry.FunnelTag); err != nil {
				result.ACLAction = ACLSkipped
				result.Warnings = append(result.Warnings, fmt.Sprintf("unused Funnel ACL cleanup skipped: %v", err))
			} else {
				result.ACLAction = ACLDeleted
			}
		}
	}

	sort.Strings(result.ExpiredFunnels)
	sort.Strings(result.DevicesMatched)
	sort.Strings(result.DevicesWouldDelete)
	sort.Strings(result.DevicesDeleted)
	sort.Strings(result.DevicesProtected)
	return result, nil
}
