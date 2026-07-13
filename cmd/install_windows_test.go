//go:build windows

package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	got := windowsStartupScript(exe)
	want := "CreateObject(\"Wscript.Shell\").Run \"\"\"\" & \"C:\\Program Files\\TS \"\"Link\"\"\\tslink.exe\" & \"\"\" serve\", 0, False\r\n"
	if got != want {
		t.Fatalf("script = %q, want %q", got, want)
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

func TestWindowsInstallJSONEnvelope(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	oldExe := windowsExecutablePathFn
	oldEval := windowsEvalSymlinksFn
	t.Cleanup(func() {
		windowsExecutablePathFn = oldExe
		windowsEvalSymlinksFn = oldEval
		_ = rootCmd.PersistentFlags().Set("json", "false")
	})

	windowsExecutablePathFn = func() (string, error) { return `C:\Program Files\TSLink\tslink.exe`, nil }
	windowsEvalSymlinksFn = func(path string) (string, error) { return path, nil }
	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set json true: %v", err)
	}

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
	t.Cleanup(func() { _ = rootCmd.PersistentFlags().Set("json", "false") })

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
	if err := rootCmd.PersistentFlags().Set("json", "true"); err != nil {
		t.Fatalf("set json true: %v", err)
	}

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
