//go:build darwin

package cmd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

func stubDarwinInstallDaemonStopped(t *testing.T) {
	t.Helper()
	oldConflict := installDaemonConflictFn
	installDaemonConflictFn = func() error { return nil }
	t.Cleanup(func() { installDaemonConflictFn = oldConflict })
}

func runningLaunchAgentState() []byte {
	return []byte("state = running\npid = 1775\n")
}

func stubDarwinLaunchAgentVerificationNoWait(t *testing.T) {
	t.Helper()
	oldTimeout := launchAgentVerifyTimeout
	oldPollInterval := launchAgentVerifyPollInterval
	oldSettle := launchAgentSettleWindow
	launchAgentVerifyTimeout = 10 * time.Millisecond
	launchAgentVerifyPollInterval = 0
	launchAgentSettleWindow = 0
	t.Cleanup(func() {
		launchAgentVerifyTimeout = oldTimeout
		launchAgentVerifyPollInterval = oldPollInterval
		launchAgentSettleWindow = oldSettle
	})
}

func runDarwinInstallGuardTruthCase(t *testing.T, plistPresent, daemonRunning bool, launchdPID int) bool {
	t.Helper()
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		launchctlCombinedOutput = oldLaunchctl
		installCmd.SetOut(nil)
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/new/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }

	path := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if plistPresent {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte("old plist"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	pidPath := filepath.Join(home, "tslink.pid")
	observedDaemonRunning := false
	pidPathFn = func() (string, error) { return pidPath, nil }
	isRunningFn = func(path string) bool {
		if path != pidPath {
			t.Fatalf("isRunningFn path = %q, want %q", path, pidPath)
		}
		observedDaemonRunning = daemonRunning
		return daemonRunning
	}
	readPIDFn = func(path string) (int, error) {
		if path != pidPath {
			t.Fatalf("readPIDFn path = %q, want %q", path, pidPath)
		}
		return 1775, nil
	}
	launchctlCalls := 0
	bootstrapCalls := 0
	mutatingLaunchctlCalls := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		launchctlCalls++
		if len(args) > 0 && args[0] == "print" {
			pid := launchdPID
			if pid <= 0 {
				pid = 1775
			}
			return []byte(fmt.Sprintf("state = running\npid = %d\n", pid)), nil
		}
		if len(args) > 0 && args[0] == "bootstrap" {
			bootstrapCalls++
		}
		if len(args) > 0 && args[0] != "print" {
			mutatingLaunchctlCalls++
		}
		return nil, nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	expectConflict := daemonRunning && (!plistPresent || launchdPID != 1775)
	if expectConflict {
		if output.ExitCode(err) != output.ExitConflict {
			t.Fatalf("ExitCode = %d, want %d: %v", output.ExitCode(err), output.ExitConflict, err)
		}
		if bootstrapCalls != 0 {
			t.Fatalf("bootstrap calls = %d, want 0 before daemon conflict", bootstrapCalls)
		}
		if mutatingLaunchctlCalls != 0 {
			t.Fatalf("mutating launchctl calls = %d, want 0 before daemon conflict", mutatingLaunchctlCalls)
		}
		if plistPresent {
			for _, want := range []string{"a LaunchAgent plist is installed", "could not confirm that launchd owns", "tslink stop", "keep the existing plist installed"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("artifact-present conflict = %q, want %q", err, want)
				}
			}
			for _, forbidden := range []string{"no LaunchAgent plist is installed", "tslink uninstall"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("artifact-present conflict = %q, must not contain %q", err, forbidden)
				}
			}
			plist, readErr := os.ReadFile(path)
			if readErr != nil || string(plist) != "old plist" {
				t.Fatalf("existing plist changed during conflict: %q, %v", plist, readErr)
			}
		} else if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("plist exists after conflict: %v", statErr)
		}
		return observedDaemonRunning
	}

	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}
	if launchctlCalls == 0 {
		t.Fatal("launchctl was not called for successful install")
	}
	plist, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile(plist) error = %v", readErr)
	}
	if !strings.Contains(string(plist), "/new/tslink") {
		t.Fatalf("plist was not refreshed to new executable: %s", plist)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("installed plist mode = %v, %v; want 0600", info, statErr)
	}
	return observedDaemonRunning
}

func TestDarwinInstallNoPlistDaemonStoppedProceeds(t *testing.T) {
	if got := runDarwinInstallGuardTruthCase(t, false, false, 0); got {
		t.Fatal("daemon-running seam observed true, want false")
	}
}

func TestDarwinInstallNoPlistDaemonRunningConflicts(t *testing.T) {
	if got := runDarwinInstallGuardTruthCase(t, false, true, 0); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestDarwinInstallExistingPlistDaemonStoppedReinstalls(t *testing.T) {
	if got := runDarwinInstallGuardTruthCase(t, true, false, 0); got {
		t.Fatal("daemon-running seam observed true, want false")
	}
}

func TestDarwinInstallExistingPlistDaemonRunningReinstalls(t *testing.T) {
	if got := runDarwinInstallGuardTruthCase(t, true, true, 1775); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestDarwinInstallExistingPlistManualDaemonPIDMismatchConflicts(t *testing.T) {
	if got := runDarwinInstallGuardTruthCase(t, true, true, 1888); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestDarwinInstallHelpDocumentsUpgradeAndFailurePolicy(t *testing.T) {
	for _, want := range []string{
		"supported upgrade path",
		"saves it",
		"previous plist is restored",
		"does not restore an executable binary",
		"new install",
		"only after bootout succeeds",
		"re-run 'tslink install'",
		"no desktop session exists for this user",
		"tslink install --force",
		"second daemon",
	} {
		if !strings.Contains(installCmd.Long, want) {
			t.Fatalf("install help missing %q:\n%s", want, installCmd.Long)
		}
	}
	for _, stale := range []string{"SSH/headless", "in this SSH session"} {
		if strings.Contains(installCmd.Long, stale) {
			t.Fatalf("install help retains inaccurate session wording %q:\n%s", stale, installCmd.Long)
		}
	}
}

func TestPlistPath(t *testing.T) {
	old := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "/Users/testuser", nil }
	defer func() { userHomeDirFn = old }()

	got, err := plistPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join("/Users/testuser", "Library", "LaunchAgents", plistLabel+".plist")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestPlistPath_Error(t *testing.T) {
	old := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", fmt.Errorf("injected homedir error") }
	defer func() { userHomeDirFn = old }()

	_, err := plistPath()
	if err == nil {
		t.Fatal("expected error from userHomeDirFn")
	}
	if !strings.Contains(err.Error(), "injected homedir error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPlistTemplateIncludesRestartThrottle(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:            plistLabel,
		Executable:       "/usr/local/bin/tslink",
		OutLog:           "/tmp/tslink.out.log",
		ErrLog:           "/tmp/tslink.err.log",
		ThrottleInterval: launchdThrottleInterval,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}

	plist := buf.String()
	for _, want := range []string{
		"<key>KeepAlive</key>",
		"<key>ThrottleInterval</key>",
		"<integer>30</integer>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestPlistTemplateCarriesNoAutoProvision(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label: plistLabel, Executable: "/usr/local/bin/tslink",
		OutLog: "/tmp/out", ErrLog: "/tmp/err", ThrottleInterval: launchdThrottleInterval,
		NoAutoProvision: true,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}
	if count := strings.Count(buf.String(), "<string>--no-auto-provision</string>"); count != 1 {
		t.Fatalf("kill-switch arg count = %d, want 1:\n%s", count, buf.String())
	}
}

func TestPlistTemplateEscapesXMLPaths(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:            plistLabel,
		Executable:       "/Applications/TSLink & Tools/<tslink>/tslink",
		OutLog:           "/tmp/tslink > out.log",
		ErrLog:           "/tmp/tslink < err.log",
		ThrottleInterval: launchdThrottleInterval,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}

	// encoding/xml.Unmarshal is a weak, non-authoritative check: it is
	// lenient about the document prolog (it did not reject the historic
	// html/template bug that rendered "&lt;?xml ...?>" as the first line
	// of the file -- see TestPlistTemplateRendersWellFormedPlist for the
	// checks that actually catch that regression) and it does not enforce
	// launchd's stricter plist grammar. All a failure here proves is that
	// plistData field values contain raw XML-significant characters
	// (&, <, >) inside element text, i.e. that they were not escaped.
	var parsed struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("plistData values were not escaped for XML text content (encoding/xml rejected them): %v\n%s", err, buf.String())
	}
	for _, want := range []string{
		"/Applications/TSLink &amp; Tools/&lt;tslink&gt;/tslink",
		"/tmp/tslink &gt; out.log",
		"/tmp/tslink &lt; err.log",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("plist missing escaped path %q:\n%s", want, buf.String())
		}
	}
}

// TestPlistTemplateRendersWellFormedPlist guards against the html/template
// regression that TestPlistTemplateEscapesXMLPaths could not catch: html/template
// applied context-sensitive HTML autoescaping, which happened to escape
// plistData field values correctly (satisfying the checks above) while
// simultaneously mis-escaping the *static* leading "<" of the "<?xml ...?>"
// declaration into "&lt;" because it treated the XML prolog as a suspicious
// HTML construct. Go's encoding/xml.Unmarshal does not reject that leading
// "&lt;" (text before the root element is lenient), so the historic bug
// shipped with TestPlistTemplateEscapesXMLPaths green. This test instead
// checks (1) the byte-exact prolog and (2) the real downstream consumer's
// parser (plutil), the two checks with actual discriminating power.
func TestPlistTemplateRendersWellFormedPlist(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:            plistLabel,
		Executable:       "/usr/local/bin/tslink",
		OutLog:           "/tmp/tslink.out.log",
		ErrLog:           "/tmp/tslink.err.log",
		ThrottleInterval: launchdThrottleInterval,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}
	rendered := buf.String()

	// Positive assertion: the rendered XML declaration must be byte-exact.
	// Positive assertions like this cannot silently rot the way a "must not
	// contain X" assertion can -- if the leading "<" is ever mangled again,
	// this fails loudly instead of quietly stopping catching anything.
	const wantProlog = `<?xml version="1.0" encoding="UTF-8"?>`
	firstLine := rendered
	if idx := strings.IndexByte(rendered, '\n'); idx >= 0 {
		firstLine = rendered[:idx]
	}
	if firstLine != wantProlog {
		t.Fatalf("plist XML declaration = %q, want %q (byte-exact):\n%s", firstLine, wantProlog, rendered)
	}

	// Hand the rendered bytes to the real downstream consumer. launchd's
	// plist parser is stricter than Go's encoding/xml (see
	// TestPlistTemplateEscapesXMLPaths's guard above), so `plutil -lint` is
	// the closest thing to ground truth this suite can reach without
	// actually invoking launchctl.
	plutilPath, lookErr := exec.LookPath("plutil")
	if lookErr != nil {
		t.Skip("plutil not found on PATH; this test did NOT verify plist well-formedness against the real consumer (skipped, not passed)")
	}
	plistFile := filepath.Join(t.TempDir(), "tslink-plist-lint.plist")
	if err := os.WriteFile(plistFile, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write temp plist file: %v", err)
	}
	lintOutput, lintErr := exec.Command(plutilPath, "-lint", plistFile).CombinedOutput()
	if lintErr != nil {
		t.Fatalf("plutil -lint rejected the rendered plist: %v\n%s\n--- rendered plist ---\n%s", lintErr, lintOutput, rendered)
	}
}

func TestInstallCommandBootoutThenBootstrapsLaunchAgentOnSuccess(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	stubDarwinInstallDaemonStopped(t)
	resetRootJSONFlag(t)
	t.Cleanup(func() { installCmd.SetOut(nil) })

	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		if len(args) > 0 && args[0] == "print" {
			return runningLaunchAgentState(), nil
		}
		return []byte("bootstrap ok"), nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00"),
		strings.Join([]string{"print", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"print", "gui/501/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "installed and loaded in gui/501") {
		t.Fatalf("install output missing success domain: %s", out.String())
	}
}

func TestInstallCommandBootstrapsLaunchAgentAndSurfacesOutput(t *testing.T) {
	stubDarwinInstallDaemonStopped(t)
	resetRootJSONFlag(t)
	t.Cleanup(func() { installCmd.SetOut(nil) })

	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		if len(args) > 0 && args[0] == "bootout" {
			return []byte("Boot-out failed: 3: No such process"), errors.New("bootout failed")
		}
		return []byte("bootstrap stderr"), errors.New("launchctl failed")
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err == nil {
		t.Fatal("install RunE() error = nil, want bootstrap failure")
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(err.Error(), "bootstrap stderr") {
		t.Fatalf("install error did not surface launchctl output: %v", err)
	}
	if strings.Contains(out.String(), "✓") {
		t.Fatalf("install printed success after bootstrap failure: %s", out.String())
	}
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read generated plist: %v", err)
	}
	if !strings.Contains(string(plist), "<key>ThrottleInterval</key>") {
		t.Fatalf("plist missing throttle interval:\n%s", plist)
	}
}

func TestInstallCommandFallsBackToUserDomainWhenGUIDomainMissing(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	stubDarwinInstallDaemonStopped(t)
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, installCmd)
	t.Cleanup(func() {
		installCmd.SetOut(nil)
		installCmd.SetErr(nil)
	})

	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 503 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		if len(args) >= 2 && strings.HasPrefix(args[1], "gui/503") {
			return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
		}
		if len(args) >= 2 && args[0] == "bootout" {
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		}
		if len(args) > 0 && args[0] == "print" {
			return runningLaunchAgentState(), nil
		}
		return []byte("user bootstrap ok"), nil
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	installCmd.SetOut(&out)
	installCmd.SetErr(&errOut)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/503/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/503/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/503", plistPath}, "\x00"),
		strings.Join([]string{"bootstrap", "user/503", plistPath}, "\x00"),
		strings.Join([]string{"print", "user/503/" + plistLabel}, "\x00"),
		strings.Join([]string{"print", "user/503/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "installed and loaded in user/503") {
		t.Fatalf("install stdout = %q, want success domain", out.String())
	}
	for _, want := range []string{"no desktop session exists for this user", "user/503"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("install stderr = %q, want %q", errOut.String(), want)
		}
	}
	if strings.Contains(out.String(), "→ ⚠") || !strings.Contains(errOut.String(), "→ ⚠") {
		t.Fatalf("warning streams: stdout=%q stderr=%q, want warning only on stderr", out.String(), errOut.String())
	}
}

func TestInstallUpgradeDoesNotBootstrapFallbackWhenGUIDomainBootoutIsUnavailable(t *testing.T) {
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, installCmd)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldArtifactConflict := installDaemonArtifactConflictFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		installDaemonArtifactConflictFn = oldArtifactConflict
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	installDaemonArtifactConflictFn = func() error { return nil }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	oldPlist := []byte("<plist>old</plist>")
	if err := os.WriteFile(plistPath, oldPlist, 0o600); err != nil {
		t.Fatalf("WriteFile(old plist) error = %v", err)
	}

	var calls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		call := strings.Join(args, "\x00")
		calls = append(calls, call)
		if len(args) != 2 || args[0] != "bootout" {
			t.Fatalf("unsafe launchctl call after unconfirmed upgrade handoff: %q", args)
		}
		if strings.HasPrefix(args[1], "gui/") {
			return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
		}
		return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "could not confirm the prior job was unloaded") || !strings.Contains(err.Error(), "previous LaunchAgent plist was restored") || !strings.Contains(err.Error(), "tslink install --force") || !strings.Contains(err.Error(), "second daemon") {
		t.Fatalf("install upgrade error = %v, want unavailable-domain handoff failure with restored plist", err)
	}
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/501/" + plistLabel}, "\x00"),
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want only both bootout probes %q", calls, wantCalls)
	}
	gotPlist, readErr := os.ReadFile(plistPath)
	if readErr != nil || !bytes.Equal(gotPlist, oldPlist) {
		t.Fatalf("restored plist = %q, %v; want %q", gotPlist, readErr, oldPlist)
	}

	calls = nil
	setRootJSONFlag(t, true)
	var jsonErr error
	gotJSON := captureStdout(t, func() {
		jsonErr = installCmd.RunE(installCmd, nil)
	})
	if !output.IsSilent(jsonErr) || output.ExitCode(jsonErr) != output.ExitError {
		t.Fatalf("install JSON error = %v (exit %d), want silent exit 1", jsonErr, output.ExitCode(jsonErr))
	}
	result := parseResult(t, gotJSON)
	if result.Error == nil || result.Error.Code != registry.CodeLaunchctlDomainUnavailable {
		t.Fatalf("install JSON error = %+v, want %q", result.Error, registry.CodeLaunchctlDomainUnavailable)
	}
	data := dataMap(t, gotJSON)
	if data["unavailable_domain"] != "gui/501" || data["force_available"] != true || data["force_command"] != "tslink install --force" {
		t.Fatalf("install JSON recovery data = %#v, want gui domain and exact force command", data)
	}
	risk, _ := data["force_risk"].(string)
	if !strings.Contains(risk, "second daemon") {
		t.Fatalf("install JSON force_risk = %q, want second-daemon risk", risk)
	}
}

func TestInstallForceRecoversHeadlessUpgradeStatesDAndE(t *testing.T) {
	for _, tc := range []struct {
		name       string
		userLoaded bool
	}{
		{name: "D_nothing_loaded", userLoaded: false},
		{name: "E_prior_user_job", userLoaded: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRootJSONFlag(t)
			resetCommandLocalFlags(t, installCmd)
			stubDarwinLaunchAgentVerificationNoWait(t)
			home := t.TempDir()
			testenv.SetHome(t, home)

			oldHome := userHomeDirFn
			oldExe := executablePathFn
			oldEval := evalSymlinksFn
			oldUID := userUIDFn
			oldArtifactConflict := installDaemonArtifactConflictFn
			oldPIDPath := pidPathFn
			oldRunning := isRunningFn
			oldReadPID := readPIDFn
			oldLaunchctl := launchctlCombinedOutput
			t.Cleanup(func() {
				userHomeDirFn = oldHome
				executablePathFn = oldExe
				evalSymlinksFn = oldEval
				userUIDFn = oldUID
				installDaemonArtifactConflictFn = oldArtifactConflict
				pidPathFn = oldPIDPath
				isRunningFn = oldRunning
				readPIDFn = oldReadPID
				launchctlCombinedOutput = oldLaunchctl
				installCmd.SetOut(nil)
				installCmd.SetErr(nil)
			})

			userHomeDirFn = func() (string, error) { return home, nil }
			executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
			evalSymlinksFn = func(path string) (string, error) { return path, nil }
			userUIDFn = func() int { return 501 }
			installDaemonArtifactConflictFn = func() error { return nil }
			pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
			isRunningFn = func(string) bool { return tc.userLoaded }
			readPIDFn = func(string) (int, error) { return 1775, nil }

			plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
			if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(plistPath, []byte("old plist"), 0o600); err != nil {
				t.Fatalf("WriteFile(old plist) error = %v", err)
			}

			userLoaded := tc.userLoaded
			launchctlCombinedOutput = func(args ...string) ([]byte, error) {
				isGUI := len(args) >= 2 && strings.HasPrefix(args[1], "gui/")
				if isGUI {
					return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
				}
				switch args[0] {
				case "print":
					if userLoaded {
						return runningLaunchAgentState(), nil
					}
					return []byte("Could not find service " + args[1]), errors.New("exit status 113")
				case "bootout":
					if userLoaded {
						userLoaded = false
						return nil, nil
					}
					return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
				case "bootstrap":
					userLoaded = true
					return nil, nil
				default:
					t.Fatalf("unexpected launchctl call: %q", args)
					return nil, nil
				}
			}

			var stdout, stderr bytes.Buffer
			installCmd.SetOut(&stdout)
			installCmd.SetErr(&stderr)
			defaultErr := installCmd.RunE(installCmd, nil)
			if defaultErr == nil || !strings.Contains(defaultErr.Error(), "tslink install --force") || !strings.Contains(defaultErr.Error(), "second daemon") {
				t.Fatalf("default install error = %v, want conservative refusal with exact override and risk", defaultErr)
			}
			if err := installCmd.Flags().Set("force", "true"); err != nil {
				t.Fatalf("set install --force: %v", err)
			}
			stdout.Reset()
			stderr.Reset()
			if err := installCmd.RunE(installCmd, nil); err != nil {
				t.Fatalf("install --force error = %v", err)
			}
			if !userLoaded || !strings.Contains(stdout.String(), "installed and loaded in user/501") {
				t.Fatalf("forced install did not reach working user-domain install: loaded=%v stdout=%q", userLoaded, stdout.String())
			}
			for _, want := range []string{"--force proceeded", "second daemon", "no desktop session exists for this user"} {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("forced install stderr = %q, want %q", stderr.String(), want)
				}
			}
		})
	}
}

func TestInstallCommandRefusesRunningDaemonBeforeWritingPlist(t *testing.T) {
	resetRootJSONFlag(t)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldLaunchctl := launchctlCombinedOutput
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		launchctlCombinedOutput = oldLaunchctl
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return 1676, nil }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		t.Fatalf("launchctl called during daemon conflict: %v", args)
		return nil, nil
	}

	err := installCmd.RunE(installCmd, nil)
	if output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "pid 1676") || !strings.Contains(err.Error(), "tslink stop") || !strings.Contains(err.Error(), "tslink uninstall") || !strings.Contains(err.Error(), "KeepAlive") {
		t.Fatalf("install conflict = %v (exit %d)", err, output.ExitCode(err))
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Fatalf("plist exists after conflict: %v", statErr)
	}
}

func TestInstallCommandDoesNotClaimLoadedWhenLaunchAgentIsWaiting(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	resetRootJSONFlag(t)
	t.Cleanup(func() { installCmd.SetOut(nil) })
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	pidPath := filepath.Join(home, "tslink.pid")
	pidPathFn = func() (string, error) { return pidPath, nil }
	isRunningFn = func(path string) bool { return path == pidPath }
	readPIDFn = func(path string) (int, error) { return 1775, nil }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("old plist"), 0o644); err != nil {
		t.Fatalf("WriteFile(old plist) error = %v", err)
	}

	guiTarget := "gui/501/" + plistLabel
	userTarget := "user/501/" + plistLabel
	var launchctlCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		call := strings.Join(args, "\x00")
		if len(launchctlCalls) == 5 && call == strings.Join([]string{"print", guiTarget}, "\x00") {
			return []byte("state = waiting\npid = 0\n"), nil
		}
		launchctlCalls = append(launchctlCalls, call)
		switch len(launchctlCalls) {
		case 1:
			if call != strings.Join([]string{"print", guiTarget}, "\x00") {
				t.Fatalf("ownership probe = %q, want gui target", call)
			}
			return runningLaunchAgentState(), nil
		case 2:
			if call != strings.Join([]string{"bootout", guiTarget}, "\x00") {
				t.Fatalf("first install handoff = %q, want gui bootout", call)
			}
		case 3:
			if call != strings.Join([]string{"bootout", userTarget}, "\x00") {
				t.Fatalf("second install handoff = %q, want user bootout", call)
			}
		case 4:
			if call != strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00") {
				t.Fatalf("new-job bootstrap = %q, want gui domain and plist path", call)
			}
		case 5:
			if call != strings.Join([]string{"print", guiTarget}, "\x00") {
				t.Fatalf("new-job verification = %q, want gui target", call)
			}
			return []byte("state = waiting\npid = 0\n"), nil
		case 6:
			if call != strings.Join([]string{"bootout", guiTarget}, "\x00") {
				t.Fatalf("failed-new-job cleanup = %q, want exact loaded target", call)
			}
		case 7:
			if call != strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00") {
				t.Fatalf("previous-job bootstrap = %q, want captured domain and plist path", call)
			}
		case 8, 9:
			if call != strings.Join([]string{"print", guiTarget}, "\x00") {
				t.Fatalf("previous-job verification = %q, want captured target", call)
			}
			return runningLaunchAgentState(), nil
		default:
			t.Fatalf("unexpected launchctl call %d: %q", len(launchctlCalls), call)
		}
		return nil, nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err == nil {
		t.Fatal("install RunE() error = nil, want waiting-state failure")
	}
	if strings.Contains(out.String(), "✓") || strings.Contains(out.String(), "installed and loaded") {
		t.Fatalf("install claimed loaded for waiting LaunchAgent: %s", out.String())
	}
	if !strings.Contains(err.Error(), "did not reach running state") || !strings.Contains(err.Error(), "launchctl print") {
		t.Fatalf("install error = %q, want actionable post-install state", err)
	}
	plist, readErr := os.ReadFile(plistPath)
	if readErr != nil || string(plist) != "old plist" {
		t.Fatalf("previous plist was not restored: %q, %v", plist, readErr)
	}
	if info, statErr := os.Stat(plistPath); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("previous plist mode was not restored: %v, %v", info, statErr)
	}
	if !strings.Contains(err.Error(), "previous LaunchAgent plist was restored and reloaded") || !strings.Contains(err.Error(), "re-run 'tslink install'") {
		t.Fatalf("install error = %q, want honest upgrade restoration guidance", err)
	}
	if strings.Contains(err.Error(), "installation was rolled back") {
		t.Fatalf("install error falsely uses generic rollback wording: %q", err)
	}
	if len(launchctlCalls) != 9 {
		t.Fatalf("launchctl calls = %q, want nine-step ownership/handoff/restore sequence", launchctlCalls)
	}
}

func TestRestorePreviousLaunchAgentDoesNotClaimReloadedWhenBootstrapFails(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	home := t.TempDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("new plist"), 0o600); err != nil {
		t.Fatalf("WriteFile(new plist) error = %v", err)
	}

	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() { launchctlCombinedOutput = oldLaunchctl })
	previousTarget := "gui/501/" + plistLabel
	var calls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		call := strings.Join(args, "\x00")
		calls = append(calls, call)
		switch len(calls) {
		case 1:
			if call != strings.Join([]string{"bootout", previousTarget}, "\x00") {
				t.Fatalf("cleanup call = %q, want exact failed-new-job target", call)
			}
			return nil, nil
		case 2:
			if call != strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00") {
				t.Fatalf("restore bootstrap = %q, want captured domain", call)
			}
			return []byte("bootstrap denied"), errors.New("bootstrap failed")
		default:
			t.Fatalf("unexpected launchctl call after failed restore bootstrap: %q", call)
		}
		return nil, nil
	}

	result, err := restorePreviousLaunchAgent(
		launchAgentPreviousState{Existed: true, Plist: []byte("old plist"), Mode: 0o644, Domain: "gui/501", Target: previousTarget},
		launchctlLoadResult{Target: previousTarget, Bootstrapped: true},
		plistPath,
	)
	if err == nil || !strings.Contains(err.Error(), "bootstrap failed") || !strings.Contains(err.Error(), "bootstrap denied") {
		t.Fatalf("restorePreviousLaunchAgent() error = %v, want surfaced bootstrap failure", err)
	}
	if !result.PlistRestored || result.Reloaded {
		t.Fatalf("restore result = %+v, want restored bytes and Reloaded=false", result)
	}
	if info, statErr := os.Stat(plistPath); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("restored plist mode = %v, %v; want unsafe prior mode converged to 0600", info, statErr)
	}
	if len(calls) != 2 {
		t.Fatalf("launchctl calls = %q, want cleanup then failed bootstrap only", calls)
	}
}

func TestInstallCommandAtomicWriteRejectsExistingPlistSymlink(t *testing.T) {
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)
	oldHome := userHomeDirFn
	oldArtifactConflict := installDaemonArtifactConflictFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		installDaemonArtifactConflictFn = oldArtifactConflict
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	installDaemonArtifactConflictFn = func() error { return nil }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		t.Fatalf("launchctl called after unsafe plist target: %v", args)
		return nil, nil
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	referent := filepath.Join(home, "protected.plist")
	want := []byte("protected referent")
	if err := os.WriteFile(referent, want, 0o600); err != nil {
		t.Fatalf("WriteFile(referent) error = %v", err)
	}
	if err := os.Symlink(referent, plistPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("install RunE() error = %v, want atomic writer symlink rejection", err)
	}
	got, readErr := os.ReadFile(referent)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("referent changed through plist symlink: %q, %v", got, readErr)
	}
}

func TestInstallCommandSupportsSymlinkedLaunchAgentsDirectoryWithoutChangingMode(t *testing.T) {
	stubDarwinInstallDaemonStopped(t)
	stubDarwinLaunchAgentVerificationNoWait(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return runningLaunchAgentState(), nil
		}
		return nil, nil
	}

	libraryDir := filepath.Join(home, "Library")
	realDir := filepath.Join(home, "RelocatedLaunchAgents")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(Library) error = %v", err)
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(real LaunchAgents) error = %v", err)
	}
	if err := os.Chmod(realDir, 0o755); err != nil {
		t.Fatalf("Chmod(real LaunchAgents) error = %v", err)
	}
	if err := os.Symlink(realDir, filepath.Join(libraryDir, "LaunchAgents")); err != nil {
		t.Fatalf("Symlink(LaunchAgents) error = %v", err)
	}

	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("install RunE() error = %v, want symlinked LaunchAgents support", err)
	}
	if info, err := os.Stat(realDir); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("shared LaunchAgents mode = %v, %v; want unchanged 0755", info, err)
	}
	plistPath := filepath.Join(realDir, plistLabel+".plist")
	if info, err := os.Stat(plistPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("installed plist mode = %v, %v; want 0600", info, err)
	}
}

func TestInstallCommandGroupWritableLaunchAgentsErrorIncludesRemedy(t *testing.T) {
	stubDarwinInstallDaemonStopped(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		t.Fatalf("launchctl called after unsafe parent rejection: %q", args)
		return nil, nil
	}

	launchAgentsDir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(LaunchAgents) error = %v", err)
	}
	if err := os.Chmod(launchAgentsDir, 0o775); err != nil {
		t.Fatalf("Chmod(LaunchAgents) error = %v", err)
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "group- or world-writable (0775)") || !strings.Contains(err.Error(), "chmod g-w,o-w "+launchAgentsDir) {
		t.Fatalf("install error = %v, want exact chmod remedy for %s", err, launchAgentsDir)
	}
	if _, statErr := os.Stat(filepath.Join(launchAgentsDir, plistLabel+".plist")); !os.IsNotExist(statErr) {
		t.Fatalf("plist exists after unsafe parent rejection: %v", statErr)
	}
}

func TestRestorePreviousLaunchAgentSupportsSymlinkedLaunchAgentsDirectoryWithoutChangingMode(t *testing.T) {
	home := t.TempDir()
	libraryDir := filepath.Join(home, "Library")
	realDir := filepath.Join(home, "RelocatedLaunchAgents")
	if err := os.MkdirAll(libraryDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(Library) error = %v", err)
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(real LaunchAgents) error = %v", err)
	}
	if err := os.Chmod(realDir, 0o755); err != nil {
		t.Fatalf("Chmod(real LaunchAgents) error = %v", err)
	}
	launchAgentsDir := filepath.Join(libraryDir, "LaunchAgents")
	if err := os.Symlink(realDir, launchAgentsDir); err != nil {
		t.Fatalf("Symlink(LaunchAgents) error = %v", err)
	}
	plistPath := filepath.Join(launchAgentsDir, plistLabel+".plist")
	if err := os.WriteFile(filepath.Join(realDir, plistLabel+".plist"), []byte("new plist"), 0o600); err != nil {
		t.Fatalf("WriteFile(new plist) error = %v", err)
	}

	result, err := restorePreviousLaunchAgent(
		launchAgentPreviousState{Existed: true, Plist: []byte("old plist"), Mode: 0o644},
		launchctlLoadResult{},
		plistPath,
	)
	if err != nil || !result.PlistRestored || result.Reloaded {
		t.Fatalf("restore result = %+v, error = %v; want restored bytes without reload", result, err)
	}
	if info, err := os.Stat(realDir); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("shared LaunchAgents mode = %v, %v; want unchanged 0755", info, err)
	}
	got, readErr := os.ReadFile(filepath.Join(realDir, plistLabel+".plist"))
	if readErr != nil || string(got) != "old plist" {
		t.Fatalf("restored plist = %q, %v; want old plist", got, readErr)
	}
	if info, err := os.Stat(filepath.Join(realDir, plistLabel+".plist")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("restored plist mode = %v, %v; want 0600", info, err)
	}
}

func TestRestorePreviousLaunchAgentAtomicWriteRejectsReplacementSymlink(t *testing.T) {
	home := t.TempDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	referent := filepath.Join(home, "protected.plist")
	want := []byte("protected referent")
	if err := os.WriteFile(referent, want, 0o600); err != nil {
		t.Fatalf("WriteFile(referent) error = %v", err)
	}
	if err := os.Symlink(referent, plistPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	result, err := restorePreviousLaunchAgent(
		launchAgentPreviousState{Existed: true, Plist: []byte("old plist"), Mode: 0o600},
		launchctlLoadResult{},
		plistPath,
	)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("restorePreviousLaunchAgent() error = %v, want atomic writer symlink rejection", err)
	}
	if result.PlistRestored || result.Reloaded {
		t.Fatalf("restore result = %+v, want no claimed restoration", result)
	}
	got, readErr := os.ReadFile(referent)
	if readErr != nil || !bytes.Equal(got, want) {
		t.Fatalf("referent changed through restore symlink: %q, %v", got, readErr)
	}
}

func TestInstallCommandRemovesNewPlistWhenLaunchAgentVerificationFails(t *testing.T) {
	stubDarwinInstallDaemonStopped(t)
	stubDarwinLaunchAgentVerificationNoWait(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 0 && args[0] == "print" {
			return []byte("state = waiting\npid = 0\n"), nil
		}
		return nil, nil
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "new installation was rolled back") || !strings.Contains(err.Error(), "re-run 'tslink install'") {
		t.Fatalf("install RunE() error = %v, want fresh-install rollback guidance", err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Fatalf("new plist remains after successful verification rollback: %v", statErr)
	}
}

func TestInstallCommandKeepsNewPlistWhenRollbackBootoutFails(t *testing.T) {
	stubDarwinInstallDaemonStopped(t)
	stubDarwinLaunchAgentVerificationNoWait(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	bootstrapped := false
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		switch args[0] {
		case "bootstrap":
			bootstrapped = true
			return nil, nil
		case "print":
			return []byte("state = waiting\npid = 0\n"), nil
		case "bootout":
			if bootstrapped {
				return []byte("permission denied"), errors.New("bootout failed")
			}
		}
		return nil, nil
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "automatic rollback was incomplete") || !strings.Contains(err.Error(), "plist was kept") || !strings.Contains(err.Error(), "tslink uninstall") {
		t.Fatalf("install RunE() error = %v, want retained-plist recovery guidance", err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, statErr := os.Stat(plistPath); statErr != nil {
		t.Fatalf("new plist was removed after failed bootout: %v", statErr)
	}
}

func TestWaitForLaunchAgentRunningSettlesAfterTransientWaiting(t *testing.T) {
	stubDarwinLaunchAgentVerificationNoWait(t)
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() { launchctlCombinedOutput = oldLaunchctl })

	printCalls := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		printCalls++
		if printCalls < 3 {
			return []byte("state = waiting\npid = 0\n"), nil
		}
		return runningLaunchAgentState(), nil
	}

	output, err := waitForLaunchAgentRunning("gui/501/"+plistLabel, time.Second, 0)
	if err != nil {
		t.Fatalf("waitForLaunchAgentRunning() error = %v", err)
	}
	if printCalls != 4 {
		t.Fatalf("launchctl print calls = %d, want 4", printCalls)
	}
	state, pid := parseLaunchAgentState(output)
	if state != "running" || pid != 1775 {
		t.Fatalf("settled state = %q/%d, want running/1775", state, pid)
	}
}

func TestReinstallLaunchAgentWaitsForInProgressBootout(t *testing.T) {
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	oldTimeout := launchAgentBootoutTimeout
	oldPollInterval := launchAgentBootoutPollInterval
	t.Cleanup(func() {
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
		launchAgentBootoutTimeout = oldTimeout
		launchAgentBootoutPollInterval = oldPollInterval
	})
	userUIDFn = func() int { return 501 }
	launchAgentBootoutTimeout = time.Second
	launchAgentBootoutPollInterval = 0

	guiTarget := "gui/501/" + plistLabel
	bootoutPrintCalls := 0
	bootstrapCalls := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		switch args[0] {
		case "bootout":
			if args[1] == guiTarget {
				return []byte("Boot-out failed: 36: Operation now in progress"), errors.New("bootout failed")
			}
			return []byte("Boot-out failed: 3: No such process"), errors.New("bootout failed")
		case "print":
			if bootstrapCalls == 0 {
				bootoutPrintCalls++
				if bootoutPrintCalls < 3 {
					return runningLaunchAgentState(), nil
				}
				return []byte("Could not find service"), errors.New("print failed")
			}
			return runningLaunchAgentState(), nil
		case "bootstrap":
			if bootoutPrintCalls != 3 {
				t.Fatalf("bootstrap began after %d bootout polls, want 3", bootoutPrintCalls)
			}
			bootstrapCalls++
			return nil, nil
		}
		return nil, nil
	}

	result := reinstallLaunchAgent("/tmp/com.tslink.daemon.plist")
	if result.Err != nil || !result.Bootstrapped {
		t.Fatalf("reinstallLaunchAgent() = %+v", result)
	}
	if bootstrapCalls != 1 || bootoutPrintCalls != 3 {
		t.Fatalf("bootstrap/poll calls = %d/%d, want 1/3", bootstrapCalls, bootoutPrintCalls)
	}
}

func TestReinstallLaunchAgentDoesNotBootstrapWhenInProgressBootoutTimesOut(t *testing.T) {
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	oldTimeout := launchAgentBootoutTimeout
	oldPollInterval := launchAgentBootoutPollInterval
	t.Cleanup(func() {
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
		launchAgentBootoutTimeout = oldTimeout
		launchAgentBootoutPollInterval = oldPollInterval
	})
	userUIDFn = func() int { return 501 }
	launchAgentBootoutTimeout = 0
	launchAgentBootoutPollInterval = 0

	bootstrapCalls := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) == 0 {
			return nil, nil
		}
		switch args[0] {
		case "bootout":
			return []byte("Boot-out failed: 36: Operation now in progress"), errors.New("bootout failed")
		case "print":
			return runningLaunchAgentState(), nil
		case "bootstrap":
			bootstrapCalls++
		}
		return nil, nil
	}

	result := reinstallLaunchAgent("/tmp/com.tslink.daemon.plist")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "remained in progress") {
		t.Fatalf("reinstallLaunchAgent() error = %v, want bounded bootout timeout", result.Err)
	}
	if result.Bootstrapped || bootstrapCalls != 0 {
		t.Fatalf("reinstallLaunchAgent() bootstrapped = %v, calls = %d", result.Bootstrapped, bootstrapCalls)
	}
}

func TestWaitForLaunchAgentRunningRejectsRunningWithoutPID(t *testing.T) {
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() { launchctlCombinedOutput = oldLaunchctl })
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		return []byte("state = running\n"), nil
	}

	_, err := waitForLaunchAgentRunning("gui/501/"+plistLabel, 0, 0)
	if err == nil || !strings.Contains(err.Error(), `state="running"`) || !strings.Contains(err.Error(), "pid=0") {
		t.Fatalf("waitForLaunchAgentRunning() error = %v, want running-without-pid failure", err)
	}
}

func TestWaitForLaunchAgentRunningRejectsWaitingWithStalePID(t *testing.T) {
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() { launchctlCombinedOutput = oldLaunchctl })
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		return []byte("state = waiting\npid = 1775\n"), nil
	}

	_, err := waitForLaunchAgentRunning("gui/501/"+plistLabel, 0, 0)
	if err == nil || !strings.Contains(err.Error(), `state="waiting"`) || !strings.Contains(err.Error(), "pid=1775") {
		t.Fatalf("waitForLaunchAgentRunning() error = %v, want waiting-with-stale-pid failure", err)
	}
}

func TestLaunchctlDomainNotFoundMatchesRealCouldNotFindDomainWording(t *testing.T) {
	if !launchctlDomainNotFound([]byte("Could not find domain for: gui/503"), errors.New("bootstrap failed")) {
		t.Fatal("expected real launchctl domain-missing wording to be matched")
	}
}

func TestLaunchctlTargetNotFoundRequiresErrorForEveryClassifierPhrase(t *testing.T) {
	phrases := []string{
		"Boot-out failed: 3: No such process",
		"Could not find service com.tslink.daemon",
		"Service not found",
		"Could not find specified service",
		"Could not find domain for: gui/503",
		"Domain does not exist",
		"domain is not found",
		"no such domain",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			if launchctlTargetNotFound([]byte(phrase), nil) {
				t.Fatalf("launchctlTargetNotFound(%q, nil) = true, want false", phrase)
			}
			if !launchctlTargetNotFound([]byte(phrase), errors.New("launchctl failed")) {
				t.Fatalf("launchctlTargetNotFound(%q, err) = false, want true", phrase)
			}
		})
	}
}

func TestLaunchctlDomainNotFoundRequiresErrorForEveryPhrase(t *testing.T) {
	phrases := []string{
		"Could not find domain for: gui/503",
		"Domain does not exist",
		"domain is not found",
		"no such domain",
	}
	for _, phrase := range phrases {
		t.Run(phrase, func(t *testing.T) {
			if launchctlDomainNotFound([]byte(phrase), nil) {
				t.Fatalf("launchctlDomainNotFound(%q, nil) = true, want false", phrase)
			}
			if !launchctlDomainNotFound([]byte(phrase), errors.New("launchctl failed")) {
				t.Fatalf("launchctlDomainNotFound(%q, err) = false, want true", phrase)
			}
		})
	}
}

func TestDarwinUninstallManifestOutcomeValuesMatchWireConstants(t *testing.T) {
	field := commandJSONResultFields("tslink uninstall")["launchctl_outcome"]
	want := []string{
		launchctlOutcomeNotInstalled,
		launchctlOutcomeUnloaded,
		launchctlOutcomeAlreadyAbsent,
		launchctlOutcomeUnconfirmed,
	}
	if strings.Join(field.Values, "\n") != strings.Join(want, "\n") {
		t.Fatalf("manifest launchctl_outcome values = %q, wire constants = %q", field.Values, want)
	}
}

func TestUninstallDocumentationMatchesConfirmedRemovalPolicy(t *testing.T) {
	for _, want := range []string{"every domain", "If any domain is unavailable", "tslink uninstall --force", "may leave a job running", "real launchctl error", "plist is kept"} {
		if !strings.Contains(uninstallCmd.Long, want) {
			t.Fatalf("uninstall help missing %q:\n%s", want, uninstallCmd.Long)
		}
	}
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return this test file")
	}
	readmePath := filepath.Join(filepath.Dir(filename), "..", "README.md")
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", readmePath, err)
	}
	for _, want := range []string{
		"If any domain is unavailable",
		"tslink uninstall --force",
		"job may remain running",
		"Real launchctl errors remain fatal with `--force`",
	} {
		if !strings.Contains(string(readme), want) {
			t.Fatalf("README uninstall policy missing %q", want)
		}
	}
	if strings.Contains(string(readme), "During SSH/headless macOS installs") {
		t.Fatal("README retains inaccurate SSH/headless domain wording")
	}
}

func TestUninstallCommandJSONDistinguishesNotInstalledWithoutLaunchctlGuess(t *testing.T) {
	resetRootJSONFlag(t)
	setRootJSONFlag(t, true)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		t.Fatalf("launchctl called for not-installed uninstall: %v", args)
		return nil, nil
	}

	var runErr error
	gotJSON := captureStdout(t, func() {
		runErr = uninstallCmd.RunE(uninstallCmd, nil)
	})
	if runErr != nil {
		t.Fatalf("uninstall error = %v, want idempotent success", runErr)
	}
	data := dataMap(t, gotJSON)
	if data["removed"] != false || data["launchctl_outcome"] != launchctlOutcomeNotInstalled || data["launchctl_target"] != "" {
		t.Fatalf("uninstall not-installed data = %#v", data)
	}
	for _, absentField := range []string{"launchctl_output", "detail", "warning"} {
		if _, ok := data[absentField]; ok {
			t.Fatalf("uninstall not-installed data unexpectedly contains %q: %#v", absentField, data)
		}
	}
}

func TestUninstallCommandJSONCoversLaunchctlOutcomeMatrix(t *testing.T) {
	type outcome string
	const (
		okOutcome          outcome = "ok"
		absentOutcome      outcome = "absent"
		realErrOutcome     outcome = "real-error"
		unavailableOutcome outcome = "domain-unavailable"
	)
	guiTarget := "gui/505/" + plistLabel
	userTarget := "user/505/" + plistLabel
	guiOK := guiTarget + " bootout ok"
	userOK := userTarget + " bootout ok"
	guiRealErr := guiTarget + " permission denied"
	userRealErr := userTarget + " permission denied"
	absent := "Boot-out failed: 3: No such process"
	guiUnavailable := "Could not find domain for: " + guiTarget
	userUnavailable := "Could not find domain for: " + userTarget
	alreadyAbsentDetail := "LaunchAgent was already absent from all launchd domains"
	guiUnavailableDetailWithUserSuccess := "launchctl " + guiTarget + " was unavailable because no desktop session exists for this user; " + userTarget + " confirmed a successful bootout, but the plist was kept because a job may still be loaded there; retry when the domain is addressable, or run 'tslink uninstall --force' to remove the plist while accepting that risk"
	userUnavailableDetailWithGUISuccess := "launchctl " + userTarget + " could not be addressed from the current launchd context; " + guiTarget + " confirmed a successful bootout, but the plist was kept because a job may still be loaded there; retry when the domain is addressable, or run 'tslink uninstall --force' to remove the plist while accepting that risk"
	guiUnavailableDetailNoSuccess := "launchctl " + guiTarget + " was unavailable because no desktop session exists for this user; no other domain confirmed a successful bootout, but the plist was kept because a job may still be loaded there; retry when the domain is addressable, or run 'tslink uninstall --force' to remove the plist while accepting that risk"
	userUnavailableDetailNoSuccess := "launchctl " + userTarget + " could not be addressed from the current launchd context; no other domain confirmed a successful bootout, but the plist was kept because a job may still be loaded there; retry when the domain is addressable, or run 'tslink uninstall --force' to remove the plist while accepting that risk"
	forcedGUIWarning := "--force removed the plist even though launchctl " + guiTarget + " was unavailable because no desktop session exists for this user; a job may still be loaded there; when the domain is addressable, run 'launchctl print " + guiTarget + "' to confirm, then run 'launchctl bootout " + guiTarget + "' to remove the job if it is loaded"
	forcedUserWarning := "--force removed the plist even though launchctl " + userTarget + " could not be addressed from the current launchd context; a job may still be loaded there; when the domain is addressable, run 'launchctl print " + userTarget + "' to confirm, then run 'launchctl bootout " + userTarget + "' to remove the job if it is loaded"
	forcedDetail := "The plist was removed by explicit --force without confirming every launchd domain"
	tests := []struct {
		name        string
		gui         outcome
		user        outcome
		force       bool
		wantCode    int
		wantRemoved bool
		wantOutcome string
		wantTarget  string
		wantOutput  string
		wantDetail  string
		wantWarning string
	}{
		{name: "ok-ok", gui: okOutcome, user: okOutcome, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnloaded, wantTarget: guiTarget, wantOutput: guiOK},
		{name: "ok-absent", gui: okOutcome, user: absentOutcome, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnloaded, wantTarget: guiTarget, wantOutput: guiOK},
		{name: "absent-ok", gui: absentOutcome, user: okOutcome, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnloaded, wantTarget: userTarget, wantOutput: userOK},
		{name: "absent-absent", gui: absentOutcome, user: absentOutcome, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeAlreadyAbsent, wantOutput: absent + "\n" + absent, wantDetail: alreadyAbsentDetail},
		{name: "real-error-ok", gui: realErrOutcome, user: okOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiRealErr + "\n" + userOK},
		{name: "ok-real-error", gui: okOutcome, user: realErrOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: userTarget, wantOutput: guiOK + "\n" + userRealErr},
		{name: "real-error-absent", gui: realErrOutcome, user: absentOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiRealErr + "\n" + absent},
		{name: "absent-real-error", gui: absentOutcome, user: realErrOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: userTarget, wantOutput: absent + "\n" + userRealErr},
		{name: "real-error-real-error", gui: realErrOutcome, user: realErrOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiRealErr + "\n" + userRealErr},
		{name: "domain-unavailable-ok", gui: unavailableOutcome, user: okOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiUnavailable + "\n" + userOK, wantDetail: guiUnavailableDetailWithUserSuccess},
		{name: "ok-domain-unavailable", gui: okOutcome, user: unavailableOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: userTarget, wantOutput: guiOK + "\n" + userUnavailable, wantDetail: userUnavailableDetailWithGUISuccess},
		{name: "domain-unavailable-absent", gui: unavailableOutcome, user: absentOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiUnavailable + "\n" + absent, wantDetail: guiUnavailableDetailNoSuccess},
		{name: "absent-domain-unavailable", gui: absentOutcome, user: unavailableOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: userTarget, wantOutput: absent + "\n" + userUnavailable, wantDetail: userUnavailableDetailNoSuccess},
		{name: "domain-unavailable-domain-unavailable", gui: unavailableOutcome, user: unavailableOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiUnavailable + "\n" + userUnavailable, wantDetail: guiUnavailableDetailNoSuccess},
		{name: "domain-unavailable-real-error", gui: unavailableOutcome, user: realErrOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: userTarget, wantOutput: guiUnavailable + "\n" + userRealErr},
		{name: "real-error-domain-unavailable", gui: realErrOutcome, user: unavailableOutcome, wantCode: output.ExitError, wantRemoved: false, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiRealErr + "\n" + userUnavailable},
		{name: "force-domain-unavailable-ok", gui: unavailableOutcome, user: okOutcome, force: true, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnloaded, wantTarget: userTarget, wantOutput: userOK, wantWarning: forcedGUIWarning},
		{name: "force-ok-domain-unavailable", gui: okOutcome, user: unavailableOutcome, force: true, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnloaded, wantTarget: guiTarget, wantOutput: guiOK, wantWarning: forcedUserWarning},
		{name: "force-domain-unavailable-absent", gui: unavailableOutcome, user: absentOutcome, force: true, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiUnavailable + "\n" + absent, wantDetail: forcedDetail, wantWarning: forcedGUIWarning},
		{name: "force-domain-unavailable-domain-unavailable", gui: unavailableOutcome, user: unavailableOutcome, force: true, wantCode: output.ExitSuccess, wantRemoved: true, wantOutcome: launchctlOutcomeUnconfirmed, wantTarget: guiTarget, wantOutput: guiUnavailable + "\n" + userUnavailable, wantDetail: forcedDetail, wantWarning: forcedGUIWarning},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetRootJSONFlag(t)
			resetCommandLocalFlags(t, uninstallCmd)
			if tc.force {
				if err := uninstallCmd.Flags().Set("force", "true"); err != nil {
					t.Fatalf("set uninstall --force: %v", err)
				}
			}
			setRootJSONFlag(t, true)
			home := t.TempDir()
			oldHome := userHomeDirFn
			oldUID := userUIDFn
			oldLaunchctl := launchctlCombinedOutput
			t.Cleanup(func() {
				userHomeDirFn = oldHome
				userUIDFn = oldUID
				launchctlCombinedOutput = oldLaunchctl
			})
			userHomeDirFn = func() (string, error) { return home, nil }
			userUIDFn = func() int { return 505 }
			plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
			if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			outcomes := []outcome{tc.gui, tc.user}
			var targets []string
			launchctlCombinedOutput = func(args ...string) ([]byte, error) {
				if len(args) != 2 || args[0] != "bootout" {
					t.Fatalf("launchctl args = %q, want bootout target", args)
				}
				index := len(targets)
				if index >= len(outcomes) {
					t.Fatalf("unexpected extra launchctl call: %q", args)
				}
				targets = append(targets, args[1])
				switch outcomes[index] {
				case okOutcome:
					return []byte(args[1] + " bootout ok"), nil
				case absentOutcome:
					return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
				case realErrOutcome:
					return []byte(args[1] + " permission denied"), fmt.Errorf("%s bootout denied", args[1])
				case unavailableOutcome:
					return []byte("Could not find domain for: " + args[1]), fmt.Errorf("%s domain unavailable", args[1])
				default:
					t.Fatalf("unknown outcome %q", outcomes[index])
					return nil, nil
				}
			}

			var runErr error
			gotJSON := captureStdout(t, func() {
				runErr = uninstallCmd.RunE(uninstallCmd, nil)
			})
			if gotCode := output.ExitCode(runErr); gotCode != tc.wantCode {
				t.Fatalf("uninstall exit = %d (%v), want %d; json=%s", gotCode, runErr, tc.wantCode, gotJSON)
			}
			result := parseResult(t, gotJSON)
			if result.OK != (tc.wantCode == output.ExitSuccess) {
				t.Fatalf("uninstall JSON ok = %v, want %v", result.OK, tc.wantCode == output.ExitSuccess)
			}
			data := dataMap(t, gotJSON)
			domainUnavailableRefusal := !tc.force && strings.Contains(tc.name, "domain-unavailable") && !strings.Contains(tc.name, "real-error")
			if domainUnavailableRefusal {
				if result.Error == nil || result.Error.Code != registry.CodeLaunchctlDomainUnavailable {
					t.Fatalf("uninstall JSON error = %+v, want %q", result.Error, registry.CodeLaunchctlDomainUnavailable)
				}
				if data["unavailable_domain"] != launchctlDomainForTarget(tc.wantTarget) || data["force_available"] != true || data["force_command"] != "tslink uninstall --force" {
					t.Fatalf("uninstall JSON recovery data = %#v, want unavailable domain and exact force command", data)
				}
				risk, _ := data["force_risk"].(string)
				if !strings.Contains(risk, "job remains running") {
					t.Fatalf("uninstall JSON force_risk = %q, want residual running-job risk", risk)
				}
			}
			if data["removed"] != tc.wantRemoved {
				t.Fatalf("uninstall JSON removed = %#v, want %v; data=%#v", data["removed"], tc.wantRemoved, data)
			}
			if data["launchctl_outcome"] != tc.wantOutcome {
				t.Fatalf("uninstall JSON outcome = %#v, want %q; data=%#v", data["launchctl_outcome"], tc.wantOutcome, data)
			}
			if data["launchctl_target"] != tc.wantTarget {
				t.Fatalf("uninstall JSON target = %#v, want %q; data=%#v", data["launchctl_target"], tc.wantTarget, data)
			}
			wantTargets := []string{"gui/505/" + plistLabel, "user/505/" + plistLabel}
			if strings.Join(targets, "\n") != strings.Join(wantTargets, "\n") {
				t.Fatalf("launchctl targets = %q, want %q", targets, wantTargets)
			}
			_, statErr := os.Stat(plistPath)
			if tc.wantRemoved && !os.IsNotExist(statErr) {
				t.Fatalf("plist stat after successful removal = %v, want not-exist", statErr)
			}
			if !tc.wantRemoved && statErr != nil {
				t.Fatalf("plist stat after failure = %v, want retained", statErr)
			}
			launchctlOutput, outputPresent := data["launchctl_output"].(string)
			if launchctlOutput != tc.wantOutput || outputPresent != (tc.wantOutput != "") {
				t.Fatalf("uninstall JSON launchctl_output = %q (present=%v), want %q (present=%v); data=%#v", launchctlOutput, outputPresent, tc.wantOutput, tc.wantOutput != "", data)
			}
			detail, detailPresent := data["detail"].(string)
			if detail != tc.wantDetail || detailPresent != (tc.wantDetail != "") {
				t.Fatalf("uninstall JSON detail = %q (present=%v), want %q (present=%v); data=%#v", detail, detailPresent, tc.wantDetail, tc.wantDetail != "", data)
			}
			warning, warningPresent := data["warning"].(string)
			if tc.wantCode == output.ExitSuccess && (warning != tc.wantWarning || warningPresent != (tc.wantWarning != "")) {
				t.Fatalf("uninstall JSON warning = %q (present=%v), want %q (present=%v); data=%#v", warning, warningPresent, tc.wantWarning, tc.wantWarning != "", data)
			}
			if tc.wantCode != output.ExitSuccess && warningPresent {
				t.Fatalf("failed uninstall JSON overloaded non-fatal warning field: data=%#v", data)
			}
			if tc.wantOutcome == launchctlOutcomeUnloaded && strings.Contains(strings.ToLower(launchctlOutput), "failed") {
				t.Fatalf("successful JSON contains launchctl failure text: %q", launchctlOutput)
			}
		})
	}
}

func TestForcedUninstallWarningIncludesRemovalCommand(t *testing.T) {
	target := "gui/501/" + plistLabel
	warning := forcedUninstallWarning(target)
	for _, command := range []string{"launchctl print " + target, "launchctl bootout " + target} {
		if !strings.Contains(warning, command) {
			t.Fatalf("forced-uninstall warning = %q, want actionable command %q", warning, command)
		}
	}
}

func TestDocumentationDoesNotConflateSSHWithLaunchdGUIDomain(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return this test file")
	}
	repoRoot := filepath.Join(filepath.Dir(filename), "..")
	for _, name := range []string{"README_zh.md", "CLAUDE.md"} {
		contents, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		text := string(contents)
		for _, stale := range []string{"SSH/headless macOS", "fails over SSH/headless macOS"} {
			if strings.Contains(text, stale) {
				t.Fatalf("%s retains launchd gui-domain conflation %q", name, stale)
			}
		}
		if !strings.Contains(text, "Aqua") {
			t.Fatalf("%s does not name the desktop (Aqua) session boundary", name)
		}
	}
}

func TestUninstallCommandKeepsPlistWhenLaunchctlDomainIsUnavailable(t *testing.T) {
	if got, want := t.Name(), "TestUninstallCommandKeepsPlistWhenLaunchctlDomainIsUnavailable"; got != want {
		t.Fatalf("test unexpectedly wrapped in a hard-coded subtest: name=%q, want %q", got, want)
	}
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, uninstallCmd)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 506 }
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	callIndex := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		callIndex++
		if callIndex == 1 {
			return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
		}
		return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
	}

	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err == nil || output.ExitCode(err) != output.ExitError || !strings.Contains(err.Error(), "Could not find domain") || !strings.Contains(err.Error(), "tslink uninstall --force") {
		t.Fatalf("uninstall error = %v, want domain-unavailable failure", err)
	}
	if _, statErr := os.Stat(plistPath); statErr != nil {
		t.Fatalf("plist should remain after unavailable domain: %v", statErr)
	}

	callIndex = 0
	if err := uninstallCmd.Flags().Set("force", "true"); err != nil {
		t.Fatalf("set uninstall --force: %v", err)
	}
	var stderr bytes.Buffer
	uninstallCmd.SetErr(&stderr)
	t.Cleanup(func() { uninstallCmd.SetErr(nil) })
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall --force error = %v, want recovery from unavailable-plus-absent state", err)
	}
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Fatalf("plist stat after forced recovery = %v, want not-exist", statErr)
	}
	if !strings.Contains(stderr.String(), "--force removed") || !strings.Contains(stderr.String(), "job may still be loaded") {
		t.Fatalf("forced recovery stderr = %q, want explicit residual risk", stderr.String())
	}
}

func TestUninstallCommandRequiresForceWhenOtherDomainIsUnavailable(t *testing.T) {
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, uninstallCmd)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 506 }
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	callIndex := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		callIndex++
		if callIndex == 1 {
			return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
		}
		return []byte("bootout ok"), nil
	}

	if err := uninstallCmd.RunE(uninstallCmd, nil); err == nil || !strings.Contains(err.Error(), "tslink uninstall --force") {
		t.Fatalf("default uninstall error = %v, want explicit --force remedy", err)
	}
	if _, statErr := os.Stat(plistPath); statErr != nil {
		t.Fatalf("plist stat after conservative refusal = %v, want retained", statErr)
	}

	callIndex = 0
	if err := uninstallCmd.Flags().Set("force", "true"); err != nil {
		t.Fatalf("set uninstall --force: %v", err)
	}
	var stderr bytes.Buffer
	uninstallCmd.SetErr(&stderr)
	t.Cleanup(func() { uninstallCmd.SetErr(nil) })
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall --force error = %v, want explicit recovery", err)
	}
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Fatalf("plist stat after forced removal = %v, want not-exist", statErr)
	}
	for _, want := range []string{"--force removed", "job may still be loaded", "no desktop session exists for this user"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("forced uninstall stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestUninstallThenInstallCannotCreateSecondDaemonWithoutForce(t *testing.T) {
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, uninstallCmd)
	resetCommandLocalFlags(t, installCmd)
	stubDarwinLaunchAgentVerificationNoWait(t)
	home := t.TempDir()
	testenv.SetHome(t, home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldArtifactConflict := installDaemonArtifactConflictFn
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		installDaemonArtifactConflictFn = oldArtifactConflict
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink.app/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }
	installDaemonArtifactConflictFn = func() error { return nil }
	pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
	isRunningFn = func(string) bool { return false }
	readPIDFn = func(string) (int, error) { return 0, nil }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("old plist"), 0o600); err != nil {
		t.Fatalf("WriteFile(old plist) error = %v", err)
	}

	guiLoaded := true
	userLoaded := true
	var calls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		isGUI := len(args) >= 2 && strings.HasPrefix(args[1], "gui/")
		if isGUI {
			return []byte("Could not find domain for: " + args[1]), errors.New("exit status 112")
		}
		switch args[0] {
		case "bootout":
			if userLoaded {
				userLoaded = false
				return nil, nil
			}
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
		case "bootstrap":
			userLoaded = true
			return nil, nil
		case "print":
			if userLoaded {
				return runningLaunchAgentState(), nil
			}
			return []byte("Could not find service " + args[1]), errors.New("exit status 113")
		default:
			t.Fatalf("unexpected launchctl call: %q", args)
			return nil, nil
		}
	}

	uninstallErr := uninstallCmd.RunE(uninstallCmd, nil)
	if uninstallErr == nil || !strings.Contains(uninstallErr.Error(), "tslink uninstall --force") {
		t.Fatalf("default uninstall = %v, want conservative refusal with escape hatch", uninstallErr)
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist missing after conservative uninstall refusal: %v", err)
	}
	if userLoaded {
		t.Fatal("addressable user-domain job still loaded after uninstall attempt")
	}

	installErr := installCmd.RunE(installCmd, nil)
	if installErr == nil || !strings.Contains(installErr.Error(), "tslink install --force") {
		t.Fatalf("default install = %v, want conservative refusal with escape hatch", installErr)
	}
	if !guiLoaded || userLoaded {
		t.Fatalf("default handoff created second daemon: gui=%v user=%v calls=%q", guiLoaded, userLoaded, calls)
	}
	if _, err := os.Stat(plistPath); err != nil {
		t.Fatalf("plist missing after refused install handoff: %v", err)
	}
	for _, call := range calls {
		if strings.HasPrefix(call, "bootstrap ") {
			t.Fatalf("default chain reached unsafe bootstrap: calls=%q", calls)
		}
	}
}

func TestBootoutLaunchAgentRetainsFirstOfTwoDifferentRealErrors(t *testing.T) {
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userUIDFn = func() int { return 507 }
	firstErr := errors.New("gui denied")
	secondErr := errors.New("user I/O failure")
	callIndex := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		callIndex++
		if callIndex == 1 {
			return []byte("gui permission denied"), firstErr
		}
		return []byte("user input/output error"), secondErr
	}

	result := bootoutLaunchAgent(false)
	if !errors.Is(result.Err, firstErr) {
		t.Fatalf("bootout error = %v, want first error %v", result.Err, firstErr)
	}
	if result.Target != "gui/507/"+plistLabel {
		t.Fatalf("bootout target = %q, want first real-error target", result.Target)
	}
	for _, want := range []string{"gui permission denied", "user input/output error"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("bootout output = %q, want %q", result.Output, want)
		}
	}
}

func TestUninstallForceDoesNotOverrideRealLaunchctlError(t *testing.T) {
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userUIDFn = func() int { return 507 }
	callIndex := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		callIndex++
		if callIndex == 1 {
			return []byte("Boot-out failed: 1: Operation not permitted"), errors.New("exit status 1")
		}
		return []byte("bootout ok"), nil
	}

	result := bootoutLaunchAgent(true)
	if result.Err == nil || result.Outcome != launchctlOutcomeUnconfirmed || result.Target != "gui/507/"+plistLabel {
		t.Fatalf("forced bootout result = %+v, want real error to remain fatal", result)
	}
}

func TestUninstallCommandBootoutsLaunchAgentOnSuccess(t *testing.T) {
	resetRootJSONFlag(t)
	t.Cleanup(func() { uninstallCmd.SetOut(nil) })

	home := t.TempDir()

	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 504 }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootout ok"), nil
	}

	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/504/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/504/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "LaunchAgent removed") {
		t.Fatalf("uninstall output missing success: %s", out.String())
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, stat error = %v", err)
	}
}

func TestUninstallCommandBootoutsLaunchAgentAndSurfacesOutput(t *testing.T) {
	resetRootJSONFlag(t)
	t.Cleanup(func() { uninstallCmd.SetOut(nil) })

	home := t.TempDir()

	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 502 }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootout stderr"), errors.New("bootout failed")
	}

	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err == nil || output.ExitCode(err) != output.ExitError {
		t.Fatalf("uninstall RunE() error = %v (exit %d), want non-zero bootout failure", err, output.ExitCode(err))
	}

	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/502/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/502/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	for _, want := range []string{"bootout stderr", "plist was kept", "retry 'tslink uninstall'"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("uninstall error = %q, want %q", err, want)
		}
	}
	if strings.Contains(out.String(), "LaunchAgent removed") {
		t.Fatalf("uninstall output falsely claimed removal: %s", out.String())
	}
	if _, statErr := os.Stat(plistPath); statErr != nil {
		t.Fatalf("plist should be retained after bootout failure, stat error = %v", statErr)
	}
}

func TestUninstallCommandJSONReportsRemovedFalseWhenBootoutFails(t *testing.T) {
	resetRootJSONFlag(t)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 502 }
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		return []byte("bootout stderr"), errors.New("bootout failed")
	}
	setRootJSONFlag(t, true)

	var runErr error
	got := captureStdout(t, func() {
		runErr = uninstallCmd.RunE(uninstallCmd, nil)
	})
	if !output.IsSilent(runErr) || output.ExitCode(runErr) != output.ExitError {
		t.Fatalf("uninstall JSON error = %v (exit %d), want silent non-zero", runErr, output.ExitCode(runErr))
	}
	result := parseResult(t, got)
	if result.OK || result.Code != output.ExitError {
		t.Fatalf("uninstall JSON result = %#v, want failure", result)
	}
	data := dataMap(t, got)
	if data["removed"] != false || data["plist_path"] != plistPath {
		t.Fatalf("uninstall JSON data = %#v, want removed=false and retained path", data)
	}
	if _, ok := data["warning"]; ok {
		t.Fatalf("failed uninstall JSON overloaded non-fatal warning field: %#v", data)
	}
	if _, statErr := os.Stat(plistPath); statErr != nil {
		t.Fatalf("plist should be retained after JSON bootout failure: %v", statErr)
	}
}

func TestUninstallCommandRemovesPlistWhenLaunchAgentTargetsAreAlreadyAbsent(t *testing.T) {
	resetRootJSONFlag(t)
	home := t.TempDir()
	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 502 }
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("orphaned plist"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var calls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, "\x00"))
		return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
	}

	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall RunE() error = %v, want absent jobs treated as success", err)
	}
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/502/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/502/" + plistLabel}, "\x00"),
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want both domains %q", calls, wantCalls)
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("orphaned plist still exists after absent-job uninstall: %v", err)
	}
}

func TestBootoutLaunchAgentDoesNotHideRealErrorBehindAbsentTarget(t *testing.T) {
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})
	userUIDFn = func() int { return 502 }

	for _, realErrorIndex := range []int{0, 1} {
		t.Run(fmt.Sprintf("real-error-target-%d", realErrorIndex), func(t *testing.T) {
			callIndex := 0
			launchctlCombinedOutput = func(args ...string) ([]byte, error) {
				current := callIndex
				callIndex++
				if current == realErrorIndex {
					return []byte("permission denied"), errors.New("exit status 1")
				}
				return []byte("Boot-out failed: 3: No such process"), errors.New("exit status 3")
			}

			result := bootoutLaunchAgent(false)
			if result.Err == nil || !strings.Contains(result.Output, "permission denied") {
				t.Fatalf("bootout result = %+v, want real error preserved", result)
			}
			if callIndex != 2 {
				t.Fatalf("bootout calls = %d, want both domains checked", callIndex)
			}
		})
	}
}

func TestLaunchAgentShutdownTimeoutPolicy(t *testing.T) {
	if launchAgentShutdownTimeout != 3*time.Minute {
		t.Fatalf("launchAgentShutdownTimeout = %s, want 3m", launchAgentShutdownTimeout)
	}
}

func TestLaunchAgentTargetForRunningDaemonRequiresRunningStateAndPositivePID(t *testing.T) {
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		launchctlCombinedOutput = oldLaunchctl
	})
	pidPathFn = func() (string, error) { return "/tmp/tslink-test.pid", nil }
	isRunningFn = func(string) bool { return true }

	t.Run("non-positive daemon pid", func(t *testing.T) {
		readPIDFn = func(string) (int, error) { return 0, nil }
		launchctlCombinedOutput = func(args ...string) ([]byte, error) {
			t.Fatalf("launchctl called with non-positive daemon PID: %v", args)
			return nil, nil
		}
		if _, _, owned := launchAgentTargetForRunningDaemon(); owned {
			t.Fatal("launchAgentTargetForRunningDaemon() owned=true for PID 0")
		}
	})

	t.Run("matching stale pid without running state", func(t *testing.T) {
		readPIDFn = func(string) (int, error) { return 1775, nil }
		launchctlCombinedOutput = func(args ...string) ([]byte, error) {
			return []byte("state = waiting\npid = 1775\n"), nil
		}
		if _, _, owned := launchAgentTargetForRunningDaemon(); owned {
			t.Fatal("launchAgentTargetForRunningDaemon() owned=true for waiting state")
		}
	})
}
