//go:build darwin

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

// UninstallResult is the JSON payload for the uninstall command.
type UninstallResult struct {
	PlistPath       string `json:"plist_path"`
	Removed         bool   `json:"removed"`
	LaunchctlTarget string `json:"launchctl_target"`
	LaunchctlOutput string `json:"launchctl_output,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

type launchctlBootoutResult struct {
	Target string
	Output string
	Err    error
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as macOS LaunchAgent",
	Long: `Remove the TSLink macOS LaunchAgent so it no longer auto-starts.

This command:
  1. Unloads the agent from gui/$(id -u), or user/$(id -u) for headless installs
  2. Deletes ~/Library/LaunchAgents/com.tslink.daemon.plist only after unload succeeds

If the LaunchAgent is not installed, prints a message and exits cleanly.
If launchctl bootout fails, the plist is kept, the command exits non-zero, and
you can fix the reported launchctl failure before retrying this command.
Log files in ~/.config/tslink/logs/ are NOT removed.

	Examples:
	  tslink uninstall              Remove the LaunchAgent`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := plistPath()
		if err != nil {
			return err
		}

		if _, err := os.Stat(path); os.IsNotExist(err) {
			if jsonOutput(cmd) {
				output.Success("uninstall", UninstallResult{PlistPath: path, Removed: false, LaunchctlTarget: launchctlServiceTarget()})
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "→ LaunchAgent not installed")
			return nil
		} else if err != nil {
			return fmt.Errorf("stat plist: %w", err)
		}

		bootout := bootoutLaunchAgent()
		if bootout.Err != nil {
			warning := launchctlWarning("LaunchAgent plist was kept because launchctl bootout failed", bootout.Err, []byte(bootout.Output))
			uninstallErr := fmt.Errorf("%s; the LaunchAgent is still installed at %s, so fix the launchctl failure and retry 'tslink uninstall'", warning, path)
			if jsonOutput(cmd) {
				result := output.NewFailureForError("uninstall", uninstallErr)
				result.Data = UninstallResult{
					PlistPath:       path,
					Removed:         false,
					LaunchctlTarget: bootout.Target,
					LaunchctlOutput: bootout.Output,
					Warning:         warning,
				}
				output.WriteJSON(os.Stdout, result)
				return output.SilentExit(output.ExitError)
			}
			return uninstallErr
		}

		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove plist: %w", err)
		}

		if jsonOutput(cmd) {
			output.Success("uninstall", UninstallResult{
				PlistPath:       path,
				Removed:         true,
				LaunchctlTarget: bootout.Target,
				LaunchctlOutput: bootout.Output,
			})
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ LaunchAgent removed")
		return nil
	},
}

func bootoutLaunchAgent() launchctlBootoutResult {
	targets := []string{
		launchctlServiceTargetForDomain(launchctlDomain()),
		launchctlServiceTargetForDomain(launchctlUserDomain()),
	}
	var outputs []string
	var last launchctlBootoutResult
	for _, target := range targets {
		output, err := launchctlCombinedOutput("bootout", target)
		text := strings.TrimSpace(string(output))
		if text != "" {
			outputs = append(outputs, text)
		}
		last = launchctlBootoutResult{Target: target, Output: strings.Join(outputs, "\n"), Err: err}
		if err == nil {
			last.Output = strings.Join(outputs, "\n")
			return last
		}
	}
	return last
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
