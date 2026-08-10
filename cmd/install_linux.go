//go:build linux

package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

const systemdServiceName = "tslink.service"
const systemdRestartSec = 30

var (
	linuxUserHomeDirFn      = os.UserHomeDir
	linuxExecutablePathFn   = os.Executable
	linuxEvalSymlinksFn     = filepath.EvalSymlinks
	linuxUserNameFn         = defaultLinuxUserName
	linuxUserIDFn           = os.Getuid
	installDaemonConflictFn = func() error {
		return detectInstallDaemonConflict("no systemd user unit is installed, so stop the manual daemon with 'tslink stop' and retry 'tslink install'")
	}
	installDaemonArtifactConflictFn = func() error {
		return detectInstallDaemonConflict("a systemd user unit is installed, but TSLink could not confirm that systemd owns the running daemon; stop the manual daemon with 'tslink stop' and retry 'tslink install'; keep the existing unit installed")
	}
	systemctlCombinedOutput  = func(args ...string) ([]byte, error) { return exec.Command("systemctl", args...).CombinedOutput() }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return exec.Command("loginctl", args...).CombinedOutput() }
)

type InstallResult struct {
	Path           string `json:"path"`
	Installed      bool   `json:"installed"`
	Started        bool   `json:"started"`
	ServiceManager string `json:"service_manager"`
	Warning        string `json:"warning,omitempty"`
}

type systemdPreviousState struct {
	Existed      bool
	Unit         []byte
	Mode         os.FileMode
	OwnedRunning bool
}

type systemdRestoreResult struct {
	UnitRestored bool
	Restarted    bool
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as systemd user service",
	Long: `Register TSLink as a systemd user service so it starts automatically
for your user session and restarts if it crashes.

This command:
  1. Creates a unit file at ~/.config/systemd/user/tslink.service
  2. Configures it to run 'tslink serve' with throttled auto-restart on failure
  3. Runs 'systemctl --user daemon-reload' to pick up the new unit
  4. Enables and restarts the service immediately so the new unit takes effect
  5. Checks systemd lingering and prints guidance for headless/logout survival

Re-running 'tslink install' is the supported upgrade path. When a unit already
exists, TSLink only treats a running daemon as a systemd handoff when the pidfile
PID matches systemd's MainPID; otherwise the manual-daemon conflict guard applies.
Before replacing an existing unit, TSLink saves it. If daemon-reload, enable,
restart, or post-restart verification fails, TSLink stops the failed service,
restores the previous unit, reloads systemd, and restarts a previously identified
systemd-owned service. This does not restore an executable binary that was
replaced before this command ran. Fix the reported cause and re-run
'tslink install'.

To check the service status:
  systemctl --user status tslink

To view logs:
  journalctl --user -u tslink

To remove the autostart:
  tslink uninstall

For headless Linux hosts where the service must survive logout:
  loginctl enable-linger "$USER"

If lingering was enabled only for TSLink, disable it after uninstall:
  loginctl disable-linger "$USER"

	Examples:
	  tslink install                Register and restart the systemd service`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		servicePath, err := systemdServicePath()
		if err != nil {
			return err
		}
		previousState, err := captureSystemdPreviousState(servicePath)
		if err != nil {
			return err
		}

		exe, err := linuxExecutablePathFn()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = linuxEvalSymlinksFn(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
			return fmt.Errorf("create systemd user dir: %w", err)
		}

		service := systemdServiceContents(exe)
		if err := atomicfile.WriteFile(servicePath, []byte(service)); err != nil {
			return fmt.Errorf("write systemd service: %w", err)
		}

		if installErr := activateSystemdService(); installErr != nil {
			if !previousState.Existed {
				return installErr
			}
			restoreResult, restoreErr := restorePreviousSystemdUnit(previousState, servicePath)
			if restoreErr != nil {
				status := "the previous systemd user unit could not be restored"
				if restoreResult.UnitRestored {
					status = "the previous systemd user unit was restored, but the prior managed service is not confirmed running"
				}
				return fmt.Errorf("%v; upgrade failed and automatic restoration was incomplete: %v; %s; fix the reported cause and re-run 'tslink install'", installErr, restoreErr, status)
			}
			if restoreResult.Restarted {
				return fmt.Errorf("%v; upgrade failed, so the previous systemd user unit was restored and restarted; fix the reported cause and re-run 'tslink install'", installErr)
			}
			return fmt.Errorf("%v; upgrade failed, so the previous systemd user unit was restored; no prior systemd-owned running daemon was identified, so no service was restarted; fix the reported cause and re-run 'tslink install'", installErr)
		}

		warning := linuxLingerWarning()
		if jsonOutput(cmd) {
			output.Success("install", InstallResult{
				Path:           servicePath,
				Installed:      true,
				Started:        true,
				ServiceManager: systemdServiceName,
				Warning:        warning,
			})
			return nil
		}

		if warning != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", warning)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ systemd user service installed and restarted: %s\n", servicePath)
		return nil
	},
}

func captureSystemdPreviousState(servicePath string) (systemdPreviousState, error) {
	info, err := os.Stat(servicePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return systemdPreviousState{}, fmt.Errorf("inspect existing systemd user unit: %w", err)
		}
		if err := installDaemonConflictFn(); err != nil {
			return systemdPreviousState{}, err
		}
		return systemdPreviousState{}, nil
	}

	unit, err := os.ReadFile(servicePath)
	if err != nil {
		return systemdPreviousState{}, fmt.Errorf("read existing systemd user unit before upgrade: %w", err)
	}
	state := systemdPreviousState{
		Existed:      true,
		Unit:         unit,
		Mode:         secureSystemdUnitMode(info.Mode().Perm()),
		OwnedRunning: systemdOwnsRunningDaemon(),
	}
	if !state.OwnedRunning {
		if err := installDaemonArtifactConflictFn(); err != nil {
			return systemdPreviousState{}, err
		}
	}
	return state, nil
}

func activateSystemdService() error {
	if commandOutput, err := systemctlCombinedOutput("--user", "daemon-reload"); err != nil {
		return fmt.Errorf("reload systemd user daemon: %w%s", err, commandOutputSuffix(commandOutput))
	}
	if commandOutput, err := systemctlCombinedOutput("--user", "enable", systemdServiceName); err != nil {
		return fmt.Errorf("enable systemd user service: %w%s", err, commandOutputSuffix(commandOutput))
	}
	if commandOutput, err := systemctlCombinedOutput("--user", "restart", systemdServiceName); err != nil {
		return fmt.Errorf("restart systemd user service: %w%s", err, commandOutputSuffix(commandOutput))
	}
	return verifySystemdServiceRunning()
}

func restorePreviousSystemdUnit(previous systemdPreviousState, servicePath string) (systemdRestoreResult, error) {
	result := systemdRestoreResult{}
	var restoreErrs []error
	if commandOutput, err := systemctlCombinedOutput("--user", "stop", systemdServiceName); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("stop failed upgraded systemd user service: %w%s", err, commandOutputSuffix(commandOutput)))
	}
	if err := atomicfile.WriteFile(servicePath, previous.Unit); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore previous systemd user unit %s: %w", servicePath, err))
		return result, errors.Join(restoreErrs...)
	}
	if err := os.Chmod(servicePath, previous.Mode); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore previous systemd user unit mode %s: %w", servicePath, err))
		return result, errors.Join(restoreErrs...)
	}
	result.UnitRestored = true
	if commandOutput, err := systemctlCombinedOutput("--user", "daemon-reload"); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("reload restored systemd user unit: %w%s", err, commandOutputSuffix(commandOutput)))
	}
	if len(restoreErrs) > 0 || !previous.OwnedRunning {
		return result, errors.Join(restoreErrs...)
	}
	if commandOutput, err := systemctlCombinedOutput("--user", "restart", systemdServiceName); err != nil {
		return result, fmt.Errorf("restart restored systemd user service: %w%s", err, commandOutputSuffix(commandOutput))
	}
	if err := verifySystemdServiceRunning(); err != nil {
		return result, fmt.Errorf("verify restored systemd user service: %w", err)
	}
	result.Restarted = true
	return result, nil
}

func secureSystemdUnitMode(mode os.FileMode) os.FileMode {
	if mode.Perm() != atomicfile.PrivateFileMode {
		return atomicfile.PrivateFileMode
	}
	return mode.Perm()
}

func systemdOwnsRunningDaemon() bool {
	pidPath, err := pidPathFn()
	if err != nil || !isRunningFn(pidPath) {
		return false
	}
	daemonPID, err := readPIDFn(pidPath)
	if err != nil || daemonPID <= 0 {
		return false
	}

	stateOutput, err := systemctlCombinedOutput(
		"--user",
		"show",
		systemdServiceName,
		"--property=MainPID",
		"--no-pager",
	)
	if err != nil {
		return false
	}
	mainPID, err := strconv.Atoi(parseSystemdProperties(stateOutput)["MainPID"])
	return err == nil && mainPID == daemonPID
}

func verifySystemdServiceRunning() error {
	output, err := systemctlCombinedOutput(
		"--user",
		"show",
		systemdServiceName,
		"--property=ActiveState",
		"--property=SubState",
		"--property=MainPID",
		"--no-pager",
	)
	if err != nil {
		return fmt.Errorf("verify systemd user service state: %w%s", err, commandOutputSuffix(output))
	}

	properties := parseSystemdProperties(output)
	activeState := properties["ActiveState"]
	subState := properties["SubState"]
	mainPID, pidErr := strconv.Atoi(properties["MainPID"])
	if activeState == "active" && subState == "running" && pidErr == nil && mainPID > 0 {
		return nil
	}
	return fmt.Errorf(
		"systemd user service did not reach active/running after restart (ActiveState=%q, SubState=%q, MainPID=%q); run 'systemctl --user status %s' and 'journalctl --user -u %s'",
		activeState,
		subState,
		properties["MainPID"],
		systemdServiceName,
		systemdServiceName,
	)
}

func parseSystemdProperties(output []byte) map[string]string {
	properties := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		properties[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return properties
}

func systemdServiceContents(exe string) string {
	return fmt.Sprintf(`[Unit]
Description=TSLink - Tailscale Service Gateway
After=network-online.target
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
ExecStart=%s serve
Restart=on-failure
RestartSec=%d

[Install]
WantedBy=default.target
`, systemdQuoteExecPath(exe), systemdRestartSec)
}

func systemdQuoteExecPath(path string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range path {
		switch r {
		case '%':
			b.WriteString("%%")
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '$':
			b.WriteString("$$")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func defaultLinuxUserName() string {
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	if logname := os.Getenv("LOGNAME"); logname != "" {
		return logname
	}
	return strconv.Itoa(linuxUserIDFn())
}

func linuxLingerWarning() string {
	user := linuxUserNameFn()
	guidance := `run 'loginctl enable-linger "$USER"' so the user service can survive logout; after uninstall, run 'loginctl disable-linger "$USER"' if lingering was enabled only for TSLink`
	if user == "" {
		return "could not check systemd lingering because USER is not set; " + guidance
	}

	output, err := loginctlCombinedOutputFn("show-user", user, "--property=Linger", "--value")
	if err != nil {
		return fmt.Sprintf("could not check systemd lingering with loginctl: %v%s; %s", err, commandOutputSuffix(output), guidance)
	}

	linger := strings.TrimSpace(string(output))
	switch linger {
	case "yes":
		return ""
	case "no", "":
		return fmt.Sprintf("systemd lingering is disabled for user %s; %s", user, guidance)
	default:
		return fmt.Sprintf("could not interpret systemd lingering status %q for user %s; %s", linger, user, guidance)
	}
}

func commandOutputSuffix(output []byte) string {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return ""
	}
	return ": " + text
}

func systemdServicePath() (string, error) {
	home, err := linuxUserHomeDirFn()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", systemdServiceName), nil
}

func init() {
	rootCmd.AddCommand(installCmd)
}
