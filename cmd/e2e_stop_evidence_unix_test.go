//go:build !windows

package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
)

// E2: `tslink stop` may delete PID artifacts only when the process is
// conclusively gone. Every inconclusive result must preserve them.
//
// Deleting a live daemon's PID file is how a machine ends up with two daemons:
// the evidence that a daemon exists disappears, so the next `serve` sees a
// clean slate and starts a second one. daemon.IsProcessAbsentFromPIDFile is the
// single guard for this and had 0% coverage before Round A.
//
// The Round A unit test (TestStopOnlyDeletesPIDArtifactsForConclusiveExitedProcess)
// covers the same predicate in-process, but it stubs isRunningFn to a constant
// false and calls stopService directly. This scenario runs the shipped binary
// against a real config directory with real OS processes and no seams stubbed,
// so it also covers the two states the unit test cannot reach: a live process
// that is the tslink binary but is not a serve daemon, and a genuinely live
// serve daemon that stop must actually stop.
func TestE2EStopPreservesLiveDaemonPIDEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level e2e: forks real processes")
	}
	binary := compiledTSLinkBinary(t)

	type expectation struct {
		wantPIDFile      bool
		wantIdentityFile bool
		wantWasRunning   bool
	}

	cases := []struct {
		name    string
		prepare func(t *testing.T, configDir, pidPath string)
		expect  expectation
		why     string
	}{
		{
			name: "unreadable PID file is preserved",
			prepare: func(t *testing.T, _, pidPath string) {
				// A directory at the PID path makes ReadPID fail with a
				// non-NotExist error: liveness is unknowable, not absent.
				if err := os.Mkdir(pidPath, 0o700); err != nil {
					t.Fatalf("create unreadable PID path: %v", err)
				}
			},
			expect: expectation{wantPIDFile: true, wantIdentityFile: true},
			why:    "an unreadable PID file proves nothing about the daemon",
		},
		{
			name: "PID of a live non-tslink process is preserved",
			prepare: func(t *testing.T, _, pidPath string) {
				pid := startLongLivedForeignProcess(t)
				writePIDFile(t, pidPath, fmt.Sprintf("%d\n", pid))
			},
			expect: expectation{wantPIDFile: true, wantIdentityFile: true},
			why:    "the PID is alive; refusing to stop it must not also erase it",
		},
		{
			name: "PID of a live tslink process that is not a daemon is preserved",
			prepare: func(t *testing.T, _, pidPath string) {
				pid := startLongLivedTSLinkStdioProcess(t, binary)
				writePIDFile(t, pidPath, fmt.Sprintf("%d\n", pid))
			},
			expect: expectation{wantPIDFile: true, wantIdentityFile: true},
			why:    "same Go module, wrong argv: identity says 'not the daemon', liveness says 'alive'",
		},
		{
			name: "garbage PID is preserved",
			prepare: func(t *testing.T, _, pidPath string) {
				writePIDFile(t, pidPath, "not-a-pid\n")
			},
			expect: expectation{wantPIDFile: true, wantIdentityFile: true},
			why:    "an unparseable PID is inconclusive, not proof of absence",
		},
		{
			name: "negative PID is preserved",
			prepare: func(t *testing.T, _, pidPath string) {
				writePIDFile(t, pidPath, "-1\n")
			},
			expect: expectation{wantPIDFile: true, wantIdentityFile: true},
			why:    "a nonsensical PID is inconclusive",
		},
		{
			name: "conclusively exited PID is cleaned up",
			prepare: func(t *testing.T, _, pidPath string) {
				pid := runAndReapShortLivedProcess(t, binary)
				writePIDFile(t, pidPath, fmt.Sprintf("%d\n", pid))
			},
			expect: expectation{wantPIDFile: false, wantIdentityFile: false},
			why:    "the only state where deletion is authorized",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			pidPath := filepath.Join(configDir, "tslink.pid")
			identityPath := pidPath + ".identity"
			tc.prepare(t, configDir, pidPath)
			// Identity evidence is written for every case, including the ones
			// where the PID path is a directory, so "was it deleted" is a
			// meaningful question in all of them.
			if err := os.WriteFile(identityPath, []byte(`{"version":1,"product":"github.com/monody0007/tslink","pid":1,"start_unix_nano":1}`+"\n"), 0o600); err != nil {
				t.Fatalf("write identity fixture: %v", err)
			}

			run := e2eRunBinary(t, binary, configDir, "", e2eEnv(configDir), "stop", "--json")
			if run.ExitCode != output.ExitSuccess || run.Stderr != "" {
				t.Fatalf("stop exit=%d stderr=%q stdout=%s", run.ExitCode, run.Stderr, run.Stdout)
			}
			_, data := e2eDecodeEnvelope(t, run, "stop")
			if wasRunning, _ := data["was_running"].(bool); wasRunning != tc.expect.wantWasRunning {
				t.Fatalf("was_running = %v, want %v", wasRunning, tc.expect.wantWasRunning)
			}

			if got := e2eFileExists(t, pidPath); got != tc.expect.wantPIDFile {
				t.Fatalf("PID artifact exists = %v, want %v (%s)", got, tc.expect.wantPIDFile, tc.why)
			}
			if got := e2eFileExists(t, identityPath); got != tc.expect.wantIdentityFile {
				t.Fatalf("identity artifact exists = %v, want %v (%s)", got, tc.expect.wantIdentityFile, tc.why)
			}
		})
	}
}

// E2, second half: a genuinely live serve daemon must actually be stopped, and
// its artifacts removed only after the process is really gone. Without this the
// preservation cases above could all be satisfied by a `stop` that never
// deletes anything.
func TestE2EStopTerminatesLiveDaemonAndClearsArtifacts(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level e2e: forks real daemons")
	}
	configDir := t.TempDir()
	binary := compiledTSLinkBinary(t)
	daemonBinary := e2eFakeDaemonBinary(t)

	handle := e2eStartFakeDaemon(t, configDir)
	pidPath := filepath.Join(configDir, "tslink.pid")
	identityPath := pidPath + ".identity"
	e2eAssertProcessCount(t, daemonBinary, 1, "daemon started")

	run := e2eRunBinary(t, binary, configDir, "", e2eEnv(configDir), "stop", "--json")
	if run.ExitCode != output.ExitSuccess || run.Stderr != "" {
		t.Fatalf("stop exit=%d stderr=%q stdout=%s", run.ExitCode, run.Stderr, run.Stdout)
	}
	_, data := e2eDecodeEnvelope(t, run, "stop")
	if wasRunning, _ := data["was_running"].(bool); !wasRunning {
		t.Fatalf("was_running = false, want true for a live daemon; data=%+v", data)
	}
	if stopped, _ := data["stopped"].(bool); !stopped {
		t.Fatalf("stopped = false, want true; data=%+v", data)
	}

	if !handle.WaitExit(10 * time.Second) {
		t.Fatalf("daemon PID %d survived `tslink stop`", handle.PID)
	}
	e2eAssertProcessCount(t, daemonBinary, 0, "after stop")
	if e2eFileExists(t, pidPath) {
		t.Fatal("PID file survived a successful stop")
	}
	if e2eFileExists(t, identityPath) {
		t.Fatal("identity sidecar survived a successful stop")
	}
}

func writePIDFile(t *testing.T, pidPath, contents string) {
	t.Helper()
	if err := os.WriteFile(pidPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write PID fixture: %v", err)
	}
}

// startLongLivedForeignProcess starts a process that is alive and is
// definitively not a Go binary, and reclaims it by recorded PID.
func startLongLivedForeignProcess(t *testing.T) int {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("no sleep binary available: %v", err)
	}
	cmd := exec.Command(sleep, "600")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start foreign process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// startLongLivedTSLinkStdioProcess starts the real tslink binary in `mcp` mode
// with an open stdin so it blocks. The process shares the daemon's Go module
// identity but its argv is not "serve", so identity verification must classify
// it as "not the daemon" while liveness classifies it as alive.
//
// `mcp` is used because it is the remaining subcommand that reads stdin until
// EOF; this helper drove `tslink api` until that command was removed. Any
// long-lived non-serve invocation of the shipped binary satisfies the
// scenario — what matters is the module identity and the argv, not the
// protocol spoken on the pipe.
func startLongLivedTSLinkStdioProcess(t *testing.T, binary string) int {
	t.Helper()
	ownConfigDir := t.TempDir()
	cmd := exec.Command(binary, "mcp")
	cmd.Env = e2eEnv(ownConfigDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdio helper stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdio helper stdout: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start stdio helper: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	// Drive one record through so the process is provably past startup and
	// blocked on stdin rather than racing us to exit.
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`
	if _, err := io.WriteString(stdin, initialize+"\n"); err != nil {
		t.Fatalf("write stdio helper record: %v", err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || !strings.Contains(line, `"serverInfo"`) {
		t.Fatalf("stdio helper first record = %q err=%v stderr=%q", line, err, stderr.String())
	}
	return cmd.Process.Pid
}

// runAndReapShortLivedProcess returns a PID that is conclusively gone.
func runAndReapShortLivedProcess(t *testing.T, binary string) int {
	t.Helper()
	cmd := exec.Command(binary, "--version")
	cmd.Env = e2eEnv(t.TempDir())
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start short-lived process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("short-lived process exited with error: %v", err)
	}
	return pid
}
