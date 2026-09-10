//go:build linux

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
)

var _ func(string, bool) string = systemdServiceContents

func stubLinuxInstallDaemonStopped(t *testing.T) {
	t.Helper()
	stubFastSystemdSettle(t)
	oldConflict := installDaemonConflictFn
	installDaemonConflictFn = func() error { return nil }
	t.Cleanup(func() { installDaemonConflictFn = oldConflict })
}

func stubFastSystemdSettle(t *testing.T) {
	t.Helper()
	oldTimeout := systemdSettleTimeout
	oldInterval := systemdSettleInterval
	oldStable := systemdStableWindow
	systemdSettleTimeout = 10 * time.Millisecond
	systemdSettleInterval = time.Millisecond
	systemdStableWindow = 0
	t.Cleanup(func() {
		systemdSettleTimeout = oldTimeout
		systemdSettleInterval = oldInterval
		systemdStableWindow = oldStable
	})
}

func runningSystemdState() []byte {
	return []byte("ActiveState=active\nSubState=running\nMainPID=1775\nNRestarts=0\n")
}

func stubSystemdStateSequence(t *testing.T, states ...[]byte) *int {
	t.Helper()
	stubFastSystemdSettle(t)
	oldSystemctl := systemctlCombinedOutput
	calls := 0
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		want := []string{
			"--user",
			"show",
			systemdServiceName,
			"--property=ActiveState",
			"--property=SubState",
			"--property=MainPID",
			"--property=NRestarts",
			"--no-pager",
		}
		if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("systemctl args = %q, want %q", args, want)
		}
		index := calls
		if index >= len(states) {
			index = len(states) - 1
		}
		calls++
		return states[index], nil
	}
	t.Cleanup(func() { systemctlCombinedOutput = oldSystemctl })
	return &calls
}

func runLinuxInstallGuardTruthCase(t *testing.T, unitPresent, daemonRunning bool, systemdPID int) bool {
	t.Helper()
	stubFastSystemdSettle(t)
	resetRootJSONFlag(t)
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
		installCmd.SetOut(nil)
		installCmd.SetErr(nil)
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/new/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return []byte("yes\n"), nil }

	path := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if unitPresent {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte("old unit"), 0o644); err != nil {
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
	systemctlCalls := 0
	restartCalls := 0
	mutatingSystemctlCalls := 0
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		systemctlCalls++
		if len(args) > 1 && args[1] == "show" {
			pid := systemdPID
			if pid <= 0 {
				pid = 1775
			}
			return []byte(fmt.Sprintf("ActiveState=active\nSubState=running\nMainPID=%d\nNRestarts=0\n", pid)), nil
		}
		if len(args) > 1 && args[1] == "restart" {
			restartCalls++
		}
		if len(args) > 1 && args[1] != "show" {
			mutatingSystemctlCalls++
		}
		return nil, nil
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	installCmd.SetOut(&out)
	installCmd.SetErr(&errOut)
	err := installCmd.RunE(installCmd, nil)
	expectConflict := daemonRunning && (!unitPresent || systemdPID != 1775)
	if expectConflict {
		if output.ExitCode(err) != output.ExitConflict {
			t.Fatalf("ExitCode = %d, want %d: %v", output.ExitCode(err), output.ExitConflict, err)
		}
		if restartCalls != 0 {
			t.Fatalf("restart calls = %d, want 0 before daemon conflict", restartCalls)
		}
		if mutatingSystemctlCalls != 0 {
			t.Fatalf("mutating systemctl calls = %d, want 0 before daemon conflict", mutatingSystemctlCalls)
		}
		if unitPresent {
			for _, want := range []string{"a systemd user unit is installed", "could not confirm that systemd owns", "tslink stop", "keep the existing unit installed"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("artifact-present conflict = %q, want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "no systemd user unit is installed") {
				t.Fatalf("artifact-present conflict falsely claims no unit: %q", err)
			}
			unit, readErr := os.ReadFile(path)
			if readErr != nil || string(unit) != "old unit" {
				t.Fatalf("existing unit changed during conflict: %q, %v", unit, readErr)
			}
		} else if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("unit exists after conflict: %v", statErr)
		}
		return observedDaemonRunning
	}

	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}
	if systemctlCalls == 0 {
		t.Fatal("systemctl was not called for successful install")
	}
	unit, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile(unit) error = %v", readErr)
	}
	if !strings.Contains(string(unit), "/new/tslink") {
		t.Fatalf("unit was not refreshed to new executable: %s", unit)
	}
	if info, statErr := os.Stat(path); statErr != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("installed unit mode = %v, %v; want 0600", info, statErr)
	}
	return observedDaemonRunning
}

func TestLinuxInstallNoUnitDaemonStoppedProceeds(t *testing.T) {
	if got := runLinuxInstallGuardTruthCase(t, false, false, 0); got {
		t.Fatal("daemon-running seam observed true, want false")
	}
}

func TestLinuxInstallNoUnitDaemonRunningConflicts(t *testing.T) {
	if got := runLinuxInstallGuardTruthCase(t, false, true, 0); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestLinuxInstallExistingUnitDaemonStoppedReinstalls(t *testing.T) {
	if got := runLinuxInstallGuardTruthCase(t, true, false, 0); got {
		t.Fatal("daemon-running seam observed true, want false")
	}
}

func TestLinuxInstallExistingUnitDaemonRunningReinstalls(t *testing.T) {
	if got := runLinuxInstallGuardTruthCase(t, true, true, 1775); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestLinuxInstallExistingUnitManualDaemonPIDMismatchConflicts(t *testing.T) {
	if got := runLinuxInstallGuardTruthCase(t, true, true, 1888); !got {
		t.Fatal("daemon-running seam observed false, want true")
	}
}

func TestSystemdServiceContentsThrottlesRestart(t *testing.T) {
	unit := systemdServiceContents("/usr/local/bin/tslink", false)
	for _, want := range []string{
		"StartLimitIntervalSec=300",
		"StartLimitBurst=5",
		`ExecStart="/usr/local/bin/tslink" serve`,
		"Restart=on-failure",
		"RestartSec=30",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

func TestSystemdServiceContentsCarriesNoAutoProvision(t *testing.T) {
	unit := systemdServiceContents("/usr/local/bin/tslink", true)
	if count := strings.Count(unit, " --no-auto-provision"); count != 1 {
		t.Fatalf("kill-switch arg count = %d, want 1:\n%s", count, unit)
	}
}

func TestSystemdServiceContentsQuotesExecutableWithSpaces(t *testing.T) {
	unit := systemdServiceContents("/opt/My App/tslink", false)
	if !strings.Contains(unit, `ExecStart="/opt/My App/tslink" serve`) {
		t.Fatalf("unit did not quote executable path with spaces:\n%s", unit)
	}
}

func TestSystemdServiceContentsEscapesSystemdSpecials(t *testing.T) {
	unit := systemdServiceContents(`/opt/100%/$build "TSLink"\tslink`, false)
	if !strings.Contains(unit, `ExecStart="/opt/100%%/$$build \"TSLink\"\\tslink" serve`) {
		t.Fatalf("unit did not escape systemd executable path:\n%s", unit)
	}
}

func TestLinuxInstallCommandRunsSystemctlAndWarnsAboutLinger(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/My App/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) {
		return []byte("no\n"), nil
	}

	var systemctlCalls []string
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		systemctlCalls = append(systemctlCalls, strings.Join(args, "\x00"))
		if len(args) > 1 && args[1] == "show" {
			return runningSystemdState(), nil
		}
		return nil, nil
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	installCmd.SetOut(&out)
	installCmd.SetErr(&errOut)
	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	wantCalls := []string{
		strings.Join([]string{"--user", "daemon-reload"}, "\x00"),
		strings.Join([]string{"--user", "enable", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "reset-failed", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "restart", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "show", systemdServiceName, "--property=ActiveState", "--property=SubState", "--property=MainPID", "--property=NRestarts", "--no-pager"}, "\x00"),
		strings.Join([]string{"--user", "show", systemdServiceName, "--property=ActiveState", "--property=SubState", "--property=MainPID", "--property=NRestarts", "--no-pager"}, "\x00"),
	}
	if strings.Join(systemctlCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("systemctl calls = %q, want %q", systemctlCalls, wantCalls)
	}
	if !strings.Contains(errOut.String(), `loginctl enable-linger "$USER"`) {
		t.Fatalf("linger warning missing guidance: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), `loginctl disable-linger "$USER"`) {
		t.Fatalf("linger warning missing uninstall guidance: %s", errOut.String())
	}

	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	unit, readErr := os.ReadFile(servicePath)
	if readErr != nil {
		t.Fatalf("read generated service: %v", readErr)
	}
	if !strings.Contains(string(unit), "StartLimitBurst=5") {
		t.Fatalf("unit missing StartLimitBurst:\n%s", unit)
	}
	if !strings.Contains(string(unit), `ExecStart="/opt/My App/tslink" serve`) {
		t.Fatalf("unit missing quoted ExecStart:\n%s", unit)
	}
}

func TestLinuxInstallJSONEnvelope(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return []byte("yes\n"), nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show" {
			return runningSystemdState(), nil
		}
		return nil, nil
	}
	setRootJSONFlag(t, true)

	got := captureStdout(t, func() {
		if err := installCmd.RunE(installCmd, nil); err != nil {
			t.Fatalf("install RunE() error = %v", err)
		}
	})
	res := parseResult(t, got)
	if !res.OK || res.Command != "install" {
		t.Fatalf("install JSON result = %#v", res)
	}
	data := dataMap(t, got)
	if data["installed"] != true || data["started"] != true || data["service_manager"] != systemdServiceName {
		t.Fatalf("install data = %#v, want installed/started/systemd service manager", data)
	}
	if _, ok := data["warning"]; ok {
		t.Fatalf("install data unexpectedly included warning: %#v", data)
	}
}

func TestLinuxInstallSurfacesSystemctlOutput(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		return []byte("systemctl stderr"), errors.New("systemctl failed")
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil {
		t.Fatal("install RunE() error = nil, want systemctl error")
	}
	if !strings.Contains(err.Error(), "systemctl stderr") {
		t.Fatalf("systemctl output not surfaced: %v", err)
	}
}

func TestLinuxInstallRefusesRunningDaemonBeforeWritingUnit(t *testing.T) {
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return 1676, nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		t.Fatalf("systemctl called during daemon conflict: %v", args)
		return nil, nil
	}

	err := installCmd.RunE(installCmd, nil)
	if output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "pid 1676") || !strings.Contains(err.Error(), "tslink stop") || !strings.Contains(err.Error(), "no systemd user unit") {
		t.Fatalf("install conflict = %v (exit %d)", err, output.ExitCode(err))
	}
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if _, statErr := os.Stat(servicePath); !os.IsNotExist(statErr) {
		t.Fatalf("service file exists after conflict: %v", statErr)
	}
}

func TestLinuxInstallDoesNotClaimSuccessWhenServiceIsAutoRestarting(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show" {
			return []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\n"), nil
		}
		return nil, nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "auto-restart") || !strings.Contains(err.Error(), "journalctl") {
		t.Fatalf("install RunE() error = %v, want actionable post-install state failure", err)
	}
	if strings.Contains(out.String(), "✓") {
		t.Fatalf("install printed success for auto-restart state: %s", out.String())
	}
}

func TestLinuxInstallDoesNotClaimSuccessWhenServiceDiesDuringSettlement(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return []byte("yes\n"), nil }
	showCalls := 0
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show" {
			showCalls++
			if showCalls == 1 {
				return []byte("ActiveState=active\nSubState=running\nMainPID=100\nNRestarts=0\n"), nil
			}
			return []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=1\n"), nil
		}
		return nil, nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "auto-restart") {
		t.Fatalf("install RunE() error = %v, want active/running to auto-restart settlement failure", err)
	}
	if showCalls != 2 {
		t.Fatalf("verify show calls = %d, want 2 sequential samples", showCalls)
	}
	if strings.Contains(out.String(), "✓") {
		t.Fatalf("install printed success after service died during settlement: %s", out.String())
	}
}

func TestVerifySystemdServiceRunningSettlement(t *testing.T) {
	t.Run("main pid drift fails", func(t *testing.T) {
		calls := stubSystemdStateSequence(t,
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\nNRestarts=0\n"),
			[]byte("ActiveState=active\nSubState=running\nMainPID=200\nNRestarts=0\n"),
		)
		_, err := verifySystemdServiceRunning()
		if err == nil || !strings.Contains(err.Error(), `MainPID="200"`) {
			t.Fatalf("verify error = %v, want PID drift failure with last observation", err)
		}
		if *calls < 2 {
			t.Fatalf("verify show calls = %d, want at least 2", *calls)
		}
	})

	t.Run("nrestarts increase fails with reset guidance", func(t *testing.T) {
		stubSystemdStateSequence(t,
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\nNRestarts=0\n"),
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\nNRestarts=1\n"),
		)
		_, err := verifySystemdServiceRunning()
		if err == nil || !strings.Contains(err.Error(), `NRestarts="1"`) || !strings.Contains(err.Error(), "systemctl --user reset-failed tslink.service") {
			t.Fatalf("verify error = %v, want restart growth failure with reset-failed guidance", err)
		}
	})

	t.Run("two stable healthy samples succeed after one interval", func(t *testing.T) {
		calls := stubSystemdStateSequence(t, runningSystemdState(), runningSystemdState())
		started := time.Now()
		degraded, err := verifySystemdServiceRunning()
		if err != nil {
			t.Fatalf("verify error = %v, want stable success", err)
		}
		if degraded {
			t.Fatalf("verify reported degraded = true, want false when NRestarts is present")
		}
		elapsed := time.Since(started)
		if *calls != 2 {
			t.Fatalf("verify show calls = %d, want 2", *calls)
		}
		if elapsed < systemdSettleInterval || elapsed >= systemdSettleTimeout {
			t.Fatalf("verify elapsed = %v, want one interval (%v) and less than timeout (%v)", elapsed, systemdSettleInterval, systemdSettleTimeout)
		}
	})

	t.Run("initial auto-restart fails immediately", func(t *testing.T) {
		calls := stubSystemdStateSequence(t, []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=1\n"))
		_, err := verifySystemdServiceRunning()
		if err == nil || !strings.Contains(err.Error(), "auto-restart") {
			t.Fatalf("verify error = %v, want auto-restart failure", err)
		}
		if *calls != 1 {
			t.Fatalf("verify show calls = %d, want immediate single-sample failure", *calls)
		}
	})

	t.Run("initial failed state fails immediately", func(t *testing.T) {
		calls := stubSystemdStateSequence(t, []byte("ActiveState=failed\nSubState=failed\nMainPID=0\nNRestarts=5\n"))
		_, err := verifySystemdServiceRunning()
		if err == nil || !strings.Contains(err.Error(), `ActiveState="failed"`) || !strings.Contains(err.Error(), "systemctl --user reset-failed tslink.service") {
			t.Fatalf("verify error = %v, want failed-state error with reset-failed guidance", err)
		}
		if *calls != 1 {
			t.Fatalf("verify show calls = %d, want immediate single-sample failure", *calls)
		}
	})

	t.Run("activating throughout times out with last observation", func(t *testing.T) {
		calls := stubSystemdStateSequence(t,
			[]byte("ActiveState=activating\nSubState=start\nMainPID=0\nNRestarts=3\n"),
			[]byte("ActiveState=activating\nSubState=start-post\nMainPID=321\nNRestarts=3\n"),
		)
		_, err := verifySystemdServiceRunning()
		for _, want := range []string{`ActiveState="activating"`, `SubState="start-post"`, `MainPID="321"`, `NRestarts="3"`} {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("verify error = %v, want timeout containing %s", err, want)
			}
		}
		if *calls < 2 {
			t.Fatalf("verify show calls = %d, want multiple samples through timeout", *calls)
		}
	})

	t.Run("missing nrestarts degrades to healthy state checks", func(t *testing.T) {
		calls := stubSystemdStateSequence(t,
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\n"),
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\n"),
		)
		degraded, err := verifySystemdServiceRunning()
		if err != nil {
			t.Fatalf("verify error = %v, want success without NRestarts", err)
		}
		// Degrading is correct; degrading silently is not. The success path is the
		// only one where the operator would otherwise never learn that one of the
		// four criteria was unavailable.
		if !degraded {
			t.Fatalf("verify reported degraded = false, want true so the caller can warn")
		}
		if systemdVerifyDegradedWarning(degraded) == "" {
			t.Fatalf("degraded verification produced no operator-visible warning")
		}
		if *calls != 2 {
			t.Fatalf("verify show calls = %d, want 2", *calls)
		}
	})

	// The state real systemd was actually observed in during the crash loop that
	// motivated this settle window: activating/auto-restart with MainPID=0 and
	// NRestarts still at 0. The first revision of the reset-failed predicate was
	// `failed || nRestartsIncreased`, so this exact state -- the only one that has
	// ever occurred in practice -- was the one that got no recovery step.
	t.Run("observed crash-loop shape carries reset-failed guidance", func(t *testing.T) {
		stubSystemdStateSequence(t, []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=0\n"))
		_, err := verifySystemdServiceRunning()
		if err == nil {
			t.Fatalf("verify error = nil, want auto-restart failure")
		}
		for _, want := range []string{`SubState="auto-restart"`, `NRestarts="0"`, "systemctl --user reset-failed tslink.service"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("verify error = %v, want it to contain %s", err, want)
			}
		}
	})

	// MainPID drift means the unit restarted inside the window, which is on the
	// same path to the start-limit lockout as a rising NRestarts, so it earns the
	// same guidance.
	t.Run("pid drift timeout carries reset-failed guidance", func(t *testing.T) {
		stubSystemdStateSequence(t,
			[]byte("ActiveState=active\nSubState=running\nMainPID=100\nNRestarts=0\n"),
			[]byte("ActiveState=active\nSubState=running\nMainPID=200\nNRestarts=0\n"),
		)
		_, err := verifySystemdServiceRunning()
		if err == nil || !strings.Contains(err.Error(), "systemctl --user reset-failed tslink.service") {
			t.Fatalf("verify error = %v, want PID drift failure with reset-failed guidance", err)
		}
	})
}

func TestJoinInstallWarningsKeepsOnlyPresentClauses(t *testing.T) {
	if got := joinInstallWarnings("", ""); got != "" {
		t.Fatalf("joinInstallWarnings(empty) = %q, want empty", got)
	}
	if got := joinInstallWarnings("", "only"); got != "only" {
		t.Fatalf("joinInstallWarnings = %q, want %q", got, "only")
	}
	if got := joinInstallWarnings("first", "second"); got != "first; second" {
		t.Fatalf("joinInstallWarnings = %q, want %q", got, "first; second")
	}
}

// A degraded verification is still a success, so the only way the operator finds
// out that one of the four criteria was unavailable is the warning field. This
// asserts it survives the whole install path rather than only the helper.
func TestLinuxInstallSurfacesDegradedVerificationInWarning(t *testing.T) {
	stubFastSystemdSettle(t)
	stubLinuxInstallDaemonStopped(t)
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	// Linger enabled, so the only warning available is the degradation notice.
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return []byte("yes\n"), nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show" {
			// Healthy, but this systemd does not expose NRestarts.
			return []byte("ActiveState=active\nSubState=running\nMainPID=4242\n"), nil
		}
		return nil, nil
	}
	setRootJSONFlag(t, true)

	got := captureStdout(t, func() {
		if err := installCmd.RunE(installCmd, nil); err != nil {
			t.Fatalf("install RunE() error = %v, want success on degraded verification", err)
		}
	})
	res := parseResult(t, got)
	if !res.OK {
		t.Fatalf("install JSON result = %#v, want ok", res)
	}
	data := dataMap(t, got)
	if data["installed"] != true || data["started"] != true {
		t.Fatalf("install data = %#v, want installed/started", data)
	}
	warning, ok := data["warning"].(string)
	if !ok || !strings.Contains(warning, "NRestarts was unavailable") {
		t.Fatalf("install warning = %#v, want the degraded-verification notice", data["warning"])
	}
}

func TestLinuxInstallDoesNotClaimRestoredServiceRestartedWhenItDiesDuringSettlement(t *testing.T) {
	stubFastSystemdSettle(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/new/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return 1775, nil }
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	oldUnit := []byte("[Service]\nExecStart=/old/tslink serve\n")
	if err := os.WriteFile(servicePath, oldUnit, 0o600); err != nil {
		t.Fatalf("WriteFile(old unit) error = %v", err)
	}

	daemonReloadCalls := 0
	verifyCalls := 0
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) < 2 {
			t.Fatalf("malformed systemctl call: %q", args)
		}
		switch args[1] {
		case "show":
			if len(args) == 5 {
				return []byte("MainPID=1775\n"), nil
			}
			verifyCalls++
			if verifyCalls == 1 {
				return []byte("ActiveState=active\nSubState=running\nMainPID=1775\nNRestarts=0\n"), nil
			}
			return []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=1\n"), nil
		case "daemon-reload":
			daemonReloadCalls++
			if daemonReloadCalls == 1 {
				return []byte("reload stderr"), errors.New("injected upgrade reload failure")
			}
		}
		return nil, nil
	}

	err := installCmd.RunE(installCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "prior managed service is not confirmed running") || !strings.Contains(err.Error(), "auto-restart") {
		t.Fatalf("install RunE() error = %v, want incomplete restoration after restored service dies", err)
	}
	if strings.Contains(err.Error(), "previous systemd user unit was restored and restarted") {
		t.Fatalf("install falsely claimed restored service restarted: %v", err)
	}
	if verifyCalls != 2 {
		t.Fatalf("restored-service verify calls = %d, want 2 sequential samples", verifyCalls)
	}
	gotUnit, readErr := os.ReadFile(servicePath)
	if readErr != nil || !bytes.Equal(gotUnit, oldUnit) {
		t.Fatalf("restored unit = %q, %v; want %q", gotUnit, readErr, oldUnit)
	}
}

func TestLinuxInstallRestoresPreviousUnitAfterUpgradeFailures(t *testing.T) {
	for _, failStage := range []string{"daemon-reload", "enable", "reset-failed", "restart", "verify"} {
		t.Run(failStage, func(t *testing.T) {
			stubFastSystemdSettle(t)
			resetRootJSONFlag(t)
			home := t.TempDir()
			oldHome := linuxUserHomeDirFn
			oldExe := linuxExecutablePathFn
			oldEval := linuxEvalSymlinksFn
			oldPIDPath := pidPathFn
			oldRunning := isRunningFn
			oldReadPID := readPIDFn
			oldSystemctl := systemctlCombinedOutput
			t.Cleanup(func() {
				linuxUserHomeDirFn = oldHome
				linuxExecutablePathFn = oldExe
				linuxEvalSymlinksFn = oldEval
				pidPathFn = oldPIDPath
				isRunningFn = oldRunning
				readPIDFn = oldReadPID
				systemctlCombinedOutput = oldSystemctl
			})

			linuxUserHomeDirFn = func() (string, error) { return home, nil }
			linuxExecutablePathFn = func() (string, error) { return "/new/tslink", nil }
			linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
			pidPathFn = func() (string, error) { return filepath.Join(home, "tslink.pid"), nil }
			isRunningFn = func(string) bool { return true }
			readPIDFn = func(string) (int, error) { return 1775, nil }

			servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
			if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			oldUnit := []byte("[Service]\nExecStart=/old/tslink serve\n")
			if err := os.WriteFile(servicePath, oldUnit, 0o644); err != nil {
				t.Fatalf("WriteFile(old unit) error = %v", err)
			}

			failed := false
			var calls []string
			systemctlCombinedOutput = func(args ...string) ([]byte, error) {
				call := strings.Join(args, "\x00")
				calls = append(calls, call)
				if len(args) < 2 {
					t.Fatalf("malformed systemctl call: %q", call)
				}
				op := args[1]
				if op == "show" {
					if len(args) == 5 {
						return []byte("MainPID=1775\n"), nil
					}
					if failStage == "verify" && !failed {
						failed = true
						return []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\n"), nil
					}
					return runningSystemdState(), nil
				}
				if op == failStage && !failed {
					failed = true
					return []byte(failStage + " stderr"), errors.New("injected " + failStage + " failure")
				}
				return nil, nil
			}

			err := installCmd.RunE(installCmd, nil)
			if err == nil || !failed || !strings.Contains(err.Error(), "previous systemd user unit was restored and restarted") {
				t.Fatalf("install RunE() error = %v, want %s failure plus restored/restarted policy", err, failStage)
			}
			gotUnit, readErr := os.ReadFile(servicePath)
			if readErr != nil || !bytes.Equal(gotUnit, oldUnit) {
				t.Fatalf("restored unit = %q, %v; want %q", gotUnit, readErr, oldUnit)
			}
			if info, statErr := os.Stat(servicePath); statErr != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("restored unit mode = %v, %v; want 0600", info, statErr)
			}
			ownershipShow := strings.Join([]string{"--user", "show", systemdServiceName, "--property=MainPID", "--no-pager"}, "\x00")
			verifyShow := strings.Join([]string{"--user", "show", systemdServiceName, "--property=ActiveState", "--property=SubState", "--property=MainPID", "--property=NRestarts", "--no-pager"}, "\x00")
			daemonReload := strings.Join([]string{"--user", "daemon-reload"}, "\x00")
			enable := strings.Join([]string{"--user", "enable", systemdServiceName}, "\x00")
			reset := strings.Join([]string{"--user", "reset-failed", systemdServiceName}, "\x00")
			restart := strings.Join([]string{"--user", "restart", systemdServiceName}, "\x00")
			stop := strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00")
			wantCalls := []string{ownershipShow, daemonReload}
			switch failStage {
			case "enable":
				wantCalls = append(wantCalls, enable)
			case "reset-failed":
				wantCalls = append(wantCalls, enable, reset)
			case "restart":
				wantCalls = append(wantCalls, enable, reset, restart)
			case "verify":
				wantCalls = append(wantCalls, enable, reset, restart, verifyShow)
			}
			wantCalls = append(wantCalls, stop, daemonReload, reset, restart, verifyShow, verifyShow)
			if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
				t.Fatalf("systemctl calls = %q, want exact forward/failure/restore sequence %q", calls, wantCalls)
			}
		})
	}
}

func TestRestorePreviousSystemdUnitReportsAccurateProgress(t *testing.T) {
	t.Run("write failure", func(t *testing.T) {
		home := t.TempDir()
		servicePath := filepath.Join(home, systemdServiceName)
		referent := filepath.Join(home, "protected.service")
		if err := os.WriteFile(referent, []byte("protected"), 0o600); err != nil {
			t.Fatalf("WriteFile(referent) error = %v", err)
		}
		if err := os.Symlink(referent, servicePath); err != nil {
			t.Fatalf("Symlink(service) error = %v", err)
		}
		oldSystemctl := systemctlCombinedOutput
		t.Cleanup(func() { systemctlCombinedOutput = oldSystemctl })
		var calls []string
		systemctlCombinedOutput = func(args ...string) ([]byte, error) {
			calls = append(calls, strings.Join(args, "\x00"))
			return nil, nil
		}

		result, err := restorePreviousSystemdUnit(
			systemdPreviousState{Existed: true, Unit: []byte("old unit"), Mode: 0o644, OwnedRunning: true},
			servicePath,
		)
		if err == nil || result.UnitRestored || result.Restarted {
			t.Fatalf("restore result = %+v, error = %v; want write failure with no success claims", result, err)
		}
		wantCalls := []string{strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00")}
		if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
			t.Fatalf("systemctl calls = %q, want stop only before write failure", calls)
		}
	})

	for _, tc := range []struct {
		name      string
		failOp    string
		wantCalls []string
	}{
		{
			name:   "daemon reload failure",
			failOp: "daemon-reload",
			wantCalls: []string{
				strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00"),
				strings.Join([]string{"--user", "daemon-reload"}, "\x00"),
			},
		},
		{
			name:   "restart failure",
			failOp: "restart",
			wantCalls: []string{
				strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00"),
				strings.Join([]string{"--user", "daemon-reload"}, "\x00"),
				strings.Join([]string{"--user", "reset-failed", systemdServiceName}, "\x00"),
				strings.Join([]string{"--user", "restart", systemdServiceName}, "\x00"),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servicePath := filepath.Join(t.TempDir(), systemdServiceName)
			if err := os.WriteFile(servicePath, []byte("new unit"), 0o600); err != nil {
				t.Fatalf("WriteFile(new unit) error = %v", err)
			}
			oldSystemctl := systemctlCombinedOutput
			t.Cleanup(func() { systemctlCombinedOutput = oldSystemctl })
			var calls []string
			systemctlCombinedOutput = func(args ...string) ([]byte, error) {
				call := strings.Join(args, "\x00")
				calls = append(calls, call)
				if len(args) > 1 && args[1] == tc.failOp {
					return []byte(tc.failOp + " stderr"), errors.New("injected " + tc.failOp + " failure")
				}
				return nil, nil
			}

			result, err := restorePreviousSystemdUnit(
				systemdPreviousState{Existed: true, Unit: []byte("old unit"), Mode: 0o644, OwnedRunning: true},
				servicePath,
			)
			if err == nil || !result.UnitRestored || result.Restarted {
				t.Fatalf("restore result = %+v, error = %v; want restored bytes, Restarted=false, and surfaced %s failure", result, err, tc.failOp)
			}
			if strings.Join(calls, "\n") != strings.Join(tc.wantCalls, "\n") {
				t.Fatalf("systemctl calls = %q, want %q", calls, tc.wantCalls)
			}
			got, readErr := os.ReadFile(servicePath)
			if readErr != nil || string(got) != "old unit" {
				t.Fatalf("restored unit = %q, %v; want old unit", got, readErr)
			}
			if info, statErr := os.Stat(servicePath); statErr != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("restored unit mode = %v, %v; want unsafe prior mode converged to 0600", info, statErr)
			}
		})
	}
}

func TestLinuxInstallSupportsSymlinkedSystemdUserDirectoryWithoutChangingMode(t *testing.T) {
	stubLinuxInstallDaemonStopped(t)
	resetRootJSONFlag(t)
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldExe := linuxExecutablePathFn
	oldEval := linuxEvalSymlinksFn
	oldUser := linuxUserNameFn
	oldSystemctl := systemctlCombinedOutput
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		linuxExecutablePathFn = oldExe
		linuxEvalSymlinksFn = oldEval
		linuxUserNameFn = oldUser
		systemctlCombinedOutput = oldSystemctl
		loginctlCombinedOutputFn = oldLoginctl
		installCmd.SetOut(nil)
		installCmd.SetErr(nil)
	})
	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	linuxExecutablePathFn = func() (string, error) { return "/opt/tslink", nil }
	linuxEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) { return []byte("yes\n"), nil }
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "show" {
			return runningSystemdState(), nil
		}
		return nil, nil
	}

	systemdDir := filepath.Join(home, ".config", "systemd")
	realDir := filepath.Join(home, "RelocatedSystemdUser")
	if err := os.MkdirAll(systemdDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(systemd) error = %v", err)
	}
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(real systemd user) error = %v", err)
	}
	if err := os.Chmod(realDir, 0o755); err != nil {
		t.Fatalf("Chmod(real systemd user) error = %v", err)
	}
	if err := os.Symlink(realDir, filepath.Join(systemdDir, "user")); err != nil {
		t.Fatalf("Symlink(systemd user) error = %v", err)
	}

	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("install RunE() error = %v, want symlinked systemd user directory support", err)
	}
	if info, err := os.Stat(realDir); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("shared systemd user dir mode = %v, %v; want unchanged 0755", info, err)
	}
	servicePath := filepath.Join(realDir, systemdServiceName)
	if info, err := os.Stat(servicePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("installed systemd unit mode = %v, %v; want 0600", info, err)
	}
}

func TestSystemdOwnsRunningDaemonRequiresPositivePIDAndMatchingMainPID(t *testing.T) {
	oldPIDPath := pidPathFn
	oldRunning := isRunningFn
	oldReadPID := readPIDFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		pidPathFn = oldPIDPath
		isRunningFn = oldRunning
		readPIDFn = oldReadPID
		systemctlCombinedOutput = oldSystemctl
	})
	pidPathFn = func() (string, error) { return "/tmp/tslink-test.pid", nil }
	isRunningFn = func(string) bool { return true }

	t.Run("non-positive daemon pid", func(t *testing.T) {
		readPIDFn = func(string) (int, error) { return 0, nil }
		systemctlCombinedOutput = func(args ...string) ([]byte, error) {
			t.Fatalf("systemctl called with non-positive daemon PID: %v", args)
			return nil, nil
		}
		if systemdOwnsRunningDaemon() {
			t.Fatal("systemdOwnsRunningDaemon() = true for PID 0")
		}
	})

	t.Run("mismatched main pid", func(t *testing.T) {
		readPIDFn = func(string) (int, error) { return 1775, nil }
		systemctlCombinedOutput = func(args ...string) ([]byte, error) {
			return []byte("MainPID=1888\n"), nil
		}
		if systemdOwnsRunningDaemon() {
			t.Fatal("systemdOwnsRunningDaemon() = true for mismatched MainPID")
		}
	})
}

func TestLinuxUninstallRunsSystemctlPathsAndSurfacesWarnings(t *testing.T) {
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(servicePath, []byte("unit"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var systemctlCalls []string
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		systemctlCalls = append(systemctlCalls, strings.Join(args, "\x00"))
		if len(args) == 3 && args[1] == "stop" {
			return []byte("stop stderr"), errors.New("stop failed")
		}
		return nil, nil
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	uninstallCmd.SetOut(&out)
	uninstallCmd.SetErr(&errOut)
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	wantCalls := []string{
		strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "disable", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "reset-failed", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "daemon-reload"}, "\x00"),
	}
	if strings.Join(systemctlCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("systemctl calls = %q, want %q", systemctlCalls, wantCalls)
	}
	if !strings.Contains(errOut.String(), "stop stderr") {
		t.Fatalf("uninstall warning missing systemctl output: %s", errOut.String())
	}
	if _, err := os.Stat(servicePath); !os.IsNotExist(err) {
		t.Fatalf("service file should be removed, stat error = %v", err)
	}
}

func TestLinuxUninstallJSONEnvelope(t *testing.T) {
	home := t.TempDir()

	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(servicePath, []byte("unit"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	systemctlCombinedOutput = func(args ...string) ([]byte, error) { return nil, nil }
	setRootJSONFlag(t, true)

	got := captureStdout(t, func() {
		if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
			t.Fatalf("uninstall RunE() error = %v", err)
		}
	})
	res := parseResult(t, got)
	if !res.OK || res.Command != "uninstall" {
		t.Fatalf("uninstall JSON result = %#v", res)
	}
	data := dataMap(t, got)
	if data["removed"] != true || data["service_manager"] != systemdServiceName {
		t.Fatalf("uninstall data = %#v, want removed/systemd service manager", data)
	}
}

func TestLinuxUninstallAbsentUnitStillResetsFailedStateBestEffort(t *testing.T) {
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
		uninstallCmd.SetOut(nil)
		uninstallCmd.SetErr(nil)
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	var calls []string
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, "\x00"))
		if len(args) > 1 && args[1] == "show" {
			return []byte("LoadState=not-found\nActiveState=inactive\n"), nil
		}
		return []byte("unit not loaded"), errors.New("reset failed")
	}

	var out, errOut bytes.Buffer
	uninstallCmd.SetOut(&out)
	uninstallCmd.SetErr(&errOut)
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall RunE() error = %v, want absent unit and reset failure tolerated", err)
	}
	wantCalls := []string{
		strings.Join([]string{"--user", "reset-failed", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "show", systemdServiceName, "--property=LoadState", "--property=ActiveState", "--no-pager"}, "\x00"),
	}
	if strings.Join(calls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("systemctl calls = %q, want reset-failed plus absent-state confirmation", calls)
	}
	if !strings.Contains(out.String(), "not installed") || errOut.Len() != 0 {
		t.Fatalf("stdout = %q stderr = %q, want clean not-installed success after absent-state confirmation", out.String(), errOut.String())
	}
}

func TestLinuxUninstallResetFailedFailureIsNonFatal(t *testing.T) {
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
		uninstallCmd.SetOut(nil)
		uninstallCmd.SetErr(nil)
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(servicePath, []byte("unit"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "reset-failed" {
			return []byte("reset stderr"), errors.New("reset failed")
		}
		return nil, nil
	}

	var errOut bytes.Buffer
	uninstallCmd.SetErr(&errOut)
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall RunE() error = %v, want reset-failed failure downgraded to warning", err)
	}
	if !strings.Contains(errOut.String(), "reset failed") || !strings.Contains(errOut.String(), "reset stderr") {
		t.Fatalf("stderr = %q, want reset-failed warning with command output", errOut.String())
	}
}

func TestLinuxUninstallDaemonReloadEmptyOutputHasNoTrailingSeparator(t *testing.T) {
	home := t.TempDir()
	oldHome := linuxUserHomeDirFn
	oldSystemctl := systemctlCombinedOutput
	t.Cleanup(func() {
		linuxUserHomeDirFn = oldHome
		systemctlCombinedOutput = oldSystemctl
	})

	linuxUserHomeDirFn = func() (string, error) { return home, nil }
	servicePath := filepath.Join(home, ".config", "systemd", "user", systemdServiceName)
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(servicePath, []byte("unit"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == "daemon-reload" {
			return nil, errors.New("reload failed")
		}
		return nil, nil
	}

	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err == nil {
		t.Fatal("uninstall RunE() error = nil, want daemon-reload failure")
	}
	want := "reload systemd user daemon: reload failed"
	if err.Error() != want {
		t.Fatalf("uninstall error = %q, want exact %q without stray separator", err, want)
	}
}

func TestLinuxLingerWarningWhenLoginctlUnavailable(t *testing.T) {
	oldUser := linuxUserNameFn
	oldLoginctl := loginctlCombinedOutputFn
	t.Cleanup(func() {
		linuxUserNameFn = oldUser
		loginctlCombinedOutputFn = oldLoginctl
	})

	linuxUserNameFn = func() string { return "alice" }
	loginctlCombinedOutputFn = func(args ...string) ([]byte, error) {
		return []byte("loginctl missing"), errors.New("exec: loginctl: not found")
	}

	warning := linuxLingerWarning()
	for _, want := range []string{"loginctl missing", `loginctl enable-linger "$USER"`, `loginctl disable-linger "$USER"`} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning = %q, want %q", warning, want)
		}
	}
}

func TestDefaultLinuxUserNameFallsBackToLognameAndUID(t *testing.T) {
	oldUID := linuxUserIDFn
	t.Cleanup(func() { linuxUserIDFn = oldUID })

	t.Setenv("USER", "")
	t.Setenv("LOGNAME", "logname-user")
	if got := defaultLinuxUserName(); got != "logname-user" {
		t.Fatalf("defaultLinuxUserName() = %q, want LOGNAME fallback", got)
	}

	t.Setenv("LOGNAME", "")
	linuxUserIDFn = func() int { return 12345 }
	if got := defaultLinuxUserName(); got != "12345" {
		t.Fatalf("defaultLinuxUserName() = %q, want UID fallback", got)
	}
}

// Stateful manager control: normal explicit starts consume the same budget as
// crashes. Never pre-reset or sleep to drain the budget in this fixture.
func TestLinuxExplicitInstallClearsPriorStartBudget(t *testing.T) {
	setupRepairManager(t, func() {})
	original := systemctlCombinedOutput
	starts, resets := 5, 0
	systemctlCombinedOutput = func(args ...string) ([]byte, error) {
		switch args[1] {
		case "reset-failed":
			starts = 0
			resets++
		case "restart":
			if starts >= 5 {
				return []byte("start-limit-hit"), errors.New("start request repeated too quickly")
			}
			starts++
		}
		return original(args...)
	}
	for i := 0; i < 8; i++ {
		if err := repairOperation(context.Background(), "install"); err != nil {
			t.Fatalf("explicit install %d locked out by prior starts: %v", i, err)
		}
		if starts != 1 || resets != i+1 {
			t.Fatalf("reset scope: starts=%d resets=%d iteration=%d", starts, resets, i)
		}
	}
	// Unattended starts still hit the original budget: no implicit reset path.
	for i := 0; i < 4; i++ {
		if _, err := systemctlCombinedOutput("--user", "restart", systemdServiceName); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := systemctlCombinedOutput("--user", "restart", systemdServiceName); err == nil {
		t.Fatal("background start budget disabled")
	}
	unit := systemdServiceContents("/test/tslink", false)
	for _, policy := range []string{"StartLimitIntervalSec=300\n", "StartLimitBurst=5\n", "Restart=on-failure\n", "RestartSec=30\n"} {
		if !strings.Contains(unit, policy) {
			t.Fatalf("default crash protection changed: %s", policy)
		}
	}
}

func TestLinuxExplicitResetFailureAndBadBuildStayFailures(t *testing.T) {
	for _, mode := range []string{"reset-error", "reset-ineffective", "bad-build"} {
		t.Run(mode, func(t *testing.T) {
			stubFastSystemdSettle(t)
			old := systemctlCombinedOutput
			t.Cleanup(func() { systemctlCombinedOutput = old })
			var calls []string
			systemctlCombinedOutput = func(args ...string) ([]byte, error) {
				calls = append(calls, args[1])
				if args[1] == "reset-failed" && mode == "reset-error" {
					return []byte("access denied"), errors.New("reset rejected")
				}
				if args[1] == "restart" && mode == "reset-ineffective" {
					return []byte("start-limit-hit"), errors.New("still limited")
				}
				if args[1] == "show" {
					return []byte("ActiveState=activating\nSubState=auto-restart\nMainPID=0\nNRestarts=0\n"), nil
				}
				return nil, nil
			}
			_, err := activateSystemdService()
			if err == nil {
				t.Fatal("failure hidden")
			}
			want := "daemon-reload enable reset-failed"
			switch mode {
			case "reset-error":
				if !strings.Contains(err.Error(), "access denied") {
					t.Fatalf("lost reset diagnostics: %v", err)
				}
			case "reset-ineffective":
				want += " restart"
			case "bad-build":
				want += " restart show"
			}
			if strings.Join(calls, " ") != want {
				t.Fatalf("failure sequence=%v want=%s", calls, want)
			}
		})
	}
}

func TestLinuxRestoreResetsOnlyAfterRestoredReload(t *testing.T) {
	for _, mode := range []string{"limited", "reset-error", "not-owned", "stop-error", "reload-error", "bad-build"} {
		t.Run(mode, func(t *testing.T) {
			stubFastSystemdSettle(t)
			path := filepath.Join(t.TempDir(), systemdServiceName)
			if err := os.WriteFile(path, []byte("new unit"), 0600); err != nil {
				t.Fatal(err)
			}
			previous := systemdPreviousState{Existed: true, Unit: []byte("old unit"), Mode: 0600, OwnedRunning: mode != "not-owned"}
			old := systemctlCombinedOutput
			t.Cleanup(func() { systemctlCombinedOutput = old })
			limited := true
			var calls []string
			systemctlCombinedOutput = func(args ...string) ([]byte, error) {
				op := args[1]
				calls = append(calls, op)
				if op != "stop" {
					data, err := os.ReadFile(path)
					if err != nil || !bytes.Equal(data, previous.Unit) {
						t.Fatalf("%s before old bytes restored: %q %v", op, data, err)
					}
				}
				switch op {
				case "stop":
					if mode == "stop-error" {
						return nil, errors.New("stop rejected")
					}
				case "daemon-reload":
					if mode == "reload-error" {
						return nil, errors.New("reload rejected")
					}
				case "reset-failed":
					if mode == "reset-error" {
						return []byte("reset denied"), errors.New("reset rejected")
					}
					limited = false
				case "restart":
					if limited {
						return []byte("start-limit-hit"), errors.New("limited")
					}
				case "show":
					if mode == "bad-build" {
						return []byte("ActiveState=failed\nSubState=failed\nMainPID=0\nNRestarts=1\n"), nil
					}
					return runningSystemdState(), nil
				}
				return nil, nil
			}
			result, err := restorePreviousSystemdUnit(previous, path)
			want := "stop daemon-reload"
			switch mode {
			case "limited":
				want += " reset-failed restart show show"
			case "reset-error":
				want += " reset-failed"
			case "bad-build":
				want += " reset-failed restart show"
			}
			if strings.Join(calls, " ") != want {
				t.Fatalf("restore sequence=%v want=%s", calls, want)
			}
			wantOK := mode == "limited" || mode == "not-owned"
			if (err == nil) != wantOK || !result.UnitRestored || result.Restarted != (mode == "limited") {
				t.Fatalf("restore result=%+v err=%v", result, err)
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil || !bytes.Equal(data, previous.Unit) {
				t.Fatalf("old bytes lost: %q %v", data, readErr)
			}
		})
	}
}
