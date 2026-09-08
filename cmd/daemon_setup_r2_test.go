package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

func TestBootstrapConcurrentEnsureInstallsOnce(t *testing.T) {
	dir := isolateBootstrap(t)
	var running atomic.Bool
	var installs atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	isRunningFn = func(string) bool { return running.Load() }
	installDaemonFn = func(context.Context, io.Writer) error {
		n := installs.Add(1)
		if n == 1 {
			close(entered)
		}
		<-release
		if err := tsruntime.Save(filepath.Join(dir, "runtime.json"), tsruntime.NewSnapshot(4242, time.Now(), "fp", time.Now(), nil)); err != nil {
			return err
		}
		running.Store(true)
		return nil
	}
	detectSupervisionFn = func(string, bool, int) Supervision { return Supervision{Manager: "launchd", Autostart: true} }
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() { defer wg.Done(); errs[0] = ensureDaemon(context.Background(), io.Discard, false) }()
	<-entered
	wg.Add(1)
	go func() { defer wg.Done(); errs[1] = ensureDaemon(context.Background(), io.Discard, false) }()
	// Force overlap while the first installer holds the OS lock.
	time.Sleep(100 * time.Millisecond)
	countWhileBlocked := installs.Load()
	close(release)
	wg.Wait()
	if countWhileBlocked != 1 || installs.Load() != 1 || errors.Join(errs...) != nil {
		t.Fatalf("overlap installs=%d final=%d errors=%v", countWhileBlocked, installs.Load(), errs)
	}
}

func TestBootstrapEnrollmentAcrossServices(t *testing.T) {
	for _, scenario := range []string{"same_service", "other_service", "wrong_pid", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			dir := isolateBootstrap(t)
			pidPath, regPath, snapshotPath := filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "registry.json"), filepath.Join(dir, "runtime.json")
			svc := registry.Service{Name: "app2", Type: registry.TypeProxy, Target: "http://localhost:3001"}
			if _, err := registry.Add(regPath, svc); err != nil {
				t.Fatal(err)
			}
			isRunningFn = func(string) bool { return true }
			handoff := newAuthHandoffRecord("app", "https://login.tailscale.com/a/test-fixture", 4242)
			if scenario == "same_service" {
				handoff.Service = "app2"
			}
			if scenario == "wrong_pid" {
				handoff.DaemonPID = 9999
			}
			if scenario == "expired" {
				handoff.ExpiresAt = time.Now().Add(-time.Hour)
			}
			if err := saveAuthHandoff(filepath.Join(dir, "auth-handoff.json"), handoff); err != nil {
				t.Fatal(err)
			}
			valid := scenario == "same_service" || scenario == "other_service"
			wait := time.Duration(0)
			if valid {
				wait = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			defer cancel()
			started := time.Now()
			result, err := buildAddResult(ctx, svc, true, pidPath, regPath, snapshotPath, wait)
			if err != nil {
				t.Fatal(err)
			}
			if (result.AuthURL != "") != valid || (valid && result.AuthURL != handoff.AuthURL) {
				t.Fatalf("add handoff=%q valid=%t", result.AuthURL, valid)
			}
			_, err = resolveServiceURL(ctx, pidPath, regPath, snapshotPath, "app2", wait)
			code, _ := registry.ErrorCode(err)
			if valid {
				var next interface{ NextCommands() []string }
				if code != "enrollment_required" || !errors.As(err, &next) || !strings.Contains(strings.Join(next.NextCommands(), " "), handoff.AuthURL) || strings.Contains(strings.Join(next.NextCommands(), " "), "tslink url") {
					t.Fatalf("URL repair loop: %v", err)
				}
				if time.Since(started) > time.Second {
					t.Fatalf("enrollment was delayed: %s", time.Since(started))
				}
			} else if code != registry.CodeURLNotReady {
				t.Fatalf("stale/wrong process handoff accepted: %v", err)
			}
		})
	}
}

// Only inconclusive evidence earns the conservative treatment. A PID file
// naming a live process that is provably some other program is covered by
// TestBootstrapDoctorForeignPIDIsAStoppedDaemon instead.
func TestBootstrapDoctorUnverifiedIdentityKeepsProbes(t *testing.T) {
	for _, contents := range []string{"unreadable PID"} {
		t.Run(contents, func(t *testing.T) {
			env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
			if err := os.WriteFile(env.pidPath, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			isRunningFn = func(string) bool { return false }
			oldDetect := detectSupervisionFn
			t.Cleanup(func() { detectSupervisionFn = oldDetect })
			detectSupervisionFn = func(string, bool, int) Supervision { return unmanagedSupervision(false, "test") }
			probes := 0
			doctorProbeTargetFn = func(context.Context, string, time.Duration) error { probes++; return syscall.ECONNREFUSED }
			result := buildDoctorResult(doctorOptions{})
			if !result.Daemon.IdentityUnverified || probes != 1 {
				t.Fatalf("uncertain=%t probes=%d", result.Daemon.IdentityUnverified, probes)
			}
			f := assertDoctorFinding(t, result, inspect.WarningCodeDaemonIdentityUnverified)
			if f.Severity != "warning" {
				t.Fatalf("identity finding=%+v", f)
			}
			assertDoctorFinding(t, result, inspect.WarningCodeTargetProbeRefused)
			assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonNotRunning)
			assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonUnsupervised)
			assertDoctorNoFinding(t, result, inspect.WarningCodeTargetProbeSkippedDaemon)
			// Same scanner with no PID artifact must take the proven-stopped branch.
			if err := os.Remove(env.pidPath); err != nil {
				t.Fatal(err)
			}
			stopped := buildDoctorResult(doctorOptions{})
			assertDoctorFinding(t, stopped, inspect.WarningCodeDaemonNotRunning)
			if probes != 1 {
				t.Fatalf("stopped branch probed again: %d", probes)
			}
		})
	}
}

func TestBootstrapWindowsStartupDoctorContract(t *testing.T) {
	newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	old := detectSupervisionFn
	t.Cleanup(func() { detectSupervisionFn = old })
	detectSupervisionFn = func(string, bool, int) Supervision { return windowsStartupSupervision("fixture.vbs") }
	result := buildDoctorResult(doctorOptions{})
	if result.Supervision.Manager != "windows-startup" || !result.Supervision.Autostart || !result.Supervision.Installed || result.Supervision.RestartOnExit {
		t.Fatalf("startup=%+v", result.Supervision)
	}
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonUnsupervised)
	if f := assertDoctorFinding(t, result, inspect.WarningCodeDaemonRestartUnavailable); f.Severity != "warning" {
		t.Fatalf("startup limitation=%+v", f)
	}
}

func TestBootstrapMCPWritesThenInstallsWithOptOut(t *testing.T) {
	for _, tool := range []string{"add", "template_apply"} {
		for _, noInstall := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/optout_%t", tool, noInstall), func(t *testing.T) {
				dir := isolateBootstrap(t)
				paths := sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid"), Snapshot: filepath.Join(dir, "runtime.json")}
				calls := 0
				ensureDaemonFn = func(ctx context.Context, out io.Writer, optout bool) error {
					calls++
					reg, err := registry.Load(paths.Registry)
					if err != nil || len(reg.Services) == 0 {
						t.Fatalf("MCP installed before write: %+v %v", reg, err)
					}
					if optout != noInstall {
						t.Fatalf("optout=%t want=%t", optout, noInstall)
					}
					if !optout {
						return errors.New("installer reached")
					}
					return nil
				}
				args := fmt.Sprintf(`{"name":"app","type":"proxy","target":"localhost:3000","no_daemon_install":%t}`, noInstall)
				if tool == "template_apply" {
					args = fmt.Sprintf(`{"name":"personal-harness","no_daemon_install":%t}`, noInstall)
				}
				var stderr bytes.Buffer
				result, err := callMCPTool(context.Background(), defaultMCPActions(paths, &stderr), tool, json.RawMessage(args))
				if err != nil || calls != 1 || result == nil || result.IsError == noInstall {
					t.Fatalf("calls=%d result=%+v err=%v", calls, result, err)
				}
			})
		}
	}
}

func TestBootstrapSetupFailureDisclosesRemainingDefinition(t *testing.T) {
	for _, remaining := range []bool{false, true} {
		t.Run(fmt.Sprint(remaining), func(t *testing.T) {
			isolateBootstrap(t)
			path, _ := supervisorPath()
			if remaining {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := daemonSetupError(errors.New("daemon did not settle"))
			var next interface{ NextCommands() []string }
			if !errors.As(err, &next) || !strings.Contains(strings.Join(next.NextCommands(), " "), "tslink logs") {
				t.Fatalf("no log recovery: %v", err)
			}
			if remaining && (!strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "crash-looping")) {
				t.Fatalf("half-install hidden: %v", err)
			}
			if !remaining && !strings.Contains(err.Error(), "No supervisor definition") {
				t.Fatalf("rollback hidden: %v", err)
			}
		})
	}
}
