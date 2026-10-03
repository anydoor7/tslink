//go:build windows

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
)

func TestBuiltinSupervisorStateFilesFailClosed(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	path, _ := builtinSupervisorPaths(pidPath)
	good := builtinSupervisorRecord{Version: 1, ConfigDir: dir, Instance: daemon.SupervisorInstance{PID: 42, StartUnixNano: 123, Executable: "fixture.exe"}, SupervisorState: daemon.SupervisorState{State: "restarting"}}
	data, _ := json.Marshal(good)
	wrongVersion := good
	wrongVersion.Version = 2
	wrongVersionData, _ := json.Marshal(wrongVersion)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBuiltinSupervisor(pidPath); err != nil || got.State != "restarting" {
		t.Fatalf("control=%+v,%v", got, err)
	}
	t.Run("relative_config", func(t *testing.T) {
		// TEMP and the checkout can be on different volumes. Resolve this
		// relative fixture from its own directory, as a caller would.
		t.Chdir(dir)
		if got, err := readBuiltinSupervisor("tslink.pid"); err != nil || got.State != "restarting" {
			t.Fatalf("relative config=%+v,%v", got, err)
		}
	})
	for _, tc := range []struct {
		name   string
		data   []byte
		reason string
	}{
		{"partial", []byte(`{"version":1`), "malformed"},
		{"oversized", []byte(strings.Repeat(" ", 65537)), "oversized"},
		{"wrong_config", []byte(`{"version":1,"config_dir":"other"}`), "version/config"},
		{"wrong_version", wrongVersionData, "version/config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, tc.data, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := readBuiltinSupervisor(pidPath)
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("err=%v want=%s", err, tc.reason)
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := readBuiltinSupervisor(pidPath); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("special file err=%v", err)
	}
}

func TestBuiltinSupervisorHiddenFromPublicManifest(t *testing.T) {
	c, _, err := rootCmd.Find([]string{"supervise"})
	if err != nil || c.Name() != "supervise" || !c.Hidden {
		t.Fatalf("internal command=%v,%v", c, err)
	}
	manifest := Manifest()
	for _, command := range manifest.Commands {
		if command.Path == "tslink supervise" {
			t.Fatal("internal supervisor in public manifest")
		}
	}
}

func TestBuiltinSupervisorStateConcurrentReadersSeeCompleteRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	record := builtinSupervisorRecord{Version: 1, ConfigDir: dir, SupervisorState: daemon.SupervisorState{State: "running", Reason: strings.Repeat("A", 8192)}}
	data, _ := json.Marshal(record)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	joined := make(chan int, 1)
	go func() {
		reads := 0
		defer func() { joined <- reads }()
		for {
			select {
			case <-done:
				return
			default:
			}
			got, err := readBuiltinSupervisor(pidPath)
			if err != nil || len(got.Reason) != 8192 || got.State != "running" {
				t.Errorf("partial concurrent read: state=%s reason_len=%d err=%v", got.State, len(got.Reason), err)
				return
			}
			reads++
		}
	}()
	var writeErr error
	for i := 0; i < 30; i++ {
		record.Reason = strings.Repeat(string(rune('A'+i%2)), 8192)
		data, _ = json.Marshal(record)
		if err := writeBuiltinSupervisorState(path, data, daemon.NewSupervisorClock()); err != nil {
			writeErr = err
			break
		}
	}
	close(done)
	reads := <-joined
	if writeErr != nil || reads == 0 {
		t.Fatalf("concurrent reader count=%d write_err=%v", reads, writeErr)
	}
	if got, err := readBuiltinSupervisor(pidPath); err != nil || got.Reason != record.Reason {
		t.Fatalf("final record=%+v err=%v", got.SupervisorState, err)
	}
}

func TestBuiltinSupervisorStateWriterPreservesHeldSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	// The same shared-delete handle a concurrent status reader holds.
	f, err := openBuiltinSupervisorState(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// No retry budget: the first replacement must succeed while the handle is
	// held. Plain rename returns ERROR_ACCESS_DENIED here until the reader closes.
	sleeps := 0
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := daemon.SupervisorClock{Now: func() time.Time { return now }, Sleep: func(context.Context, time.Duration) error {
		sleeps++
		now = now.Add(time.Hour)
		return nil
	}}
	if err := writeBuiltinSupervisorState(path, []byte("new"), clock); err != nil || sleeps != 0 {
		t.Fatalf("held shared reader blocked replacement: err=%v sleeps=%d", err, sleeps)
	}
	old, err := io.ReadAll(f)
	if err != nil || string(old) != "old" {
		t.Fatalf("held snapshot = %q, %v", old, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("fresh record = %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v, %v", entries, err)
	}
}

func TestBuiltinSupervisorStateWriterRetriesSharingAndBoundsStall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { time.Sleep(30 * time.Millisecond); f.Close(); close(closed) }()
	err = writeBuiltinSupervisorState(path, []byte("new"), daemon.NewSupervisorClock())
	<-closed
	if err != nil {
		t.Fatalf("transient reader=%v", err)
	}
	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	sleeps := 0
	clock := daemon.SupervisorClock{Now: func() time.Time { return now }, Sleep: func(context.Context, time.Duration) error { now = now.Add(10 * time.Millisecond); sleeps++; return nil }}
	err = writeBuiltinSupervisorState(path, []byte("blocked"), clock)
	if err == nil || sleeps != 100 {
		t.Fatalf("stalled writer err=%v sleeps=%d", err, sleeps)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("failed replacement damaged old state=%s", data)
	}
}

func TestBuiltinSupervisorStatusRestartingAndBreaker(t *testing.T) {
	dir, spec := isolateWindowsTask(t)
	pidPath := filepath.Join(dir, "tslink.pid")
	data, _ := renderWindowsTask(spec)
	task := windowsSchedulerStatus{Exists: true, Enabled: true, State: 4, Engines: []int{42}, XML: string(data)}
	windowsSchedulerFn = func(context.Context, string, string, []byte) (windowsSchedulerStatus, error) { return task, nil }
	for _, state := range []string{"restarting", "starting", "stopped", "circuit_open", "failed"} {
		t.Run(state, func(t *testing.T) {
			alive := state == "restarting" || state == "starting"
			builtinSupervisorAliveFn = func(daemon.SupervisorInstance) (bool, error) { return alive, nil }
			readBuiltinSupervisorFn = func(string) (builtinSupervisorRecord, error) {
				return builtinSupervisorRecord{Version: 1, ConfigDir: dir, Instance: daemon.SupervisorInstance{PID: 43, Executable: spec.Executable}, SupervisorState: daemon.SupervisorState{State: state, Reason: "fixture_reason", Failures: 8}}, nil
			}
			got := detectSupervision(context.Background(), pidPath, false, 0)
			if got.Manager != "windows-task-scheduler" || got.RuntimeState != state || got.RestartOnExit != alive || got.FailureReason != "fixture_reason" {
				t.Fatalf("supervision=%+v", got)
			}
			if !strings.Contains(got.Detail, "supervisor="+state) {
				t.Fatalf("detail=%s", got.Detail)
			}
		})
	}
	builtinSupervisorAliveFn = func(daemon.SupervisorInstance) (bool, error) { return false, errors.New("identity mismatch") }
	readBuiltinSupervisorFn = func(string) (builtinSupervisorRecord, error) {
		return builtinSupervisorRecord{Version: 1, ConfigDir: dir, Instance: daemon.SupervisorInstance{PID: 43, Executable: spec.Executable}, SupervisorState: daemon.SupervisorState{State: "restarting"}}, nil
	}
	if got := detectSupervision(context.Background(), pidPath, false, 0); got.Manager != "none" || !strings.Contains(got.Detail, "ownership unverified") {
		t.Fatalf("identity accepted=%+v", got)
	}
}

func TestBuiltinSupervisorTerminalPIDReusePreservesHistoryAndDoesNotSignal(t *testing.T) {
	dir, spec := isolateWindowsTask(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(dir, "tslink.pid")
	data, _ := renderWindowsTask(spec)
	windowsSchedulerFn = func(context.Context, string, string, []byte) (windowsSchedulerStatus, error) {
		return windowsSchedulerStatus{Exists: true, Enabled: true, State: 3, XML: string(data)}, nil
	}
	// Reuse a real live PID with deliberately stale launch identity. No seam
	// grants process ownership and this process must never receive an event.
	instance, err := daemon.CurrentSupervisorInstance()
	if err != nil {
		t.Fatal(err)
	}
	instance.StartUnixNano++
	builtinSupervisorAliveFn = daemon.SupervisorAlive
	for _, state := range []string{"stopped", "circuit_open", "failed"} {
		t.Run(state, func(t *testing.T) {
			r := builtinSupervisorRecord{Version: 1, ConfigDir: dir, Instance: instance, SupervisorState: daemon.SupervisorState{State: state, Reason: "historical_reason"}}
			path, _ := builtinSupervisorPaths(pidPath)
			data, _ := json.Marshal(r)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			readBuiltinSupervisorFn = readBuiltinSupervisor
			got := detectSupervision(context.Background(), pidPath, false, 0)
			if got.Manager != "windows-task-scheduler" || got.RuntimeState != state || got.FailureReason != "historical_reason" || got.SupervisorPID != 0 || got.RestartOnExit {
				t.Fatalf("terminal history=%+v", got)
			}
			if stopped, err := stopBuiltinSupervisor(pidPath); err != nil || stopped {
				t.Fatalf("stale terminal stop=%t,%v", stopped, err)
			}
		})
	}
}

func TestBuiltinSupervisorRealProcessLifecycle(t *testing.T) {
	// No Scheduler/keychain/network calls: real TSLink processes, real events,
	// an empty registry and isolated config/APPDATA. The VM smoke adds Scheduler.
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.json"), []byte(`{"schema_version":1,"services":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "tslink.exe")
	build := exec.Command("go", "build", "-o", bin, "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build=%v %s", err, out)
	}
	env := append(os.Environ(), config.ConfigDirEnv+"="+dir, "APPDATA="+filepath.Join(root, "AppData"), "TSLINK_DISABLE_KEYRING=1", "TSLINK_API_KEY=", "TSLINK_CLIENT_SECRET=", "TSLINK_MANAGED_LOGS=1", "TSLINK_DOCTOR_SKIP_TAILSCALE_SSH=1")
	parent := exec.Command(bin, "supervise", "--no-auto-provision")
	parent.Env = env
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	joined := make(chan error, 1)
	go func() { joined <- parent.Wait() }()
	defer func() {
		_ = parent.Process.Kill()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("supervisor not reaped")
		}
	}()
	pidPath := filepath.Join(dir, "tslink.pid")
	wait := func(want string, oldPID int) builtinSupervisorRecord {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		var last builtinSupervisorRecord
		var lastErr error
		for time.Now().Before(deadline) {
			r, err := readBuiltinSupervisor(pidPath)
			last, lastErr = r, err
			if err == nil && r.State == want && (oldPID == 0 || r.DaemonPID != oldPID) {
				if want != "running" || daemon.IsRunning(pidPath) {
					return r
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("did not reach %s; last=%+v err=%v daemon_running=%t", want, last, lastErr, daemon.IsRunning(pidPath))
		return builtinSupervisorRecord{}
	}
	first := wait("running", 0)
	if alive, err := daemon.SupervisorAlive(first.Instance); err != nil || !alive {
		t.Fatalf("real identity=%t,%v", alive, err)
	}
	identityData, err := os.ReadFile(pidPath + ".identity")
	if err != nil {
		t.Fatal(err)
	}
	var childIdentity struct {
		StartUnixNano int64 `json:"start_unix_nano"`
	}
	if err := json.Unmarshal(identityData, &childIdentity); err != nil {
		t.Fatal(err)
	}
	childInstance := daemon.SupervisorInstance{PID: first.DaemonPID, StartUnixNano: childIdentity.StartUnixNano, Executable: bin}
	if alive, err := daemon.SupervisorAlive(childInstance); alive || err == nil || !strings.Contains(err.Error(), "command unverified") {
		t.Fatalf("serve child accepted as supervisor=%t,%v", alive, err)
	}
	wrong := first.Instance
	wrong.StartUnixNano++
	if alive, err := daemon.SupervisorAlive(wrong); alive || err == nil {
		t.Fatalf("reused instance accepted=%t,%v", alive, err)
	}
	wrong = first.Instance
	wrong.Executable += ".other"
	if alive, err := daemon.SupervisorAlive(wrong); alive || err == nil {
		t.Fatalf("wrong executable accepted=%t,%v", alive, err)
	}
	secondParent := exec.Command(bin, "supervise")
	secondParent.Env = env
	if out, err := secondParent.CombinedOutput(); err == nil || !strings.Contains(string(out), "supervisor already running") {
		t.Fatalf("single instance=%v %s", err, out)
	}
	child, _ := os.FindProcess(first.DaemonPID)
	killedAt := time.Now()
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Release()
	second := wait("running", first.DaemonPID)
	if elapsed := time.Since(killedAt); elapsed < time.Second || elapsed > 8*time.Second {
		t.Fatalf("first crash restart time=%s", elapsed)
	}
	// Remove only proven-dead PID evidence. Stopping the parent must still work
	// with a missing PID while it is backing off.
	child, _ = os.FindProcess(second.DaemonPID)
	if err := child.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Release()
	wait("restarting", 0)
	daemon.RemovePID(pidPath)
	if stopped, err := stopBuiltinSupervisor(pidPath); err != nil || !stopped {
		t.Fatalf("stop during backoff=%t,%v", stopped, err)
	}
	wait("stopped", 0)
	time.Sleep(2500 * time.Millisecond)
	r, err := readBuiltinSupervisor(pidPath)
	if err != nil || r.State != "stopped" || daemon.IsRunning(pidPath) {
		t.Fatalf("stop undone: %+v,%v", r, err)
	}
	if err := clearBuiltinSupervisor(pidPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readBuiltinSupervisor(pidPath); !os.IsNotExist(err) {
		t.Fatalf("state remains: %v", err)
	}
	// A breaker survives a new logon/task launch until explicit install resets it.
	path, _ := builtinSupervisorPaths(pidPath)
	r.State = "circuit_open"
	r.Reason = "eight_consecutive_unstable_runs"
	data, _ := json.Marshal(r)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	breakerCtx, breakerCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer breakerCancel()
	breaker := exec.CommandContext(breakerCtx, bin, "supervise")
	breaker.Env = env
	if out, err := breaker.CombinedOutput(); err != nil {
		t.Fatalf("breaker relaunch=%v %s", err, out)
	}
	got, _ := readBuiltinSupervisor(pidPath)
	if got.State != "circuit_open" || daemon.IsRunning(pidPath) {
		t.Fatalf("breaker reset at launch=%+v", got)
	}
}
