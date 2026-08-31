//go:build windows

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// windowsStartupScript must take the kill switch as a required argument. A
// variadic parameter lets a call site drop it silently, which would write a
// Startup script without --no-auto-provision for a user who asked for it.
var _ func(string, bool) string = windowsStartupScript

func TestWindowsStartupScriptPath(t *testing.T) {
	appData := filepath.Join("C:\\Users", "Alice Example", "AppData", "Roaming")
	t.Setenv("APPDATA", appData)

	got, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatalf("windowsStartupScriptPath() error = %v", err)
	}
	want := filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", windowsStartupScriptName)
	if got != want {
		t.Fatalf("startup path = %q, want %q", got, want)
	}
}

func TestWindowsStartupScriptEscapesSpacesAndQuotes(t *testing.T) {
	exe := `C:\Program Files\TS "Link"\tslink.exe`
	got := windowsStartupScript(exe, false)
	want := "CreateObject(\"Wscript.Shell\").Run \"\"\"\" & \"C:\\Program Files\\TS \"\"Link\"\"\\tslink.exe\" & \"\"\" serve\", 0, False\r\n"
	if got != want {
		t.Fatalf("script = %q, want %q", got, want)
	}
}

func TestWindowsStartupScriptCarriesNoAutoProvision(t *testing.T) {
	script := windowsStartupScript(`C:\Program Files\TSLink\tslink.exe`, true)
	if count := strings.Count(script, " --no-auto-provision"); count != 1 {
		t.Fatalf("kill-switch arg count = %d, want 1: %q", count, script)
	}
}

func TestWindowsInstallCommandWritesStartupScript(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	oldExe := windowsExecutablePathFn
	oldEval := windowsEvalSymlinksFn
	t.Cleanup(func() {
		windowsExecutablePathFn = oldExe
		windowsEvalSymlinksFn = oldEval
	})

	windowsExecutablePathFn = func() (string, error) { return `C:\Program Files\TSLink\tslink.exe`, nil }
	windowsEvalSymlinksFn = func(path string) (string, error) { return path, nil }

	var out bytes.Buffer
	installCmd.SetOut(&out)
	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatalf("windowsStartupScriptPath() error = %v", err)
	}
	script, err := os.ReadFile(startupPath)
	if err != nil {
		t.Fatalf("read Startup script: %v", err)
	}
	if !strings.Contains(string(script), `C:\Program Files\TSLink\tslink.exe`) {
		t.Fatalf("script missing executable path: %s", script)
	}
}

func TestWindowsInstallIntentionallyDoesNotInspectCurrentDaemon(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	oldExe := windowsExecutablePathFn
	oldEval := windowsEvalSymlinksFn
	oldPIDPath := pidPathFn
	t.Cleanup(func() {
		windowsExecutablePathFn = oldExe
		windowsEvalSymlinksFn = oldEval
		pidPathFn = oldPIDPath
		installCmd.SetOut(nil)
	})

	windowsExecutablePathFn = func() (string, error) { return `C:\Program Files\TSLink\tslink.exe`, nil }
	windowsEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	pidPathFn = func() (string, error) {
		t.Fatal("Windows install inspected the current daemon PID path")
		return "", nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	if err := installCmd.RunE(installCmd, nil); err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}
	if !strings.Contains(out.String(), "Startup script installed") {
		t.Fatalf("install output = %q, want Startup script success", out.String())
	}
}

func TestWindowsInstallJSONEnvelope(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	oldExe := windowsExecutablePathFn
	oldEval := windowsEvalSymlinksFn
	t.Cleanup(func() {
		windowsExecutablePathFn = oldExe
		windowsEvalSymlinksFn = oldEval
	})

	windowsExecutablePathFn = func() (string, error) { return `C:\Program Files\TSLink\tslink.exe`, nil }
	windowsEvalSymlinksFn = func(path string) (string, error) { return path, nil }
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
	if data["installed"] != true || data["started"] != false || data["service_manager"] != "windows-startup" {
		t.Fatalf("install data = %#v, want installed/not-started/windows-startup", data)
	}
}

func TestWindowsUninstallCommandRemovesStartupScript(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatalf("windowsStartupScriptPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(startupPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(startupPath, []byte("script"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	if err := uninstallCmd.RunE(uninstallCmd, nil); err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	if _, err := os.Stat(startupPath); !os.IsNotExist(err) {
		t.Fatalf("Startup script should be removed, stat error = %v", err)
	}
}

func TestWindowsUninstallJSONEnvelope(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	startupPath, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatalf("windowsStartupScriptPath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(startupPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(startupPath, []byte("script"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
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
	if data["removed"] != true || data["service_manager"] != "windows-startup" {
		t.Fatalf("uninstall data = %#v, want removed/windows-startup", data)
	}
}
