package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

// RemoveResult is the JSON payload for the remove command.
type RemoveResult struct {
	Name                 string `json:"name"`
	Removed              bool   `json:"removed"`
	DeviceCleaned        bool   `json:"device_cleaned"`
	DeviceCleanupSkipped bool   `json:"device_cleanup_skipped"`
	DeviceSkipReason     string `json:"device_skip_reason,omitempty"`
	DeviceWarning        string `json:"device_warning,omitempty"`
	// NodeStateKeptReason explains a local tsnet state directory this command
	// left in place, and only for the cause that reaches no other field here.
	// A protected hostname or a skipped cleanup are already stated by
	// device_cleanup_skipped and device_skip_reason; partial resolution --
	// some of the service's recorded nodes deleted and some unaccounted for --
	// was previously a silent decision.
	NodeStateKeptReason string `json:"node_state_kept_reason,omitempty"`
}

var deleteDevicesFn = tailapi.DeleteDevicesForService
var ensureDirFn = config.EnsureDir
var removeNowFn = time.Now
var removeNodeStateFn = tsruntime.RemoveServiceNodeState

// removeDaemonRunningFn answers "could a live tsnet server still be holding
// this service's state directory?". It is deliberately pessimistic when it
// cannot tell: an unresolvable PID path reports running, which keeps the
// directory. A wrong "not running" deletes state underneath a live node; a
// wrong "running" leaves a directory behind, which is the bug this is fixing
// and not a new one.
//
// It asks the process-default config directory, while the directory this
// command deletes is derived from the registry path it was given. In
// production those are the same place -- the registry path comes from
// config.RegistryPath(), and the PID file is its sibling -- so the two agree.
// They can disagree only for a caller that passes a registry path from a
// non-default installation, and the disagreement is one-directional and
// harmless in the safe sense: the answer is then about some other
// installation's daemon, and the worst outcome is a keep that did not need to
// happen. Deriving the PID path from the registry path as well would make them
// same-origin by construction, which is the better shape if this command ever
// grows a --config-dir style flag.
var removeDaemonPIDPathFn = config.PIDPath

var removeDaemonRunningFn = func() bool {
	pidPath, err := removeDaemonPIDPathFn()
	if err != nil {
		return true
	}
	return daemon.IsRunning(pidPath)
}

// localNodeStateIsStale reports whether the local tsnet state for a service
// just removed from the registry can no longer authenticate to anything, and
// returns a reason for the one keep this command would otherwise never
// explain.
//
// A keep with no stated cause is indistinguishable, from outside, from a
// cleanup that never ran. Two of the three causes are already stated -- a
// protected hostname and a skipped cleanup both reach the caller through
// RemoveResult -- so only partial resolution needs a reason of its own.
//
// Every condition below is a fact this process observed in this invocation, not
// an inference:
//
//   - the cleanup was neither Skipped nor Protected. A Protected hostname means
//     the API listed a device matching this service's name that TSLink could
//     not prove it owns; the local key may still be that device's key.
//   - every recorded node ID came back in ResolvedOwnershipIDs, which is the
//     union of the devices the API accepted a DELETE for and the recorded IDs
//     the API no longer lists at all. Partial resolution means some identity
//     survived remotely, so the state that authenticates as it is still live.
//   - no recorded node ID at all is its own sufficient case: the service never
//     proved a remote node, and the clause above already established the API
//     did not report a hostname match either.
//
// There is no error parameter. The only call site is inside the branch where
// the cleanup call returned no error, so an error argument could only ever be
// nil -- a parameter that cannot vary looks like a guard and is not one.
//
// Absent a remote identity the directory holds only a key that authenticates to
// nothing, which is why removing it is not a loss.
func localNodeStateIsStale(ownedNodeIDs []string, cleanup tailapi.CleanupResult) (stale bool, unreportedKeepReason string) {
	// Protected and Skipped are already carried by device_cleanup_skipped and
	// device_skip_reason, so they return no reason here: repeating them would
	// put the same fact on two surfaces and make the field useless as a signal
	// that something went unexplained.
	if len(cleanup.Protected) > 0 || cleanup.Skipped {
		return false, ""
	}
	resolved := make(map[string]struct{}, len(cleanup.ResolvedOwnershipIDs))
	for _, id := range cleanup.ResolvedOwnershipIDs {
		resolved[id] = struct{}{}
	}
	unresolved := 0
	for _, id := range ownedNodeIDs {
		if _, ok := resolved[id]; !ok {
			unresolved++
		}
	}
	if unresolved > 0 {
		return false, fmt.Sprintf("%d of %d recorded tailnet node(s) for this service were neither deleted nor confirmed absent", unresolved, len(ownedNodeIDs))
	}
	return true, ""
}

func removeServiceResult(regPath, ownershipPath, name string) (RemoveResult, error) {
	return removeServiceResultContext(context.Background(), regPath, ownershipPath, name)
}

// removeServiceResultContext is removeServiceResult with the caller's context
// handed to remote device cleanup, so a cancelled request or a stopped MCP
// session interrupts a cleanup call the Tailscale API never answers.
func removeServiceResultContext(ctx context.Context, regPath, ownershipPath, name string) (RemoveResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// The ledger is read before anything changes. Without it this command
	// can neither record that the user removed the service nor find its
	// devices, and unregistering it anyway leaves ownership rows nobody
	// retired, which withhold that service's device deletion for good.
	if _, err := tsruntime.LoadOwnership(ownershipPath); err != nil {
		return RemoveResult{}, err
	}
	// The retirement is recorded before the registry entry goes, so no crash
	// or failure in between can leave an unregistered service whose rows say
	// nothing about its removal, and a failed registry write puts the rows
	// back. Both happen under the registry lock and then the ledger lock, so
	// the rows retired are the ones recorded up to the moment the entry goes:
	// a node that enrolled while this command waited for the registry lock is
	// retired too, rather than left with a row nobody retired.
	var owned []tsruntime.OwnedNode
	svc, removed, err := registry.RemoveAndReturnWithin(regPath, name, func(_ registry.Service, commit func() error) error {
		committing := false
		var err error
		owned, err = tsruntime.RetireServiceNodes(ownershipPath, name, removeNowFn(), func() error {
			committing = true
			return commit()
		})
		if err != nil && !committing {
			return fmt.Errorf("record the removal of %q in the node ownership ledger before unregistering it: %w", name, err)
		}
		return err
	})
	if err != nil {
		return RemoveResult{}, err
	}
	var ownedNodeIDs []string
	for _, node := range owned {
		ownedNodeIDs = append(ownedNodeIDs, node.NodeID)
	}

	result := RemoveResult{Name: name, Removed: removed}

	if result.Removed {
		cleanup, err := deleteDevicesFn(ctx, tailapi.CleanupTargetForOwnedService(svc, ownedNodeIDs))
		if err != nil {
			if errors.Is(err, tailapi.ErrNoAPIClient) {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = err.Error()
			} else {
				result.DeviceWarning = fmt.Sprintf("could not remove tailnet node: %v", err)
			}
		} else {
			result.DeviceCleaned = len(cleanup.Deleted) > 0
			// While a daemon runs, a live tsnet server may still hold the
			// state directory, and only the daemon can tell. Its lifecycle
			// reconciler is then the one authority that deletes the state: it
			// finds the service through these retired ownership rows, confirms
			// the remote side again, and removes the directory once no node
			// holds it. So the rows stay retired here instead of being
			// forgotten; the reconciler forgets them when it finishes.
			//
			// Without a daemon nothing else will ever look, so this command
			// finishes the job itself: that is how ~/.config/tslink/nodes/
			// would otherwise accumulate directories for services removed
			// months ago.
			daemonRunning := removeDaemonRunningFn()
			if len(cleanup.ResolvedOwnershipIDs) > 0 && !daemonRunning {
				if err := tsruntime.RemoveOwnedNodeIDs(ownershipPath, cleanup.ResolvedOwnershipIDs); err != nil {
					result.DeviceWarning = fmt.Sprintf("device cleanup succeeded but ownership ledger update failed: %v", err)
				}
			}
			if cleanup.Skipped {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = cleanup.SkipReason
			}
			// Nothing on this path writes to stderr. `tslink remove` reports
			// through its result envelope, and the compiled-binary contract
			// tests assert stderr is empty on success, so an slog line here
			// would fail the machine contract on the branch that fires most
			// often -- once for every removal without an API client, and once
			// for every removal while a daemon is running.
			stale, unreportedKeepReason := localNodeStateIsStale(ownedNodeIDs, cleanup)
			switch {
			case !stale:
				result.NodeStateKeptReason = unreportedKeepReason
			case daemonRunning:
				// Deliberately silent: the daemon's reconciler deleting the
				// directory is the ordinary outcome.
			default:
				if removeErr := removeNodeStateFn(tsruntime.ServiceNodeStateConfigDir(regPath), name); removeErr != nil {
					result.DeviceWarning = fmt.Sprintf("service and tailnet node removed but local node state could not be deleted: %v", removeErr)
				}
			}
		}
	}
	return result, nil
}

func removeService(regPath, ownershipPath, name string, out, errOut io.Writer, isJSON bool) error {
	return removeServiceWithOptions(regPath, ownershipPath, name, out, errOut, isJSON, false)
}

func removeServiceWithOptions(regPath, ownershipPath, name string, out, errOut io.Writer, isJSON, strict bool) error {
	return removeServiceWithContext(context.Background(), regPath, ownershipPath, name, out, errOut, isJSON, strict)
}

func removeServiceWithContext(ctx context.Context, regPath, ownershipPath, name string, out, errOut io.Writer, isJSON, strict bool) error {
	result, err := removeServiceResultContext(ctx, regPath, ownershipPath, name)
	if err != nil {
		return err
	}
	if strict && !result.Removed {
		return output.ErrNotFound(fmt.Sprintf("service not found: %s", name))
	}
	if result.DeviceWarning != "" && !isJSON {
		fmt.Fprintf(errOut, "→ warning: %s\n", result.DeviceWarning)
	}

	if isJSON {
		output.Success("remove", result)
		return nil
	}

	if result.Removed {
		fmt.Fprintf(out, "→ ✓ removed: %s\n", name)
		if result.DeviceCleanupSkipped {
			fmt.Fprintf(out, "→ remote tailnet node cleanup skipped: %s\n", result.DeviceSkipReason)
		}
		if result.NodeStateKeptReason != "" {
			fmt.Fprintf(out, "→ local node state kept: %s\n", result.NodeStateKeptReason)
		}
	} else {
		fmt.Fprintf(out, "→ %s not registered, nothing to remove\n", name)
	}
	return nil
}

func init() {
	removeCmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a service",
		Long: `Remove a registered service from the TSLink registry.

This command:
  1. Removes the service entry from ~/.config/tslink/registry.json
  2. Deletes a matching tailnet device only when TSLink has durable exact
     NodeID ownership proof. Hostname-only matches remain protected.

If the gateway is running, it will detect the registry change via hot-reload
and stop the removed service's tsnet node automatically.

The service's node state in ~/.config/tslink/nodes/<name>/ is removed once the
service is confirmed to hold no remote tailnet identity: either its recorded
device was deleted, or it never had one.

While a daemon is running this command leaves that directory to the daemon: it
keeps the service's retired ownership record, and the daemon's lifecycle
reconciliation confirms the remote side again and removes the directory once no
node of its own holds it. Without a daemon this command removes it itself.

State is also kept whenever the remote side could not be confirmed, such as a
protected hostname-only match or an unavailable API client.

By default, removal is idempotent: an absent service is reported as unchanged
and the command exits successfully. Use --strict to return not_found (exit 5)
when the service is absent.

Examples:
  tslink remove myapp          Remove a proxy service
  tslink remove docs           Remove a file-sharing service
  tslink remove mydb           Remove a TCP service`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			strict, err := cmd.Flags().GetBool("strict")
			if err != nil {
				return err
			}
			if err := ensureDirFn(); err != nil {
				return err
			}

			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			ownershipPath, err := config.NodeOwnershipPath()
			if err != nil {
				return err
			}

			return removeServiceWithContext(cmd.Context(), regPath, ownershipPath, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonOutput(cmd), strict)
		},
	}

	removeCmd.Flags().Bool("strict", false, "Return not_found (exit 5) when absent; default is idempotent")
	rootCmd.AddCommand(removeCmd)
}
