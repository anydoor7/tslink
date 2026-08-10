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

func runDarwinInstallGuardTruthCase(t *testing.T, plistPresent, daemonRunning bool) {
	t.Helper()
	resetRootJSONFlag(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldConflict := installDaemonConflictFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		installDaemonConflictFn = oldConflict
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

	conflictCalls := 0
	installDaemonConflictFn = func() error {
		conflictCalls++
		if daemonRunning {
			return output.ErrConflict("manual TSLink daemon is running")
		}
		return nil
	}
	launchctlCalls := 0
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		launchctlCalls++
		if len(args) > 0 && args[0] == "print" {
			return runningLaunchAgentState(), nil
		}
		return nil, nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if !plistPresent && daemonRunning {
		if output.ExitCode(err) != output.ExitConflict {
			t.Fatalf("ExitCode = %d, want %d: %v", output.ExitCode(err), output.ExitConflict, err)
		}
		if conflictCalls != 1 || launchctlCalls != 0 {
			t.Fatalf("conflict/launchctl calls = %d/%d, want 1/0", conflictCalls, launchctlCalls)
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("plist exists after conflict: %v", statErr)
		}
		return
	}

	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}
	wantConflictCalls := 1
	if plistPresent {
		wantConflictCalls = 0
	}
	if conflictCalls != wantConflictCalls {
		t.Fatalf("conflict calls = %d, want %d", conflictCalls, wantConflictCalls)
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
}

func TestDarwinInstallNoPlistDaemonStoppedProceeds(t *testing.T) {
	runDarwinInstallGuardTruthCase(t, false, false)
}

func TestDarwinInstallNoPlistDaemonRunningConflicts(t *testing.T) {
	runDarwinInstallGuardTruthCase(t, false, true)
}

func TestDarwinInstallExistingPlistDaemonStoppedReinstalls(t *testing.T) {
	runDarwinInstallGuardTruthCase(t, true, false)
}

func TestDarwinInstallExistingPlistDaemonRunningReinstalls(t *testing.T) {
	runDarwinInstallGuardTruthCase(t, true, true)
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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", home)

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
	stubDarwinInstallDaemonStopped(t)
	stubDarwinLaunchAgentVerificationNoWait(t)
	resetRootJSONFlag(t)
	t.Cleanup(func() { installCmd.SetOut(nil) })
	home := t.TempDir()
	t.Setenv("HOME", home)

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
	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if _, statErr := os.Stat(plistPath); !os.IsNotExist(statErr) {
		t.Fatalf("plist remains after verification rollback: %v", statErr)
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("install error = %q, want rollback confirmation", err)
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

	wantCalls := []string{strings.Join([]string{"bootout", "gui/504/" + plistLabel}, "\x00")}
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
	if err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/502/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/502/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "bootout stderr") {
		t.Fatalf("uninstall output did not surface launchctl output: %s", out.String())
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, stat error = %v", err)
	}
}
