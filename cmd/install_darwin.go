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

	"github.com/monody0007/tslink/internal/atomicfile"
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
	installDaemonArtifactConflictFn = func() error {
		return detectInstallDaemonConflict("a LaunchAgent plist is installed, but TSLink could not confirm that launchd owns the running daemon; stop the manual daemon with 'tslink stop' and retry 'tslink install'; keep the existing plist installed")
	}
	errLaunchctlDomainUnavailable  = errors.New("launchctl domain unavailable")
	launchAgentVerifyTimeout       = launchAgentStartupTimeout
	launchAgentVerifyPollInterval  = launchAgentStartupPollInterval
	launchAgentBootoutTimeout      = launchAgentShutdownTimeout
	launchAgentBootoutPollInterval = launchAgentStartupPollInterval
	launchctlCombinedOutput        = func(args ...string) ([]byte, error) {
		return exec.Command("launchctl", args...).CombinedOutput()
	}
)

const plistLabel = "com.tslink.daemon"
const launchdThrottleInterval = 30
const launchAgentStartupTimeout = 10 * time.Second
const launchAgentStartupPollInterval = 50 * time.Millisecond
const launchAgentShutdownTimeout = 3 * time.Minute

type launchctlLoadResult struct {
	Domain        string
	Target        string
	Output        string
	Err           error
	Warning       string
	Bootstrapped  bool
	BootoutFailed bool
}

type launchAgentPreviousState struct {
	Existed bool
	Plist   []byte
	Mode    os.FileMode
	Domain  string
	Target  string
}

type launchAgentRestoreResult struct {
	PlistRestored bool
	Reloaded      bool
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
  7. Falls back from gui/$(id -u) to user/$(id -u) when no desktop session exists for this user

Re-running 'tslink install' is the supported upgrade path. Before replacing an
existing plist, TSLink saves it and verifies whether launchd owns the running
daemon by matching the pidfile PID to 'launchctl print'. If the upgrade fails,
the previous plist is restored and a previously managed job is reloaded. This
does not restore an executable binary that was replaced before this command ran.
For a new install, failed post-bootstrap verification boots out the new job and
removes the new plist only after bootout succeeds. Fix the reported cause and
re-run 'tslink install'.

To check if the agent is loaded:
  launchctl list | grep tslink
  launchctl print gui/$(id -u)/com.tslink.daemon
  launchctl print user/$(id -u)/com.tslink.daemon

To remove the autostart:
  tslink uninstall

Desktop-session caveat:
  macOS may not expose gui/$(id -u) until a desktop login exists. In that case
  tslink install tries launchctl bootstrap user/$(id -u) and prints the domain
  it used. An upgrade keeps the existing plist and refuses the handoff if any
  prior launchd domain cannot be checked. Re-run from a desktop login, or use
  'tslink install --force' only after confirming no job remains in the unavailable
  domain; otherwise --force may start a second daemon. Re-run tslink install from
  a desktop login to move back to gui/$(id -u).

	Examples:
	  tslink install                Register and start the LaunchAgent`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, err := cmd.Flags().GetBool("force")
		if err != nil {
			return fmt.Errorf("read --force: %w", err)
		}
		plistPath, err := plistPath()
		if err != nil {
			return err
		}
		previousState, err := captureLaunchAgentPreviousState(plistPath)
		if err != nil {
			return err
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
		if err := atomicfile.WriteFileInExistingDir(plistPath, plist.Bytes(), atomicfile.PrivateFileMode); err != nil {
			return fmt.Errorf("write plist: %w", err)
		}

		var loadResult launchctlLoadResult
		if previousState.Existed {
			if force {
				loadResult = loadLaunchAgent(plistPath, false)
				if loadResult.Err == nil && loadResult.Warning != "" {
					loadResult.Warning += "; --force proceeded without confirming that every prior launchd job was unloaded; a second daemon may still be running in the unavailable domain"
				}
			} else {
				loadResult = reinstallLaunchAgent(plistPath)
			}
		} else {
			loadResult = loadLaunchAgent(plistPath, false)
		}
		if loadResult.Err != nil {
			retryAdvice := installRetryAdvice(loadResult.Err)
			warning := loadResult.Warning
			if warning == "" && loadResult.BootoutFailed {
				warning = launchctlWarning("LaunchAgent plist was written, but the existing launchd job could not be booted out", loadResult.Err, []byte(loadResult.Output))
			}
			if warning == "" {
				warning = launchctlWarning("LaunchAgent plist installed but the service did not reach running state", loadResult.Err, []byte(loadResult.Output))
			}
			if previousState.Existed {
				restoreResult, restoreErr := restorePreviousLaunchAgent(previousState, loadResult, plistPath)
				if restoreErr != nil {
					status := "the previous plist could not be restored"
					if restoreResult.PlistRestored {
						status = "the previous plist bytes were restored, but the prior managed job is not confirmed running"
					}
					return fmt.Errorf("%s; upgrade failed and automatic restoration was incomplete: %v; %s; %s", warning, restoreErr, status, retryAdvice)
				}
				if restoreResult.Reloaded {
					return fmt.Errorf("%s; upgrade failed, so the previous LaunchAgent plist was restored and reloaded in %s; %s", warning, previousState.Domain, retryAdvice)
				}
				return fmt.Errorf("%s; upgrade failed, so the previous LaunchAgent plist was restored; the install handoff checked and booted out both launchd service targets, but no prior launchd-owned running daemon was identified, so no job was reloaded; %s", warning, retryAdvice)
			}
			if loadResult.Bootstrapped {
				if rollbackErr := rollbackNewLaunchAgent(loadResult.Target, plistPath); rollbackErr != nil {
					return fmt.Errorf("%s; automatic rollback was incomplete: %v; the plist was kept at %s so 'tslink uninstall' can retry bootout; %s", warning, rollbackErr, plistPath, retryAdvice)
				}
				return fmt.Errorf("%s; the new installation was rolled back by booting out %s and removing %s; %s", warning, loadResult.Target, plistPath, retryAdvice)
			}
			return fmt.Errorf("%s; plist remains installed at %s but no job was bootstrapped; %s", warning, plistPath, retryAdvice)
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
			fmt.Fprintf(cmd.ErrOrStderr(), "→ ⚠ %s\n", loadResult.Warning)
		}
		if loadResult.Output != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "→ launchctl output: %s\n", loadResult.Output)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ LaunchAgent installed and loaded in %s: %s\n", loadResult.Domain, plistPath)
		return nil
	},
}

func captureLaunchAgentPreviousState(plistPath string) (launchAgentPreviousState, error) {
	info, err := os.Stat(plistPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return launchAgentPreviousState{}, fmt.Errorf("inspect existing LaunchAgent plist: %w", err)
		}
		if err := installDaemonConflictFn(); err != nil {
			return launchAgentPreviousState{}, err
		}
		return launchAgentPreviousState{}, nil
	}

	plist, err := os.ReadFile(plistPath)
	if err != nil {
		return launchAgentPreviousState{}, fmt.Errorf("read existing LaunchAgent plist before upgrade: %w", err)
	}
	state := launchAgentPreviousState{
		Existed: true,
		Plist:   plist,
		Mode:    info.Mode().Perm(),
	}

	domain, target, owned := launchAgentTargetForRunningDaemon()
	if owned {
		state.Domain = domain
		state.Target = target
		return state, nil
	}
	if err := installDaemonArtifactConflictFn(); err != nil {
		return launchAgentPreviousState{}, err
	}
	return state, nil
}

func launchAgentTargetForRunningDaemon() (string, string, bool) {
	pidPath, err := pidPathFn()
	if err != nil || !isRunningFn(pidPath) {
		return "", "", false
	}
	daemonPID, err := readPIDFn(pidPath)
	if err != nil || daemonPID <= 0 {
		return "", "", false
	}

	for _, domain := range []string{launchctlDomain(), launchctlUserDomain()} {
		target := launchctlServiceTargetForDomain(domain)
		stateOutput, printErr := launchctlCombinedOutput("print", target)
		if printErr != nil {
			continue
		}
		state, launchdPID := parseLaunchAgentState(stateOutput)
		if state == "running" && launchdPID == daemonPID {
			return domain, target, true
		}
	}
	return "", "", false
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

func launchctlServiceTargetForDomain(domain string) string {
	return domain + "/" + plistLabel
}

func reinstallLaunchAgent(plistPath string) launchctlLoadResult {
	return loadLaunchAgent(plistPath, true)
}

func loadLaunchAgent(plistPath string, replacingExisting bool) launchctlLoadResult {
	guiDomain := launchctlDomain()
	userDomain := launchctlUserDomain()
	var bootoutErr error
	if replacingExisting {
		bootoutErr = bootoutLaunchAgentTargetsForUpgrade(guiDomain, userDomain)
	} else {
		bootoutErr = bootoutLaunchAgentTargets(guiDomain, userDomain)
	}
	if bootoutErr != nil {
		return launchctlLoadResult{
			Domain:        guiDomain,
			Target:        launchctlServiceTargetForDomain(guiDomain),
			Err:           bootoutErr,
			BootoutFailed: true,
		}
	}

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
	warning := fmt.Sprintf("launchctl %s is unavailable because no desktop session exists for this user; tried %s fallback", guiDomain, userDomain)
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
	lastOutput, lastErr, _ := pollLaunchAgent(
		target,
		timeout,
		pollInterval,
		func(output []byte, err error) bool {
			if err != nil {
				return false
			}
			state, pid := parseLaunchAgentState(output)
			return state == "running" && pid > 0
		},
	)
	lastState, lastPID := parseLaunchAgentState(lastOutput)
	if lastErr == nil && lastState == "running" && lastPID > 0 {
		return lastOutput, nil
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

func pollLaunchAgent(target string, timeout, pollInterval time.Duration, done func([]byte, error) bool) ([]byte, error, bool) {
	deadline := time.Now().Add(timeout)
	for {
		output, err := launchctlCombinedOutput("print", target)
		if done(output, err) {
			return output, err, true
		}
		if !time.Now().Before(deadline) {
			return output, err, false
		}
		if pollInterval > 0 {
			time.Sleep(pollInterval)
		}
	}
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

func bootoutLaunchAgentTargets(domains ...string) error {
	for _, domain := range domains {
		if err := bootoutLaunchAgentTarget(launchctlServiceTargetForDomain(domain)); err != nil {
			return err
		}
	}
	return nil
}

func bootoutLaunchAgentTargetsForUpgrade(domains ...string) error {
	var firstUnavailable error
	for _, domain := range domains {
		err := bootoutLaunchAgentTargetForUpgrade(launchctlServiceTargetForDomain(domain))
		if err == nil {
			continue
		}
		if errors.Is(err, errLaunchctlDomainUnavailable) {
			if firstUnavailable == nil {
				firstUnavailable = err
			}
			continue
		}
		return err
	}
	return firstUnavailable
}

func bootoutLaunchAgentTarget(target string) error {
	return bootoutLaunchAgentTargetWithPolicy(target, true)
}

func bootoutLaunchAgentTargetForUpgrade(target string) error {
	return bootoutLaunchAgentTargetWithPolicy(target, false)
}

func bootoutLaunchAgentTargetWithPolicy(target string, allowUnavailableDomain bool) error {
	output, err := launchctlCombinedOutput("bootout", target)
	if err == nil || launchctlServiceNotFound(output, err) {
		return nil
	}
	if launchctlDomainNotFound(output, err) {
		if allowUnavailableDomain {
			return nil
		}
		return fmt.Errorf("%w: %s", errLaunchctlDomainUnavailable, launchctlWarning("bootout "+target+" could not confirm the prior job was unloaded", err, output))
	}
	if !launchctlOperationInProgress(output, err) {
		return errors.New(launchctlWarning("bootout "+target, err, output))
	}

	lastOutput, lastErr, gone := pollLaunchAgent(
		target,
		launchAgentBootoutTimeout,
		launchAgentBootoutPollInterval,
		func(output []byte, err error) bool {
			return launchctlServiceNotFound(output, err) || (allowUnavailableDomain && launchctlDomainNotFound(output, err))
		},
	)
	if gone {
		return nil
	}
	detail := strings.TrimSpace(string(lastOutput))
	if lastErr != nil {
		detail = launchctlWarning("wait for bootout "+target, lastErr, lastOutput)
	} else if detail == "" {
		detail = "launchctl print still reports the job"
	}
	return fmt.Errorf("launchctl bootout remained in progress after %s: %s", launchAgentBootoutTimeout, detail)
}

func rollbackNewLaunchAgent(target, plistPath string) error {
	if err := bootoutLaunchAgentTarget(target); err != nil {
		return err
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist %s: %w", plistPath, err)
	}
	return nil
}

func restorePreviousLaunchAgent(previous launchAgentPreviousState, loadResult launchctlLoadResult, plistPath string) (launchAgentRestoreResult, error) {
	result := launchAgentRestoreResult{}
	var restoreErrs []error
	if loadResult.Bootstrapped {
		if err := bootoutLaunchAgentTarget(loadResult.Target); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
	}

	if err := atomicfile.WriteFileInExistingDir(plistPath, previous.Plist, secureLaunchAgentMode(previous.Mode)); err != nil {
		restoreErrs = append(restoreErrs, fmt.Errorf("restore previous plist %s: %w", plistPath, err))
		return result, errors.Join(restoreErrs...)
	}
	result.PlistRestored = true
	if len(restoreErrs) > 0 || previous.Target == "" {
		return result, errors.Join(restoreErrs...)
	}

	bootstrapOutput, err := launchctlCombinedOutput("bootstrap", previous.Domain, plistPath)
	if err != nil {
		return result, errors.New(launchctlWarning("restore previous LaunchAgent with bootstrap "+previous.Domain, err, bootstrapOutput))
	}
	verificationOutput, err := verifyLaunchAgentRunning(previous.Target)
	if err != nil {
		detail := strings.TrimSpace(string(verificationOutput))
		if detail != "" {
			detail = ": " + detail
		}
		return result, fmt.Errorf("restored previous plist but could not verify the prior managed job: %w%s", err, detail)
	}
	result.Reloaded = true
	return result, nil
}

func secureLaunchAgentMode(mode os.FileMode) os.FileMode {
	if mode.Perm() != atomicfile.PrivateFileMode {
		return atomicfile.PrivateFileMode
	}
	return mode.Perm()
}

func launchctlOperationInProgress(output []byte, err error) bool {
	text := strings.ToLower(string(output))
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	return strings.Contains(text, "operation now in progress")
}

func launchctlTargetNotFound(output []byte, err error) bool {
	if err == nil {
		return false
	}
	return launchctlServiceNotFound(output, err) || launchctlDomainNotFound(output, err)
}

func launchctlServiceNotFound(output []byte, err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(string(output)) + "\n" + strings.ToLower(err.Error())
	return strings.Contains(text, "no such process") ||
		strings.Contains(text, "could not find service") ||
		strings.Contains(text, "service not found") ||
		strings.Contains(text, "could not find specified service")
}

func launchctlDomainNotFound(output []byte, err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(string(output))
	text += "\n" + strings.ToLower(err.Error())
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

func installRetryAdvice(err error) string {
	if errors.Is(err, errLaunchctlDomainUnavailable) {
		return "retry from a desktop session for this user, or run 'tslink install --force' only after confirming no job is loaded in the unavailable launchd domain; --force may otherwise start a second daemon"
	}
	return "fix the reported cause and re-run 'tslink install'"
}

func init() {
	installCmd.Flags().Bool("force", false, "Proceed with an upgrade despite an unavailable launchd domain (may start a second daemon)")
	rootCmd.AddCommand(installCmd)
}
