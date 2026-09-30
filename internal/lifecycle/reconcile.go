package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

const (
	ACLNotRequested = "not_requested"
	ACLStillInUse   = "still_in_use"
	ACLSkipped      = "skipped"

	cleanupUnavailableReason      = "device cleanup could not be completed; see warnings"
	ownershipUnavailableReason    = "ownership ledger unavailable; device deletion disabled"
	registryUnavailableReason     = "registry file is structurally untrusted; remote device deletion disabled"
	unknownRetirementReasonPrefix = "orphan ownership records lack retired_at provenance (including legacy schema records)"
)

func unknownRetirementReason(serviceNames []string) string {
	names := append([]string(nil), serviceNames...)
	sort.Strings(names)
	return fmt.Sprintf("%s for: %s; remote device deletion is disabled for these services, while retired services are still cleaned up; restore registry.json, or explicitly review every listed orphan hostname with tslink cleanup --adopt <hostname> --force --dry-run=false", unknownRetirementReasonPrefix, strings.Join(names, ", "))
}

// unretiredOrphanNames returns, sorted, the services absent from the registry
// that have an ownership record without retired_at. Nothing says the user
// removed them, so their remote devices are not deleted; every other orphan
// is unaffected by them.
func unretiredOrphanNames(ledger tsruntime.OwnershipLedger, active map[string]struct{}) []string {
	unknown := make(map[string]struct{})
	for _, node := range ledger.Nodes {
		if _, registered := active[node.ServiceName]; registered {
			continue
		}
		if node.RetiredAt == nil {
			unknown[node.ServiceName] = struct{}{}
		}
	}
	names := make([]string, 0, len(unknown))
	for name := range unknown {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UnretiredOrphanServices lists the services whose remote device deletion is
// dormant because an ownership record for them has no retired_at while they
// are absent from a present, valid registry.json. It reads local files only,
// so doctor can report it without calling the Tailscale API. A registry file
// that is missing or blank makes every deletion dormant for that reason
// instead, and yields no names.
func UnretiredOrphanServices(registryPath, ownershipPath string) ([]string, error) {
	reg, state, err := registry.LoadWithFileState(registryPath)
	if err != nil {
		return nil, err
	}
	if state != registry.RegistryFileValid {
		return nil, nil
	}
	ledger, err := tsruntime.LoadOwnership(ownershipPath)
	if err != nil {
		return nil, err
	}
	active := make(map[string]struct{}, len(reg.Services))
	for _, svc := range reg.Services {
		active[svc.Name] = struct{}{}
	}
	return unretiredOrphanNames(ledger, active), nil
}

func registryFileReason(state registry.RegistryFileState) string {
	return fmt.Sprintf("%s: registry.json is %s", registryUnavailableReason, state)
}

type Options struct {
	RegistryPath      string
	OwnershipPath     string
	OwnershipOverride *tsruntime.OwnershipLedger
	Now               time.Time
	DryRun            bool
	ManageACL         bool
	// CheckUnusedACL asks this run to report on the shared Funnel grant.
	// Local absence cannot authorize deletion of a tailnet-wide shared grant,
	// so the report is a skip with a warning, never a deletion.
	CheckUnusedACL bool
	// CleanLocalNodeState allows this run to delete the local tsnet state
	// directory of an orphan service whose remote nodes it proved are gone.
	//
	// It is off by default and set only by the daemon, because the daemon is
	// the process that can answer LocalNodeStateInUse. A separate `tslink
	// cleanup` process cannot see another process's tsnet servers, so it leaves
	// the directories alone rather than guessing.
	CleanLocalNodeState bool
	// LocalNodeStateInUse reports whether a tsnet server in this process still
	// holds the state directory for serviceName.
	//
	// It is required whenever CleanLocalNodeState is set, and a nil function
	// disables the cleanup instead of being read as "nothing holds any state".
	// "Nobody told me" and "nothing holds it" are different statements, and
	// only the caller can tell them apart.
	LocalNodeStateInUse func(serviceName string) bool
}

type AdoptionResult struct {
	ServiceName string `json:"service_name"`
	Matches     int    `json:"matches"`
	Written     bool   `json:"written"`
}

// Result intentionally contains service/hostname labels but never NodeIDs.
type Result struct {
	DryRun                      bool            `json:"dry_run"`
	RegistryChanged             bool            `json:"registry_changed"`
	ExpiredFunnels              []string        `json:"expired_funnels"`
	DevicesMatched              []string        `json:"devices_matched"`
	DevicesWouldDelete          []string        `json:"devices_would_delete"`
	DevicesDeleted              []string        `json:"devices_deleted"`
	DevicesProtected            []string        `json:"devices_protected"`
	DevicesAdopted              []string        `json:"devices_adopted,omitempty"`
	Adoption                    *AdoptionResult `json:"adoption,omitempty"`
	DeviceCleanupSkipped        bool            `json:"device_cleanup_skipped"`
	DeviceSkipReason            string          `json:"device_skip_reason,omitempty"`
	DeviceSkipUnknownProvenance []string        `json:"device_skip_unknown_provenance,omitempty"`
	ACLAction                   string          `json:"acl_action"`
	Warnings                    []string        `json:"warnings,omitempty"`
	// ExpiredFunnelsNotWritten names services whose Funnel deadline has
	// passed while another registry entry has a problem of its own, so the
	// downgrade was not written to registry.json. A daemon withdraws them in
	// memory at every sync and must sync; the warning reports them.
	ExpiredFunnelsNotWritten []string `json:"-"`
}

var (
	cleanupDevicesFn          = tailapi.CleanupStaleNodesResultWithDryRun
	deleteTagFn               = tailapi.DeleteTag
	downgradeExpiredFunnelsFn = registry.DowngradeExpiredFunnels
	removeNodeStateFn         = tsruntime.RemoveServiceNodeState
)

// expiredFunnelsBesideIsolatedEntries names the entries the daemon's loader
// isolates as per-service issues and, when there are any, the valid services
// whose Funnel deadline has passed.
func expiredFunnelsBesideIsolatedEntries(registryPath string, now time.Time) (isolated, due []string, err error) {
	reg, issues, err := registry.LoadForRuntime(registryPath)
	if err != nil || len(issues) == 0 {
		return nil, nil, err
	}
	for _, issue := range issues {
		isolated = append(isolated, issue.Name)
	}
	for _, svc := range reg.Services {
		if registry.FunnelExpiredAt(svc, now) {
			due = append(due, svc.Name)
		}
	}
	return isolated, due, nil
}

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

	reg, registryState, err := registry.LoadWithFileState(options.RegistryPath)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", registryUnavailableReason, err)
	}
	var expired []registry.Service
	if registryState == registry.RegistryFileValid {
		isolated, due, err := expiredFunnelsBesideIsolatedEntries(options.RegistryPath, options.Now)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", registryUnavailableReason, err)
		}
		if len(isolated) > 0 {
			// registry.json is rewritten only when every entry in it is
			// valid, and one bad entry must not stop the lifecycle of the
			// others (serve starts with it): the downgrade waits for the file
			// to be fixed while the daemon keeps these services tailnet-only.
			if len(due) > 0 {
				result.ExpiredFunnelsNotWritten = due
				result.Warnings = append(result.Warnings, fmt.Sprintf("Funnel deadline passed for %s; registry.json is not rewritten while %s has a problem of its own, so the daemon keeps them tailnet-only in memory until that entry is fixed or removed", strings.Join(due, ", "), strings.Join(isolated, ", ")))
			}
		} else {
			expired, err = downgradeExpiredFunnelsFn(options.RegistryPath, options.Now, options.DryRun)
			if err != nil {
				return Result{}, fmt.Errorf("downgrade expired Funnel services: %w", err)
			}
			reg, registryState, err = registry.LoadWithFileState(options.RegistryPath)
			if err != nil {
				return Result{}, fmt.Errorf("%s: %w", registryUnavailableReason, err)
			}
		}
	}
	for _, svc := range expired {
		result.ExpiredFunnels = append(result.ExpiredFunnels, svc.Name)
	}
	result.RegistryChanged = !options.DryRun && len(expired) > 0

	active := make(map[string]struct{}, len(reg.Services))
	activeFunnels := 0
	for _, svc := range reg.Services {
		active[svc.Name] = struct{}{}
		effective := registry.EffectiveServiceAt(svc, options.Now)
		if effective.Funnel {
			activeFunnels++
		}
	}

	var ledger tsruntime.OwnershipLedger
	var ownershipErr error
	if options.OwnershipOverride != nil {
		ledger = *options.OwnershipOverride
	} else {
		ledger, ownershipErr = tsruntime.LoadOwnership(options.OwnershipPath)
	}
	if ownershipErr != nil {
		result.DeviceCleanupSkipped = true
		result.DeviceSkipReason = ownershipUnavailableReason
		result.Warnings = append(result.Warnings, ownershipErr.Error())
	}
	registryTrusted := registryState == registry.RegistryFileValid
	if !registryTrusted {
		reason := registryFileReason(registryState)
		result.DeviceCleanupSkipped = true
		result.DeviceSkipReason = reason
		result.Warnings = append(result.Warnings, reason)
	}
	// Dormancy is per service: an orphan record without retired_at withholds
	// deletion of that service's devices only. It says nothing about another
	// service that `tslink remove` retired.
	orphanIDs := make(map[string][]string)
	if ownershipErr == nil && registryTrusted {
		unknownRetirementNames := unretiredOrphanNames(ledger, active)
		unknown := make(map[string]struct{}, len(unknownRetirementNames))
		for _, name := range unknownRetirementNames {
			unknown[name] = struct{}{}
		}
		for _, node := range ledger.Nodes {
			if _, registered := active[node.ServiceName]; registered {
				continue
			}
			if _, dormant := unknown[node.ServiceName]; dormant {
				continue
			}
			orphanIDs[node.ServiceName] = append(orphanIDs[node.ServiceName], node.NodeID)
		}
		if len(unknownRetirementNames) > 0 {
			reason := unknownRetirementReason(unknownRetirementNames)
			result.DeviceCleanupSkipped = true
			result.DeviceSkipReason = reason
			result.DeviceSkipUnknownProvenance = unknownRetirementNames
			result.Warnings = append(result.Warnings, reason)
		}
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
		if cleanup.Skipped {
			result.DeviceCleanupSkipped = true
			result.DeviceSkipReason = cleanup.SkipReason
		}
		if cleanupErr != nil {
			result.DeviceCleanupSkipped = true
			if result.DeviceSkipReason == "" {
				result.DeviceSkipReason = cleanupUnavailableReason
			}
			result.Warnings = append(result.Warnings, cleanupErr.Error())
		}
		gone := removeStaleNodeState(options, &result, cleanup, cleanupErr, orphanIDs, serviceNames, active)
		if cleanupErr == nil && !options.DryRun && len(cleanup.ResolvedOwnershipIDs) > 0 {
			// A service's rows are the only way a later run finds its local
			// state again, so they are forgotten only once that state is
			// gone: never while a tsnet server holds it, after a failed
			// removal, or for a caller that may not remove it.
			if err := tsruntime.RemoveOwnedNodeIDs(options.OwnershipPath, idsOf(cleanup.ResolvedOwnershipIDs, orphanIDs, gone)); err != nil {
				return Result{}, fmt.Errorf("update node ownership ledger: %w", err)
			}
		}
	}

	if options.ManageACL {
		// The shared grant is reported on only when this run asks about it:
		// explicitly, or because a Funnel expired in this very run. The serve
		// loop asks on its first tick and on a Funnel-to-none transition; a
		// steady tick stays not_requested, or the same warning would be
		// logged every 30 seconds for as long as the daemon runs.
		requested := options.CheckUnusedACL || len(expired) > 0
		switch {
		case !registryTrusted && requested:
			result.ACLAction = ACLSkipped
			result.Warnings = append(result.Warnings, fmt.Sprintf("Funnel ACL cleanup skipped: registry.json is %s; active Funnel use cannot be established", registryState))
		case activeFunnels > 0:
			result.ACLAction = ACLStillInUse
		case requested:
			// The grant is shared by every TSLink installation. This machine's
			// registry cannot prove that another host has no tagged node, even
			// after a successful device-list snapshot. Never preview or perform
			// an automatic global revocation from local reconciliation.
			result.ACLAction = ACLSkipped
			result.Warnings = append(result.Warnings, "shared Funnel ACL cleanup skipped: local absence does not prove tailnet-wide nonuse; after independently verifying all hosts and devices, explicitly run tslink tags delete-remote tag:tslink-funnel --force --manage-acl to revoke the global grant")
		}
	}

	sort.Strings(result.ExpiredFunnels)
	sort.Strings(result.DevicesMatched)
	sort.Strings(result.DevicesWouldDelete)
	sort.Strings(result.DevicesDeleted)
	sort.Strings(result.DevicesProtected)
	return result, nil
}

// removeStaleNodeState deletes the local tsnet state directory of an orphan
// service this run proved holds no remote identity any more.
//
// Safe means every one of these, checked rather than assumed:
//
//   - Not a dry run. A dry run writes nothing anywhere.
//   - The caller opted in and supplied the in-use predicate, and the predicate
//     says no tsnet server in this process holds the directory. Closing the
//     server is what releases the state; deleting it underneath a live one
//     leaves that node writing into a directory no path points at.
//   - The cleanup call itself succeeded, was not Skipped, and protected
//     nothing. Protected means the API listed a device matching one of these
//     service names that TSLink could not prove it owns. Protection is reported
//     by hostname while ownership is tracked by node ID, so there is no
//     reliable way to attribute a protection back to one service -- a single
//     protected hostname therefore stops the whole sweep rather than one entry
//     of it.
//   - Every node ID recorded for that service came back in
//     ResolvedOwnershipIDs: the union of the devices the API accepted a DELETE
//     for and the recorded IDs the API no longer lists at all. Partial
//     resolution means an identity survived remotely and the key that
//     authenticates as it is still live.
//   - The service is absent from the registry. Every name reaching here is an
//     orphan by construction, so this can only fire if that ever stops being
//     true; it costs one map lookup and the alternative is a live service
//     losing its identity.
//
// One window is open and is worth naming rather than implying away. This
// function does not run under the daemon's sync gate, and HoldsNodeState
// answers only "is this name in s.nodes right now". So between the registry
// read at the top of Reconcile and the removal here, `tslink add` can put the
// same name back and startNodeLocked can be partway through starting it --
// before the node is published into s.nodes, where HoldsNodeState would see it.
// In that interleaving the directory of a starting node is removed.
//
// The cost is bounded by what is in the directory at that moment. This branch
// is reached only after every remote node recorded for that name was deleted or
// confirmed absent, so the key being removed authenticates to nothing; the
// service that was just re-added is enrolling a new identity, not reusing that
// one. The outcome is a node that enrolls from scratch, which is what a
// re-added service does anyway.
//
// Closing it properly means holding the sync gate across the reconcile, or
// re-reading the registry immediately before each removal. Both are cheap; both
// were left out because the window is narrow enough that neither has been
// observed, and a gate held across a network-bound reconcile is its own
// availability risk.
//
// What it deliberately does not do is sweep node directories that have no
// ownership record at all -- the shape a long-removed service's directory has
// once `tslink remove` has consumed its ledger entries. That sweep would have
// to read "absent from the registry" as "not a live service", and
// registry.Load is allowed to drop entries it cannot parse, so a registry with
// one malformed service would make a live service look absent and cost it its
// node identity. cmd/remove covers that case at the point where the facts are
// still in hand.
//
// It returns the services whose remote side resolved and whose local state is
// confirmed gone: removed by this run, or already absent. Only their ownership
// rows may be forgotten. Every other service keeps its rows, because they are
// the only record through which a later run finds the state and finishes: a
// directory a live tsnet server still holds (a node the daemon has not stopped
// yet), one whose removal failed, and one this caller may not remove at all,
// such as a `tslink cleanup` process between two daemon ticks.
func removeStaleNodeState(options Options, result *Result, cleanup tailapi.CleanupResult, cleanupErr error, orphanIDs map[string][]string, serviceNames []string, active map[string]struct{}) (gone map[string]struct{}) {
	gone = make(map[string]struct{})
	configDir := tsruntime.ServiceNodeStateConfigDir(options.RegistryPath)
	mayRemove := !options.DryRun && options.CleanLocalNodeState && options.LocalNodeStateInUse != nil
	if mayRemove && (cleanupErr != nil || cleanup.Skipped || len(cleanup.Protected) > 0) {
		slog.Info("keeping local node state for orphan services; this run could not confirm every remote device is gone",
			"services", serviceNames, "protected", len(cleanup.Protected), "skipped", cleanup.Skipped)
		mayRemove = false
	}
	resolved := make(map[string]struct{}, len(cleanup.ResolvedOwnershipIDs))
	for _, id := range cleanup.ResolvedOwnershipIDs {
		resolved[id] = struct{}{}
	}
	for _, name := range serviceNames {
		if _, registered := active[name]; registered {
			continue
		}
		unresolved := 0
		for _, id := range orphanIDs[name] {
			if _, ok := resolved[id]; !ok {
				unresolved++
			}
		}
		if unresolved > 0 {
			if mayRemove {
				slog.Info("keeping local node state; some remote devices for this service are unaccounted for",
					"service", name, "unresolved_nodes", unresolved)
			}
			continue
		}
		if !mayRemove {
			// Nothing is removed here, but state that is already gone
			// leaves the rows nothing to find.
			if _, err := os.Lstat(filepath.Join(config.NodesDirIn(configDir), name)); errors.Is(err, fs.ErrNotExist) {
				gone[name] = struct{}{}
			}
			continue
		}
		if options.LocalNodeStateInUse(name) {
			slog.Info("keeping local node state and its ownership record; a tsnet server still holds it", "service", name)
			continue
		}
		if err := removeNodeStateFn(configDir, name); err != nil {
			warning := fmt.Sprintf("local node state for %q could not be removed: %v", name, err)
			slog.Warn("local node state removal failed; its ownership record is kept so a later run retries", "service", name, "error", err)
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		slog.Info("removed local node state for an orphan service with no remaining remote identity", "service", name)
		gone[name] = struct{}{}
	}
	return gone
}

// idsOf returns the ids that are ownership rows of the services in only.
func idsOf(ids []string, orphanIDs map[string][]string, only map[string]struct{}) []string {
	wanted := make(map[string]struct{})
	for name := range only {
		for _, id := range orphanIDs[name] {
			wanted[id] = struct{}{}
		}
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := wanted[id]; ok {
			kept = append(kept, id)
		}
	}
	return kept
}
