//go:build !windows

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// serveSignalHandlerChildEnv re-enters this test file as a child process. The
// child is the only place we may actually raise SIGTERM: if the production
// default ever stops installing a signal handler, the default disposition
// terminates whoever raises it, and that must not be the test runner.
const serveSignalHandlerChildEnv = "TSLINK_TEST_SIGNAL_HANDLER_CHILD"

// TestServeSignalContextInstallsRealHandler pins the production default of
// serveSignalContextFn to something that actually catches SIGINT/SIGTERM.
//
// Why this exists: review-17 showed by surviving mutation that nothing pinned
// it. Replacing the default with a plain context.WithCancel only upsets the
// compiler ("os/signal" and "syscall" imported and not used) -- and a refactor
// that tidies away those two now-unused imports leaves the daemon with no
// signal handling at all while every gate stays green. The consequence is not
// cosmetic: SIGTERM would then kill the process by default disposition,
// systemd would record Result=signal, Restart=on-failure would fire, and the
// F-1 defect that cmd/serve.go's shutdown guard exists to fix would come back
// in a worse form -- silently.
//
// The guard in runForeground is already pinned three ways (see
// TestRunForegroundTreatsShutdownCancellationAsSuccess and siblings), but each
// of those injects a context. They therefore say nothing about what the
// production path installs. This test covers only that remaining edge.
//
// Unix-only: raising a signal at yourself has no equivalent on Windows, and
// the behaviour being pinned is what systemd observes.
func TestServeSignalContextInstallsRealHandler(t *testing.T) {
	if os.Getenv(serveSignalHandlerChildEnv) == "1" {
		runServeSignalHandlerChild()
		return
	}

	child := exec.Command(os.Args[0], "-test.run=^TestServeSignalContextInstallsRealHandler$", "-test.v")
	child.Env = append(os.Environ(), serveSignalHandlerChildEnv+"=1")
	out, err := child.CombinedOutput()

	if err == nil {
		return
	}

	// A child killed by its own SIGTERM is exactly the regression this test
	// guards: no handler was installed, so the default disposition ran.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			t.Fatalf("production serveSignalContextFn did not install a signal handler: "+
				"child was terminated by %v instead of catching it. Restart=on-failure would "+
				"fire on every clean stop.\n%s", status.Signal(), out)
		}
	}
	t.Fatalf("signal-handler child failed: %v\n%s", err, out)
}

// runServeSignalHandlerChild exercises the real production default and reports
// through the process exit code, because a t.Fatalf here would be reported by
// the child's own test output rather than the parent's.
func runServeSignalHandlerChild() {
	ctx, stop := serveSignalContextFn()
	defer stop()

	// If no handler is installed this call terminates the child, and the
	// parent observes WaitStatus.Signaled().
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		os.Exit(3)
	}

	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			os.Exit(5)
		}
		os.Exit(0)
	case <-time.After(10 * time.Second):
		// Handler swallowed the signal without cancelling the context, so a
		// clean stop would hang instead of exiting.
		os.Exit(4)
	}
}
