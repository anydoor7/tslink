package testenv

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// ServiceManagerGuardReportEnv makes the guard print its per-package summary
// line even when it recorded zero blocked attempts, so a green run can still
// prove the guard was installed rather than silently absent.
//
// Accepted true values are 1, t, true, y, yes and on (case-insensitive).
// Anything else, including the empty string, is off. The spelling used to be
// an exact "1" comparison, which silently treated TSLINK_..._REPORT=true as
// off; a CI operator who asked for the audit trail and got silence had no way
// to tell that from "the guard is not installed".
const ServiceManagerGuardReportEnv = "TSLINK_SERVICE_MANAGER_GUARD_REPORT"

// ErrServiceManagerBlocked is returned in place of running the real service
// manager binary. It is a sentinel no real launchctl/systemctl invocation can
// ever produce, so a test can assert on it with errors.Is and know for certain
// that nothing was executed.
//
// Why this guard exists: on 2026-09-16 a `go test` run of package cmd reached
// the real launchctl exit and ran `bootout`+`bootstrap` against the live
// gui/<uid> domain. launchd's domain namespace is global to the login session,
// so it is reachable from any process regardless of HOME or t.TempDir(); the
// job label is a compile-time constant shared with production. The test
// therefore replaced the operator's real com.tslink.daemon job with one
// pointing at the ephemeral cmd.test binary, and when the temp dir was cleaned
// up launchd could no longer start it (exit 78, EX_CONFIG). Production was
// down for 23 minutes. Path isolation cannot prevent this; only closing the
// process exit can.
var ErrServiceManagerBlocked = errors.New("test service manager guard blocked a real OS service manager call")

// ServiceManagerCall is one function-shaped process exit to a service manager.
type ServiceManagerCall func(args ...string) ([]byte, error)

// ServiceManagerAttempt records one blocked call.
type ServiceManagerAttempt struct {
	Manager string
	Args    []string
	Stack   []byte
}

// ServiceManagerSeam binds one package-level seam variable to the guard. Get
// captures the real implementation so an explicitly opted-in test can still
// reach it; Set installs the blocking replacement.
type ServiceManagerSeam struct {
	Manager string
	Get     func() ServiceManagerCall
	Set     func(ServiceManagerCall)
}

// installedSeam is what the guard remembers about a seam it took over, so it
// can check at teardown that the seam is still pointing at guard code.
type installedSeam struct {
	manager string
	get     func() ServiceManagerCall
	blocked uintptr
}

// ServiceManagerGuard intercepts service-manager process exits for the
// lifetime of one test binary. The default state is deny: a seam that no test
// stubbed returns ErrServiceManagerBlocked without executing anything, the
// call site is recorded with its stack, and the package exits non-zero.
type ServiceManagerGuard struct {
	pkg string

	mu        sync.Mutex
	attempts  []ServiceManagerAttempt
	real      map[string]ServiceManagerCall
	allowed   map[string]string
	optIns    []string
	installed []installedSeam
}

// NewServiceManagerGuard builds an installed-nowhere guard. Callers wire seams
// with Install; tests construct guards directly to exercise the guard itself.
func NewServiceManagerGuard(packageName string) *ServiceManagerGuard {
	return &ServiceManagerGuard{
		pkg:     packageName,
		real:    make(map[string]ServiceManagerCall),
		allowed: make(map[string]string),
	}
}

// Blocked returns the replacement call for manager, remembering real so an
// explicit AllowReal opt-in can delegate to it later.
func (g *ServiceManagerGuard) Blocked(manager string, real ServiceManagerCall) ServiceManagerCall {
	g.mu.Lock()
	g.real[manager] = real
	g.mu.Unlock()

	return func(args ...string) ([]byte, error) {
		g.mu.Lock()
		delegate, allowed := g.real[manager], g.allowed[manager]
		g.mu.Unlock()
		if allowed != "" && delegate != nil {
			return delegate(args...)
		}

		g.mu.Lock()
		g.attempts = append(g.attempts, ServiceManagerAttempt{
			Manager: manager,
			Args:    append([]string(nil), args...),
			Stack:   debug.Stack(),
		})
		g.mu.Unlock()
		return nil, fmt.Errorf("%w: %s %s", ErrServiceManagerBlocked, manager, strings.Join(args, " "))
	}
}

// Install replaces every seam with its blocking counterpart and returns a
// restore function that puts the real implementations back.
//
// A malformed seam panics rather than being skipped. A seam with a nil Get or
// Set protects nothing, and the old silent `continue` produced a guard that
// looked installed and reported nothing while one exit stayed wide open.
func (g *ServiceManagerGuard) Install(seams ...ServiceManagerSeam) func() {
	restores := make([]func(), 0, len(seams))
	for _, seam := range seams {
		seam := seam
		if seam.Manager == "" || seam.Get == nil || seam.Set == nil {
			panic(fmt.Sprintf("testenv: malformed ServiceManagerSeam %q: Manager, Get and Set are all required "+
				"(a seam that cannot be read or written protects nothing)", seam.Manager))
		}
		real := seam.Get()
		blocked := g.Blocked(seam.Manager, real)
		seam.Set(blocked)
		g.mu.Lock()
		g.installed = append(g.installed, installedSeam{
			manager: seam.Manager,
			get:     seam.Get,
			blocked: reflect.ValueOf(blocked).Pointer(),
		})
		g.mu.Unlock()
		restores = append(restores, func() { seam.Set(real) })
	}
	return func() {
		for _, restore := range restores {
			restore()
		}
	}
}

// DriftedSeams names every seam that is no longer pointing at guard code.
//
// This is the commission half of the guard. Default-deny closes the omission
// case (a test that forgot to stub), but nothing stops a test from writing
//
//	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
//	        return exec.Command(theRealBinary, args...).CombinedOutput()
//	}
//
// and never putting it back: that goes *through* the seam variable, so the
// guard's own closure is simply gone. Comparing the seam's current code
// pointer against the closure Install put there turns that into a red run.
//
// What it cannot see, stated plainly:
//   - a seam that is re-pointed and then restored before this runs. Every
//     closure Blocked returns shares one code pointer (they come from the same
//     func literal), so "restored to a guard closure" is the only property
//     checked, not "restored to *my* guard closure".
//   - anything that happens after the check: a leaked goroutine, or the window
//     between restore() and process exit.
//   - a test that never touches the seam and calls exec.Command itself. That
//     is the source inventory's job, not this one.
func (g *ServiceManagerGuard) DriftedSeams() []string {
	g.mu.Lock()
	installed := append([]installedSeam(nil), g.installed...)
	g.mu.Unlock()

	var drifted []string
	for _, seam := range installed {
		if reflect.ValueOf(seam.get()).Pointer() != seam.blocked {
			drifted = append(drifted, seam.manager)
		}
	}
	sort.Strings(drifted)
	return drifted
}

// AllowReal is the only way for a guarded seam to reach the real binary:
// forgetting to stub a seam cannot silently fall through to it, and every
// opt-in is named in the guard's report.
//
// It is not a claim that the real binary is otherwise unreachable from a test
// process. A test can still build its own exec.Command, or re-point the seam
// variable at one (see DriftedSeams and the source inventory test for the two
// checks that cover those). The returned function revokes the opt-in.
func (g *ServiceManagerGuard) AllowReal(manager, reason string) func() {
	if reason == "" {
		panic("testenv: AllowReal requires a reason")
	}
	g.mu.Lock()
	g.allowed[manager] = reason
	g.optIns = append(g.optIns, manager+": "+reason)
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		delete(g.allowed, manager)
		g.mu.Unlock()
	}
}

// Attempts returns the blocked calls recorded so far.
func (g *ServiceManagerGuard) Attempts() []ServiceManagerAttempt {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]ServiceManagerAttempt(nil), g.attempts...)
}

// Report writes the guard summary to stderr and reports whether the package
// must fail. A blocked attempt is always fatal for the package: the test that
// made it is not isolated, and the next run of it on a developer machine is
// what takes production down.
//
// An opt-in line is printed whenever one was taken, independent of the env var
// and of whether anything was blocked. An AllowReal that runs the real binary
// and blocks nothing used to leave no trace at all unless the operator had
// already set the report env, i.e. the audit record vanished in exactly the
// environment (CI) where nobody is watching. A run with no opt-ins and no
// blocked attempts stays silent unless the env asks.
func (g *ServiceManagerGuard) Report() bool {
	hits := g.Attempts()
	g.mu.Lock()
	optIns := append([]string(nil), g.optIns...)
	g.mu.Unlock()
	sort.Strings(optIns)

	if envIsTrue(os.Getenv(ServiceManagerGuardReportEnv)) || len(hits) > 0 || len(optIns) > 0 {
		fmt.Fprintf(os.Stderr, "service manager guard [%s]: blocked real service manager calls = %d\n", g.pkg, len(hits))
	}
	for _, optIn := range optIns {
		fmt.Fprintf(os.Stderr, "service manager guard [%s]: explicit real opt-in %s\n", g.pkg, optIn)
	}
	for i, hit := range hits {
		fmt.Fprintf(os.Stderr, "service manager guard [%s]: blocked attempt %d: %s %s\n%s",
			g.pkg, i+1, hit.Manager, strings.Join(hit.Args, " "), hit.Stack)
	}
	return len(hits) > 0
}

// envIsTrue accepts the spellings an operator actually types.
func envIsTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "t", "true", "y", "yes", "on":
		return true
	default:
		return false
	}
}

// RunWithServiceManagerGuard runs one package's complete test binary with every
// listed OS service manager exit closed. See ErrServiceManagerBlocked for the
// incident this prevents.
//
// Teardown is deferred so a panic in run() still restores the seams and still
// emits the report: the blocked-attempt list is the only record of what went
// wrong, and losing it to a panic means the next reader sees a crash with no
// explanation.
func RunWithServiceManagerGuard(run func() int, packageName string, seams ...ServiceManagerSeam) (code int) {
	guard := NewServiceManagerGuard(packageName)
	restore := guard.Install(seams...)
	previous := swapActiveServiceManagerGuard(guard)

	defer func() {
		// Restore the enclosing guard rather than clearing the slot: a nested
		// run used to leave the outer, still-installed guard invisible.
		swapActiveServiceManagerGuard(previous)
		drifted := guard.DriftedSeams()
		restore()
		for _, manager := range drifted {
			fmt.Fprintf(os.Stderr, "service manager guard [%s]: seam %s was re-pointed away from the guard "+
				"and never restored; a test replaced the guarded call with its own implementation, "+
				"so this package ran unguarded\n", packageName, manager)
		}
		if guard.Report() || len(drifted) > 0 {
			code = 1
		}
	}()

	return run()
}

var (
	activeMu    sync.Mutex
	activeGuard *ServiceManagerGuard
)

// swapActiveServiceManagerGuard installs g and returns the guard it replaced.
func swapActiveServiceManagerGuard(g *ServiceManagerGuard) *ServiceManagerGuard {
	activeMu.Lock()
	defer activeMu.Unlock()
	previous := activeGuard
	activeGuard = g
	return previous
}

// ActiveServiceManagerGuard returns the guard installed by the running
// RunWithServiceManagerGuard, or nil when no guard is installed. A package
// whose TestMain claims to be guarded can assert on this being non-nil.
func ActiveServiceManagerGuard() *ServiceManagerGuard {
	activeMu.Lock()
	defer activeMu.Unlock()
	return activeGuard
}
