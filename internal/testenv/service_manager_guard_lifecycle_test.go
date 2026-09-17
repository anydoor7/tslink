package testenv

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
// The reader runs concurrently: a blocked attempt's report carries a full
// debug.Stack(), which can outgrow the pipe buffer and deadlock fn.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	original := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	func() {
		defer func() {
			os.Stderr = original
			_ = w.Close()
		}()
		fn()
	}()
	out := <-done
	_ = r.Close()
	return out
}

// TestDriftedSeamsControlGroupSeesNoDrift is the control for the two negatives
// below. Same guard, same seam, and the stub-then-restore pattern every
// isolated test uses; if this were red, a red negative would prove nothing.
func TestDriftedSeamsControlGroupSeesNoDrift(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	guard := NewServiceManagerGuard("control")
	restore := guard.Install(seam)
	defer restore()

	blocked := slot
	slot = func(args ...string) ([]byte, error) { return []byte("fixture\n"), nil }
	slot = blocked // what a defer/t.Cleanup restore does

	if drifted := guard.DriftedSeams(); len(drifted) != 0 {
		t.Fatalf("drifted = %v, want none for a seam that was restored", drifted)
	}
}

// TestDriftedSeamsFlagsARePointedSeam is the commission case the reviewer
// found: a test replaces the guarded call with its own implementation, so the
// call goes through the seam variable and the guard never sees it.
func TestDriftedSeamsFlagsARePointedSeam(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	guard := NewServiceManagerGuard("negative")
	restore := guard.Install(seam)
	defer restore()

	// Shaped like the real bypass, but harmless: the point is that the guard
	// is no longer in the path, not what the replacement does.
	slot = func(args ...string) ([]byte, error) { return []byte("bypassed\n"), nil }

	drifted := guard.DriftedSeams()
	if len(drifted) != 1 || drifted[0] != "launchctl" {
		t.Fatalf("drifted = %v, want [launchctl]", drifted)
	}
	if len(guard.Attempts()) != 0 {
		t.Fatal("a re-pointed seam records no blocked attempt; that is exactly why the pointer check is needed")
	}
}

// TestRunWithServiceManagerGuardFailsThePackageOnSeamDrift is the same
// negative at the level that matters: the exit code of the whole package.
func TestRunWithServiceManagerGuardFailsThePackageOnSeamDrift(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	var log string
	code := 0
	log = captureStderr(t, func() {
		code = RunWithServiceManagerGuard(func() int {
			slot = func(args ...string) ([]byte, error) { return []byte("bypassed\n"), nil }
			return 0
		}, "drift", seam)
	})

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 for a package that left a seam re-pointed", code)
	}
	if !strings.Contains(log, "seam launchctl was re-pointed away from the guard") {
		t.Fatalf("report does not name the drifted seam:\n%s", log)
	}
	if slot == nil {
		t.Fatal("guard did not restore the seam")
	}
}

// TestRunWithServiceManagerGuardControlGroupStaysGreenWithoutDrift pins the
// other direction: the drift check must not fire on the normal path.
//
// Both values of the report env are exercised because this assertion is about
// the guard's behaviour and must not be about the caller's shell. The previous
// spelling captured stderr and required it to be empty, which silently made the
// test's colour a function of the ambient TSLINK_SERVICE_MANAGER_GUARD_REPORT:
// green with the variable unset, red when an external runner sets
// that env on purpose so a green run still prints proof the guard was
// installed. A test whose result depends on the environment it is invoked from
// cannot gate anything.
func TestRunWithServiceManagerGuardControlGroupStaysGreenWithoutDrift(t *testing.T) {
	for _, env := range []struct{ name, value string }{
		{"report off", ""},
		{"report on", "1"},
	} {
		t.Run(env.name, func(t *testing.T) {
			t.Setenv(ServiceManagerGuardReportEnv, env.value)

			real := &realCallRecorder{}
			var slot ServiceManagerCall
			seam := seamFor("launchctl", &slot, real.Call)

			code := 0
			log := captureStderr(t, func() {
				code = RunWithServiceManagerGuard(func() int {
					blocked := slot
					slot = func(args ...string) ([]byte, error) { return []byte("fixture\n"), nil }
					defer func() { slot = blocked }()
					if _, err := slot("print", "x"); err != nil {
						t.Errorf("stubbed seam: %v", err)
					}
					return 0
				}, "control", seam)
			})

			if code != 0 {
				t.Fatalf("exit code = %d, want 0:\n%s", code, log)
			}
			// The load-bearing half, and it holds under both env values: a
			// clean run produces no blocked attempt and no drift. This is what
			// must not be weakened to buy the env independence above.
			if strings.Contains(log, "blocked attempt") {
				t.Fatalf("a clean run recorded a blocked attempt:\n%s", log)
			}
			if strings.Contains(log, "re-pointed away from the guard") {
				t.Fatalf("a clean run was reported as drifted:\n%s", log)
			}
			if real.Ran {
				t.Fatal("the control group reached the real service manager")
			}

			// The env decides one thing only: whether the zero-hit summaries are
			// printed. Pinning the exact text in both directions is stronger
			// than the old "must be silent", because it also proves the env=on
			// run says = 0 rather than merely saying something.
			//
			// Both halves of the guard report their own zero: the in-process
			// seams and the child-process PATH shim fail in different places,
			// so one line saying = 0 would leave the other half's absence
			// indistinguishable from its silence.
			want := ""
			if env.value != "" {
				want = "service manager guard [control]: child process service manager calls intercepted = 0\n" +
					"service manager guard [control]: blocked real service manager calls = 0\n"
			}
			if log != want {
				t.Fatalf("stderr = %q, want %q", log, want)
			}
		})
	}
}

// TestReportNamesOptInsWithoutBlockedCallsOrEnv is F2: an AllowReal that runs
// the real binary and blocks nothing used to leave no trace unless the report
// env was already set, i.e. the audit record vanished in CI.
func TestReportNamesOptInsWithoutBlockedCallsOrEnv(t *testing.T) {
	t.Setenv(ServiceManagerGuardReportEnv, "")

	guard := NewServiceManagerGuard("optin")
	control := captureStderr(t, func() { guard.Report() })
	if control != "" {
		t.Fatalf("control: a guard with no opt-ins and no hits must stay silent, got:\n%s", control)
	}

	guard.AllowReal("launchctl", "exercise the real read-only print path")
	log := captureStderr(t, func() { guard.Report() })
	if !strings.Contains(log, "explicit real opt-in launchctl: exercise the real read-only print path") {
		t.Fatalf("opt-in is invisible with hits == 0 and the env unset:\n%s", log)
	}
	if !strings.Contains(log, "blocked real service manager calls = 0") {
		t.Fatalf("opt-in line printed without its summary context:\n%s", log)
	}
}

// TestReportEnvAcceptsCommonTruthySpellings: the gate used to be == "1", so
// TSLINK_SERVICE_MANAGER_GUARD_REPORT=true asked for the audit trail and got
// silence, which reads exactly like "the guard is not installed".
func TestReportEnvAcceptsCommonTruthySpellings(t *testing.T) {
	for _, value := range []string{"1", "true", "TRUE", "yes", "on", " t "} {
		if !envIsTrue(value) {
			t.Errorf("envIsTrue(%q) = false, want true", value)
		}
	}
	for _, value := range []string{"", "0", "false", "no", "off", "maybe"} {
		if envIsTrue(value) {
			t.Errorf("envIsTrue(%q) = true, want false", value)
		}
	}

	t.Setenv(ServiceManagerGuardReportEnv, "true")
	log := captureStderr(t, func() { NewServiceManagerGuard("env").Report() })
	if !strings.Contains(log, "blocked real service manager calls = 0") {
		t.Fatalf("REPORT=true printed nothing:\n%s", log)
	}
}

// TestRunWithServiceManagerGuardRestoresTheEnclosingGuard is F4: a nested run
// used to clear the global slot, leaving the outer, still-installed guard
// invisible to ActiveServiceManagerGuard().
func TestRunWithServiceManagerGuardRestoresTheEnclosingGuard(t *testing.T) {
	var outer, innerSaw, afterInner *ServiceManagerGuard

	code := RunWithServiceManagerGuard(func() int {
		outer = ActiveServiceManagerGuard()
		RunWithServiceManagerGuard(func() int {
			innerSaw = ActiveServiceManagerGuard()
			return 0
		}, "inner")
		afterInner = ActiveServiceManagerGuard()
		return 0
	}, "outer")

	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if outer == nil || innerSaw == nil {
		t.Fatalf("outer=%v inner=%v, both runs must publish a guard", outer, innerSaw)
	}
	if innerSaw == outer {
		t.Fatal("the inner run did not publish its own guard")
	}
	if afterInner != outer {
		t.Fatalf("after the inner run the active guard is %v, want the outer guard %v", afterInner, outer)
	}
	if g := ActiveServiceManagerGuard(); g != nil {
		t.Fatalf("guard still active after the outer run: %v", g)
	}
}

// TestRunWithServiceManagerGuardReportsThroughAPanic is F5: the blocked-attempt
// list is the only record of what went wrong, and a panic in TestMain used to
// take it with it.
func TestRunWithServiceManagerGuardReportsThroughAPanic(t *testing.T) {
	real := &realCallRecorder{}
	var slot ServiceManagerCall
	seam := seamFor("launchctl", &slot, real.Call)

	log := captureStderr(t, func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic must keep propagating; teardown is not error handling")
			}
		}()
		RunWithServiceManagerGuard(func() int {
			if _, err := slot("bootout", "gui/501/com.tslink.daemon"); !errors.Is(err, ErrServiceManagerBlocked) {
				t.Errorf("err = %v", err)
			}
			panic("TestMain body blew up")
		}, "panic", seam)
	})

	if !strings.Contains(log, "blocked attempt 1: launchctl bootout gui/501/com.tslink.daemon") {
		t.Fatalf("the blocked attempt was lost to the panic:\n%s", log)
	}
	if real.Ran {
		t.Fatal("guard let the real call through")
	}
	if out, err := slot("print", "x"); err != nil || string(out) != "real output\n" {
		t.Fatalf("seam not restored after the panic: out=%q err=%v", out, err)
	}
	if g := ActiveServiceManagerGuard(); g != nil {
		t.Fatalf("panic left a guard published: %v", g)
	}
}

// TestInstallRejectsMalformedSeams is F6: a seam with a nil Get or Set
// protects nothing, and skipping it silently produced a guard that looked
// installed while one exit stayed open.
func TestInstallRejectsMalformedSeams(t *testing.T) {
	real := &realCallRecorder{}

	// Control: a complete seam installs without complaint.
	var slot ServiceManagerCall
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("control: a well-formed seam panicked: %v", r)
			}
		}()
		NewServiceManagerGuard("control").Install(seamFor("launchctl", &slot, real.Call))()
	}()

	cases := map[string]ServiceManagerSeam{
		"nil Set": {Manager: "launchctl", Get: func() ServiceManagerCall { return real.Call }},
		"nil Get": {Manager: "launchctl", Set: func(ServiceManagerCall) {}},
		"no name": {Get: func() ServiceManagerCall { return real.Call }, Set: func(ServiceManagerCall) {}},
	}
	for name, seam := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("Install accepted a seam that protects nothing")
				}
			}()
			NewServiceManagerGuard("malformed").Install(seam)
		})
	}
}
