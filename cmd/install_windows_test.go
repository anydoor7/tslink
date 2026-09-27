//go:build windows

package cmd

import (
	"bytes"
	"context"
	"errors"
	"github.com/spf13/cobra"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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

func TestWindowsInstallRejectsStartupSymlinkWithoutChangingOldBytes(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	path, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	referent := filepath.Join(t.TempDir(), "previous-script.vbs")
	const oldScript = "previous working startup script\r\n"
	if err := os.WriteFile(referent, []byte(oldScript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(referent, path); err != nil {
		t.Skipf("Windows host cannot create test symlink: %v", err)
	}
	oldExe, oldEval := windowsExecutablePathFn, windowsEvalSymlinksFn
	t.Cleanup(func() { windowsExecutablePathFn, windowsEvalSymlinksFn = oldExe, oldEval })
	windowsExecutablePathFn = func() (string, error) { return `C:\TSLink\tslink.exe`, nil }
	windowsEvalSymlinksFn = func(p string) (string, error) { return p, nil }
	if err := installCmd.RunE(installCmd, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("install through Startup symlink error = %v, want rejection", err)
	}
	got, err := os.ReadFile(referent)
	if err != nil || string(got) != oldScript {
		t.Fatalf("old script changed after failed install: %q, %v", got, err)
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

// Uses real per-user file locking on Windows. A different config must still
// contend for the same Startup script, and a canceled waiter must never mutate.
func TestWindowsDirectTransactionsCancelAndPreserveFinalState(t *testing.T) {
	for _, op := range []string{"install", "uninstall", "auto"} {
		t.Run(op, func(t *testing.T) {
			isolateBootstrap(t)
			oldExe, oldEval := windowsExecutablePathFn, windowsEvalSymlinksFn
			t.Cleanup(func() { windowsExecutablePathFn, windowsEvalSymlinksFn = oldExe, oldEval })
			var mutations atomic.Int32
			windowsExecutablePathFn = func() (string, error) { mutations.Add(1); return `C:\TSLink\tslink.exe`, nil }
			windowsEvalSymlinksFn = func(p string) (string, error) { return p, nil }
			command := func(ctx context.Context) *cobra.Command {
				c := &cobra.Command{}
				c.SetContext(ctx)
				c.SetOut(io.Discard)
				c.SetErr(io.Discard)
				c.Flags().Bool("no-auto-provision", false, "")
				return c
			}
			if err := installCmd.RunE(command(context.Background()), nil); err != nil {
				t.Fatal(err)
			}
			path, _ := windowsStartupScriptPath()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			owner := make(chan error, 1)
			go func() {
				owner <- withSupervisorTransaction(context.Background(), func() error { close(entered); <-release; return nil })
			}()
			<-entered
			// Change config only after the owner is paused and no longer reads env.
			t.Setenv("TSLINK_CONFIG_DIR", t.TempDir())
			newDir, _ := absoluteConfigDir()
			count := mutations.Load()
			operation := func(ctx context.Context) error {
				switch op {
				case "install":
					return installCmd.RunE(command(ctx), nil)
				case "uninstall":
					return uninstallCmd.RunE(command(ctx), nil)
				default:
					return ensureDaemon(ctx, io.Discard, false)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			err = operation(ctx)
			cancel()
			after, readErr := os.ReadFile(path)
			pausedCount := mutations.Load()
			close(release)
			if ownerErr := <-owner; ownerErr != nil {
				t.Fatal(ownerErr)
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s bypassed per-user lock: %v", op, err)
			}
			if readErr != nil || !bytes.Equal(before, after) || pausedCount != count {
				t.Fatalf("canceled %s entered mutation: %v, calls %d -> %d", op, readErr, count, pausedCount)
			}
			if op == "auto" {
				// Bootstrap refuses replacement across config boundaries after unlock.
				if err := operation(context.Background()); err == nil || !strings.Contains(err.Error(), "not bound to config") {
					t.Fatalf("cross-config auto replacement accepted: %v", err)
				}
				if err := uninstallCmd.RunE(command(context.Background()), nil); err != nil {
					t.Fatal(err)
				}
				// Keep bootstrap's real lock + locked installer; simulate only the
				// daemon becoming live so this test never starts wscript or a daemon.
				installDaemonFn = func(ctx context.Context, _ io.Writer) error {
					if err := runInstallLocked(command(ctx), nil); err != nil {
						return err
					}
					isRunningFn = func(string) bool { return true }
					detectSupervisionFn = func(string, bool, int) Supervision { return windowsStartupSupervision(path) }
					return nil
				}
			}
			ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := operation(ctx); err != nil {
				t.Fatalf("positive control after unlock (nested lock?): %v", err)
			}
			if op == "uninstall" {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("script remains: %v", err)
				}
			} else {
				data, err := os.ReadFile(path)
				if err != nil || !supervisorConfigMatches(data, newDir) {
					t.Fatalf("final config binding lost: %v", err)
				}
				s := windowsStartupSupervision(path)
				if s.RestartOnExit || s.AutostartScope != autostartScopeLogin {
					t.Fatalf("Startup policy changed: %+v", s)
				}
			}
		})
	}
}
