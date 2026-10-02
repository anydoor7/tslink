//go:build windows

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

// Match the scheduler's actual disable projection, including Settings.Enabled.
func setFakeWindowsTaskEnabled(t *testing.T, task *windowsSchedulerStatus, enabled bool) {
	t.Helper()
	start, end := strings.Index(task.XML, "<Settings>"), strings.Index(task.XML, "</Settings>")
	if start < 0 || end < start {
		t.Fatal("fake definition lacks Settings")
	}
	settings := task.XML[start:end]
	from, to := "<Enabled>true</Enabled>", "<Enabled>false</Enabled>"
	if enabled {
		from, to = to, from
	}
	settings = strings.Replace(settings, from, to, 1)
	task.XML = task.XML[:start] + settings + task.XML[end:]
	task.Enabled = enabled
	definition, err := parseWindowsTask([]byte(task.XML))
	if err != nil || !boolIs(definition.Settings.Enabled, enabled) {
		t.Fatalf("fake Enabled/XML mismatch: %v", err)
	}
	if task.State != 4 {
		task.State = 1
		if enabled {
			task.State = 3
		}
	}
}

type fakeWindowsTaskLifecycle struct {
	task    windowsSchedulerStatus
	running bool
	calls   []string
	fail    string
}

func fakeWindowsLifecycle(t *testing.T) (string, windowsTaskSpec, *fakeWindowsTaskLifecycle) {
	t.Helper()
	dir, spec := isolateWindowsTask(t)
	f := &fakeWindowsTaskLifecycle{}
	pidPath, err := config.PIDPath()
	if err != nil {
		t.Fatal(err)
	}
	isRunningFn = func(string) bool { return f.running }
	detectSupervisionFn = detectSupervision
	windowsSchedulerFn = func(op, name string, data []byte) (windowsSchedulerStatus, error) {
		f.calls = append(f.calls, op)
		if op == f.fail {
			return windowsSchedulerStatus{}, errors.New("injected " + op)
		}
		switch op {
		case "register":
			f.task = windowsSchedulerStatus{Exists: true, Enabled: true, State: 3, XML: string(data)}
		case "disable":
			setFakeWindowsTaskEnabled(t, &f.task, false)
		case "run":
			f.running = true
			f.task.State, f.task.Engines = 4, []int{42}
			if err := daemon.WritePIDForProcess(pidPath, 4242); err != nil {
				t.Fatal(err)
			}
			if err := tsruntime.Save(filepath.Join(dir, "runtime.json"), tsruntime.NewSnapshot(4242, time.Now(), "fixture", time.Now(), nil)); err != nil {
				t.Fatal(err)
			}
		case "delete":
			f.task = windowsSchedulerStatus{}
		}
		return f.task, nil
	}
	oldStop := stopDaemonFn
	t.Cleanup(func() { stopDaemonFn = oldStop })
	stopDaemonFn = func(path string) error {
		f.calls = append(f.calls, "stop")
		if f.fail == "stop" {
			return errors.New("injected stop")
		}
		f.running = false
		f.task.State, f.task.Engines = 3, nil
		if !f.task.Enabled {
			f.task.State = 1
		}
		daemon.RemovePID(path)
		return nil
	}
	return dir, spec, f
}

func TestWindowsTaskFreshAndStopThenInstall(t *testing.T) {
	_, _, f := fakeWindowsLifecycle(t)
	pidPath, _ := config.PIDPath()
	for _, stage := range []string{"fresh", "stop-then-install"} {
		t.Run(stage, func(t *testing.T) {
			if !daemon.IsPIDFileMissing(pidPath) {
				t.Fatal("native installer must receive an actually missing PID file")
			}
			f.calls = nil
			if err := runInstallLocked(windowsTestCommand(), nil); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(strings.Join(f.calls, ","), "query,register,run,query") {
				t.Fatalf("did not register and start: %v", f.calls)
			}
			if err := stopDaemonFn(pidPath); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsTaskInstallRejectsUncertainPIDFiles(t *testing.T) {
	for _, kind := range []string{"malformed", "nonpositive", "unreadable", "unidentified-live"} {
		t.Run(kind, func(t *testing.T) {
			_, _, f := fakeWindowsLifecycle(t)
			path, _ := config.PIDPath()
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "malformed":
				err = os.WriteFile(path, []byte("not-a-pid"), 0600)
			case "nonpositive":
				err = os.WriteFile(path, []byte("0"), 0600)
			case "unreadable":
				err = os.Mkdir(path, 0700) // A directory is unreadable as a PID file, even for admins.
			case "unidentified-live":
				// Windows System (PID 4) cannot be identified as a user TSLink
				// process. Prove the helper classification before testing install.
				err = daemon.WritePIDForProcess(path, 4)
			}
			if err != nil {
				t.Fatal(err)
			}
			if daemon.IsPIDFileMissing(path) || daemon.IsProcessAbsentFromPIDFile(path) || daemon.IsForeignProcessFromPIDFile(path) {
				t.Fatal("uncertain PID fixture does not exercise the conservative guard")
			}
			err = runInstallLocked(windowsTestCommand(), nil)
			if err == nil || !strings.Contains(err.Error(), "daemon PID identity is unverified") {
				t.Fatalf("uncertain PID guard=%v", err)
			}
			if got := strings.Join(f.calls, ","); got != "query" {
				t.Fatalf("uncertain PID reached a mutation: %s", got)
			}
		})
	}
}

func TestWindowsTaskDisabledFailureThenRetry(t *testing.T) {
	for _, failure := range []string{"stop", "delete"} {
		for _, retry := range []string{"install", "uninstall"} {
			t.Run(failure+"/"+retry, func(t *testing.T) {
				_, spec, f := fakeWindowsLifecycle(t)
				data, _ := renderWindowsTask(spec)
				path, _ := windowsTaskPath()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				f.task = windowsSchedulerStatus{Exists: true, Enabled: true, State: 4, Engines: []int{42}, XML: string(data)}
				f.running = true
				f.fail = failure
				err := runUninstallLocked(windowsTestCommand(), nil)
				if err == nil || !strings.Contains(err.Error(), "injected "+failure) {
					t.Fatalf("did not reach injected %s failure: %v", failure, err)
				}
				if f.task.Enabled || !f.task.Exists {
					t.Fatal("failure did not retain a disabled owned task")
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("failure discarded retry evidence")
				}
				if s := detectSupervision("isolated.pid", f.running, 4242); s.Autostart || s.RestartOnExit {
					t.Fatalf("disabled task earned healthy supervision: %+v", s)
				}
				f.fail, f.calls = "", nil
				if retry == "install" {
					err = runInstallLocked(windowsTestCommand(), nil)
				} else {
					err = runUninstallLocked(windowsTestCommand(), nil)
				}
				if err != nil {
					t.Fatalf("disabled owned task could not %s on retry: %v", retry, err)
				}
				if retry == "install" && (!f.task.Enabled || !f.running) || retry == "uninstall" && f.task.Exists {
					t.Fatalf("retry did not complete lifecycle: %+v", f)
				}
			})
		}
	}
}

func TestWindowsTaskDisabledOwnershipAndPolicyRepair(t *testing.T) {
	for _, variant := range []string{"disabled", "restart", "foreign-action", "foreign-config", "foreign-principal"} {
		t.Run(variant, func(t *testing.T) {
			_, spec, f := fakeWindowsLifecycle(t)
			data, _ := renderWindowsTask(spec)
			f.task = windowsSchedulerStatus{Exists: true, Enabled: true, State: 3, XML: string(data)}
			setFakeWindowsTaskEnabled(t, &f.task, false)
			switch variant {
			case "restart":
				f.task.XML = strings.Replace(f.task.XML, "<Count>255</Count>", "<Count>0</Count>", 1)
			case "foreign-action":
				f.task.XML = strings.Replace(f.task.XML, "-NoProfile", "-Profile", 1)
			case "foreign-config":
				f.task.XML = strings.Replace(f.task.XML, "config=", "config=other", 1)
			case "foreign-principal":
				f.task.XML = strings.Replace(f.task.XML, "LeastPrivilege", "HighestAvailable", 1)
			}
			foreign := strings.HasPrefix(variant, "foreign-")
			_, err := windowsTaskSpecFromDefinition([]byte(f.task.XML), spec.ConfigDir)
			if (err != nil) != foreign {
				t.Fatalf("ownership classification %s: %v", variant, err)
			}
			if s := detectSupervision("isolated.pid", false, 0); s.Autostart || s.RestartOnExit {
				t.Fatalf("disabled/broken policy earned supervision: %+v", s)
			}
			err = runInstallLocked(windowsTestCommand(), nil)
			if foreign {
				if err == nil || !strings.Contains(err.Error(), "refusing to replace foreign scheduler task") || strings.Join(f.calls, ",") != "query,query" {
					t.Fatalf("foreign task reached install mutation: %v, %v", err, f.calls)
				}
				f.calls = nil
				err = runUninstallLocked(windowsTestCommand(), nil)
				if err == nil || !strings.Contains(err.Error(), "refusing to remove foreign scheduler task") || strings.Join(f.calls, ",") != "query" {
					t.Fatalf("foreign task reached uninstall mutation: %v, %v", err, f.calls)
				}
			} else if err != nil || !f.task.Enabled || !f.running {
				t.Fatalf("owned task was not repaired: %v", err)
			}
		})
	}
}

func TestWindowsTaskNativeInstallReceiptMatchesMCPSchema(t *testing.T) {
	_, _, _ = fakeWindowsLifecycle(t)
	// An honestly stale, absent PID lets the old installer reach its receipt,
	// independently of the fresh-install regression. No guard seam is replaced.
	pidPath, _ := config.PIDPath()
	if err := daemon.WritePIDForProcess(pidPath, 2147483647); err != nil {
		t.Fatal(err)
	}
	if !daemon.IsProcessAbsentFromPIDFile(pidPath) {
		t.Fatal("stale PID control is not conclusively absent")
	}
	installDaemonFn = func(ctx context.Context, _ io.Writer) error {
		c := windowsTestCommand()
		c.SetContext(ctx)
		return runInstallLocked(c, nil)
	}
	ctx, record := recordDaemonInstall(context.Background())
	if err := ensureDaemon(ctx, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if record.installed == nil || record.installed.Manager != "windows-task-scheduler" {
		t.Fatalf("real native installation receipt=%+v", record.installed)
	}
	data, _ := json.Marshal(record.installed)
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if err := validateMCPJSONSchema(mcpDaemonInstalledSchema, payload, "daemon_installed"); err != nil {
		t.Fatal(err)
	}
}
