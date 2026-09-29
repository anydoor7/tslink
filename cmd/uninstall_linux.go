//go:build linux

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/output"
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

If the service is not installed, prints a message and exits cleanly.

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
