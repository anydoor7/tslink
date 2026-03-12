package cmd

import (
	"fmt"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

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

			name := args[0]
			if err := registry.Remove(regPath, name); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ removed: %s\n", name)
			return nil
		},
	}

	rootCmd.AddCommand(removeCmd)
}
