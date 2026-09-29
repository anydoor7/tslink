//go:build !windows

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/testenv"
)

// serveSignalHandlerChildEnv re-enters this test file as a child process. The
// child is the only place we may actually raise SIGTERM: if the production
// default ever stops installing a signal handler, the default disposition
// terminates whoever raises it, and that must not be the test runner.
const serveSignalHandlerChildEnv = "TSLINK_TEST_SIGNAL_HANDLER_CHILD"

// serveSignalHandlerChildDirMarker prefixes the child's report of the two
// directories its TestMain created: the test isolation root (which holds the
// isolated home and config dir) and the service manager PATH shim.
const serveSignalHandlerChildDirMarker = "tslink-signal-child-testmain-dir: "

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
//
// The child runs with a private TMPDIR and must leave nothing in it. It used to
// report through os.Exit inside the test body, which skipped TestMain's
// teardown and leaked the child's isolated config dir (now the isolation root)
// and PATH shim into the real TMPDIR on every run.
func TestServeSignalContextInstallsRealHandler(t *testing.T) {
	if os.Getenv(serveSignalHandlerChildEnv) == "1" {
		t.Logf("%s%s", serveSignalHandlerChildDirMarker, testenv.Root())
		t.Logf("%s%s", serveSignalHandlerChildDirMarker, filepath.SplitList(os.Getenv("PATH"))[0])
		runServeSignalHandlerChild(t)
		return
	}

	childTemp := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestServeSignalContextInstallsRealHandler$", "-test.v")
	child.Env = append(os.Environ(), serveSignalHandlerChildEnv+"=1", "TMPDIR="+childTemp)
	out, err := child.CombinedOutput()

	// A child killed by its own SIGTERM is exactly the regression this test
	// guards: no handler was installed, so the default disposition ran. It is
	// reported before the leak check, which a killed child fails as well.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			t.Fatalf("production serveSignalContextFn did not install a signal handler: "+
				"child was terminated by %v instead of catching it. Restart=on-failure would "+
				"fire on every clean stop.\n%s", status.Signal(), out)
		}
	}
	requireSignalChildLeftNoTestMainDirs(t, childTemp, out)
	if err != nil {
		t.Fatalf("signal-handler child failed: %v\n%s", err, out)
	}
}

// runServeSignalHandlerChild exercises the real production default. It reports
// through the child's own test result, which the parent sees as the exit code
// and prints with the child's output; returning, rather than calling os.Exit,
// lets the child's TestMain remove what it created.
func runServeSignalHandlerChild(t *testing.T) {
	ctx, stop := serveSignalContextFn()
	defer stop()

	// If no handler is installed this call terminates the child, and the
	// parent observes WaitStatus.Signaled().
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("raise SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Fatalf("signal context ended with %v, want context.Canceled", ctx.Err())
		}
	case <-time.After(10 * time.Second):
		// Handler swallowed the signal without cancelling the context, so a
		// clean stop would hang instead of exiting.
		t.Fatal("SIGTERM was caught but did not cancel the signal context")
	}
}

// requireSignalChildLeftNoTestMainDirs checks the child's private TMPDIR after
// it exits. The child names the directories its TestMain created, so this is
// not a scan that would pass just as well if the child had written elsewhere.
func requireSignalChildLeftNoTestMainDirs(t *testing.T, childTemp string, out []byte) {
	t.Helper()
	var reported []string
	for _, line := range strings.Split(string(out), "\n") {
		if _, dir, ok := strings.Cut(line, serveSignalHandlerChildDirMarker); ok {
			reported = append(reported, strings.TrimSpace(dir))
		}
	}
	if len(reported) != 2 {
		t.Fatalf("child reported %d TestMain directories, want the config dir and the PATH shim:\n%s", len(reported), out)
	}
	for _, dir := range reported {
		if filepath.Dir(dir) != childTemp {
			t.Fatalf("child TestMain directory %q is not in the private TMPDIR %q, so this check could not see a leak:\n%s", dir, childTemp, out)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("child left its TestMain directory %s behind (stat err=%v)", dir, err)
		}
	}
	entries, err := os.ReadDir(childTemp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), testenv.RootPrefix) || strings.HasPrefix(entry.Name(), "tslink-service-manager-shim-") {
			t.Errorf("child leaked %s into TMPDIR", entry.Name())
		}
	}
}
