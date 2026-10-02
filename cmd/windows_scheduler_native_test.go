//go:build windows

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func isolateWindowsTask(t *testing.T) (string, windowsTaskSpec) {
	t.Helper()
	dir := isolateBootstrap(t)
	oldCall, oldSID, oldOwn, oldDaemon := windowsSchedulerFn, windowsSIDFn, windowsTaskOwnsPIDFn, windowsDaemonRunningFn
	oldExe, oldEval := windowsExecutablePathFn, windowsEvalSymlinksFn
	t.Cleanup(func() {
		windowsSchedulerFn, windowsSIDFn, windowsTaskOwnsPIDFn, windowsDaemonRunningFn = oldCall, oldSID, oldOwn, oldDaemon
		windowsExecutablePathFn, windowsEvalSymlinksFn = oldExe, oldEval
	})
	windowsSIDFn = func() (string, error) { return fixtureTaskSpec().SID, nil }
	windowsExecutablePathFn = func() (string, error) { return fixtureTaskSpec().Executable, nil }
	windowsEvalSymlinksFn = func(p string) (string, error) { return p, nil }
	windowsTaskOwnsPIDFn = func(int, []int, string) bool { return true }
	windowsDaemonRunningFn = func(string) bool { return true }
	windowsSchedulerFn = func(string, string, []byte) (windowsSchedulerStatus, error) { return windowsSchedulerStatus{}, nil }
	spec := fixtureTaskSpec()
	spec.ConfigDir = dir
	spec.PowerShell = windowsPowerShellPath()
	return dir, spec
}

func windowsTestCommand() *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	c.Flags().Bool("no-auto-provision", false, "")
	c.Flags().Bool("startup", false, "")
	return c
}

func TestWindowsTaskDefinitionRoundTrip(t *testing.T) {
	dir, spec := isolateWindowsTask(t)
	for _, noAuto := range []bool{false, true} {
		spec.NoAutoProvision = noAuto
		data, err := renderWindowsTask(spec)
		if err != nil {
			t.Fatal(err)
		}
		got, err := windowsTaskSpecFromDefinition(data, dir)
		if err != nil || got != spec {
			t.Fatalf("round trip=%+v,%v", got, err)
		}
		if _, err := windowsTaskSpecFromDefinition(data, dir+"other"); err == nil {
			t.Fatal("foreign config accepted")
		}
	}
}

func TestWindowsTaskInstallMigratesAndStarts(t *testing.T) {
	_, spec := isolateWindowsTask(t)
	path, _ := windowsStartupScriptPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(windowsConfigEnvironment(spec.ConfigDir)+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls []string
	var task windowsSchedulerStatus
	windowsSchedulerFn = func(op, name string, data []byte) (windowsSchedulerStatus, error) {
		calls = append(calls, op)
		if op == "register" {
			task = windowsSchedulerStatus{Exists: true, Enabled: true, XML: string(data), State: 3}
		}
		if op == "run" {
			isRunningFn = func(string) bool { return true }
			task.State = 4
			task.Engines = []int{42}
		}
		return task, nil
	}
	detectSupervisionFn = detectSupervision
	if err := runInstallLocked(windowsTestCommand(), nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); !strings.HasPrefix(got, "query,register,run,query") {
		t.Fatalf("operations=%s", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Startup entry remains: %v", err)
	}
	definition, _ := windowsTaskPath()
	data, err := os.ReadFile(definition)
	if err != nil || !windowsTaskMatches(data, spec) {
		t.Fatalf("local task=%s,%v", data, err)
	}
}

func TestWindowsTaskInstallFailurePreservesMigrationEvidence(t *testing.T) {
	for _, failure := range []string{"query", "register", "verify", "run"} {
		t.Run(failure, func(t *testing.T) {
			_, spec := isolateWindowsTask(t)
			path, _ := windowsStartupScriptPath()
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			before := []byte(windowsConfigEnvironment(spec.ConfigDir) + "\r\n")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			var task windowsSchedulerStatus
			windowsSchedulerFn = func(op, name string, data []byte) (windowsSchedulerStatus, error) {
				if op == failure {
					return windowsSchedulerStatus{}, errors.New("injected " + failure)
				}
				if op == "register" {
					task = windowsSchedulerStatus{Exists: true, Enabled: true, XML: string(data), State: 3}
					if failure == "verify" {
						task.Enabled = false
					}
				}
				return task, nil
			}
			err := runInstallLocked(windowsTestCommand(), nil)
			if err == nil {
				t.Fatal("injected failure accepted")
			}
			if failure != "run" {
				got, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(got, before) {
					t.Fatal("failed migration discarded original Startup entry")
				}
			}
			if failure != "query" {
				def, _ := windowsTaskPath()
				if _, err := os.Stat(def); err != nil {
					t.Fatal("uncertain scheduler mutation lost recovery definition")
				}
			}
		})
	}
}

func TestWindowsTaskSupervisionLoadedPolicyAndOwnership(t *testing.T) {
	_, spec := isolateWindowsTask(t)
	data, err := renderWindowsTask(spec)
	if err != nil {
		t.Fatal(err)
	}
	task := windowsSchedulerStatus{Exists: true, Enabled: true, State: 4, Engines: []int{42}, XML: string(data)}
	windowsSchedulerFn = func(string, string, []byte) (windowsSchedulerStatus, error) { return task, nil }
	good := detectSupervision("isolated.pid", true, 4242)
	if good.Manager != "windows-task-scheduler" || !good.RestartOnExit || !good.Autostart || good.AutostartScope != "login" {
		t.Fatalf("positive control=%+v", good)
	}
	for _, variant := range []string{"disabled", "policy", "ownership", "identity", "state"} {
		t.Run(variant, func(t *testing.T) {
			copy := task
			oldOwn, oldDaemon := windowsTaskOwnsPIDFn, windowsDaemonRunningFn
			t.Cleanup(func() {
				windowsTaskOwnsPIDFn, windowsDaemonRunningFn = oldOwn, oldDaemon
				windowsSchedulerFn = func(string, string, []byte) (windowsSchedulerStatus, error) { return task, nil }
			})
			switch variant {
			case "disabled":
				copy.Enabled = false
			case "policy":
				copy.XML = strings.Replace(copy.XML, "PT1M", "PT1S", 1)
			case "ownership":
				windowsTaskOwnsPIDFn = func(int, []int, string) bool { return false }
			case "identity":
				windowsDaemonRunningFn = func(string) bool { return false }
			case "state":
				copy.State = 3
			}
			windowsSchedulerFn = func(string, string, []byte) (windowsSchedulerStatus, error) { return copy, nil }
			got := detectSupervision("isolated.pid", true, 4242)
			if got.Manager != "manual" || got.Autostart || got.RestartOnExit {
				t.Fatalf("unverified supervision accepted=%+v", got)
			}
		})
	}
}

func TestWindowsTaskUninstallDisablesStopsThenDeletes(t *testing.T) {
	_, spec := isolateWindowsTask(t)
	data, _ := renderWindowsTask(spec)
	path, _ := windowsTaskPath()
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	task := windowsSchedulerStatus{Exists: true, Enabled: true, State: 4, Engines: []int{42}, XML: string(data)}
	isRunningFn = func(string) bool { return true }
	oldStop := stopDaemonFn
	t.Cleanup(func() { stopDaemonFn = oldStop })
	var calls []string
	stopDaemonFn = func(string) error { calls = append(calls, "stop"); task.State = 3; task.Engines = []int{}; return nil }
	windowsSchedulerFn = func(op, name string, data []byte) (windowsSchedulerStatus, error) {
		calls = append(calls, op)
		if op == "disable" {
			task.Enabled = false
		}
		if op == "delete" {
			task = windowsSchedulerStatus{}
		}
		return task, nil
	}
	if err := runUninstallLocked(windowsTestCommand(), nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "query,disable,stop,query,delete,query" {
		t.Fatalf("shutdown transaction order=%s", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("local definition remains after confirmed uninstall")
	}
}

func TestWindowsTaskUninstallRefusesUncertainOrForeignTask(t *testing.T) {
	for _, variant := range []string{"query", "foreign", "disable", "stop", "ownership", "delete", "confirm"} {
		t.Run(variant, func(t *testing.T) {
			_, spec := isolateWindowsTask(t)
			data, _ := renderWindowsTask(spec)
			path, _ := windowsTaskPath()
			_ = os.MkdirAll(filepath.Dir(path), 0700)
			_ = os.WriteFile(path, data, 0600)
			task := windowsSchedulerStatus{Exists: true, Enabled: true, State: 3, XML: string(data)}
			if variant == "foreign" {
				task.XML = strings.Replace(task.XML, "LeastPrivilege", "HighestAvailable", 1)
			}
			oldStop := stopDaemonFn
			t.Cleanup(func() { stopDaemonFn = oldStop })
			stopDaemonFn = func(string) error { return errors.New("injected stop") }
			if variant == "stop" || variant == "ownership" {
				isRunningFn = func(string) bool { return true }
				task.State = 4
				task.Engines = []int{42}
			}
			if variant == "ownership" {
				windowsTaskOwnsPIDFn = func(int, []int, string) bool { return false }
			}
			deleted := false
			windowsSchedulerFn = func(op, name string, data []byte) (windowsSchedulerStatus, error) {
				if op == variant || op == "query" && deleted && variant == "confirm" {
					return windowsSchedulerStatus{}, errors.New("injected " + variant)
				}
				if op == "delete" {
					deleted = true
					task = windowsSchedulerStatus{}
				}
				return task, nil
			}
			if err := runUninstallLocked(windowsTestCommand(), nil); err == nil {
				t.Fatal("uncertain deletion reported success")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("failed uninstall lost retry definition")
			}
		})
	}
}
