//go:build darwin || linux

package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testenv"
)

// boundedCallResult carries one seam call out of the goroutine that makes it,
// so the test can stop waiting at the budget instead of at the child's exit.
type boundedCallResult struct {
	out []byte
	err error
}

// installBlockingManagerShim puts a fake manager first on PATH that never
// answers, and opts the guarded seam into delegating to its real
// implementation. The real implementation resolves the manager through PATH,
// so what it reaches is this shim, never the host's service manager; the
// lookup is checked before anything runs.
func installBlockingManagerShim(t *testing.T, manager string) {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, manager)
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	resolved, err := exec.LookPath(manager)
	if err != nil || resolved != shim {
		t.Fatalf("%s resolves to %q (err=%v), want the blocking shim %q; refusing to reach a real service manager", manager, resolved, err, shim)
	}
	guard := testenv.ActiveServiceManagerGuard()
	if guard == nil {
		t.Fatal("no service manager guard is active; TestMain is not wrapping m.Run with RunWithServiceManagerGuard")
	}
	t.Cleanup(guard.AllowReal(manager, "bounded-seam test: PATH resolves to a blocking shim in t.TempDir(), never the host binary"))
}

// requireSeamReturnsWithinBudget calls a manager seam whose child never
// answers. The seam must give up at its own query budget with a deadline
// error. An unbounded CombinedOutput would sit on the child for 30 seconds;
// the test stops waiting one second past the budget and says so.
func requireSeamReturnsWithinBudget(t *testing.T, manager string, call func() ([]byte, error)) {
	t.Helper()
	done := make(chan boundedCallResult, 1)
	go func() {
		out, err := call()
		done <- boundedCallResult{out: out, err: err}
	}()
	limit := managerQueryTimeout + time.Second
	select {
	case result := <-done:

		if !errors.Is(result.err, context.DeadlineExceeded) || !strings.Contains(result.err.Error(), manager+" command exceeded "+managerQueryTimeout.String()) {
			t.Fatalf("%s seam against a manager that never answers returned err=%v, want the bounded-command deadline error", manager, result.err)
		}
	case <-time.After(limit):
		t.Fatalf("%s seam did not return within %s against a manager that never answers; the call is unbounded", manager, limit)
	}
}

// TestBoundedManagerCommandReleasesAPipeHeldByAGrandchild pins WaitDelay.
// Killing the direct child at the deadline is not enough when that child has
// forked something that inherited its output pipe: CombinedOutput waits for
// the pipe to close, and the grandchild holds it for as long as it lives.
// WaitDelay is what closes the pipe and lets the call return.
func TestBoundedManagerCommandReleasesAPipeHeldByAGrandchild(t *testing.T) {
	const budget = 300 * time.Millisecond
	done := make(chan boundedCallResult, 1)

	go func() {
		out, err := runBoundedManagerCommand("/bin/sh", budget, "-c", "/bin/sleep 30 & echo $!; wait")
		done <- boundedCallResult{out: out, err: err}
	}()
	limit := 5 * time.Second // hang guard; error and pipe release are the assertions
	select {
	case result := <-done:

		// The grandchild outlives the call by design (only the direct child is
		// killed); do not leave it behind for the rest of the run.
		if pid, err := strconv.Atoi(strings.TrimSpace(string(result.out))); err == nil && pid > 1 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if !errors.Is(result.err, context.DeadlineExceeded) {
			t.Fatalf("pipe-holding command returned err=%v, want deadline exceeded", result.err)
		}

	case <-time.After(limit):
		t.Fatalf("pipe-holding command did not return within %s; a grandchild holding the output pipe blocks the bounded call", limit)
	}
}
