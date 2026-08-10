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
	PlistPath        string `json:"plist_path"`
	Removed          bool   `json:"removed"`
	LaunchctlOutcome string `json:"launchctl_outcome"`
	LaunchctlTarget  string `json:"launchctl_target"`
	LaunchctlOutput  string `json:"launchctl_output,omitempty"`
	Detail           string `json:"detail,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

type launchctlBootoutResult struct {
	Outcome string
	Target  string
	Output  string
	Detail  string
	Warning string
	Err     error
}

const (
	launchctlOutcomeNotInstalled  = "not_installed"
	launchctlOutcomeUnloaded      = "unloaded"
	launchctlOutcomeAlreadyAbsent = "already_absent"
	launchctlOutcomeUnconfirmed   = "unconfirmed"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as macOS LaunchAgent",
	Long: `Remove the TSLink macOS LaunchAgent so it no longer auto-starts.

This command:
  1. Unloads the agent from gui/$(id -u), or user/$(id -u) for headless installs
  2. Deletes ~/Library/LaunchAgents/com.tslink.daemon.plist after launchctl
     reports no real error and either confirms a successful bootout in an
     addressable domain or confirms the job absent from every domain

If the LaunchAgent is not installed, prints a message and exits cleanly.
An unavailable domain does not block removal when the other domain confirms a
successful bootout. If no domain succeeds and any domain is unavailable, or if
launchctl reports a real error, the plist is kept and the command exits non-zero.
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
				output.Success("uninstall", UninstallResult{
					PlistPath:        path,
					Removed:          false,
					LaunchctlOutcome: launchctlOutcomeNotInstalled,
				})
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
					PlistPath:        path,
					Removed:          false,
					LaunchctlOutcome: bootout.Outcome,
					LaunchctlTarget:  bootout.Target,
					LaunchctlOutput:  bootout.Output,
					Detail:           bootout.Detail,
					Warning:          warning,
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
				PlistPath:        path,
				Removed:          true,
				LaunchctlOutcome: bootout.Outcome,
				LaunchctlTarget:  bootout.Target,
				LaunchctlOutput:  bootout.Output,
				Detail:           bootout.Detail,
				Warning:          bootout.Warning,
			})
			return nil
		}

		if bootout.Warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", bootout.Warning)
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
	var firstSuccess *launchctlBootoutResult
	var firstRealError *launchctlBootoutResult
	var firstUnavailable *launchctlBootoutResult
	for _, target := range targets {
		output, err := launchctlCombinedOutput("bootout", target)
		text := strings.TrimSpace(string(output))
		if text != "" {
			outputs = append(outputs, text)
		}
		attempt := launchctlBootoutResult{Target: target, Output: text, Err: err}
		if err == nil && firstSuccess == nil {
			success := attempt
			success.Outcome = launchctlOutcomeUnloaded
			firstSuccess = &success
		}
		if err != nil && launchctlDomainNotFound(output, err) && firstUnavailable == nil {
			unavailable := attempt
			unavailable.Outcome = launchctlOutcomeUnconfirmed
			firstUnavailable = &unavailable
		}
		if err != nil && !launchctlServiceNotFound(output, err) && !launchctlDomainNotFound(output, err) && firstRealError == nil {
			failure := attempt
			failure.Outcome = launchctlOutcomeUnconfirmed
			firstRealError = &failure
		}
	}
	combinedOutput := strings.Join(outputs, "\n")
	if firstRealError != nil {
		firstRealError.Output = combinedOutput
		return *firstRealError
	}
	if firstSuccess != nil {
		if firstUnavailable != nil {
			firstSuccess.Warning = fmt.Sprintf(
				"launchctl %s could not be addressed from this session; a job may still be loaded there; from a GUI session run 'launchctl print %s' to confirm",
				firstUnavailable.Target,
				firstUnavailable.Target,
			)
		}
		return *firstSuccess
	}
	if firstUnavailable != nil {
		firstUnavailable.Output = combinedOutput
		return *firstUnavailable
	}
	return launchctlBootoutResult{
		Outcome: launchctlOutcomeAlreadyAbsent,
		Output:  combinedOutput,
		Detail:  "LaunchAgent was already absent from all launchd domains",
	}
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
