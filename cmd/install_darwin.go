//go:build darwin

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

// InstallResult is the JSON payload for the install command.
type InstallResult struct {
	PlistPath       string `json:"plist_path"`
	Loaded          bool   `json:"loaded"`
	LaunchctlTarget string `json:"launchctl_target"`
	LaunchctlOutput string `json:"launchctl_output,omitempty"`
	Warning         string `json:"warning,omitempty"`
}

var (
	userHomeDirFn           = os.UserHomeDir
	executablePathFn        = os.Executable
	evalSymlinksFn          = filepath.EvalSymlinks
	userUIDFn               = os.Getuid
	installDaemonConflictFn = func() error {
		return detectInstallDaemonConflict("no LaunchAgent plist is installed, so stop the manual daemon with 'tslink stop' and retry 'tslink install'; if launchd owns it, run 'tslink uninstall' first so KeepAlive cannot restart it")
	}
	launchAgentVerifyTimeout      = launchAgentStartupTimeout
	launchAgentVerifyPollInterval = launchAgentStartupPollInterval
	launchctlCombinedOutput       = func(args ...string) ([]byte, error) {
		return exec.Command("launchctl", args...).CombinedOutput()
	}
)

const plistLabel = "com.tslink.daemon"
const launchdThrottleInterval = 30
const launchAgentStartupTimeout = 10 * time.Second
const launchAgentStartupPollInterval = 50 * time.Millisecond

type launchctlLoadResult struct {
	Domain       string
	Target       string
	Output       string
	Err          error
	Warning      string
	Bootstrapped bool
}

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Executable}}</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>{{.ThrottleInterval}}</integer>
    <key>StandardOutPath</key>
    <string>{{.OutLog}}</string>
    <key>StandardErrorPath</key>
    <string>{{.ErrLog}}</string>
</dict>
</plist>
`))

type plistData struct {
	Label            string
	Executable       string
	OutLog           string
	ErrLog           string
	ThrottleInterval int
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install as macOS LaunchAgent",
	Long: `Register TSLink as a macOS LaunchAgent so it starts automatically
when you log in and restarts if it crashes.

This command:
  1. Creates a LaunchAgent plist at ~/Library/LaunchAgents/com.tslink.daemon.plist
  2. Configures it to run 'tslink serve' at login with auto-restart (KeepAlive)
  3. Logs stdout to ~/.config/tslink/logs/tslink.out.log
  4. Logs stderr to ~/.config/tslink/logs/tslink.err.log
  5. Uses launchd ThrottleInterval=30 to avoid tight restart loops on failures
  6. Reloads the agent immediately with bootout-then-bootstrap
  7. Falls back from gui/$(id -u) to user/$(id -u) in SSH/headless sessions

To check if the agent is loaded:
  launchctl list | grep tslink
  launchctl print gui/$(id -u)/com.tslink.daemon
  launchctl print user/$(id -u)/com.tslink.daemon

To remove the autostart:
  tslink uninstall

Headless/SSH caveat:
  macOS may not expose gui/$(id -u) until a desktop login exists. In that case
  tslink install tries launchctl bootstrap user/$(id -u) and prints the domain
  it used. Re-run tslink install from a desktop login to move back to gui/$(id -u).

	Examples:
	  tslink install                Register and start the LaunchAgent`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		plistPath, err := plistPath()
		if err != nil {
			return err
		}
		if _, statErr := os.Stat(plistPath); statErr != nil {
			if !os.IsNotExist(statErr) {
				return fmt.Errorf("inspect existing LaunchAgent plist: %w", statErr)
			}
			if err := installDaemonConflictFn(); err != nil {
				return err
			}
		}

		if err := config.EnsureDir(); err != nil {
			return err
		}

		exe, err := executablePathFn()
		if err != nil {
			return fmt.Errorf("find executable: %w", err)
		}
		exe, err = evalSymlinksFn(exe)
		if err != nil {
			return fmt.Errorf("resolve executable path: %w", err)
		}

		logDir, _ := config.LogDir()
		outLog := filepath.Join(logDir, "tslink.out.log")
		errLog := filepath.Join(logDir, "tslink.err.log")

		if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
			return fmt.Errorf("create LaunchAgents directory: %w", err)
		}

		data := plistData{
			Label:            plistLabel,
			Executable:       exe,
			OutLog:           outLog,
			ErrLog:           errLog,
			ThrottleInterval: launchdThrottleInterval,
		}
		var plist bytes.Buffer
		if err := plistTemplate.Execute(&plist, data); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}
		if err := os.WriteFile(plistPath, plist.Bytes(), 0o644); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}

		loadResult := reinstallLaunchAgent(plistPath)
		if loadResult.Err != nil {
			warning := loadResult.Warning
			if warning == "" {
				warning = launchctlWarning("LaunchAgent plist installed but the service did not reach running state", loadResult.Err, []byte(loadResult.Output))
			}
			if loadResult.Bootstrapped {
				if rollbackErr := rollbackLaunchAgent(loadResult.Target, plistPath); rollbackErr != nil {
					return fmt.Errorf("%s; automatic rollback was incomplete: %v; launchd may keep retrying, so run 'tslink uninstall' to finish cleanup", warning, rollbackErr)
				}
				return fmt.Errorf("%s; installation was rolled back by booting out %s and removing %s", warning, loadResult.Target, plistPath)
			}
			return fmt.Errorf("%s; plist remains installed at %s but no job was bootstrapped; run 'tslink uninstall' to remove it", warning, plistPath)
		}

		if jsonOutput(cmd) {
			result := InstallResult{
				PlistPath:       plistPath,
				Loaded:          true,
				LaunchctlTarget: loadResult.Target,
				LaunchctlOutput: loadResult.Output,
				Warning:         loadResult.Warning,
			}
			output.Success("install", result)
			return nil
		}

		if loadResult.Warning != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "→ ⚠ %s\n", loadResult.Warning)
		}
		if loadResult.Output != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "→ launchctl output: %s\n", loadResult.Output)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ LaunchAgent installed and loaded in %s: %s\n", loadResult.Domain, plistPath)
		return nil
	},
}

func plistPath() (string, error) {
	home, err := userHomeDirFn()
	if err != nil {
		return "", fmt.Errorf("get home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist"), nil
}

func launchctlDomain() string {
	return fmt.Sprintf("gui/%d", userUIDFn())
}

func launchctlUserDomain() string {
	return fmt.Sprintf("user/%d", userUIDFn())
}

func launchctlServiceTarget() string {
	return launchctlServiceTargetForDomain(launchctlDomain())
}

func launchctlServiceTargetForDomain(domain string) string {
	return domain + "/" + plistLabel
}

func reinstallLaunchAgent(plistPath string) launchctlLoadResult {
	guiDomain := launchctlDomain()
	userDomain := launchctlUserDomain()
	bootoutLaunchAgentTargets(guiDomain, userDomain)

	output, err := launchctlCombinedOutput("bootstrap", guiDomain, plistPath)
	if err == nil {
		target := launchctlServiceTargetForDomain(guiDomain)
		verificationOutput, verifyErr := verifyLaunchAgentRunning(target)
		combinedOutput := strings.TrimSpace(string(output))
		if verifyErr != nil {
			combinedOutput = combineLaunchctlOutput(output, verificationOutput)
		}
		return launchctlLoadResult{
			Domain:       guiDomain,
			Target:       target,
			Output:       combinedOutput,
			Err:          verifyErr,
			Bootstrapped: true,
		}
	}
	if !launchctlDomainNotFound(output, err) {
		return launchctlLoadResult{
			Domain: guiDomain,
			Target: launchctlServiceTargetForDomain(guiDomain),
			Output: strings.TrimSpace(string(output)),
			Err:    err,
		}
	}

	fallbackOutput, fallbackErr := launchctlCombinedOutput("bootstrap", userDomain, plistPath)
	combinedOutput := combineLaunchctlOutput(output, fallbackOutput)
	warning := fmt.Sprintf("launchctl %s is unavailable in this SSH/headless session; tried %s fallback", guiDomain, userDomain)
	if fallbackErr != nil {
		return launchctlLoadResult{
			Domain:  userDomain,
			Target:  launchctlServiceTargetForDomain(userDomain),
			Output:  combinedOutput,
			Err:     fallbackErr,
			Warning: launchctlWarning(warning, fallbackErr, []byte(combinedOutput)),
		}
	}
	target := launchctlServiceTargetForDomain(userDomain)
	verificationOutput, verifyErr := verifyLaunchAgentRunning(target)
	if verifyErr != nil {
		combinedOutput = combineLaunchctlOutput([]byte(combinedOutput), verificationOutput)
	}
	return launchctlLoadResult{
		Domain:       userDomain,
		Target:       target,
		Output:       combinedOutput,
		Err:          verifyErr,
		Warning:      warning,
		Bootstrapped: true,
	}
}

func verifyLaunchAgentRunning(target string) ([]byte, error) {
	return waitForLaunchAgentRunning(target, launchAgentVerifyTimeout, launchAgentVerifyPollInterval)
}

func waitForLaunchAgentRunning(target string, timeout, pollInterval time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var lastOutput []byte
	var lastErr error
	var lastState string
	var lastPID int
	for {
		lastOutput, lastErr = launchctlCombinedOutput("print", target)
		if lastErr == nil {
			lastState, lastPID = parseLaunchAgentState(lastOutput)
			if lastState == "running" && lastPID > 0 {
				return lastOutput, nil
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		if pollInterval > 0 {
			time.Sleep(pollInterval)
		}
	}

	if lastErr != nil {
		detail := strings.TrimSpace(string(lastOutput))
		if detail != "" {
			detail = ": " + detail
		}
		return lastOutput, fmt.Errorf("verify LaunchAgent state with 'launchctl print %s' for %s: %w%s", target, timeout, lastErr, detail)
	}
	return lastOutput, fmt.Errorf(
		"LaunchAgent did not reach running state within %s after bootstrap (target=%s, state=%q, pid=%d); run 'launchctl print %s' and inspect the TSLink error log",
		timeout,
		target,
		lastState,
		lastPID,
		target,
	)
}

func parseLaunchAgentState(output []byte) (string, int) {
	var state string
	var pid int
	for _, line := range strings.Split(string(output), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "state":
			state = strings.TrimSpace(value)
		case "pid":
			pid, _ = strconv.Atoi(strings.TrimSpace(value))
		}
	}
	return state, pid
}

func bootoutLaunchAgentTargets(domains ...string) {
	for _, domain := range domains {
		_, _ = launchctlCombinedOutput("bootout", launchctlServiceTargetForDomain(domain))
	}
}

func rollbackLaunchAgent(target, plistPath string) error {
	var rollbackErrs []error
	if output, err := launchctlCombinedOutput("bootout", target); err != nil {
		rollbackErrs = append(rollbackErrs, errors.New(launchctlWarning("bootout "+target, err, output)))
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("remove plist %s: %w", plistPath, err))
	}
	return errors.Join(rollbackErrs...)
}

func launchctlDomainNotFound(output []byte, err error) bool {
	text := strings.ToLower(string(output))
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "domain does not exist") ||
		strings.Contains(text, "could not find domain for:") ||
		strings.Contains(text, "domain is not found") ||
		strings.Contains(text, "no such domain")
}

func combineLaunchctlOutput(outputs ...[]byte) string {
	var parts []string
	for _, output := range outputs {
		text := strings.TrimSpace(string(output))
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func launchctlWarning(message string, err error, combinedOutput []byte) string {
	detail := strings.TrimSpace(string(combinedOutput))
	if detail == "" {
		return fmt.Sprintf("%s: %v", message, err)
	}
	return fmt.Sprintf("%s: %v; output: %s", message, err, detail)
}

func init() {
	rootCmd.AddCommand(installCmd)
}
