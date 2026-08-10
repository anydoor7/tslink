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
     confirms every domain was checked and reports no real error

If the LaunchAgent is not installed, prints a message and exits cleanly.
If any domain is unavailable, the plist is kept even when another domain
confirms a successful bootout. This prevents a later install from treating the
machine as fresh while a job may remain in the unavailable domain. Retry when
that domain is addressable. As an explicit recovery escape hatch,
'tslink uninstall --force' removes the plist after all addressable domains are
unloaded or absent, but it may leave a job running in an unavailable domain.
A real launchctl error is always fatal, including with --force.
Log files in ~/.config/tslink/logs/ are NOT removed.

	Examples:
	  tslink uninstall              Remove the LaunchAgent`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, err := cmd.Flags().GetBool("force")
		if err != nil {
			return fmt.Errorf("read --force: %w", err)
		}
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

		bootout := bootoutLaunchAgent(force)
		if bootout.Err != nil {
			warning := launchctlWarning("LaunchAgent plist was kept because launchctl bootout failed", bootout.Err, []byte(bootout.Output))
			remedy := "fix the launchctl failure and retry 'tslink uninstall'"
			if bootout.Detail != "" {
				remedy = bootout.Detail
			}
			uninstallErr := fmt.Errorf("%s; the LaunchAgent is still installed at %s; %s", warning, path, remedy)
			if jsonOutput(cmd) {
				result := output.NewFailureForError("uninstall", uninstallErr)
				result.Data = UninstallResult{
					PlistPath:        path,
					Removed:          false,
					LaunchctlOutcome: bootout.Outcome,
					LaunchctlTarget:  bootout.Target,
					LaunchctlOutput:  bootout.Output,
					Detail:           bootout.Detail,
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

func bootoutLaunchAgent(force bool) launchctlBootoutResult {
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
	if firstUnavailable != nil && !force {
		firstUnavailable.Output = combinedOutput
		firstUnavailable.Detail = unavailableDomainRemedy(firstUnavailable.Target, firstSuccess)
		return *firstUnavailable
	}
	if firstSuccess != nil {
		if firstUnavailable != nil {
			firstSuccess.Warning = forcedUninstallWarning(firstUnavailable.Target)
		}
		return *firstSuccess
	}
	if firstUnavailable != nil {
		firstUnavailable.Output = combinedOutput
		firstUnavailable.Err = nil
		firstUnavailable.Warning = forcedUninstallWarning(firstUnavailable.Target)
		firstUnavailable.Detail = "The plist was removed by explicit --force without confirming every launchd domain"
		return *firstUnavailable
	}
	return launchctlBootoutResult{
		Outcome: launchctlOutcomeAlreadyAbsent,
		Output:  combinedOutput,
		Detail:  "LaunchAgent was already absent from all launchd domains",
	}
}

func unavailableDomainRemedy(target string, successful *launchctlBootoutResult) string {
	confirmed := "no other domain confirmed a successful bootout"
	if successful != nil {
		confirmed = fmt.Sprintf("%s confirmed a successful bootout", successful.Target)
	}
	return fmt.Sprintf(
		"launchctl %s %s; %s, but the plist was kept because a job may still be loaded there; retry when the domain is addressable, or run 'tslink uninstall --force' to remove the plist while accepting that risk",
		target,
		launchctlUnavailableReason(target),
		confirmed,
	)
}

func forcedUninstallWarning(target string) string {
	return fmt.Sprintf(
		"--force removed the plist even though launchctl %s %s; a job may still be loaded there; run 'launchctl print %s' when the domain is addressable to confirm",
		target,
		launchctlUnavailableReason(target),
		target,
	)
}

func launchctlUnavailableReason(target string) string {
	if strings.HasPrefix(target, "gui/") {
		return "was unavailable because no desktop session exists for this user"
	}
	return "could not be addressed from the current launchd context"
}

func init() {
	uninstallCmd.Flags().Bool("force", false, "Remove the plist despite an unavailable launchd domain (may leave a daemon running)")
	rootCmd.AddCommand(uninstallCmd)
}
