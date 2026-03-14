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
		Long: `Remove a registered service or file directory.

Example:
  tslink remove myapp`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureDir(); err != nil {
				return err
			}

			regPath, err := config.RegistryPath()
			if err != nil {
				return err
			}

			return removeService(regPath, args[0], cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	rootCmd.AddCommand(removeCmd)
}
