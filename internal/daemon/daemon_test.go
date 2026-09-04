package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var (
	_ func(string, string, string, bool, bool, bool) (int, error) = Daemonize
	_ func(string, bool, bool, bool) []string                     = daemonServeArgs
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		switch os.Getenv("TSLINK_DAEMON_TEST_MODE") {
		case "success":
			blockTestHelper(false)
		case "crash":
			// Self-kill to trigger "daemon exited during startup"
			selfKill()             // platform-split; Windows lacks syscall.Kill/SIGKILL
			blockTestHelper(false) // fallback, should not reach
		default:
			os.Exit(0)
		}
	}
	if os.Getenv("TSLINK_HELPER_PROCESS") == "1" {
		blockTestHelper(os.Getenv("TSLINK_HELPER_IGNORE_TERM") == "1" && runtime.GOOS != "windows")
	}
	os.Exit(m.Run())
}

func blockTestHelper(ignoreTerm bool) {
	ch := make(chan os.Signal, 1)
	// Registering with os/signal keeps a runtime signal goroutine alive, so the
	// helper cannot trip Go's "all goroutines are asleep" deadlock detector.
	signal.Notify(ch)
	for sig := range ch {
		if ignoreTerm && sig == syscall.SIGTERM {
			continue
		}
		os.Exit(0)
	}
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
	record, err := readProcessIdentity(path)
	if err != nil {
		t.Fatalf("readProcessIdentity() error = %v", err)
	}
	if record.Version != processIdentityVersion || record.Product != processProductID || record.PID != os.Getpid() || record.StartUnixNano == 0 {
		t.Fatalf("process identity = %+v, want version/product/current PID/start time", record)
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
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("PID perms = %o, want 600", got)
		}
	} else if !info.Mode().IsRegular() {
		t.Fatalf("PID mode = %v, want regular file on Windows", info.Mode())
	}
	if _, err := os.Stat(processIdentityPath(path)); !os.IsNotExist(err) {
		t.Fatalf("WritePIDForProcess created identity sidecar, stat error = %v", err)
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

func TestIsProcessAbsentFromPIDFileUsesConclusiveRealProcessLiveness(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(*testing.T, string)
		wantAbsent bool
	}{
		{name: "PID file missing", prepare: func(*testing.T, string) {}},
		{name: "PID file unreadable", prepare: func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "PID content is garbage", prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("not-a-pid\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "PID is negative", prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("-1\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "PID is zero", prepare: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "process definitely exited", wantAbsent: true, prepare: func(t *testing.T, path string) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			pid := cmd.Process.Pid
			if err := cmd.Wait(); err != nil {
				t.Fatalf("wait for exiting helper: %v", err)
			}
			writeLegacyPIDFile(t, path, pid)
		}},
		{name: "process is alive", prepare: func(t *testing.T, path string) {
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			})
			writeLegacyPIDFile(t, path, cmd.Process.Pid)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pidPath := filepath.Join(t.TempDir(), "tslink.pid")
			tc.prepare(t, pidPath)
			if got := IsProcessAbsentFromPIDFile(pidPath); got != tc.wantAbsent {
				t.Fatalf("IsProcessAbsentFromPIDFile() = %t, want %t", got, tc.wantAbsent)
			}
		})
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
	if _, err := os.Stat(processIdentityPath(path)); !os.IsNotExist(err) {
		t.Fatalf("identity sidecar still exists, stat error = %v", err)
	}
}

func TestIsRunningRejectsNonServeCurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for a TSLink process whose argv is not serve")
	}
}

func TestIsRunningRejectsMismatchedProcessIdentity(t *testing.T) {
	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(pid int) (string, error) {
		return filepath.Join(t.TempDir(), "not-tslink"), nil
	}

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for mismatched process identity, want false")
	}
}

func TestIsRunningAllowsSameProductAtDifferentPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("different-path helper uses Unix executable semantics")
	}
	dir := t.TempDir()
	daemonPath := filepath.Join(dir, "old-go-install", "tslink")
	copyTestExecutable(t, daemonPath)
	cmd := startCopiedHelperProcess(t, daemonPath)

	currentPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	actualPath, err := processExecutable(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("processExecutable() error = %v", err)
	}
	if filepath.Clean(actualPath) == filepath.Clean(currentPath) {
		t.Fatalf("test setup did not create distinct CLI/daemon paths: %q", actualPath)
	}

	pidPath := filepath.Join(dir, "tslink.pid")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	writeProcessIdentityForPID(t, pidPath, cmd.Process.Pid)
	if !IsRunning(pidPath) {
		t.Fatal("IsRunning() = false for sidecar-bound same-product daemon at a different install path")
	}
}

func TestIsRunningRejectsReusedPIDOwnedByUnrelatedProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep helper is Unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	if IsRunning(pidPath) {
		t.Fatal("IsRunning() = true for a live unrelated process that reused the recorded PID")
	}
}

func TestIsRunningRejectsStalePIDForDeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep helper is Unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill sleep helper: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("sleep helper unexpectedly exited successfully after Kill")
	}

	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	writeLegacyPIDFile(t, pidPath, pid)
	if IsRunning(pidPath) {
		t.Fatal("IsRunning() = true for stale PID file after process exit")
	}
}

func TestIsRunningSurvivesHomebrewSymlinkTargetChange(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew symlink simulation is Unix-only")
	}
	dir := t.TempDir()
	oldKeg := filepath.Join(dir, "Cellar", "tslink", "old", "bin", "tslink")
	newKeg := filepath.Join(dir, "Cellar", "tslink", "new", "bin", "tslink")
	link := filepath.Join(dir, "opt", "homebrew", "bin", "tslink")
	copyTestExecutable(t, oldKeg)
	copyTestExecutable(t, newKeg)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(oldKeg, link); err != nil {
		t.Fatalf("Symlink(old) error = %v", err)
	}
	oldResolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("EvalSymlinks(old) error = %v", err)
	}

	cmd := startCopiedHelperProcess(t, link)
	pidPath := filepath.Join(dir, "tslink.pid")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	writeProcessIdentityForPID(t, pidPath, cmd.Process.Pid)

	if err := os.Remove(link); err != nil {
		t.Fatalf("Remove(old symlink) error = %v", err)
	}
	if err := os.Symlink(newKeg, link); err != nil {
		t.Fatalf("Symlink(new) error = %v", err)
	}
	if err := os.Remove(oldKeg); err != nil {
		t.Fatalf("Remove(old Cellar binary) error = %v", err)
	}
	newResolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("EvalSymlinks(new) error = %v", err)
	}
	if oldResolved == newResolved {
		t.Fatalf("test setup did not change resolved Cellar target: %q", oldResolved)
	}
	if !IsRunning(pidPath) {
		t.Fatal("IsRunning() = false after Homebrew symlink target changed")
	}
}

func TestIsRunningFallsBackWhenRecordedStartTimeIsStale(t *testing.T) {
	dir := t.TempDir()
	daemonPath := filepath.Join(dir, "bin", "tslink")
	cmd := startCopiedHelperProcess(t, daemonPath)
	pidPath := filepath.Join(dir, "tslink.pid")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	writeProcessIdentityForPID(t, pidPath, cmd.Process.Pid)
	record, err := readProcessIdentity(pidPath)
	if err != nil {
		t.Fatalf("readProcessIdentity() error = %v", err)
	}
	record.StartUnixNano--
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(processIdentityPath(pidPath), data, 0o600); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
	}
	if err := verifyProcessIdentity(pidPath, cmd.Process.Pid); err != nil {
		t.Fatalf("verifyProcessIdentity() did not fall back from stale sidecar: %v", err)
	}
	if !IsRunning(pidPath) {
		t.Fatal("IsRunning() = false for live daemon with stale sidecar and valid legacy evidence")
	}
}

func TestIsRunningRejectsUnrelatedProcessEvenWithMatchingRecordedStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep helper is Unix-only")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	started, err := processStartTime(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("processStartTime() error = %v", err)
	}
	pidPath := filepath.Join(t.TempDir(), "tslink.pid")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	record := processIdentityRecord{
		Version:       processIdentityVersion,
		Product:       processProductID,
		PID:           cmd.Process.Pid,
		StartUnixNano: started.UnixNano(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(processIdentityPath(pidPath), data, 0o600); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
	}
	if IsRunning(pidPath) {
		t.Fatal("IsRunning() = true for unrelated process despite matching PID/start sidecar")
	}
}

func TestIsRunningFallsBackFromOrphanedIdentitySidecar(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")

	first := startCopiedHelperProcess(t, filepath.Join(dir, "new", "tslink"))
	writeLegacyPIDFile(t, pidPath, first.Process.Pid)
	writeProcessIdentityForPID(t, pidPath, first.Process.Pid)
	if !IsRunning(pidPath) {
		t.Fatal("first daemon was not recognized")
	}
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("kill first helper: %v", err)
	}
	_ = first.Wait()
	if err := os.Remove(pidPath); err != nil {
		t.Fatalf("simulate legacy RemovePID: %v", err)
	}

	second := startCopiedHelperProcess(t, filepath.Join(dir, "old-go-install", "tslink"))
	if err := WritePIDForProcess(pidPath, second.Process.Pid); err != nil {
		t.Fatalf("simulate legacy WritePID: %v", err)
	}
	if err := verifyProcessIdentity(pidPath, second.Process.Pid); err != nil {
		t.Fatalf("orphaned sidecar did not fall back to legacy evidence: %v", err)
	}
	if !IsRunning(pidPath) {
		t.Fatal("live mixed-version daemon was hidden by an orphaned sidecar")
	}
}

func TestIsRunningFallsBackFromDamagedIdentitySidecar(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"zero-byte", nil},
		{"truncated-json", []byte(`{"version":1,"product":"github.com/mono`)},
		{"future-schema", []byte(`{"version":2,"product":"github.com/monody0007/tslink","pid":1,"start_unix_nano":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "tslink.pid")
			cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
			writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
			if err := os.WriteFile(processIdentityPath(pidPath), tc.body, 0o600); err != nil {
				t.Fatalf("WriteFile(identity) error = %v", err)
			}
			if err := verifyProcessIdentity(pidPath, cmd.Process.Pid); err != nil {
				t.Fatalf("damaged sidecar did not fall back to legacy evidence: %v", err)
			}
			if !IsRunning(pidPath) {
				t.Fatal("damaged sidecar hid a live daemon")
			}
		})
	}
}

func TestIsRunningFallsBackWhenIdentitySidecarIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	writeProcessIdentityForPID(t, pidPath, cmd.Process.Pid)

	orig := readProcessIdentityData
	t.Cleanup(func() { readProcessIdentityData = orig })
	readProcessIdentityData = func(path string) ([]byte, error) {
		if path == processIdentityPath(pidPath) {
			return nil, os.ErrPermission
		}
		return orig(path)
	}
	if err := verifyProcessIdentity(pidPath, cmd.Process.Pid); err != nil {
		t.Fatalf("unreadable sidecar did not fall back to legacy evidence: %v", err)
	}
	if !IsRunning(pidPath) {
		t.Fatal("unreadable sidecar hid a live daemon")
	}
}

func TestIsRunningFailsClosedWhenBuildMetadataIsUnavailable(t *testing.T) {
	for _, message := range []string{"injected EIO", "unrecognized file format"} {
		t.Run(message, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "tslink.pid")
			cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
			writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)

			orig := readExecutableBuildInfo
			t.Cleanup(func() { readExecutableBuildInfo = orig })
			readExecutableBuildInfo = func(string) (*debug.BuildInfo, error) {
				return nil, errors.New(message)
			}
			if !IsRunning(pidPath) {
				t.Fatal("build metadata failure was treated as not running")
			}
		})
	}
}

func TestIsRunningFailsClosedWhenProcessInspectionIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)

	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(int) (string, error) {
		return "", errors.New("injected process inspection failure")
	}
	if !IsRunning(pidPath) {
		t.Fatal("process inspection failure was treated as not running")
	}
}

func TestIsRunningFailsClosedWhenStartTimeInspectionIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)

	orig := processStartTime
	t.Cleanup(func() { processStartTime = orig })
	processStartTime = func(int) (time.Time, error) {
		return time.Time{}, errors.New("injected native process inspection failure")
	}
	if !IsRunning(pidPath) {
		t.Fatal("start-time inspection failure was treated as not running")
	}
}

func TestIsRunningFailsClosedWhenProcessInspectionPermissionDenied(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)

	orig := processExecutable
	t.Cleanup(func() { processExecutable = orig })
	processExecutable = func(int) (string, error) { return "", os.ErrPermission }
	if !IsRunning(pidPath) {
		t.Fatal("permission-denied process inspection was treated as not running")
	}
}

func TestIsRunningRejectsNonServeTSLinkProcess(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	binPath := filepath.Join(dir, "bin", "tslink")
	copyTestExecutable(t, binPath)
	cmd := startCopiedHelperProcessWithArgs(t, binPath, "logs", "-f")
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	if IsRunning(pidPath) {
		t.Fatal("non-serve TSLink process was accepted as the daemon")
	}
}

func TestIsRunningSurvivesUnlinkedBinaryAtSpacePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("running Windows executables cannot normally be unlinked")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "tslink.pid")
	binPath := filepath.Join(dir, "my go bin", "tslink")
	copyTestExecutable(t, binPath)
	cmd := startCopiedHelperProcess(t, binPath)
	writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
	if err := os.Remove(binPath); err != nil {
		t.Fatalf("Remove(binary) error = %v", err)
	}
	if !IsRunning(pidPath) {
		t.Fatal("unlinked serve binary at a path containing spaces was not recognized")
	}
}

func TestLegacyPIDStartToleranceBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		offset  time.Duration
		running bool
	}{
		{"within-90-seconds", 90 * time.Second, true},
		{"outside-3-minutes", 3 * time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pidPath := filepath.Join(dir, "tslink.pid")
			cmd := startCopiedHelperProcess(t, filepath.Join(dir, "bin", "tslink"))
			writeLegacyPIDFile(t, pidPath, cmd.Process.Pid)
			started, err := processStartTime(cmd.Process.Pid)
			if err != nil {
				t.Fatalf("processStartTime() error = %v", err)
			}
			stamp := started.Add(tc.offset)
			if err := os.Chtimes(pidPath, stamp, stamp); err != nil {
				t.Fatalf("Chtimes() error = %v", err)
			}
			if got := IsRunning(pidPath); got != tc.running {
				t.Fatalf("IsRunning() = %v, want %v at offset %s", got, tc.running, tc.offset)
			}
		})
	}
}

func TestLegacyProcessProductFallbackRequiresPlatformExecutableName(t *testing.T) {
	orig := processArguments
	t.Cleanup(func() { processArguments = orig })
	processArguments = func(int) ([]string, error) {
		return []string{processExecutableBaseName(), "serve"}, nil
	}

	if err := legacyProcessProductFallback(4242, filepath.Join(t.TempDir(), processExecutableBaseName())); err != nil {
		t.Fatalf("legacyProcessProductFallback() rejected platform executable name %q: %v", processExecutableBaseName(), err)
	}
	wrongBase := "tslink.exe"
	if runtime.GOOS == "windows" {
		wrongBase = "tslink"
	}
	if err := legacyProcessProductFallback(4242, filepath.Join(t.TempDir(), wrongBase)); !errors.Is(err, errIdentityMismatch) {
		t.Fatalf("legacyProcessProductFallback() error = %v, want identity mismatch for basename %q", err, wrongBase)
	}
}

func copyTestExecutable(t *testing.T, destination string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(destination), err)
	}
	if err := os.WriteFile(destination, data, 0o700); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", destination, err)
	}
}

func startCopiedHelperProcess(t *testing.T, executablePath string) *exec.Cmd {
	return startCopiedHelperProcessWithArgs(t, executablePath, "serve")
}

func startCopiedHelperProcessWithArgs(t *testing.T, executablePath string, args ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(executablePath), ".exe") {
		executablePath += ".exe"
	}
	if _, err := os.Stat(executablePath); errors.Is(err, os.ErrNotExist) {
		copyTestExecutable(t, executablePath)
	} else if err != nil {
		t.Fatalf("Stat(%q) error = %v", executablePath, err)
	}
	cmd := exec.Command(executablePath, args...)
	if len(args) > 0 && args[0] == "serve" {
		cmd.Env = append(os.Environ(), "TSLINK_DAEMON_TEST_MODE=success")
	} else {
		cmd.Env = append(os.Environ(), "TSLINK_HELPER_PROCESS=1")
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start copied helper %q: %v", executablePath, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func writeLegacyPIDFile(t *testing.T, path string, pid int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func writeProcessIdentityForPID(t *testing.T, pidPath string, pid int) {
	t.Helper()
	started, err := processStartTime(pid)
	if err != nil {
		t.Fatalf("processStartTime(%d) error = %v", pid, err)
	}
	record := processIdentityRecord{
		Version:       processIdentityVersion,
		Product:       processProductID,
		PID:           pid,
		StartUnixNano: started.UnixNano(),
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(processIdentityPath(pidPath), append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile(identity) error = %v", err)
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

	_, err := Daemonize(filepath.Join(parent, "stdout.log"), filepath.Join(t.TempDir(), "stderr.log"), "", false, false, false)
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

	_, err := Daemonize(outLog, filepath.Join(dir, "stderr.log"), "", false, false, false)
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

	_, err := Daemonize(filepath.Join(dir, "stdout.log"), errLog, "", false, false, false)
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

	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(errParent, "stderr.log"), "", false, false, false)
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

	pid, err := Daemonize(outLog, errLog, "", false, false, false)
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
	pid, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "https://headscale.example.com", false, false, false)
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
func TestDaemonizeServeFlagPropagation(t *testing.T) {
	t.Setenv("TSLINK_DAEMON_TEST_MODE", "success")

	cases := []struct {
		name       string
		controlURL string
		manageACL  bool
		noAuto     bool
		mcp        bool
		want       []string
	}{
		{"defaults carry no flag", "", false, false, false, []string{"serve"}},
		{"manage ACL carries flag once", "", true, false, false, []string{"serve", "--manage-acl"}},
		{"kill switch carries flag once", "", false, true, false, []string{"serve", "--no-auto-provision"}},
		{"mcp control plane carries flag once", "", false, false, true, []string{"serve", "--mcp"}},
		{"both choices with control url", "https://headscale.example.com", true, true, false, []string{"serve", "--control-url", "https://headscale.example.com", "--manage-acl", "--no-auto-provision"}},
		{"every opt-in together", "https://headscale.example.com", true, true, true, []string{"serve", "--control-url", "https://headscale.example.com", "--manage-acl", "--no-auto-provision", "--mcp"}},
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
			pid, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), tc.controlURL, tc.manageACL, tc.noAuto, tc.mcp)
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
			noAutoCount := 0
			for _, a := range gotArgs {
				if a == "--no-auto-provision" {
					noAutoCount++
				}
			}
			wantNoAutoCount := 0
			if tc.noAuto {
				wantNoAutoCount = 1
			}
			if noAutoCount != wantNoAutoCount {
				t.Fatalf("--no-auto-provision appeared %d times, want %d; argv=%q", noAutoCount, wantNoAutoCount, gotArgs)
			}
			// A dropped --mcp would leave the daemon child serving no control
			// plane while the parent reported success; a duplicated one would
			// be a cobra parse error at startup.
			mcpCount := 0
			for _, a := range gotArgs {
				if a == "--mcp" {
					mcpCount++
				}
			}
			wantMCPCount := 0
			if tc.mcp {
				wantMCPCount = 1
			}
			if mcpCount != wantMCPCount {
				t.Fatalf("--mcp appeared %d times, want %d; argv=%q", mcpCount, wantMCPCount, gotArgs)
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
	origArgs := processArguments
	t.Cleanup(func() {
		processExecutable = orig
		processArguments = origArgs
	})
	processExecutable = func(gotPID int) (string, error) {
		if gotPID == pid {
			return exe, nil
		}
		return orig(gotPID)
	}
	processArguments = func(gotPID int) ([]string, error) {
		if gotPID == pid {
			return []string{exe, "serve"}, nil
		}
		return origArgs(gotPID)
	}
}

func TestIsRunningAcceptsVerifiedServeProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	stubProcessExecutableForPID(t, os.Getpid())

	if !IsRunning(path) {
		t.Fatal("IsRunning() = false for a verified live serve process")
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

func TestIsRunningFailsClosedOnProcessLivenessError(t *testing.T) {
	stubProcessLivenessError(t)

	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if !IsRunning(path) {
		t.Fatal("IsRunning() = false when process liveness inspection is inconclusive")
	}
}

func TestStopDaemon_FindProcessError(t *testing.T) {
	stubStopProcessLookupError(t)

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
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false, false, false)
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
	_, err := Daemonize(filepath.Join(dir, "stdout.log"), filepath.Join(dir, "stderr.log"), "", false, false, false)
	if err == nil {
		t.Fatal("Daemonize() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "start daemon") {
		t.Fatalf("Daemonize() error = %v, want start daemon error", err)
	}
}
