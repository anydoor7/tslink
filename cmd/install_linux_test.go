//go:build linux

package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
)

func stubLinuxInstallDaemonStopped(t *testing.T) {
	t.Helper()
	oldConflict := installDaemonConflictFn
	installDaemonConflictFn = func() error { return nil }
	t.Cleanup(func() { installDaemonConflictFn = oldConflict })
}

func runningSystemdState() []byte {
	return []byte("ActiveState=active\nSubState=running\nMainPID=1775\n")
}

func runLinuxInstallGuardTruthCase(t *testing.T, unitPresent, daemonRunning bool, systemdPID int) bool {
	t.Helper()
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
			return []byte(fmt.Sprintf("ActiveState=active\nSubState=running\nMainPID=%d\n", pid)), nil
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
	unit := systemdServiceContents("/usr/local/bin/tslink")
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

func TestSystemdServiceContentsQuotesExecutableWithSpaces(t *testing.T) {
	unit := systemdServiceContents("/opt/My App/tslink")
	if !strings.Contains(unit, `ExecStart="/opt/My App/tslink" serve`) {
		t.Fatalf("unit did not quote executable path with spaces:\n%s", unit)
	}
}

func TestSystemdServiceContentsEscapesSystemdSpecials(t *testing.T) {
	unit := systemdServiceContents(`/opt/100%/$build "TSLink"\tslink`)
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
		strings.Join([]string{"--user", "restart", systemdServiceName}, "\x00"),
		strings.Join([]string{"--user", "show", systemdServiceName, "--property=ActiveState", "--property=SubState", "--property=MainPID", "--no-pager"}, "\x00"),
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

func TestLinuxInstallRestoresPreviousUnitAfterUpgradeFailures(t *testing.T) {
	for _, failStage := range []string{"daemon-reload", "enable", "restart", "verify"} {
		t.Run(failStage, func(t *testing.T) {
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
			joined := strings.Join(calls, "\n")
			for _, want := range []string{
				strings.Join([]string{"--user", "stop", systemdServiceName}, "\x00"),
				strings.Join([]string{"--user", "daemon-reload"}, "\x00"),
				strings.Join([]string{"--user", "restart", systemdServiceName}, "\x00"),
			} {
				if !strings.Contains(joined, want) {
					t.Fatalf("systemctl calls = %q, missing restore step %q", calls, want)
				}
			}
		})
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
