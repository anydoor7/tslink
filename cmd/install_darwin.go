//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// InstallResult is the JSON payload for the install command.
type InstallResult struct {
	PlistPath         string `json:"plist_path"`
	Loaded            bool   `json:"loaded"`
	LaunchctlTarget   string `json:"launchctl_target"`
	LaunchctlOutput   string `json:"launchctl_output,omitempty"`
	UnavailableDomain string `json:"unavailable_domain,omitempty"`
	ForceAvailable    bool   `json:"force_available,omitempty"`
	ForceCommand      string `json:"force_command,omitempty"`
	ForceRisk         string `json:"force_risk,omitempty"`
	Warning           string `json:"warning,omitempty"`
}

type launchctlDomainUnavailableTargetError struct {
	Target string
	Detail string
}

func (e *launchctlDomainUnavailableTargetError) Error() string {
	return fmt.Sprintf("%s: %s", errLaunchctlDomainUnavailable, e.Detail)
}

func (e *launchctlDomainUnavailableTargetError) Unwrap() error {
	return errLaunchctlDomainUnavailable
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
	launchAgentSettleWindow        = daemonSettleWindow
	launchAgentBootoutTimeout      = launchAgentShutdownTimeout
	launchAgentBootoutPollInterval = launchAgentStartupPollInterval
	launchctlCombinedOutput        = func(args ...string) ([]byte, error) {
		return runBoundedManagerCommand("launchctl", managerCommandTimeout(args...), args...)
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

// xmlEscapeValue renders v as text and escapes every XML-significant
// character (& < > " ') via encoding/xml's own escaper, so interpolated
// values (paths, labels) that legitimately contain those characters still
// produce well-formed XML. text/template has no automatic contextual
// escaping (unlike html/template, which also mis-escapes the literal
// leading "<?xml ...?>" prolog as HTML character data) so every
// interpolation in plistTemplate below is piped through this function
// explicitly, naming the source struct field (e.g. "ConfigDir") so a
// rejection can say which value was bad without printing the value itself.
//
// Before escaping, it validates s against the XML 1.0 Char production via
// validateXMLText below. Without that check, xml.EscapeText silently
// replaces every XML-illegal byte (control bytes, invalid UTF-8) with the
// U+FFFD replacement character instead of reporting an error: the template
// would still render a well-formed plist, just one with a corrupted field
// value baked into it (e.g. a truncated ConfigDir path), and every other
// check in this program -- the prolog assertion, encoding/xml.Unmarshal,
// plutil -lint -- would pass it, because U+FFFD is itself a legal XML
// character. That is a fail-open failure mode: a bad path silently becomes
// a different, wrong path instead of aborting the install. Rejecting the
// bad bytes here instead keeps the failure fail-closed: this function
// returns an error, plistTemplate.Execute propagates it, and installCmd's
// call site (the "write plist" handling below, at
// cmd/install_darwin.go:328-331) returns before
// atomicfile.WriteFileInExistingDir is ever reached, so no plist is
// written to disk.
func xmlEscapeValue(field string, v any) (string, error) {
	s := fmt.Sprint(v)
	if err := validateXMLText(s); err != nil {
		return "", fmt.Errorf("plist field %s: %w", field, err)
	}
	var buf bytes.Buffer
	// xml.EscapeText's only failure mode is a write error from the
	// underlying io.Writer, and bytes.Buffer.Write never returns one, so
	// this branch is unreachable in practice; the error is still checked
	// because EscapeText's signature promises one. validateXMLText above
	// has already rejected every input that would otherwise make
	// EscapeText fall back to silently emitting U+FFFD.
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return "", fmt.Errorf("plist field %s: xml escape: %w", field, err)
	}
	return buf.String(), nil
}

// validateXMLText reports an error for any byte sequence that
// encoding/xml.EscapeText would otherwise silently replace with U+FFFD
// instead of escaping intact: invalid UTF-8, or a validly-decoded code
// point outside the XML 1.0 Char production. The rune-by-rune walk and the
// xmlCharInRange check below intentionally mirror encoding/xml's own
// unexported escapeText/isInCharacterRange (Go toolchain
// src/encoding/xml/xml.go) exactly, including the width == 1 special case
// for utf8.RuneError: U+FFFD is itself a legal, correctly-encoded XML
// character (3 bytes wide), so only a *decode failure* that happens to
// produce that same rune value is illegal, not the character U+FFFD
// arriving correctly encoded in the input. This is the same test the
// escaper applies internally, run before escaping instead of during it, so
// "would this input have been silently corrupted" is answered exactly
// rather than approximated by, say, comparing input and output lengths.
func validateXMLText(s string) error {
	for i := 0; i < len(s); {
		r, width := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && width == 1 {
			return fmt.Errorf("invalid UTF-8 at byte offset %d", i)
		}
		if !xmlCharInRange(r) {
			return fmt.Errorf("XML 1.0 disallows character %U at byte offset %d", r, i)
		}
		i += width
	}
	return nil
}

// xmlCharInRange reports whether r falls in the XML 1.0 Char production
// (https://www.w3.org/TR/xml/#charsets): tab, LF, CR, and code points
// >= 0x20, excluding the UTF-16 surrogate range D800-DFFF and the two
// noncharacters FFFE/FFFF. This duplicates encoding/xml's unexported
// isInCharacterRange (Go toolchain src/encoding/xml/xml.go) byte for byte;
// it exists here only because that helper is not exported.
func xmlCharInRange(r rune) bool {
	return r == 0x09 ||
		r == 0x0A ||
		r == 0x0D ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

var plistTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{
	"xmlesc": xmlEscapeValue,
}).Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label | xmlesc "Label"}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Executable | xmlesc "Executable"}}</string>
        <string>serve</string>
        {{if .NoAutoProvision}}<string>--no-auto-provision</string>{{end}}
    </array>
    {{if .ConfigDir}}<key>EnvironmentVariables</key>
    <dict><key>TSLINK_CONFIG_DIR</key><string>{{.ConfigDir | xmlesc "ConfigDir"}}</string></dict>{{end}}
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>{{.ThrottleInterval | xmlesc "ThrottleInterval"}}</integer>
    <key>StandardOutPath</key>
    <string>{{.OutLog | xmlesc "OutLog"}}</string>
    <key>StandardErrorPath</key>
    <string>{{.ErrLog | xmlesc "ErrLog"}}</string>
</dict>
</plist>
`))

type plistData struct {
	Label            string
	Executable       string
	OutLog           string
	ErrLog           string
	ThrottleInterval int
	NoAutoProvision  bool
	ConfigDir        string
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
		return withSupervisorTransaction(cmd.Context(), func() error {
			return runInstallLocked(cmd, args)
		})
	},
}

// runInstallLocked requires the per-user supervisor transaction lock.
func runInstallLocked(cmd *cobra.Command, args []string) error {
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return fmt.Errorf("read --force: %w", err)
	}
	noAutoProvision, err := cmd.Flags().GetBool("no-auto-provision")
	if err != nil {
		return fmt.Errorf("read --no-auto-provision: %w", err)
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

	configDir, err := absoluteConfigDir()
	if err != nil {
		return err
	}
	data := plistData{
		Label:            plistLabel,
		Executable:       exe,
		OutLog:           outLog,
		ErrLog:           errLog,
		ThrottleInterval: launchdThrottleInterval,
		NoAutoProvision:  noAutoProvision,
		ConfigDir:        configDir,
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
				failure := fmt.Errorf("%s; upgrade failed, so the previous LaunchAgent plist was restored and reloaded in %s; %s", warning, previousState.Domain, retryAdvice)
				return installCommandFailure(cmd, loadResult, plistPath, failure)
			}
			failure := fmt.Errorf("%s; upgrade failed, so the previous LaunchAgent plist was restored; the install handoff checked and booted out both launchd service targets, but no prior launchd-owned running daemon was identified, so no job was reloaded; %s", warning, retryAdvice)
			return installCommandFailure(cmd, loadResult, plistPath, failure)
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
}

func installCommandFailure(cmd *cobra.Command, loadResult launchctlLoadResult, plistPath string, failure error) error {
	if !errors.Is(loadResult.Err, errLaunchctlDomainUnavailable) {
		return failure
	}
	coded := registry.CodedError{
		Code:        registry.CodeLaunchctlDomainUnavailable,
		Message:     failure.Error(),
		Next:        []string{"tslink install --force"},
		MessageOnly: true,
	}
	if !jsonOutput(cmd) {
		return coded
	}
	result := output.NewFailureForError("install", coded)
	result.Data = InstallResult{
		PlistPath:         plistPath,
		Loaded:            false,
		LaunchctlTarget:   loadResult.Target,
		LaunchctlOutput:   loadResult.Output,
		UnavailableDomain: loadResult.Domain,
		ForceAvailable:    true,
		ForceCommand:      "tslink install --force",
		ForceRisk:         "may start a second daemon because a prior job may still be running in the unavailable launchd domain",
	}
	output.WriteJSON(os.Stdout, result)
	return output.SilentExit(output.ExitError)
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
		domain := guiDomain
		target := launchctlServiceTargetForDomain(guiDomain)
		var unavailable *launchctlDomainUnavailableTargetError
		if errors.As(bootoutErr, &unavailable) {
			target = unavailable.Target
			domain = launchctlDomainForTarget(target)
		}
		return launchctlLoadResult{
			Domain:        domain,
			Target:        target,
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
	var lastOutput []byte
	_, err := waitStableDaemon(context.Background(), func() (int, error) {
		var printErr error
		lastOutput, printErr = launchctlCombinedOutput("print", target)
		if printErr != nil {
			return 0, nil
		}
		state, pid := parseLaunchAgentState(lastOutput)
		if state != "running" {
			return 0, nil
		}
		return pid, nil
	}, timeout, pollInterval, launchAgentSettleWindow)
	if err != nil {
		state, pid := parseLaunchAgentState(lastOutput)
		return lastOutput, fmt.Errorf("LaunchAgent did not reach running state after bootstrap (state=%q, pid=%d): %w; run 'launchctl print %s' and inspect the TSLink error log", state, pid, err, target)
	}
	return lastOutput, nil
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
			if state == "" {
				state = strings.TrimSpace(value)
			}
		case "pid":
			if pid == 0 {
				pid, _ = strconv.Atoi(strings.TrimSpace(value))
			}
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
	if launchctlServiceNotFound(output, err) {
		return nil
	}
	if launchctlDomainNotFound(output, err) {
		if allowUnavailableDomain {
			return nil
		}
		return &launchctlDomainUnavailableTargetError{
			Target: target,
			Detail: launchctlWarning("bootout "+target+" could not confirm the prior job was unloaded", err, output),
		}
	}
	if err != nil && !launchctlOperationInProgress(output, err) {
		return errors.New(launchctlWarning("bootout "+target, err, output))
	}

	return waitLaunchAgentAbsent(target, allowUnavailableDomain)
}

// bootout acceptance (including rc=0) precedes asynchronous job removal.
func waitLaunchAgentAbsent(target string, allowUnavailableDomain bool) error {
	lastOutput, lastErr, gone := pollLaunchAgent(
		target,
		launchAgentBootoutTimeout,
		launchAgentBootoutPollInterval,
		func(output []byte, err error) bool {
			return launchctlServiceNotFound(output, err) || launchctlDomainNotFound(output, err)
		},
	)
	if gone {
		if launchctlDomainNotFound(lastOutput, lastErr) && !allowUnavailableDomain {
			return &launchctlDomainUnavailableTargetError{Target: target, Detail: launchctlWarning("wait for bootout "+target, lastErr, lastOutput)}
		}
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

func launchctlDomainForTarget(target string) string {
	return strings.TrimSuffix(target, "/"+plistLabel)
}

func init() {
	installCmd.Flags().Bool("force", false, "Proceed with an upgrade despite an unavailable launchd domain (may start a second daemon)")
	installCmd.Flags().Bool("no-auto-provision", false, "Install the managed daemon with Funnel policy auto-provisioning disabled")
	mustMarkFlagPlatforms(installCmd, "force", "darwin")
	rootCmd.AddCommand(installCmd)
}
