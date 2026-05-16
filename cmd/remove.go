package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
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

func removeService(regPath, name string, out, errOut io.Writer, isJSON bool) error {
	svc, removed, err := registry.RemoveAndReturn(regPath, name)
	if err != nil {
		return err
	}

	result := RemoveResult{Name: name, Removed: removed}

	if removed {
		cleanup, err := deleteDevicesFn(context.Background(), tailapi.CleanupTargetForService(svc))
		if err != nil {
			if errors.Is(err, tailapi.ErrNoAPIClient) {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = err.Error()
			} else {
				result.DeviceWarning = fmt.Sprintf("could not remove tailnet node: %v", err)
				if !isJSON {
					fmt.Fprintf(errOut, "→ warning: could not remove tailnet node: %v\n", err)
				}
			}
		} else {
			result.DeviceCleaned = len(cleanup.Deleted) > 0
			if cleanup.Skipped {
				result.DeviceCleanupSkipped = true
				result.DeviceSkipReason = cleanup.SkipReason
			}
		}
	}

	if isJSON {
		output.Success("remove", result)
		return nil
	}

	if removed {
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
  2. Attempts ownership-safe remote cleanup via the Tailscale API. Remote
     devices are deleted only when TSLink has exact device ownership proof;
     hostname/tag matches without that proof are reported as protected.

If the gateway is running, it will detect the registry change via hot-reload
and stop the removed service's tsnet node automatically.

The service's node state in ~/.config/tslink/nodes/<name>/ is NOT removed by
this command. It will be cleaned up on the next 'tslink serve' or can be
removed manually.

Examples:
  tslink remove myapp          Remove a proxy service
  tslink remove docs           Remove a file-sharing service
  tslink remove mydb           Remove a TCP service`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureDirFn(); err != nil {
				return err
			}

			regPath, err := registryPathFn()
			if err != nil {
				return err
			}

			return removeService(regPath, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonOutput(cmd))
		},
	}

	rootCmd.AddCommand(removeCmd)
}
