//go:build linux

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/spf13/cobra"
)

type UninstallResult struct {
	Path           string `json:"path"`
	Removed        bool   `json:"removed"`
	ServiceManager string `json:"service_manager"`
	Warning        string `json:"warning,omitempty"`
}

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove as systemd user service",
	Long: `Remove TSLink as a systemd user service so it no longer starts
automatically for your user session.

This command:
  1. Stops the service via 'systemctl --user stop tslink'
  2. Disables it via 'systemctl --user disable tslink'
  3. Deletes the unit file at ~/.config/systemd/user/tslink.service
  4. Runs 'systemctl --user daemon-reload' to clean up systemd state

If the stop fails and TSLink cannot confirm that the service has shut down,
the unit file is kept and the command fails. When the cause is that
'systemctl --user' cannot reach your user manager (su, sudo -u, or SSH
without a login session), rerun from a login session of this user that has
the user bus (XDG_RUNTIME_DIR=/run/user/$UID). If 'systemctl --user' is
permanently unavailable on this host,
remove ~/.config/systemd/user/tslink.service by hand.

If the service is not installed, prints a message and exits cleanly. If the
unit file is already gone but systemd still runs or enables tslink.service,
the command fails and names the commands that finish the removal.

To check if the service is still active after removal:
  systemctl --user status tslink

If you enabled lingering only for TSLink, disable it after uninstall:
  loginctl disable-linger "$USER"

	Examples:
	  tslink uninstall              Remove the systemd user service`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return withSupervisorTransaction(cmd.Context(), func() error {
			return runUninstallLocked(cmd, args)
		})
	},
}

// runUninstallLocked requires the per-user supervisor transaction lock.
func runUninstallLocked(cmd *cobra.Command, args []string) error {
	servicePath, err := systemdServicePath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(servicePath); os.IsNotExist(err) {
		if err := systemdUnitLeftBehind(servicePath); err != nil {
			return err
		}
		warning := resetFailedSystemdServiceWarning()
		if jsonOutput(cmd) {
			output.Success("uninstall", UninstallResult{Path: servicePath, Removed: false, ServiceManager: systemdServiceName, Warning: warning})
			return nil
		}
		if warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", warning)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "→ systemd user service not installed")
		return nil
	} else if err != nil {
		return fmt.Errorf("stat systemd service: %w", err)
	}

	var warnings []string
	if output, err := systemctlCombinedOutput("--user", "stop", systemdServiceName); err != nil {
		if stateErr := confirmSystemdStoppedAfterError(servicePath); stateErr != nil {
			return fmt.Errorf("stop systemd user service: %w%s; unit retained because shutdown could not be confirmed: %v", err, commandOutputSuffix(output), stateErr)
		}
		warnings = append(warnings, fmt.Sprintf("stop systemd user service: %v%s", err, commandOutputSuffix(output)))
	}
	if output, err := systemctlCombinedOutput("--user", "disable", systemdServiceName); err != nil {
		warnings = append(warnings, fmt.Sprintf("disable systemd user service: %v%s", err, commandOutputSuffix(output)))
	}

	if err := os.Remove(servicePath); err != nil {
		return fmt.Errorf("remove systemd service: %w", err)
	}
	// Reset while systemd still has the just-removed unit loaded. After
	// daemon-reload a clean unit may already be forgotten, turning this
	// best-effort cleanup into a noisy "unit not loaded" failure.
	if warning := resetFailedSystemdServiceWarning(); warning != "" {
		warnings = append(warnings, warning)
	}
	if output, err := systemctlCombinedOutput("--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd user daemon: %w%s", err, commandOutputSuffix(output))
	}

	warning := strings.Join(warnings, "; ")
	if jsonOutput(cmd) {
		output.Success("uninstall", UninstallResult{
			Path:           servicePath,
			Removed:        true,
			ServiceManager: systemdServiceName,
			Warning:        warning,
		})
		return nil
	}

	if len(warnings) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", warning)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "→ ✓ systemd user service removed")
	return nil
}

// A failed stop may still have stopped the unit. Only an explicit inactive or
// failed state with no MainPID lets uninstall continue and remove its definition.
func confirmSystemdStoppedAfterError(servicePath string) error {
	output, err := systemctlCombinedOutput("--user", "show", systemdServiceName,
		"--property=ActiveState", "--property=MainPID", "--no-pager")
	if err != nil {
		return fmt.Errorf("inspect systemd service state: %w%s; %s", err, commandOutputSuffix(output), systemdUserManagerRemedy(servicePath))
	}
	state := parseSystemdProperties(output)
	pid, pidErr := strconv.Atoi(state["MainPID"])
	if (state["ActiveState"] != "inactive" && state["ActiveState"] != "failed") || pidErr != nil || pid != 0 {
		return fmt.Errorf("ActiveState=%q MainPID=%q", state["ActiveState"], state["MainPID"])
	}
	return nil
}

// systemdUnitLeftBehind reports a tslink.service that systemd still runs or
// enables after its unit file was deleted by hand, where "not installed" would
// be false. It only reports and changes nothing: the definition this command
// would stop and disable is already gone. A user manager that cannot be
// reached is evidence of neither, and keeps the plain "not installed" answer.
func systemdUnitLeftBehind(servicePath string) error {
	var problems []string
	output, err := systemctlCombinedOutput("--user", "show", systemdServiceName,
		"--property=LoadState", "--property=ActiveState", "--property=MainPID", "--no-pager")
	if err == nil {
		state := parseSystemdProperties(output)
		active := state["ActiveState"]
		pid, pidErr := strconv.Atoi(state["MainPID"])
		if (active != "" && active != "inactive" && active != "failed") || (pidErr == nil && pid != 0) {
			problems = append(problems, fmt.Sprintf("systemd still runs it (ActiveState=%q MainPID=%q); stop it with 'systemctl --user stop %s'", active, state["MainPID"], systemdServiceName))
		}
	}
	link := filepath.Join(filepath.Dir(servicePath), "default.target.wants", systemdServiceName)
	if _, err := os.Lstat(link); err == nil {
		problems = append(problems, fmt.Sprintf("it is still enabled through %s; remove that link by hand and run 'systemctl --user daemon-reload'", link))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("systemd unit file %s is already gone, but %s; then rerun 'tslink uninstall'", servicePath, strings.Join(problems, ", and "))
}

// systemdUserManagerRemedy is the way out when uninstall keeps the unit because
// systemctl --user could not answer at all: there is no user bus in this
// session, or no systemctl. Retrying from the same shell cannot change either.
func systemdUserManagerRemedy(servicePath string) string {
	return "if 'systemctl --user' cannot reach your user manager, rerun 'tslink uninstall' from a login session of this user that has the user bus (XDG_RUNTIME_DIR=/run/user/$UID); if 'systemctl --user' is permanently unavailable on this host, remove " + servicePath + " by hand"
}

func resetFailedSystemdServiceWarning() string {
	output, err := systemctlCombinedOutput("--user", "reset-failed", systemdServiceName)
	if err == nil {
		return ""
	}
	stateOutput, stateErr := systemctlCombinedOutput(
		"--user", "show", systemdServiceName,
		"--property=LoadState", "--property=ActiveState", "--no-pager",
	)
	if stateErr == nil {
		properties := parseSystemdProperties(stateOutput)
		if properties["LoadState"] == "not-found" && properties["ActiveState"] == "inactive" {
			return ""
		}
	}
	return fmt.Sprintf("reset failed systemd user service state: %v%s", err, commandOutputSuffix(output))
}

func init() {
	rootCmd.AddCommand(uninstallCmd)
}
