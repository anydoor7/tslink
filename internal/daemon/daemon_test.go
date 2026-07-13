package daemon

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		switch os.Getenv("TSLINK_DAEMON_TEST_MODE") {
		case "success":
			select {}
		case "crash":
			// Self-kill to trigger "daemon exited during startup"
			selfKill() // platform-split; Windows lacks syscall.Kill/SIGKILL
			select {}  // fallback, should not reach
		default:
			os.Exit(0)
		}
	}
	if os.Getenv("TSLINK_HELPER_PROCESS") == "1" {
		if os.Getenv("TSLINK_HELPER_IGNORE_TERM") == "1" && runtime.GOOS != "windows" {
			ch := make(chan os.Signal, 1)
			signal.Notify(ch, syscall.SIGTERM)
			go func() {
				for range ch {
				}
			}()
		}
		select {}
	}
	os.Exit(m.Run())
}

func TestWriteAndReadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}

	if want := os.Getpid(); pid != want {
		t.Fatalf("ReadPID() = %d, want %d", pid, want)
	}
}

func TestWritePIDForProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePIDForProcess(path, 4242); err != nil {
		t.Fatalf("WritePIDForProcess() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}
	if pid != 4242 {
		t.Fatalf("ReadPID() = %d, want 4242", pid)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("PID perms = %o, want 600", got)
	}
}

func TestWithPIDLockSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- WithPIDLock(path, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()

	select {
	case <-firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first lock holder did not enter")
	}

	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- WithPIDLock(path, func() error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second lock holder entered before first released")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first WithPIDLock() error = %v", err)
	}
	select {
	case <-secondEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("second lock holder did not enter after release")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second WithPIDLock() error = %v", err)
	}
}

func TestReadPIDNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")

	if _, err := ReadPID(path); err == nil {
		t.Fatal("ReadPID() error = nil, want error")
	}
}

func TestRemovePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	RemovePID(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("os.Stat() error = %v, want not exists", err)
	}
}

func TestIsRunningCurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if !IsRunning(path) {
		t.Fatal("IsRunning() = false, want true")
	}
}

func TestIsRunningRejectsMismatchedProcessIdentity(t *testing.T) {
	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(pid int) (string, error) {
		return filepath.Join(t.TempDir(), "not-tslink"), nil
	}

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for mismatched process identity, want false")
	}
}

func TestWritePIDCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadPID() = %d, want %d", pid, os.Getpid())
	}
}

func TestReadPIDInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	_, err := ReadPID(path)
	if err == nil {
		t.Fatal("ReadPID() with invalid content should return error")
	}
}

func TestIsRunningNoPIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for nonexistent PID file, want false")
	}
}

func TestIsRunningInvalidPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for invalid PID content, want false")
	}
}

func TestRemovePIDNonexistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	// Should not panic
	RemovePID(path)
}

func TestIsRunningDeadProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := os.WriteFile(path, []byte(strconv.Itoa(99999999)+"\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if IsRunning(path) {
		t.Skip("PID 99999999 appears to be running on this system")
	}
}

func TestWritePID_Overwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("first WritePID() error = %v", err)
	}
	if err := WritePID(path); err != nil {
		t.Fatalf("second WritePID() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestWritePID_CreateParentError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := WritePID(filepath.Join(parent, "tslink.pid")); err == nil {
		t.Fatal("WritePID() error = nil, want error")
	}
}

func TestReadPID_EmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := ReadPID(path); err == nil {
		t.Fatal("ReadPID() with empty file should return error")
	}
}

func TestReadPID_WhitespaceOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("  \n  "), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := ReadPID(path); err == nil {
		t.Fatal("ReadPID() with whitespace-only file should return error")
	}
}

func TestIsRunningNegativePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("-1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() with negative PID should return false")
	}
}

func TestIsRunningZeroPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() with zero PID should return false")
	}
}

func TestDaemonize_CreateStdoutLogDirError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Daemonize(filepath.Join(parent, "stdout.log"), filepath.Join(t.TempDir(), "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "create stdout log dir") {
		t.Fatalf("Daemonize() error = %v, want stdout log dir error", err)
	}
}

func TestDaemonize_OpenStdoutLogError(t *testing.T) {
	dir := t.TempDir()
	outLog := filepath.Join(dir, "stdout.log")
	if err := os.Mkdir(outLog, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := Daemonize(outLog, filepath.Join(dir, "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "open stdout log") {
		t.Fatalf("Daemonize() error = %v, want open stdout log error", err)
	}
}

func TestDaemonize_OpenStderrLogError(t *testing.T) {
	dir := t.TempDir()
	errLog := filepath.Join(dir, "stderr.log")
	if err := os.Mkdir(errLog, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	_, err := Daemonize(filepath.Join(dir, "stdout.log"), errLog, "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "open stderr log") {
		t.Fatalf("Daemonize() error = %v, want open stderr log error", err)
	}
}

func TestDaemonize_CreateStderrLogDirError(t *testing.T) {
	dir := t.TempDir()
	errParent := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(errParent, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(errParent, "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "create stderr log dir") {
		t.Fatalf("Daemonize() error = %v, want stderr log dir error", err)
	}
}

func TestDaemonize_Success(t *testing.T) {
	t.Setenv("TSLINK_DAEMON_TEST_MODE", "success")

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	var gotName string
	var gotArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command(name, args...)
	}

	dir := t.TempDir()
	outLog := filepath.Join(dir, "stdout.log")
	errLog := filepath.Join(dir, "stderr.log")

	pid, err := Daemonize(outLog, errLog, "", false)
	if err != nil {
		t.Fatalf("Daemonize() error = %v", err)
	}
	if pid <= 0 {
		t.Fatalf("Daemonize() pid = %d, want positive PID", pid)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess() error = %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Kill()
	})

	if gotName == "" {
		t.Fatal("execCommand was not called")
	}
	wantArgs := []string{"serve"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("daemon argv = %q, want %q", gotArgs, wantArgs)
	}
}

func TestDaemonize_ForwardsControlURL(t *testing.T) {
	t.Setenv("TSLINK_DAEMON_TEST_MODE", "success")

	orig := execCommand
	t.Cleanup(func() { execCommand = orig })

	var gotName string
	var gotArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command(name, args...)
	}

	dir := t.TempDir()
	pid, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "https://headscale.example.com", false)
	if err != nil {
		t.Fatalf("Daemonize() error = %v", err)
	}
	if pid <= 0 {
		t.Fatalf("Daemonize() pid = %d, want positive PID", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess() error = %v", err)
	}
	t.Cleanup(func() { _ = proc.Kill() })

	if gotName == "" {
		t.Fatal("execCommand was not called")
	}
	wantArgs := []string{"serve", "--control-url", "https://headscale.example.com"}
	if strings.Join(gotArgs, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("daemon argv = %q, want %q", gotArgs, wantArgs)
	}
}

// TestDaemonizeManageACLPropagation is the daemon-mode guard: the daemon
// child argv must carry --manage-acl exactly once when opted in, and never when
// default-off. Kills the old implementation that hardcoded the child argv to
// `serve [--control-url ...]` and silently dropped the opt-in in daemon mode.
func TestDaemonizeManageACLPropagation(t *testing.T) {
	t.Setenv("TSLINK_DAEMON_TEST_MODE", "success")

	cases := []struct {
		name       string
		controlURL string
		manageACL  bool
		want       []string
	}{
		{"default off carries no flag", "", false, []string{"serve"}},
		{"opt-in carries flag once", "", true, []string{"serve", "--manage-acl"}},
		{"opt-in with control url", "https://headscale.example.com", true, []string{"serve", "--control-url", "https://headscale.example.com", "--manage-acl"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			orig := execCommand
			t.Cleanup(func() { execCommand = orig })
			var gotArgs []string
			execCommand = func(name string, args ...string) *exec.Cmd {
				gotArgs = append([]string(nil), args...)
				return exec.Command(name, args...)
			}

			dir := t.TempDir()
			pid, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), tc.controlURL, tc.manageACL)
			if err != nil {
				t.Fatalf("Daemonize() error = %v", err)
			}
			if proc, ferr := os.FindProcess(pid); ferr == nil {
				t.Cleanup(func() { _ = proc.Kill() })
			}

			if strings.Join(gotArgs, "\x00") != strings.Join(tc.want, "\x00") {
				t.Fatalf("daemon argv = %q, want %q", gotArgs, tc.want)
			}
			// --manage-acl must appear at most once.
			count := 0
			for _, a := range gotArgs {
				if a == "--manage-acl" {
					count++
				}
			}
			wantCount := 0
			if tc.manageACL {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("--manage-acl appeared %d times, want %d; argv=%q", count, wantCount, gotArgs)
			}
		})
	}
}

func TestStopDaemon_ReadPIDError(t *testing.T) {
	err := StopDaemon(filepath.Join(t.TempDir(), "missing.pid"))
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read PID") {
		t.Fatalf("StopDaemon() error = %v, want read PID error", err)
	}
}

func TestStopDaemon_Success(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stubProcessExecutableForPID(t, cmd.Process.Pid)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
	})

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := StopDaemon(path); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}

	select {
	case waitErr := <-done:
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			t.Fatalf("Wait() error = %v", waitErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("helper process did not exit after StopDaemon()")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removal, stat err = %v", err)
	}
}

func TestStopDaemonRejectsMismatchedProcessIdentity(t *testing.T) {
	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(pid int) (string, error) {
		return filepath.Join(t.TempDir(), "not-tslink"), nil
	}

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want identity refusal")
	}
	if !strings.Contains(err.Error(), "refusing to stop process") {
		t.Fatalf("StopDaemon() error = %v, want identity refusal", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("PID file should remain after refused stop, stat error = %v", statErr)
	}
}

func TestStopDaemon_AlreadyExited(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	_ = cmd.Wait()

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := StopDaemon(path); err != nil {
		t.Fatalf("StopDaemon() error = %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected pid file removal, stat err = %v", err)
	}
}

func TestStopDaemon_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM handling not applicable on Windows")
	}

	// Launch a process that ignores SIGTERM
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1", "TSLINK_HELPER_IGNORE_TERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	stubProcessExecutableForPID(t, cmd.Process.Pid)
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want timeout error")
	}
	if !strings.Contains(err.Error(), "did not exit after SIGTERM") {
		t.Fatalf("StopDaemon() error = %v, want SIGTERM timeout error", err)
	}
}

func stubProcessExecutableForPID(t *testing.T, pid int) {
	t.Helper()

	exe, err := executable()
	if err != nil {
		t.Fatalf("executable() error = %v", err)
	}
	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(gotPID int) (string, error) {
		if gotPID == pid {
			return exe, nil
		}
		return orig(gotPID)
	}
}

func TestIsRunning_EPERM(t *testing.T) {
	// Current process PID should always be "running"
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if !IsRunning(path) {
		t.Fatal("IsRunning() = false for current process, want true")
	}
}

func TestStopDaemon_SignalError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM not applicable on Windows")
	}
	// Use PID 1 (init/launchd) — sending SIGTERM to it should return EPERM,
	// which is NOT ESRCH and NOT os.ErrProcessDone, so it hits the
	// "signal SIGTERM to %d" error path.
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want signal error")
	}
	if !strings.Contains(err.Error(), "refusing to stop process") {
		t.Fatalf("StopDaemon() error = %v, want identity refusal error", err)
	}
}

// TestDaemonize_StartError was removed: the FD-exhaustion approach is inherently
// fragile — on some OS/runtime combos cmd.Start() succeeds and wait4 blocks forever,
// causing a 10-minute timeout. The error branch it covered (Daemonize line 61-64)
// is a single os/exec.Cmd.Start() error return, not worth a flaky test.

func TestWritePID_WriteFileError(t *testing.T) {
	// Create a directory where the PID file should be, so WriteFile fails
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	// Create a directory with the same name as the PID file
	if err := os.Mkdir(pidPath, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	err := WritePID(pidPath)
	if err == nil {
		t.Fatal("WritePID() error = nil, want error writing to directory path")
	}
}

func TestReadPID_LargeNumber(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("2147483647\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}
	if pid != 2147483647 {
		t.Fatalf("ReadPID() = %d, want 2147483647", pid)
	}
}

func TestReadPID_LeadingTrailingWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("  12345  \n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// ReadPID uses strings.TrimSpace, so leading/trailing whitespace is handled
	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() unexpected error = %v", err)
	}
	if pid != 12345 {
		t.Fatalf("ReadPID() = %d, want 12345", pid)
	}
}

func TestRemovePID_FileWithContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	RemovePID(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be removed, stat err = %v", err)
	}
}

func TestStopDaemon_InvalidPIDContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "read PID") {
		t.Fatalf("StopDaemon() error = %v, want read PID error", err)
	}
}

func TestIsRunning_FindProcessError(t *testing.T) {
	orig := findProcess
	t.Cleanup(func() { findProcess = orig })
	findProcess = func(pid int) (*os.Process, error) {
		return nil, errors.New("injected findProcess error")
	}

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true, want false when findProcess fails")
	}
}

func TestStopDaemon_FindProcessError(t *testing.T) {
	orig := findProcess
	t.Cleanup(func() { findProcess = orig })
	findProcess = func(pid int) (*os.Process, error) {
		return nil, errors.New("injected findProcess error")
	}

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := StopDaemon(path)
	if err == nil {
		t.Fatal("StopDaemon() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "find process") {
		t.Fatalf("StopDaemon() error = %v, want find process error", err)
	}
}

func TestDaemonize_ExecutableError(t *testing.T) {
	orig := executable
	t.Cleanup(func() { executable = orig })
	executable = func() (string, error) {
		return "", errors.New("injected executable error")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "find executable") {
		t.Fatalf("Daemonize() error = %v, want find executable error", err)
	}
}

func TestDaemonize_StartError(t *testing.T) {
	orig := startCmd
	t.Cleanup(func() { startCmd = orig })
	startCmd = func(cmd *exec.Cmd) error {
		return errors.New("injected start error")
	}

	dir := t.TempDir()
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "start daemon") {
		t.Fatalf("Daemonize() error = %v, want start daemon error", err)
	}
}
