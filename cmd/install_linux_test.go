//go:build linux

package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestLinuxInstallSurfacesSystemctlOutput(t *testing.T) {
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
