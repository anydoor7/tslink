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
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/testenv"
)

func isolateBootstrap(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := testenv.SetHome(t, home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
	oldRunning, oldReadPID := isRunningFn, readPIDFn
	oldEnsure, oldInstall, oldDetect, oldOutput := ensureDaemonFn, installDaemonFn, detectSupervisionFn, managerOutputFn
	oldTimeout, oldInterval, oldSettle, oldEvidence := bootstrapTimeout, bootstrapInterval, bootstrapSettle, bootstrapEvidenceTimeout
	t.Cleanup(func() {
		isRunningFn, readPIDFn = oldRunning, oldReadPID
		ensureDaemonFn, installDaemonFn, detectSupervisionFn, managerOutputFn = oldEnsure, oldInstall, oldDetect, oldOutput
		bootstrapTimeout, bootstrapInterval, bootstrapSettle, bootstrapEvidenceTimeout = oldTimeout, oldInterval, oldSettle, oldEvidence
	})
	isRunningFn = func(string) bool { return false }
	readPIDFn = func(string) (int, error) { return 4242, nil }
	ensureDaemonFn = ensureDaemon
	bootstrapTimeout, bootstrapInterval, bootstrapSettle, bootstrapEvidenceTimeout = 20*time.Millisecond, time.Millisecond, 2*time.Millisecond, 20*time.Millisecond
	managerOutputFn = func(name string, args ...string) ([]byte, error) {
		if name == "systemctl" {
			return []byte("LoadState=not-found\n"), nil
		}
		if name == "launchctl" {
			return []byte("Could not find service\n"), errors.New("not found")
		}
		t.Fatalf("unexpected manager %s %v", name, args)
		return nil, errors.New("unexpected manager")
	}
	installDaemonFn = func(context.Context, io.Writer) error { t.Fatal("unexpected install"); return nil }
	detectSupervisionFn = func(_ string, running bool, _ int) Supervision { return unmanagedSupervision(running, "test") }
	return dir
}

func TestBootstrapOptOutAndAlreadyRunning(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			isolateBootstrap(t)
			isRunningFn = func(string) bool { return running }
			detectSupervisionFn = func(string, bool, int) Supervision {
				return Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true}
			}
			var log bytes.Buffer
			if err := ensureDaemon(context.Background(), &log, !running); err != nil {
				t.Fatal(err)
			}
			if !running && !strings.Contains(log.String(), "--no-daemon-install") {
				t.Fatalf("missing opt-out announcement: %s", &log)
			}
			if running && log.Len() != 0 {
				t.Fatalf("already running changed setup: %s", &log)
			}
		})
	}
}

// Setup is judged on what this machine controls. The first gate proves the
// supervisor owns a stable process; the first business artifact is produced
// only after tsnet reaches the Tailscale coordination server, so its absence
// is a statement about the network and must not be reported as a failed
// installation. Every variant below therefore succeeds, and the announcement
// is what distinguishes a daemon that has already published evidence from one
// that has not yet.
func TestBootstrapSucceedsWhenEvidenceLagsControlPlane(t *testing.T) {
	for _, evidence := range []string{"snapshot", "enrollment", "missing", "wrong_pid", "stale", "future"} {
		t.Run(evidence, func(t *testing.T) {
			dir := isolateBootstrap(t)
			installs, samples := 0, 0
			installDaemonFn = func(context.Context, io.Writer) error {
				installs++
				isRunningFn = func(string) bool { return true }
				detectSupervisionFn = func(string, bool, int) Supervision {
					samples++
					return Supervision{Manager: "systemd", Installed: true, RestartOnExit: true, Autostart: true}
				}
				if evidence == "snapshot" || evidence == "wrong_pid" || evidence == "stale" || evidence == "future" {
					pid := 4242
					if evidence == "wrong_pid" {
						pid++
					}
					snapshot := tsruntime.NewSnapshot(pid, time.Now(), "fixture", time.Now(), nil)
					if evidence == "stale" {
						snapshot.UpdatedAt = time.Now().Add(-time.Minute)
					}
					if evidence == "future" {
						snapshot.UpdatedAt = time.Now().Add(time.Minute)
					}
					return tsruntime.Save(filepath.Join(dir, "runtime.json"), snapshot)
				}
				if evidence == "enrollment" {
					return saveAuthHandoff(filepath.Join(dir, "auth-handoff.json"), newAuthHandoffRecord("myapp", "https://login.tailscale.com/a/fixture", 4242))
				}
				return nil
			}
			var log bytes.Buffer
			if err := ensureDaemon(context.Background(), &log, false); err != nil {
				t.Fatalf("evidence=%s err=%v log=%s", evidence, err, &log)
			}
			if installs != 1 || samples < 2 {
				t.Fatalf("installs=%d samples=%d", installs, samples)
			}
			ready := evidence == "snapshot" || evidence == "enrollment"
			if got := strings.Contains(log.String(), "background service is ready"); got != ready {
				t.Fatalf("evidence=%s ready-announcement=%t: %s", evidence, got, &log)
			}
			if got := strings.Contains(log.String(), "has not published a first sync/enrollment artifact"); got == ready {
				t.Fatalf("evidence=%s pending-announcement=%t: %s", evidence, got, &log)
			}
			// A successful setup never speculates about a crash loop; that
			// wording belongs to daemonSetupError and to nothing else.
			if strings.Contains(log.String(), "crash-looping") {
				t.Fatalf("evidence=%s: healthy path mentioned a crash loop: %s", evidence, &log)
			}
			path, _ := supervisorPath()
			for _, want := range []string{supervisorName(), path, dir, "tslink uninstall"} {
				if !strings.Contains(log.String(), want) {
					t.Errorf("announcement missing %q: %s", want, &log)
				}
			}
		})
	}
}

// The first gate still owns the failure verdict. Ownership that never settles
// is a real installation failure and keeps reporting one.
func TestBootstrapFailsWhenSupervisionNeverSettles(t *testing.T) {
	isolateBootstrap(t)
	installDaemonFn = func(context.Context, io.Writer) error {
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(_ string, running bool, _ int) Supervision {
			return unmanagedSupervision(running, "no manager owns this process")
		}
		return nil
	}
	err := ensureDaemon(context.Background(), io.Discard, false)
	if code, _ := registry.ErrorCode(err); code != "daemon_setup_failed" {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(err.Error(), "ownership/autostart unconfirmed") {
		t.Fatalf("cause missing from %v", err)
	}
}

// Losing the process the first gate verified is the crash/restart loop the
// failure text describes, and after this change it is the only way to reach
// that text. The two messages are worded differently from the first gate's so
// the assertion proves which gate produced the verdict.
func TestBootstrapEvidenceGateFailsOnlyWhenProcessGoesAway(t *testing.T) {
	dir := t.TempDir()
	pidPath, snapshotPath := filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json")
	old, oldRead, oldEvidence := isRunningFn, readPIDFn, bootstrapEvidenceTimeout
	t.Cleanup(func() { isRunningFn, readPIDFn, bootstrapEvidenceTimeout = old, oldRead, oldEvidence })
	bootstrapEvidenceTimeout = 20 * time.Millisecond

	for _, tc := range []struct {
		name    string
		running bool
		pid     int
		want    string
	}{
		{"stopped", false, 4242, "verified process 4242 stopped after supervision confirmed it"},
		{"replaced", true, 4343, "verified process was replaced (4242 -> 4343) after supervision confirmed it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isRunningFn = func(string) bool { return tc.running }
			readPIDFn = func(string) (int, error) { return tc.pid, nil }
			ready, err := waitDaemonEvidence(context.Background(), pidPath, snapshotPath, 4242)
			if ready || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ready=%t err=%v", ready, err)
			}
		})
	}

	t.Run("absent_evidence_is_not_an_error", func(t *testing.T) {
		isRunningFn = func(string) bool { return true }
		readPIDFn = func(string) (int, error) { return 4242, nil }
		start := time.Now()
		ready, err := waitDaemonEvidence(context.Background(), pidPath, snapshotPath, 4242)
		if ready || err != nil {
			t.Fatalf("ready=%t err=%v", ready, err)
		}
		if elapsed := time.Since(start); elapsed < bootstrapEvidenceTimeout {
			t.Fatalf("returned after %s, before the budget elapsed", elapsed)
		}
	})

	t.Run("late_evidence_is_picked_up", func(t *testing.T) {
		isRunningFn = func(string) bool { return true }
		readPIDFn = func(string) (int, error) { return 4242, nil }
		// What this subtest asserts is that evidence appearing *after* the wait
		// has already started still gets picked up. How fast that happens is not
		// under test here, so the budget must not be tight enough to race it:
		// with the 20ms shared by the subtests above, a 2ms goroutine wake plus a
		// file write plus a poll can exceed the budget under load, and the failure
		// then reports machine business rather than code. Measured on 2026-09-08:
		// idle 0/20 failures, but 1/12 while a full `go test ./...` ran alongside
		// (2/5 on another machine under heavier concurrent load).
		// A generous budget costs nothing in wall-clock: the wait returns as soon
		// as the artifact lands (~2-3ms), not when the budget expires, and a
		// regression that stops picking up late evidence still fails here because
		// it then has to burn the whole budget and return ready=false.
		bootstrapEvidenceTimeout = 2 * time.Second
		go func() {
			time.Sleep(2 * time.Millisecond)
			_ = saveAuthHandoff(filepath.Join(dir, "auth-handoff.json"), newAuthHandoffRecord("myapp", "https://login.tailscale.com/a/late", 4242))
		}()
		start := time.Now()
		ready, err := waitDaemonEvidence(context.Background(), pidPath, snapshotPath, 4242)
		if !ready || err != nil {
			t.Fatalf("ready=%t err=%v", ready, err)
		}
		// Positive assertion that the pickup was event-driven, not budget-driven:
		// returning only once the budget elapsed would mean the poll never saw the
		// artifact land. Half the budget is far above the ~2-3ms this really takes
		// and far below the 2s a broken implementation would burn.
		if elapsed := time.Since(start); elapsed > bootstrapEvidenceTimeout/2 {
			t.Fatalf("late evidence took %s, i.e. it was not picked up while waiting", elapsed)
		}
	})
}

func TestBootstrapRefusesOtherConfigBeforeInstall(t *testing.T) {
	isolateBootstrap(t)
	path, err := supervisorPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	prior := []byte("existing supervisor owned by another config\n")
	if err := os.WriteFile(path, prior, 0600); err != nil {
		t.Fatal(err)
	}
	err = ensureDaemon(context.Background(), io.Discard, false)
	if err == nil || !strings.Contains(err.Error(), "automatic replacement refused") {
		t.Fatalf("err=%v", err)
	}
	current, _ := os.ReadFile(path)
	if !bytes.Equal(prior, current) {
		t.Fatal("existing supervisor was overwritten")
	}
}

func TestBootstrapStableWindow(t *testing.T) {
	for _, scenario := range []string{"stable", "death", "pid_change", "late_death", "never_ready", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			calls := 0
			start := time.Now()
			pid, err := waitStableDaemon(ctx, func() (int, error) {
				calls++
				switch scenario {
				case "death":
					if calls > 1 {
						return 0, nil
					}
				case "pid_change":
					if calls > 1 {
						return 43, nil
					}
				case "late_death":
					if calls > 3 {
						return 0, nil
					}
				case "never_ready":
					return 0, nil
				}
				return 42, nil
			}, 25*time.Millisecond, time.Millisecond, 8*time.Millisecond)
			if scenario == "stable" {
				if err != nil || pid != 42 || calls < 2 || time.Since(start) < 8*time.Millisecond {
					t.Fatalf("pid=%d err=%v calls=%d elapsed=%s", pid, err, calls, time.Since(start))
				}
			} else if err == nil {
				t.Fatalf("accepted unstable process: %s calls=%d pid=%d", scenario, calls, pid)
			}
		})
	}
}

func TestBootstrapAddFailureRetainsRegistry(t *testing.T) {
	dir := isolateBootstrap(t)
	installs := 0
	installDaemonFn = func(context.Context, io.Writer) error { installs++; return errors.New("injected install failure") }
	_, err := runAddCmdOutput(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000", "no-daemon-install": "false"})
	if err == nil || !strings.Contains(err.Error(), "injected install failure") || installs != 1 {
		t.Fatalf("err=%v installs=%d", err, installs)
	}
	reg, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Name != "myapp" {
		t.Fatalf("saved registry lost: %+v err=%v", reg, err)
	}
}

func TestBootstrapAddWritesBeforeInstallThenReturnsURL(t *testing.T) {
	dir := isolateBootstrap(t)
	installs := 0
	regPath, snapshotPath := filepath.Join(dir, "registry.json"), filepath.Join(dir, "runtime.json")
	oldMTime := pidFileModTimeFn
	t.Cleanup(func() { pidFileModTimeFn = oldMTime })
	startedAt := time.Now().Add(-time.Second)
	pidFileModTimeFn = func(string) (time.Time, error) { return startedAt, nil }
	installDaemonFn = func(context.Context, io.Writer) error {
		installs++
		reg, err := registry.Load(regPath)
		if err != nil || len(reg.Services) != 1 || reg.Services[0].Name != "myapp" {
			t.Fatalf("daemon cannot see the saved service during installation: %+v err=%v", reg, err)
		}
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(string, bool, int) Supervision {
			return Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true}
		}
		fp, _ := tsruntime.RegistryFingerprint(reg)
		snapshot := tsruntime.NewSnapshot(4242, startedAt, fp, time.Now(), []tsruntime.ServiceState{{Service: reg.Services[0], RuntimeHost: "myapp.tailnet-example.ts.net"}})
		return tsruntime.Save(snapshotPath, snapshot)
	}
	out, err := runAddCmdOutput(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000", "wait": "1s"})
	if err != nil {
		t.Fatal(err)
	}
	if installs != 1 {
		t.Fatalf("setup was not invoked: installs=%d out=%q", installs, out)
	}
	if installs != 1 || !strings.Contains(out, "URL: https://myapp.tailnet-example.ts.net") {
		t.Fatalf("installs=%d out=%q", installs, out)
	}
}

// The whole point of splitting the gates, seen from the command the user
// actually types. The enrollment URL lands after the setup budget has already
// elapsed, which used to end the command with daemon_setup_failed while the
// daemon and the registry were both fine. It must now flow through to the
// caller's own wait and come back as a successful add with an auth URL.
func TestBootstrapAddSucceedsWhenEnrollmentURLArrivesLate(t *testing.T) {
	dir := isolateBootstrap(t)
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	record := newAuthHandoffRecord("myapp", "https://login.tailscale.com/a/late-fixture", 4242)
	installDaemonFn = func(context.Context, io.Writer) error {
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(string, bool, int) Supervision {
			return Supervision{Manager: "systemd", Installed: true, RestartOnExit: true, Autostart: true}
		}
		// Nothing to show for it yet, and nothing to show for it for longer
		// than the whole setup budget.
		go func() {
			time.Sleep(3 * bootstrapEvidenceTimeout)
			_ = saveAuthHandoff(handoffPath, record)
		}()
		return nil
	}
	var stderr bytes.Buffer
	addCmd, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	resetCommandLocalFlags(t, addCmd)
	addCmd.SetContext(context.Background())
	addCmd.SetErr(&stderr)
	var stdout bytes.Buffer
	addCmd.SetOut(&stdout)
	for k, v := range map[string]string{"proxy": "localhost:3000", "wait": "2s"} {
		if err := addCmd.Flags().Set(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := addCmd.RunE(addCmd, []string{"myapp"}); err != nil {
		t.Fatalf("late enrollment URL was reported as a failure: %v\nstderr=%s", err, &stderr)
	}
	if strings.Contains(stderr.String(), "crash-looping") || strings.Contains(stderr.String(), "daemon_setup_failed") {
		t.Fatalf("healthy path blamed the installation: %s", &stderr)
	}
	svc, err := loadPersistedService(filepath.Join(dir, "registry.json"), "myapp")
	if err != nil {
		t.Fatal(err)
	}
	result, err := buildAddResult(context.Background(), svc, true, filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "registry.json"), filepath.Join(dir, "runtime.json"), 2*time.Second)
	if err != nil || result.AuthURL != record.AuthURL || !result.URLPending {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

// TestBootstrapTemplateApplyUsesSetup counts the call; this holds the order.
// The CLI template path is the mirror of the add path: a setup failure must
// never be able to discard configuration the user already asked to save, so
// the services have to be on disk before installation is attempted.
func TestBootstrapTemplateApplyWritesBeforeSetup(t *testing.T) {
	dir := isolateBootstrap(t)
	cmd, _, _ := rootCmd.Find([]string{"template", "apply"})
	resetCommandLocalFlags(t, cmd)
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	var namesAtSetup []string
	ensureDaemonFn = func(context.Context, io.Writer, bool) error {
		reg, err := registry.Load(filepath.Join(dir, "registry.json"))
		if err != nil {
			return err
		}
		for _, svc := range reg.Services {
			namesAtSetup = append(namesAtSetup, svc.Name)
		}
		return errors.New("template setup marker")
	}
	if err := cmd.RunE(cmd, []string{"local-web"}); err == nil || !strings.Contains(err.Error(), "template setup marker") {
		t.Fatalf("err=%v", err)
	}
	if len(namesAtSetup) == 0 {
		t.Fatal("installation ran before the template services were persisted")
	}
	reg, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || len(reg.Services) != len(namesAtSetup) {
		t.Fatalf("persisted=%+v seen-at-setup=%v err=%v", reg, namesAtSetup, err)
	}
}

func TestBootstrapTemplateApplyUsesSetup(t *testing.T) {
	isolateBootstrap(t)
	cmd, _, _ := rootCmd.Find([]string{"template", "apply"})
	resetCommandLocalFlags(t, cmd)
	if err := cmd.Flags().Set("yes", "true"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { calls++; return errors.New("template setup marker") }
	err := cmd.RunE(cmd, []string{"local-web"})
	if err == nil || !strings.Contains(err.Error(), "template setup marker") || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestBootstrapOfflineAddNoGreenCheck(t *testing.T) {
	dir := isolateBootstrap(t)
	out, err := runAddCmdOutput(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000", "no-daemon-install": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "✓") || !strings.Contains(out, "not running or reachable") || !strings.Contains(out, "Next: tslink install") {
		t.Fatalf("offline output=%q", out)
	}
	reg, err := registry.Load(filepath.Join(dir, "registry.json"))
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Name != "myapp" {
		t.Fatalf("config not registered: %+v %v", reg, err)
	}
	svc := reg.Services[0]
	result, err := buildAddResult(context.Background(), svc, true, filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "registry.json"), filepath.Join(dir, "runtime.json"), time.Second)
	if err != nil || result.DaemonRunning || !result.URLPending || len(result.Warnings) != 1 || result.Warnings[0].Code != "daemon_not_running" {
		t.Fatalf("offline JSON data=%+v err=%v", result, err)
	}
}

func TestBootstrapURLRecoveryDoesNotLoop(t *testing.T) {
	isolateBootstrap(t)
	cmd, _, _ := rootCmd.Find([]string{"url"})
	resetCommandLocalFlags(t, cmd)
	if err := cmd.Flags().Set("wait", "30s"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := cmd.RunE(cmd, []string{"myapp"})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err=%v elapsed=%s", err, time.Since(start))
	}
	var next interface{ NextCommands() []string }
	if !errors.As(err, &next) || !reflect.DeepEqual(next.NextCommands(), []string{"tslink install"}) {
		t.Fatalf("non-repair next: %v", err)
	}
	if output.ExitCode(err) == 0 {
		t.Fatal("offline URL returned success")
	}
}

func TestBootstrapDoctorCauseAndProbePositiveControl(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "myapp", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	_ = env
	oldDetect := detectSupervisionFn
	t.Cleanup(func() { detectSupervisionFn = oldDetect })
	detectSupervisionFn = func(_ string, running bool, _ int) Supervision { return unmanagedSupervision(running, "fixture") }
	running, probes := false, 0
	isRunningFn = func(string) bool { return running }
	doctorProbeTargetFn = func(context.Context, string, time.Duration) error { probes++; return syscall.ECONNREFUSED }
	stopped := buildDoctorResult(doctorOptions{})
	if probes != 0 || stopped.Counts.Services != 1 || stopped.Counts.Errors < 2 {
		t.Fatalf("stopped=%+v probes=%d", stopped, probes)
	}
	for _, code := range []string{inspect.WarningCodeDaemonNotRunning, inspect.WarningCodeDaemonUnsupervised} {
		if f := assertDoctorFinding(t, stopped, code); f.Severity != "error" {
			t.Fatalf("cause not error: %+v", f)
		}
	}
	assertDoctorFinding(t, stopped, inspect.WarningCodeTargetProbeSkippedDaemon)
	assertDoctorNoFinding(t, stopped, inspect.WarningCodeTargetProbeRefused)
	// Prove that the same scanner/probe really can produce the finding.
	running = true
	live := buildDoctorResult(doctorOptions{})
	if probes != 1 || live.Counts.Services != 1 {
		t.Fatalf("positive control did not probe: %d %+v", probes, live)
	}
	if f := assertDoctorFinding(t, live, inspect.WarningCodeTargetProbeRefused); f.Severity != "error" {
		t.Fatalf("probe finding=%+v", f)
	}
	if f := assertDoctorFinding(t, live, inspect.WarningCodeDaemonUnsupervised); f.Severity != "error" {
		t.Fatalf("manual supervision=%+v", f)
	}
	var human bytes.Buffer
	formatDoctor(stopped, &human)
	if !strings.Contains(human.String(), "Supervision: none") {
		t.Fatalf("doctor missing supervision: %s", &human)
	}
	encoded, _ := json.Marshal(stopped)
	if !strings.Contains(string(encoded), `"supervision":{"manager":"none"`) {
		t.Fatalf("JSON missing supervision: %s", encoded)
	}
}

func TestBootstrapStatusSupervisionBothFormats(t *testing.T) {
	pidPath, regPath, snapshotPath := writeExactURLFixture(t, "myapp")
	oldDetect := detectSupervisionFn
	t.Cleanup(func() { detectSupervisionFn = oldDetect })
	detectSupervisionFn = func(string, bool, int) Supervision {
		return Supervision{Manager: "launchd", Installed: true, Autostart: true, RestartOnExit: true, Detail: "fixture"}
	}
	r, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var human bytes.Buffer
	formatStatusURLs(r, &human)
	encoded, _ := json.Marshal(r)
	if !strings.Contains(human.String(), "Supervision: launchd (autostart=true") || !strings.Contains(string(encoded), `"supervision":{"manager":"launchd"`) {
		t.Fatalf("human=%s JSON=%s", &human, encoded)
	}
	if !r.DaemonRunning || len(r.Services) != 1 || r.Services[0].Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("positive status evidence missing: %+v", r)
	}
}

func TestBootstrapAddEnrollmentAndExactURL(t *testing.T) {
	pidPath, regPath, snapshotPath := writeExactURLFixture(t, "myapp")
	svc, _ := loadPersistedService(regPath, "myapp")
	result, err := buildAddResult(context.Background(), svc, true, pidPath, regPath, snapshotPath, time.Second)
	if err != nil || result.URL == nil || *result.URL != "https://myapp.tailnet-example.ts.net" || result.URLPending {
		t.Fatalf("exact=%+v err=%v", result, err)
	}
	if err := os.Remove(snapshotPath); err != nil {
		t.Fatal(err)
	}
	record := newAuthHandoffRecord("myapp", "https://login.tailscale.com/a/fixture", 4242)
	if err := saveAuthHandoff(filepath.Join(filepath.Dir(pidPath), "auth-handoff.json"), record); err != nil {
		t.Fatal(err)
	}
	result, err = buildAddResult(context.Background(), svc, true, pidPath, regPath, snapshotPath, time.Second)
	if err != nil || result.AuthURL != record.AuthURL || !result.URLPending || result.URL != nil || len(result.Next) != 2 {
		t.Fatalf("enrollment=%+v err=%v", result, err)
	}
}

func TestBootstrapShareUsesManagedSetupAndOptOut(t *testing.T) {
	dir := isolateBootstrap(t)
	calls := 0
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { calls++; return errors.New("managed install failure") }
	if _, err := startShareDaemon(context.Background(), io.Discard); err == nil || calls != 1 {
		t.Fatalf("managed startup calls=%d err=%v", calls, err)
	}
	oldRunning := shareIsRunningFn
	t.Cleanup(func() { shareIsRunningFn = oldRunning })
	shareIsRunningFn = func(string) bool { return false }
	_, err := executeShare(context.Background(), sharePaths{Registry: filepath.Join(dir, "registry.json"), PID: filepath.Join(dir, "tslink.pid")}, shareRequest{Target: "localhost:3000", NoDaemonInstall: true}, 0, io.Discard)
	if codeOf(err) != "daemon_not_running" || calls != 1 {
		t.Fatalf("share opt-out err=%v calls=%d", err, calls)
	}
	reg, _ := registry.Load(filepath.Join(dir, "registry.json"))
	if len(reg.Services) != 0 {
		t.Fatal("refused share registered a service")
	}
}

func TestBootstrapDryRunHasNoInstallation(t *testing.T) {
	isolateBootstrap(t)
	out, err := runAddCmdOutput(t, []string{"myapp"}, map[string]string{"proxy": "localhost:3000", "dry-run": "true"})
	if err != nil || !strings.Contains(out, `"myapp"`) {
		t.Fatalf("dry run=%q err=%v", out, err)
	}
	cmd, _, _ := rootCmd.Find([]string{"add"})
	if cmd.Flags().Lookup("wait").DefValue != "30s" {
		t.Fatal("add must wait for an exact URL by default")
	}
	if cmd.Flags().Lookup("no-daemon-install").DefValue != "false" {
		t.Fatal("bootstrap must be the default")
	}
}

func TestBootstrapMCPShareOptOutIsForwarded(t *testing.T) {
	calls := 0
	actions := mcpActions{share: func(_ context.Context, req shareRequest) (ShareResult, error) {
		calls++
		if !req.NoDaemonInstall {
			t.Fatal("MCP discarded the installation opt-out")
		}
		return ShareResult{Status: "ready", URL: "https://fixture.ts.net"}, nil
	}}
	if _, err := callMCPTool(context.Background(), actions, "share", json.RawMessage(`{"target":"localhost:3000","no_daemon_install":true}`)); err != nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
