//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/output"
)

// Two tslink binaries at different absolute paths sharing one config
// directory must not produce two daemons, and the second binary must not report
// an assertive "no daemon" while the first one's daemon is alive.
//
// Historical incidents covered: the 2026-06-03 and 2026-08-19 double-daemon
// events (a Homebrew-installed CLI and a `go install`-ed daemon sharing
// ~/.config/tslink), plus the `tslink status` false negative that was the
// shared enabling condition for both. Those are three of the eight family-II
// incidents and carry the largest blast radius, because a second daemon claims
// the same tailnet hostnames and silently splits service state.
//
// The daemon side is the -overlay fake daemon (real Go module identity, argv
// "serve", real PID identity sidecar) because a real `tslink serve` would
// contact the control plane.
func TestE2EDualBinaryDoesNotProduceTwoDaemons(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level e2e: forks real daemons")
	}

	configDir := t.TempDir()
	daemonBinary := e2eFakeDaemonBinary(t)  // "install A": owns the daemon
	cliBinary := e2eSecondaryCLIBinary(t)   // "install B": a second real CLI
	sharedBinary := compiledTSLinkBinary(t) // a third path, for cross-checking

	// Any daemon B manages to fork is not owned by an *exec.Cmd, so reclaim it
	// explicitly. Registered before the daemon starts so it runs last.
	t.Cleanup(func() {
		e2eReclaimStrayProcesses(t, cliBinary)
		e2eReclaimStrayProcesses(t, sharedBinary)
	})

	handle := e2eStartFakeDaemon(t, configDir)
	pidPath := filepath.Join(configDir, "tslink.pid")
	identityPath := pidPath + ".identity"

	// Invariant (iii), entry condition: exactly one daemon, zero CLI processes.
	e2eAssertProcessCount(t, daemonBinary, 1, "after daemon A start")
	e2eAssertProcessCount(t, cliBinary, 0, "after daemon A start")

	// ---- (0) recognition across executable paths ---------------------------
	// The caller here is the test binary; the daemon lives at install-a/tslink.
	// If identity is decided by comparing absolute executable paths rather than
	// product identity, this is where it first shows up.
	// Reported non-fatally on purpose: recognition failure is the root cause,
	// but the consequences below (an assertive status negative, a second daemon
	// getting past the conflict guard) are the damage, and a run should show
	// all of them rather than stopping at the first.
	if !daemon.IsRunning(pidPath) {
		t.Errorf("a live daemon at %s (pid %d) is not recognised by a caller running from a different path; "+
			"this is the binary identity drift behind the double-daemon incidents", daemonBinary, handle.PID)
	}

	// ---- (i) B must not assertively deny a daemon that is alive -------------
	// The forbidden state is daemon_running=false AND daemon_state="absent".
	// "unknown" is an acceptable, honest third state; "running" is the correct
	// answer once cross-binary identity works. A bare false is the 2026 status
	// false negative that let a second daemon be started on top of a live one.
	for _, probe := range []struct {
		name   string
		binary string
	}{
		{name: "second install CLI", binary: cliBinary},
		{name: "shared build CLI", binary: sharedBinary},
	} {
		t.Run("status from "+probe.name, func(t *testing.T) {
			run := e2eRunBinary(t, probe.binary, configDir, "", e2eEnv(configDir), "status", "--json")
			if run.ExitCode != output.ExitSuccess || run.Stderr != "" {
				t.Fatalf("status exit=%d stderr=%q stdout=%s", run.ExitCode, run.Stderr, run.Stdout)
			}
			_, data := e2eDecodeEnvelope(t, run, "status")

			running, _ := data["daemon_running"].(bool)
			state, _ := data["daemon_state"].(string)
			if !running && state != daemonStateUnknown {
				t.Fatalf("binary at %s reported daemon_running=%v daemon_state=%q while daemon PID %d from %s is alive; "+
					"an assertive negative here is the status false negative that enabled both double-daemon incidents",
					probe.binary, running, state, handle.PID, daemonBinary)
			}
			if running {
				if state != daemonStateRunning {
					t.Fatalf("daemon_running=true but daemon_state=%q, want %q", state, daemonStateRunning)
				}
				gotPID, ok := data["daemon_pid"].(float64)
				if !ok || int(gotPID) != handle.PID {
					t.Fatalf("daemon_pid = %v, want the live daemon PID %d", data["daemon_pid"], handle.PID)
				}
			}
			t.Logf("cross-binary status from %s: daemon_running=%v daemon_state=%q", probe.binary, running, state)
		})
	}

	// ---- (ii) B must be refused a daemon, and must not fork one ------------
	for _, attempt := range []struct {
		name string
		args []string
	}{
		{name: "serve --daemon", args: []string{"serve", "--daemon", "--json", "--no-browser"}},
		{name: "serve foreground json", args: []string{"serve", "--json", "--no-browser"}},
		{name: "serve human", args: []string{"serve", "--no-browser"}},
	} {
		t.Run("rejected "+attempt.name, func(t *testing.T) {
			run := e2eRunBinary(t, cliBinary, configDir, "", e2eEnv(configDir), attempt.args...)
			// Non-fatal: if the conflict guard stops refusing, the interesting
			// question is what happened to the process table, and that is
			// checked immediately below.
			if run.ExitCode != output.ExitConflict {
				t.Errorf("%v exit = %d, want %d (conflict)\nstdout=%s\nstderr=%s",
					attempt.args, run.ExitCode, output.ExitConflict, run.Stdout, run.Stderr)
			}

			// Sampled once, without convergence: B has already exited, and a
			// correctly refused serve never forks, so this is deterministic at
			// zero.
			//
			// SCOPE OF THIS ASSERTION — read before treating it as coverage of
			// "no second daemon is created".
			//
			// It evaluates only when a process forked from the second binary is
			// still alive at this sampling point, immediately after the command
			// returned. That is a real and reachable condition: injecting a
			// long-lived process from install-b before the three attempts makes
			// every attempt report the exact stray PID, so the check is not a
			// tautology. But it is not the condition the conflict-guard
			// regression produces on this host. Remove the guard and the second
			// `serve` does fork a daemon, which then dies during tsnet startup
			// for want of a reachable control plane — usually before this line
			// runs. The regression is caught here by the exit-code assertion
			// above, not by this count.
			//
			// So: this is a genuine check with a narrow trigger, and it must
			// NOT be described as mutation-proven coverage of "the product does
			// not create a second daemon". Making that claim testable needs a
			// host with a reachable control plane, where the forked daemon
			// survives long enough to be counted.
			if strays := e2eLivePIDsForBinary(t, cliBinary); len(strays) != 0 {
				t.Errorf("%v forked %d process(es) from the second binary despite a live daemon: %v",
					attempt.args, len(strays), strays)
			}

			// No second process from B, and A's daemon untouched.
			e2eAssertProcessCount(t, cliBinary, 0, "after "+attempt.name)
			pids := e2eAssertProcessCount(t, daemonBinary, 1, "after "+attempt.name)
			if pids[0] != handle.PID {
				t.Fatalf("surviving daemon PID = %d, want the original %d", pids[0], handle.PID)
			}
			if handle.Exited() {
				t.Fatalf("daemon A exited while binary B attempted %v", attempt.args)
			}

			// B must not have rewritten A's PID evidence.
			currentPID, err := daemon.ReadPID(pidPath)
			if err != nil || currentPID != handle.PID {
				t.Fatalf("PID file after %v = %d (err=%v), want unchanged %d", attempt.args, currentPID, err, handle.PID)
			}
			if !e2eFileExists(t, identityPath) {
				t.Fatalf("binary B removed the live daemon's identity sidecar during %v", attempt.args)
			}
		})
	}

	// ---- (iii) exit condition -------------------------------------------
	e2eAssertProcessCount(t, daemonBinary, 1, "end of scenario")
	e2eAssertProcessCount(t, cliBinary, 0, "end of scenario")
	e2eAssertProcessCount(t, sharedBinary, 0, "end of scenario")

	// The two binaries really are distinct files. Without this the whole
	// scenario could silently degrade into "one binary talking to itself".
	if daemonBinary == cliBinary {
		t.Fatal("daemon and CLI resolved to the same path; the scenario is vacuous")
	}
	daemonInfo, err := os.Stat(daemonBinary)
	if err != nil {
		t.Fatalf("stat daemon binary: %v", err)
	}
	cliInfo, err := os.Stat(cliBinary)
	if err != nil {
		t.Fatalf("stat CLI binary: %v", err)
	}
	if os.SameFile(daemonInfo, cliInfo) {
		t.Fatal("daemon and CLI are the same file; the scenario is vacuous")
	}
	t.Logf("daemon binary=%s cli binary=%s daemon pid=%d", daemonBinary, cliBinary, handle.PID)
}

// E1 residue guard: after the scenario completes, the config dir must own no
// live process. Runs as its own test so the cleanup ordering of the scenario
// above is actually observed rather than asserted inside its own cleanup.
func TestE2EDualBinaryLeavesNoResidualProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("process-level e2e: forks real daemons")
	}
	configDir := t.TempDir()
	daemonBinary := e2eFakeDaemonBinary(t)
	cliBinary := e2eSecondaryCLIBinary(t)

	handle := e2eStartFakeDaemon(t, configDir)
	e2eAssertProcessCount(t, daemonBinary, 1, "daemon started")

	handle.stopAndReap()
	if !handle.WaitExit(5 * time.Second) {
		t.Fatal("owned daemon did not exit after deterministic reclamation")
	}
	e2eAssertNoResidualProcesses(t, daemonBinary, cliBinary)
}
