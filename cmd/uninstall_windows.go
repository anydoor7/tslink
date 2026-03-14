//go:build windows

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove from Windows Startup",
	Long: `Remove the TSLink Startup entry so it no longer launches in the
background when you sign in.

Example:
  tslink uninstall`,
	RunE: func(cmd *cobra.Command, args []string) error {
		startupPath, err := windowsStartupScriptPath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(startupPath); os.IsNotExist(err) {
			fmt.Println("→ Startup script not installed")
			return nil
		}

		if err := os.Remove(startupPath); err != nil {
			return fmt.Errorf("remove Startup script: %w", err)
		}

		fmt.Println("→ ✓ Startup script removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
