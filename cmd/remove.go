package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var deleteDevicesFn = tailapi.DeleteDevicesByHostname
var ensureDirFn = config.EnsureDir

func removeService(regPath, name string, out, errOut io.Writer) error {
	if err := registry.Remove(regPath, name); err != nil {
		return err
	}

	fmt.Fprintf(out, "→ ✓ removed: %s\n", name)

	if err := deleteDevicesFn(context.Background(), name); err != nil {
		fmt.Fprintf(errOut, "→ warning: could not remove tailnet node: %v\n", err)
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
  2. Automatically deletes the corresponding tailnet device via the Tailscale API
     (requires a valid API access token; skipped if using OAuth client secret)

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

			return removeService(regPath, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	rootCmd.AddCommand(removeCmd)
}
