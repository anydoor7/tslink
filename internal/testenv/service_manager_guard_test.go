package testenv

import (
	"errors"
	"strings"
	"testing"
)

// realCallRecorder stands in for the real exec.Command exit. Its Ran flag is
// the discriminating observation: a guard that "fails the test" but still
// executed the binary would leave Ran true, and that is exactly the failure
// mode the 2026-09-16 incident was.
type realCallRecorder struct {
	Ran  bool
	Args []string
}

func (r *realCallRecorder) Call(args ...string) ([]byte, error) {
	r.Ran = true
	r.Args = append([]string(nil), args...)
	return []byte("real output\n"), nil
}

func seamFor(manager string, slot *ServiceManagerCall, real ServiceManagerCall) ServiceManagerSeam {
	*slot = real
	return ServiceManagerSeam{
		Manager: manager,
		Get:     func() ServiceManagerCall { return *slot },
		Set:     func(fn ServiceManagerCall) { *slot = fn },
	}
}

// TestServiceManagerGuardControlGroupStaysGreen is the control for
// TestServiceManagerGuardBlocksUnstubbedSeam below. It is the same harness,
// the same guard and the same seam; the only difference is that the body does
// not reach the seam. If this one were red too, a red negative case would
// prove nothing about the guard.
func TestServiceManagerGuardControlGroupStaysGreen(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	code := RunWithServiceManagerGuard(func() int {
		// A properly isolated test stubs the seam for itself.
		stub := slot
		slot = func(args ...string) ([]byte, error) { return []byte("fixture\n"), nil }
		defer func() { slot = stub }()
		out, err := slot("print", "gui/0/com.tslink.daemon")
		if err != nil || string(out) != "fixture\n" {
			t.Fatalf("stubbed seam: out=%q err=%v", out, err)
		}
		return 0
	}, "control", seam)

	if code != 0 {
		t.Fatalf("control group exit code = %d, want 0", code)
	}
	if real.Ran {
		t.Fatal("control group reached the real service manager")
	}
}

func TestServiceManagerGuardBlocksUnstubbedSeam(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	var (
		gotOut []byte
		gotErr error
	)
	code := RunWithServiceManagerGuard(func() int {
		// The mutation: a test that forgot to stub the seam, exactly as
		// TestBootstrapInstallerUsesExistingConflictGuard did on 2026-09-16.
		gotOut, gotErr = slot("bootout", "gui/501/com.tslink.daemon")
		return 0
	}, "negative", seam)

	if real.Ran {
		t.Fatalf("guard let the real service manager run with args %v", real.Args)
	}
	if gotOut != nil {
		t.Fatalf("blocked call returned output %q, want none", gotOut)
	}
	if !errors.Is(gotErr, ErrServiceManagerBlocked) {
		t.Fatalf("blocked call error = %v, want ErrServiceManagerBlocked", gotErr)
	}
	if !strings.Contains(gotErr.Error(), "bootout gui/501/com.tslink.daemon") {
		t.Fatalf("blocked call error %v omits the attempted arguments", gotErr)
	}
	if code == 0 {
		t.Fatal("guard returned exit code 0 for a package that reached a real service manager")
	}
	if slot == nil {
		t.Fatal("guard did not restore the seam")
	}
	if out, err := slot("print", "x"); err != nil || !real.Ran || string(out) != "real output\n" {
		t.Fatalf("after restore: out=%q err=%v ran=%t", out, err, real.Ran)
	}
}

func TestServiceManagerGuardRecordsAttemptWithStack(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("systemctl", &slot, real.Call)

	guard := NewServiceManagerGuard("record")
	restore := guard.Install(seam)
	if _, err := slot("--user", "restart", "tslink.service"); !errors.Is(err, ErrServiceManagerBlocked) {
		t.Fatalf("err = %v, want ErrServiceManagerBlocked", err)
	}
	restore()

	attempts := guard.Attempts()
	if len(attempts) != 1 {
		t.Fatalf("attempts = %d, want 1", len(attempts))
	}
	if attempts[0].Manager != "systemctl" {
		t.Fatalf("manager = %q", attempts[0].Manager)
	}
	if strings.Join(attempts[0].Args, " ") != "--user restart tslink.service" {
		t.Fatalf("args = %v", attempts[0].Args)
	}
	if !strings.Contains(string(attempts[0].Stack), "TestServiceManagerGuardRecordsAttemptWithStack") {
		t.Fatalf("stack does not name the calling test:\n%s", attempts[0].Stack)
	}
	if !guard.Report() {
		t.Fatal("Report() = false for a guard with one blocked attempt")
	}
}

func TestServiceManagerGuardAllowRealIsExplicitAndRevocable(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	guard := NewServiceManagerGuard("optin")
	restore := guard.Install(seam)
	defer restore()

	// Default deny: the opt-in has to be an action, not an omission.
	if _, err := slot("print", "gui/0/x"); !errors.Is(err, ErrServiceManagerBlocked) {
		t.Fatalf("pre-opt-in err = %v", err)
	}
	if real.Ran {
		t.Fatal("pre-opt-in call reached the real binary")
	}

	revoke := guard.AllowReal("launchctl", "exercise the real read-only print path")
	out, err := slot("print", "gui/0/x")
	if err != nil || string(out) != "real output\n" || !real.Ran {
		t.Fatalf("opted-in call: out=%q err=%v ran=%t", out, err, real.Ran)
	}

	revoke()
	real.Ran = false
	if _, err := slot("print", "gui/0/x"); !errors.Is(err, ErrServiceManagerBlocked) {
		t.Fatalf("post-revoke err = %v", err)
	}
	if real.Ran {
		t.Fatal("post-revoke call reached the real binary")
	}
}

func TestServiceManagerGuardAllowRealRequiresReason(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("AllowReal with an empty reason did not panic")
		}
	}()
	NewServiceManagerGuard("reason").AllowReal("launchctl", "")
}

func TestActiveServiceManagerGuardIsScopedToTheRun(t *testing.T) {
	if g := ActiveServiceManagerGuard(); g != nil {
		t.Fatalf("guard active before any run: %v", g)
	}
	var inner *ServiceManagerGuard
	if code := RunWithServiceManagerGuard(func() int {
		inner = ActiveServiceManagerGuard()
		return 0
	}, "scope"); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if inner == nil {
		t.Fatal("no guard was active during the run")
	}
	if g := ActiveServiceManagerGuard(); g != nil {
		t.Fatalf("guard still active after the run: %v", g)
	}
}
