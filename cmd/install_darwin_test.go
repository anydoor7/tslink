//go:build darwin

package cmd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
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
	launchAgentVerifyTimeout = 0
	launchAgentVerifyPollInterval = 0
	t.Cleanup(func() {
		launchAgentVerifyTimeout = oldTimeout
		launchAgentVerifyPollInterval = oldPollInterval
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
	} {
		if !strings.Contains(installCmd.Long, want) {
			t.Fatalf("install help missing %q:\n%s", want, installCmd.Long)
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

	var parsed struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("rendered plist is not well-formed XML: %v\n%s", err, buf.String())
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

func TestInstallCommandBootoutThenBootstrapsLaunchAgentOnSuccess(t *testing.T) {
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
	userUIDFn = func() int { return 503 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		if len(args) >= 2 && args[0] == "bootstrap" && args[1] == "gui/503" {
			return []byte("Bootstrap failed: 113: Domain does not exist"), errors.New("bootstrap failed")
		}
		if len(args) > 0 && args[0] == "print" {
			return runningLaunchAgentState(), nil
		}
		return []byte("user bootstrap ok"), nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
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
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	for _, want := range []string{"SSH/headless", "user/503", "installed and loaded in user/503"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("install output = %q, want %q", out.String(), want)
		}
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
		case 8:
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
	if len(launchctlCalls) != 8 {
		t.Fatalf("launchctl calls = %q, want exact eight-step ownership/handoff/restore sequence", launchctlCalls)
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
	if printCalls != 3 {
		t.Fatalf("launchctl print calls = %d, want 3", printCalls)
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

			result := bootoutLaunchAgent()
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
