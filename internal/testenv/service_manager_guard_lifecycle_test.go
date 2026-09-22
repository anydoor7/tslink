package testenv

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// TestReportDistinguishesAnUnplantedShimFromZeroInterceptions is the negative
// case for a shim that was never planted. Windows plants no fake at all, yet the teardown still printed
// "child process service manager calls intercepted = 0" -- a sentence whose
// meaning on unix is "the shim was in front of every child and none called a
// manager" and whose meaning there is "nothing was in front of anything". No
// field told the two apart, so the run that protected nothing emitted the same
// audit line as the run that protected everything.
//
// The control is the unix shape in the same shape of call, so a batch where
// both go red (because reportShimCalls stopped printing at all) cannot be read
// as the check working.
func TestReportDistinguishesAnUnplantedShimFromZeroInterceptions(t *testing.T) {
	t.Setenv(ServiceManagerGuardReportEnv, "1")

	newGuard := func(planted []string) *ServiceManagerGuard {
		t.Helper()
		logPath := filepath.Join(t.TempDir(), "service-manager-calls.log")
		if err := os.WriteFile(logPath, nil, 0o600); err != nil {
			t.Fatalf("create the shim log: %v", err)
		}
		guard := NewServiceManagerGuard("shimreport")
		guard.shim = &ServiceManagerPathShim{Dir: filepath.Dir(logPath), LogPath: logPath, Planted: planted}
		return guard
	}

	// Control: fakes were planted and the log is empty. This is the only state
	// in which a bare zero is evidence, and it must keep reading exactly as it
	// did before, because the whole-suite gate pins this text byte for byte.
	planted := newGuard(serviceManagerShimBinaries)
	control := captureStderr(t, func() {
		if planted.reportShimCalls() {
			t.Error("an empty log must not fail the package")
		}
	})
	if control != "service manager guard [shimreport]: child process service manager calls intercepted = 0\n" {
		t.Fatalf("control stderr = %q", control)
	}

	// Negative: nothing was planted. The count is zero by construction, so the
	// line must not claim a measurement.
	unplanted := newGuard(nil)
	log := captureStderr(t, func() {
		if unplanted.reportShimCalls() {
			t.Error("an unplanted shim must not fail the package either; it is a platform fact, not a test defect")
		}
	})
	if strings.Contains(log, "intercepted = 0\n") {
		t.Fatalf("an unplanted shim reported a measured zero:\n%s", log)
	}
	if !strings.Contains(log, "not measured") || !strings.Contains(log, runtime.GOOS) {
		t.Fatalf("stderr does not say the count was never measured, or on which platform:\n%s", log)
	}
}

// TestAllowRealServiceManagerInChildProcessesIsExplicitAndAudited covers the
// audited opt-out. The gated systemd e2e must be able to reach the caller's
// real manager, and until this existed the shim silently answered its calls
// instead -- an e2e that claims in its own doc comment to drive real systemd
// and in fact drives a fake is worse than no e2e.
//
// Resolution is checked with exec.LookPath rather than by running anything:
// after the opt-out the bare name resolves to the machine's real launchctl, and
// this test has no business executing that.
func TestAllowRealServiceManagerInChildProcessesIsExplicitAndAudited(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no fake is planted on Windows, so there is nothing to opt out of")
	}
	t.Setenv(ServiceManagerGuardReportEnv, "")

	code := RunWithServiceManagerGuard(func() int {
		guard := ActiveServiceManagerGuard()
		shim := guard.ServiceManagerShim()

		// Control: with the shim installed, a bare manager name resolves into
		// the planted directory. Every assertion below is measured against it.
		resolved, err := exec.LookPath(serviceManagerShimBinaries[0])
		if err != nil {
			t.Errorf("control: %s does not resolve at all: %v", serviceManagerShimBinaries[0], err)
			return 0
		}
		if filepath.Dir(resolved) != shim.Dir {
			t.Errorf("control: %s resolved to %s, want a fake inside %s", serviceManagerShimBinaries[0], resolved, shim.Dir)
			return 0
		}

		// An opt-out with no reason is refused: the reason is the audit record.
		if _, err := AllowRealServiceManagerInChildProcesses(""); err == nil {
			t.Error("an opt-out with no reason was accepted")
		}

		restore, err := AllowRealServiceManagerInChildProcesses("unit test: prove the shim leaves PATH")
		if err != nil {
			t.Errorf("opt-out: %v", err)
			return 0
		}
		after, err := exec.LookPath(serviceManagerShimBinaries[0])
		if err == nil && filepath.Dir(after) == shim.Dir {
			t.Errorf("after the opt-out %s still resolved to the fake at %s", serviceManagerShimBinaries[0], after)
		}
		if strings.Contains(os.Getenv("PATH"), shim.Dir) {
			t.Errorf("the shim directory is still on PATH after the opt-out: %s", os.Getenv("PATH"))
		}

		restore()
		back, err := exec.LookPath(serviceManagerShimBinaries[0])
		if err != nil || filepath.Dir(back) != shim.Dir {
			t.Errorf("restore did not put the shim back: %s (%v)", back, err)
		}
		return 0
	}, "optout")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	// The opt-out has to be visible without the report env set, for the same
	// reason the seam-level AllowReal is: CI is exactly where nobody is
	// watching, and an unaudited weakening of the guard is the thing that makes
	// the next incident unexplainable.
	guard := NewServiceManagerGuard("optout")
	logPath := filepath.Join(t.TempDir(), "calls.log")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatalf("seed the log: %v", err)
	}
	guard.shim = &ServiceManagerPathShim{Dir: filepath.Dir(logPath), LogPath: logPath, Planted: serviceManagerShimBinaries}
	silent := captureStderr(t, func() { guard.Report() })
	if silent != "" {
		t.Fatalf("control: a guard with no opt-ins must stay silent, got:\n%s", silent)
	}
	guard.recordOptIn("child processes reach the real service manager: a gated e2e")
	report := captureStderr(t, func() { guard.Report() })
	if !strings.Contains(report, "explicit real opt-in child processes reach the real service manager: a gated e2e") {
		t.Fatalf("the child-process opt-out is missing from the report:\n%s", report)
	}
}

// TestAllowRealServiceManagerInChildProcessesFailsWithoutAGuard pins the
// fail-closed direction. A caller reaching for this has already decided the
// fakes are in its way; handing it a silent no-op leaves it believing it got
// what it asked for, which is how the e2e came to verify the shim in the first
// place.
func TestAllowRealServiceManagerInChildProcessesFailsWithoutAGuard(t *testing.T) {
	if ActiveServiceManagerGuard() != nil {
		t.Skip("a guard is active in this process; this case needs the bare state")
	}
	before := os.Getenv("PATH")
	restore, err := AllowRealServiceManagerInChildProcesses("no guard is installed")
	if err == nil {
		if restore != nil {
			restore()
		}
		t.Fatal("opting out with no guard installed succeeded; a no-op here reads as protection removed")
	}
	if os.Getenv("PATH") != before {
		t.Fatal("a refused opt-out still changed PATH")
	}
}
