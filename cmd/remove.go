package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/monody0007/tslink/internal/config"
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
}

var deleteDevicesFn = tailapi.DeleteDevicesForService
var ensureDirFn = config.EnsureDir

func removeServiceResult(regPath, name string) (RemoveResult, error) {
	ownershipPath := filepath.Join(filepath.Dir(regPath), "node-ownership.json")
	ledger, ownershipErr := tsruntime.LoadOwnership(ownershipPath)
	var ownedNodeIDs []string
	if ownershipErr == nil {
		for _, node := range ledger.Nodes {
			if node.ServiceName == name {
				ownedNodeIDs = append(ownedNodeIDs, node.NodeID)
			}
		}
	}
	svc, removed, err := registry.RemoveAndReturn(regPath, name)
	if err != nil {
		return RemoveResult{}, err
	}

	result := RemoveResult{Name: name, Removed: removed}

	if result.Removed {
		if ownershipErr != nil {
			result.DeviceWarning = fmt.Sprintf("could not read node ownership proof: %v", ownershipErr)
			return result, nil
		}
		cleanup, err := deleteDevicesFn(context.Background(), tailapi.CleanupTargetForOwnedService(svc, ownedNodeIDs))
		if err != nil {
			if errors.Is(err, tailapi.ErrNoAPIClient) {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = err.Error()
			} else {
				result.DeviceWarning = fmt.Sprintf("could not remove tailnet node: %v", err)
			}
		} else {
			result.DeviceCleaned = len(cleanup.Deleted) > 0
			if len(cleanup.ResolvedOwnershipIDs) > 0 {
				if err := tsruntime.RemoveOwnedNodeIDs(ownershipPath, cleanup.ResolvedOwnershipIDs); err != nil {
					result.DeviceWarning = fmt.Sprintf("device cleanup succeeded but ownership ledger update failed: %v", err)
				}
			}
			if cleanup.Skipped {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = cleanup.SkipReason
			}
		}
	}
	return result, nil
}

func removeService(regPath, name string, out, errOut io.Writer, isJSON bool) error {
	return removeServiceWithOptions(regPath, name, out, errOut, isJSON, false)
}

func removeServiceWithOptions(regPath, name string, out, errOut io.Writer, isJSON, strict bool) error {
	result, err := removeServiceResult(regPath, name)
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

The service's node state in ~/.config/tslink/nodes/<name>/ is NOT removed by
this command. It will be cleaned up on the next 'tslink serve' or can be
removed manually.

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

			return removeServiceWithOptions(regPath, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonOutput(cmd), strict)
		},
	}

	removeCmd.Flags().Bool("strict", false, "Return not_found (exit 5) when absent; default is idempotent")
	rootCmd.AddCommand(removeCmd)
}
