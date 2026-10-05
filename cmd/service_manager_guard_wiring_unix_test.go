//go:build darwin || linux

package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testwait"
)

const serviceManagerGuardTripwireEnv = "TSLINK_SERVICE_MANAGER_GUARD_TRIPWIRE"

// How to check by hand whether a test run took the live daemon down, on a
// machine that has one: compare `runs`, `execs` and `last exit code` from
// `launchctl print gui/<uid>/com.tslink.daemon` before and after, plus
// `program`/`path`/`state`. A job that was booted out and re-bootstrapped
// cannot keep `runs = 1` with `last exit code = (never exited)`.
//
// Do not diff the whole `launchctl print` output. `forks` is a live counter
// that moves on its own: sampled ten times across 110 idle seconds on
// 2026-09-16 with no tests running at all, it went 374 -> 378. Reading its
// increase as evidence that the test suite disturbed the daemon is a wrong
// conclusion this comment exists to prevent; an earlier 18-second sample
// happened to land in a quiet window and showed no movement, which is how the
// wrong conclusion nearly got drawn.

// TestServiceManagerGuardIsInstalledForThisPackage is the cheap half of the
// evidence: it proves TestMain actually handed the seams to the guard, which
// is the one step that silently degrades to "no guard at all" if someone edits
// TestMain later.
func TestServiceManagerGuardIsInstalledForThisPackage(t *testing.T) {
	if testenv.ActiveServiceManagerGuard() == nil {
		t.Fatal("no service manager guard is active; TestMain is not wrapping m.Run with RunWithServiceManagerGuard")
	}
	seams := osServiceManagerSeams()
	if len(seams) == 0 {
		t.Fatal("this platform installs an OS service through a process exit but registered no seam")
	}
	for _, seam := range seams {
		if seam.Manager == "" || seam.Get == nil || seam.Set == nil {
			t.Fatalf("seam %+v is incompletely wired", seam.Manager)
		}
		if seam.Get() == nil {
			t.Fatalf("seam %s resolves to a nil call", seam.Manager)
		}
	}
}

// TestServiceManagerGuardTripwireHelper is the body that runs inside the child
// test binary. It is skipped in a normal run: the negative case has to execute
// in its own process because tripping the guard is by design fatal to the
// whole package, and a self-tripping test inside this package would take every
// other test down with it.
func TestServiceManagerGuardTripwireHelper(t *testing.T) {
	switch os.Getenv(serviceManagerGuardTripwireEnv) {
	case "":
		t.Skip("helper process only; driven by TestServiceManagerGuardFailsThePackageOnRealCall")
	case "control":
		// The unmutated control: same binary, same TestMain, same guard, and
		// it never reaches the seam. This must exit 0, otherwise a red
		// negative case would prove nothing.
	case "negative":
		out, err := serviceManagerGuardProbe()
		if out != nil {
			t.Fatalf("guarded seam returned output %q; the real binary ran", out)
		}
		if !errors.Is(err, testenv.ErrServiceManagerBlocked) {
			t.Fatalf("guarded seam error = %v, want ErrServiceManagerBlocked", err)
		}
	default:
		t.Fatalf("unknown tripwire mode %q", os.Getenv(serviceManagerGuardTripwireEnv))
	}
}

func runTripwireChild(t *testing.T, mode string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testwait.Budget(t))
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run=^TestServiceManagerGuardTripwireHelper$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), serviceManagerGuardTripwireEnv+"="+mode)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("tripwire child %q timed out:\n%s", mode, combined.String())
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, combined.String()
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), combined.String()
	default:
		t.Fatalf("tripwire child %q: %v\n%s", mode, err, combined.String())
		return -1, ""
	}
}

// TestServiceManagerGuardFailsThePackageOnRealCall runs a control and a
// mutation through the same child harness. The mutation is "a test that
// forgot to stub the service manager seam", which is literally what happened
// on 2026-09-16 and took the developer's own LaunchAgent down for 23 minutes.
func TestServiceManagerGuardFailsThePackageOnRealCall(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child test binary")
	}

	controlCode, controlLog := runTripwireChild(t, "control")
	if controlCode != 0 {
		t.Fatalf("control child exit = %d, want 0; the harness itself is broken, so the negative case below proves nothing:\n%s", controlCode, controlLog)
	}
	if !strings.Contains(controlLog, "--- PASS: TestServiceManagerGuardTripwireHelper") {
		t.Fatalf("control child did not run the helper:\n%s", controlLog)
	}
	if strings.Contains(controlLog, "service manager guard [cmd]: blocked attempt") {
		t.Fatalf("control child tripped the guard without touching the seam:\n%s", controlLog)
	}

	negativeCode, negativeLog := runTripwireChild(t, "negative")
	// The helper's own assertions must pass: that is what proves the seam
	// returned the guard sentinel and no output, i.e. nothing was executed.
	if !strings.Contains(negativeLog, "--- PASS: TestServiceManagerGuardTripwireHelper") {
		t.Fatalf("negative child's helper did not pass, so the block is unproven:\n%s", negativeLog)
	}
	// And the package must still fail, because an unisolated test is a defect
	// even when its own assertions are green.
	if negativeCode == 0 {
		t.Fatalf("negative child exit = 0; the guard did not fail the package:\n%s", negativeLog)
	}
	if !strings.Contains(negativeLog, "service manager guard [cmd]: blocked attempt 1:") {
		t.Fatalf("negative child did not report the blocked attempt:\n%s", negativeLog)
	}
	if !strings.Contains(negativeLog, "TestServiceManagerGuardTripwireHelper") {
		t.Fatalf("blocked attempt report does not name the offending test:\n%s", negativeLog)
	}
}
