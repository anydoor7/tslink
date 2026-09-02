package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
)

// Round C-1 process-level e2e scaffolding.
//
// These helpers extend the four existing compiled-binary contract suites
// (api_binary_contract_test.go, machine_contract_binary_test.go,
// doctor_binary_contract_test.go, compiled_daemon_lifetime_unix_test.go)
// rather than starting a parallel harness. Concretely they reuse:
//
//   - compiledTSLinkBinary: the sync.Once "build the shipped binary exactly
//     once per package test run" contract, and its temp directory, so this
//     suite adds zero additional leaked build directories.
//   - the `go build -overlay` fake-daemon technique from
//     compiledDaemonIdentityFixture: build the real module main package with
//     main() swapped for a stub, so the resulting process is indistinguishable
//     from a real serve daemon to daemon.verifyProcessIdentity (same Go module
//     path, argv[1] == "serve", real PID identity sidecar) without ever
//     contacting a control plane.
//   - runTSLinkBinaryWithConfigDir's environment isolation shape.
//
// PROCESS SAFETY CONTRACT (non-negotiable, see report §4):
//   - Every process this suite starts is started by this suite and its PID is
//     recorded in an *exec.Cmd. Termination is always by that recorded handle.
//   - Nothing here ever matches processes by name. e2eLivePIDsForBinary only
//     matches an absolute path that this test run created under the per-run
//     temp build directory, and e2eRequireTempPath fails closed if that path is
//     not under os.TempDir(). A production daemon installed at, for example,
//     /Users/<user>/go/bin/tslink is therefore structurally unmatchable.
//   - No helper reads or writes the real ~/.config/tslink. Every caller passes
//     an explicit t.TempDir() config directory.

const (
	// e2eDaemonReadyLine is what the overlay fake daemon prints once it has
	// published its PID identity sidecar.
	e2eDaemonReadyLine = "ready\n"

	// e2eCommandTimeout bounds every non-daemon CLI invocation so a hung child
	// fails the test instead of hanging the package.
	e2eCommandTimeout = 30 * time.Second

	// e2eDaemonReadyTimeout bounds fake-daemon startup.
	e2eDaemonReadyTimeout = 30 * time.Second
)

var (
	e2eSecondaryBinaryOnce sync.Once
	e2eSecondaryBinaryPath string
	e2eSecondaryBinaryErr  error

	e2eDaemonBinaryOnce sync.Once
	e2eDaemonBinaryPath string
	e2eDaemonBinaryErr  error
)

// e2eRequireTempPath fails the test unless path is inside the OS temp root.
// Every destructive or process-matching helper routes through this, so a bug in
// a caller cannot aim this suite at an installed binary or the real config dir.
func e2eRequireTempPath(t *testing.T, path string) string {
	t.Helper()
	tempRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}
	resolvedDir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	resolved := filepath.Join(resolvedDir, filepath.Base(path))
	rel, err := filepath.Rel(tempRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("refusing to operate on %q: not under the OS temp root %q", path, tempRoot)
	}
	return resolved
}

// e2eBinaryDir returns the temp directory the package's single compiled binary
// already lives in. Reusing it keeps the "build once per package run" property
// and avoids creating a second temp tree.
func e2eBinaryDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Dir(compiledTSLinkBinary(t))
	e2eRequireTempPath(t, filepath.Join(dir, "tslink"))
	return dir
}

func e2eExeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func e2eRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository root")
	}
	return filepath.Dir(filepath.Dir(file))
}

// e2eSecondaryCLIBinary builds a second copy of the real shipped binary at a
// distinct absolute path. Together with e2eFakeDaemonBinary this reproduces the
// "two installs of tslink on one machine sharing one state directory" shape
// that caused the 2026-06-03 and 2026-08-19 double-daemon incidents. It is
// built exactly once per package test run.
func e2eSecondaryCLIBinary(t *testing.T) string {
	t.Helper()
	binDir := e2eBinaryDir(t)
	repoRoot := e2eRepoRoot(t)
	e2eSecondaryBinaryOnce.Do(func() {
		outDir := filepath.Join(binDir, "install-b")
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			e2eSecondaryBinaryErr = err
			return
		}
		out := filepath.Join(outDir, e2eExeName("tslink"))
		build := exec.Command("go", "build", "-o", out, ".")
		build.Dir = repoRoot
		if combined, err := build.CombinedOutput(); err != nil {
			e2eSecondaryBinaryErr = fmt.Errorf("build secondary tslink copy: %w\n%s", err, combined)
			return
		}
		e2eSecondaryBinaryPath = out
	})
	if e2eSecondaryBinaryErr != nil {
		t.Fatalf("%v", e2eSecondaryBinaryErr)
	}
	return e2eSecondaryBinaryPath
}

// e2eFakeDaemonBinary builds the real module main package with main() replaced
// by a stub that publishes a genuine PID identity sidecar and then blocks on
// stdin. This is the same -overlay technique already used by
// compiledDaemonIdentityFixture; the difference is that the output basename is
// "tslink" and it lives at its own install-a/ path, so the daemon's executable
// path differs from every CLI copy exactly the way two real installs differ.
//
// A real `tslink serve` cannot be used here: it would start tsnet nodes and
// contact the Tailscale control plane, which this task forbids.
func e2eFakeDaemonBinary(t *testing.T) string {
	t.Helper()
	binDir := e2eBinaryDir(t)
	repoRoot := e2eRepoRoot(t)
	e2eDaemonBinaryOnce.Do(func() {
		outDir := filepath.Join(binDir, "install-a")
		if err := os.MkdirAll(outDir, 0o700); err != nil {
			e2eDaemonBinaryErr = err
			return
		}
		fixtureDir := filepath.Join(binDir, "fake-daemon-src")
		if err := os.MkdirAll(fixtureDir, 0o700); err != nil {
			e2eDaemonBinaryErr = err
			return
		}
		source := filepath.Join(fixtureDir, "main.go")
		if err := os.WriteFile(source, []byte(e2eFakeDaemonSource), 0o600); err != nil {
			e2eDaemonBinaryErr = err
			return
		}
		overlay, err := json.Marshal(struct {
			Replace map[string]string `json:"Replace"`
		}{Replace: map[string]string{
			filepath.ToSlash(filepath.Join(repoRoot, "main.go")): filepath.ToSlash(source),
		}})
		if err != nil {
			e2eDaemonBinaryErr = err
			return
		}
		overlayPath := filepath.Join(fixtureDir, "overlay.json")
		if err := os.WriteFile(overlayPath, overlay, 0o600); err != nil {
			e2eDaemonBinaryErr = err
			return
		}
		out := filepath.Join(outDir, e2eExeName("tslink"))
		build := exec.Command("go", "build", "-overlay", overlayPath, "-o", out, ".")
		build.Dir = repoRoot
		if combined, err := build.CombinedOutput(); err != nil {
			e2eDaemonBinaryErr = fmt.Errorf("build fake daemon binary: %w\n%s", err, combined)
			return
		}
		e2eDaemonBinaryPath = out
	})
	if e2eDaemonBinaryErr != nil {
		t.Fatalf("%v", e2eDaemonBinaryErr)
	}
	return e2eDaemonBinaryPath
}

const e2eFakeDaemonSource = `package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/daemon"
)

func main() {
	configDir := os.Getenv("TSLINK_CONFIG_DIR")
	if configDir == "" {
		fmt.Fprintln(os.Stderr, "TSLINK_CONFIG_DIR is required")
		os.Exit(1)
	}
	if err := daemon.WritePID(filepath.Join(configDir, "tslink.pid")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := fmt.Fprintln(os.Stdout, "ready"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
`

// e2eDaemonHandle is a fake daemon this test run owns end to end.
type e2eDaemonHandle struct {
	PID        int
	BinaryPath string
	ConfigDir  string

	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
}

// e2eStartFakeDaemon starts the overlay fake daemon against configDir, waits
// for it to publish its PID identity, and registers deterministic reclamation.
//
// Reclamation order: close stdin (the stub's io.Copy returns and it exits
// normally) -> wait with a bounded deadline -> Kill() the recorded PID only if
// it is still alive -> Wait() to reap. Nothing is ever matched by name.
func e2eStartFakeDaemon(t *testing.T, configDir string) *e2eDaemonHandle {
	t.Helper()
	binary := e2eFakeDaemonBinary(t)

	cmd := exec.Command(binary, "serve")
	cmd.Env = append(os.Environ(),
		"TSLINK_CONFIG_DIR="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("fake daemon stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("fake daemon stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fake daemon: %v", err)
	}

	handle := &e2eDaemonHandle{
		PID:        cmd.Process.Pid,
		BinaryPath: binary,
		ConfigDir:  configDir,
		cmd:        cmd,
		stdin:      stdin,
		done:       make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(handle.done)
	}()
	t.Cleanup(handle.stopAndReap)

	readyCh := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		readyCh <- line
	}()
	select {
	case line := <-readyCh:
		if line != e2eDaemonReadyLine {
			t.Fatalf("fake daemon readiness = %q, want %q (stderr=%q)", line, e2eDaemonReadyLine, stderr.String())
		}
	case <-time.After(e2eDaemonReadyTimeout):
		t.Fatalf("fake daemon did not become ready within %s (stderr=%q)", e2eDaemonReadyTimeout, stderr.String())
	}

	pidPath := filepath.Join(configDir, "tslink.pid")
	published, err := daemon.ReadPID(pidPath)
	if err != nil {
		t.Fatalf("read fake daemon PID: %v", err)
	}
	if published != handle.PID {
		t.Fatalf("published PID = %d, want the process this test started (%d)", published, handle.PID)
	}

	// Readiness is confirmed against the OS, not against daemon.IsRunning.
	// Whether the product recognises this daemon is what several scenarios
	// below are measuring; using that predicate as a precondition here would
	// make the measurement self-referential and would convert a genuine
	// recognition regression into a setup error, hiding which assertion
	// actually caught it.
	live := e2eLivePIDsForBinary(t, binary)
	if len(live) != 1 || live[0] != handle.PID {
		t.Fatalf("fake daemon liveness = %v, want exactly [%d]", live, handle.PID)
	}
	return handle
}

// stopAndReap terminates only the recorded PID owned by this handle.
func (h *e2eDaemonHandle) stopAndReap() {
	_ = h.stdin.Close()
	select {
	case <-h.done:
		return
	case <-time.After(5 * time.Second):
	}
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
	}
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
	}
}

// Exited reports whether the owned process has already been reaped.
func (h *e2eDaemonHandle) Exited() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// WaitExit blocks until the owned process exits or the deadline elapses.
func (h *e2eDaemonHandle) WaitExit(d time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(d):
		return false
	}
}

// e2eEnv builds the isolated child environment shared by every e2e invocation.
// It mirrors runTSLinkBinaryWithConfigDir and adds nothing that could reach a
// real credential store or a real control plane.
func e2eEnv(configDir string, extra ...string) []string {
	env := append(os.Environ(),
		"TSLINK_CONFIG_DIR="+configDir,
		"TSLINK_DISABLE_KEYRING=1",
		"TSLINK_API_KEY=",
		"TSLINK_CLIENT_SECRET=",
		testDaemonParentLifetimeEnv+"=1",
	)
	return append(env, extra...)
}

type e2eRun struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Elapsed  time.Duration
}

// e2eRunBinary invokes a compiled tslink binary with a bounded deadline.
func e2eRunBinary(t *testing.T, binary, configDir, stdin string, env []string, args ...string) e2eRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), e2eCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	result := e2eRun{Stdout: outBuf.String(), Stderr: errBuf.String(), Elapsed: elapsed}
	if runErr != nil {
		exitErr, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s %v: %v (stderr=%q)", filepath.Base(binary), args, runErr, errBuf.String())
		}
		result.ExitCode = exitErr.ExitCode()
	}
	if ctx.Err() != nil {
		t.Fatalf("run %s %v exceeded %s", filepath.Base(binary), args, e2eCommandTimeout)
	}
	return result
}

// e2eDecodeEnvelope requires stdout to be exactly one well-formed result
// envelope and returns it together with its raw data object.
func e2eDecodeEnvelope(t *testing.T, run e2eRun, context string) (output.Result, map[string]any) {
	t.Helper()
	results := parseCompiledJSONLines(t, run.Stdout)
	if len(results) != 1 {
		t.Fatalf("%s: stdout envelope count = %d, want 1\nstdout=%s", context, len(results), run.Stdout)
	}
	data := map[string]any{}
	if results[0].Data != nil {
		raw, err := json.Marshal(results[0].Data)
		if err != nil {
			t.Fatalf("%s: marshal data: %v", context, err)
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			t.Fatalf("%s: decode data object: %v\nraw=%s", context, err, raw)
		}
	}
	return results[0], data
}

// e2eLivePIDsForBinary returns the PIDs of live processes whose argv[0] is
// exactly binaryPath.
//
// This is an absolute-path match against a binary this test run built under the
// OS temp root, enforced by e2eRequireTempPath. It is deliberately NOT a name
// match: an installed daemon at ~/go/bin/tslink or /opt/homebrew/bin/tslink can
// never satisfy it. The result is only ever used for counting and assertions,
// never to select a signal target.
func e2eLivePIDsForBinary(t *testing.T, binaryPath string) []int {
	t.Helper()
	resolved := e2eRequireTempPath(t, binaryPath)
	if runtime.GOOS == "windows" {
		t.Skip("process enumeration helper is POSIX-only")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("enumerate processes: %v", err)
	}

	var pids []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidField, argv, found := strings.Cut(line, " ")
		if !found {
			continue
		}
		pid, convErr := strconv.Atoi(strings.TrimSpace(pidField))
		if convErr != nil {
			continue
		}
		argv = strings.TrimSpace(argv)
		if argv != binaryPath && argv != resolved &&
			!strings.HasPrefix(argv, binaryPath+" ") && !strings.HasPrefix(argv, resolved+" ") {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// e2eAssertProcessCount asserts how many live processes were started from
// binaryPath and returns them for identity checks.
func e2eAssertProcessCount(t *testing.T, binaryPath string, want int, context string) []int {
	t.Helper()
	// A just-signalled process can linger briefly before the kernel reaps it,
	// so converge on the expected count instead of sampling once.
	deadline := time.Now().Add(5 * time.Second)
	var pids []int
	for {
		pids = e2eLivePIDsForBinary(t, binaryPath)
		if len(pids) == want || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(pids) != want {
		t.Fatalf("%s: live processes from %s = %v (%d), want %d", context, binaryPath, pids, len(pids), want)
	}
	return pids
}

// e2eAssertNoResidualProcesses is the suite's orphan-process guard. The 83
// orphaned processes of 2026 are one of the incidents this suite exists to
// prevent, so the suite must not create any itself.
func e2eAssertNoResidualProcesses(t *testing.T, binaryPaths ...string) {
	t.Helper()
	for _, path := range binaryPaths {
		e2eAssertProcessCount(t, path, 0, "residual process check")
	}
}

// e2eReclaimStrayProcesses terminates processes started from a temp binary this
// test run built that the test did not intend to create.
//
// It exists because the failure this suite is designed to detect -- a second
// daemon getting past the conflict guard -- would itself fork a detached
// process. Without this the very test that proves the regression would leak the
// orphan it just proved. Signal targets come only from argv[0] equal to an
// absolute path under the OS temp root that this run created
// (e2eRequireTempPath fails closed otherwise), so an installed daemon is
// structurally unreachable from here.
func e2eReclaimStrayProcesses(t *testing.T, binaryPath string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	for _, pid := range e2eLivePIDsForBinary(t, binaryPath) {
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		t.Logf("reclaiming unexpected process pid=%d started from %s", pid, binaryPath)
		_ = proc.Kill()
		_, _ = proc.Wait()
	}
}

// e2eFileExists reports whether path exists without following symlinks.
func e2eFileExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	t.Fatalf("stat %s: %v", path, err)
	return false
}
