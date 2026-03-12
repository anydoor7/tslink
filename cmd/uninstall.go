package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove macOS LaunchAgent",
	Long: `Remove the TSLink LaunchAgent so it no longer auto-starts.

Example:
  tslink uninstall`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := plistPath()

		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Println("→ LaunchAgent not installed")
			return nil
		}

		exec.Command("launchctl", "unload", path).Run()

		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove plist: %w", err)
		}

		fmt.Println("→ ✓ LaunchAgent removed")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
