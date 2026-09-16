package testenv

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// ServiceManagerGuardReportEnv makes the guard print its per-package summary
// line even when it recorded zero blocked attempts, so a green run can still
// prove the guard was installed rather than silently absent.
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

// ServiceManagerGuard intercepts service-manager process exits for the
// lifetime of one test binary. The default state is deny: a seam that no test
// stubbed returns ErrServiceManagerBlocked without executing anything, the
// call site is recorded with its stack, and the package exits non-zero.
type ServiceManagerGuard struct {
	pkg string

	mu       sync.Mutex
	attempts []ServiceManagerAttempt
	real     map[string]ServiceManagerCall
	allowed  map[string]string
	optIns   []string
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
func (g *ServiceManagerGuard) Install(seams ...ServiceManagerSeam) func() {
	restores := make([]func(), 0, len(seams))
	for _, seam := range seams {
		seam := seam
		if seam.Get == nil || seam.Set == nil {
			continue
		}
		real := seam.Get()
		seam.Set(g.Blocked(seam.Manager, real))
		restores = append(restores, func() { seam.Set(real) })
	}
	return func() {
		for _, restore := range restores {
			restore()
		}
	}
}

// AllowReal is the only way to reach the real binary. It is deliberately
// explicit and per-manager: forgetting to stub a seam cannot silently fall
// through to it, and every use is named in the guard's report. The returned
// function revokes the opt-in.
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
func (g *ServiceManagerGuard) Report() bool {
	hits := g.Attempts()
	g.mu.Lock()
	optIns := append([]string(nil), g.optIns...)
	g.mu.Unlock()
	sort.Strings(optIns)

	if os.Getenv(ServiceManagerGuardReportEnv) == "1" || len(hits) > 0 {
		fmt.Fprintf(os.Stderr, "service manager guard [%s]: blocked real service manager calls = %d\n", g.pkg, len(hits))
		for _, optIn := range optIns {
			fmt.Fprintf(os.Stderr, "service manager guard [%s]: explicit real opt-in %s\n", g.pkg, optIn)
		}
	}
	for i, hit := range hits {
		fmt.Fprintf(os.Stderr, "service manager guard [%s]: blocked attempt %d: %s %s\n%s",
			g.pkg, i+1, hit.Manager, strings.Join(hit.Args, " "), hit.Stack)
	}
	return len(hits) > 0
}

// RunWithServiceManagerGuard runs one package's complete test binary with every
// listed OS service manager exit closed. See ErrServiceManagerBlocked for the
// incident this prevents.
func RunWithServiceManagerGuard(run func() int, packageName string, seams ...ServiceManagerSeam) int {
	guard := NewServiceManagerGuard(packageName)
	restore := guard.Install(seams...)
	setActiveServiceManagerGuard(guard)

	code := run()

	setActiveServiceManagerGuard(nil)
	restore()
	if guard.Report() {
		return 1
	}
	return code
}

var (
	activeMu    sync.Mutex
	activeGuard *ServiceManagerGuard
)

func setActiveServiceManagerGuard(g *ServiceManagerGuard) {
	activeMu.Lock()
	activeGuard = g
	activeMu.Unlock()
}

// ActiveServiceManagerGuard returns the guard installed by the running
// RunWithServiceManagerGuard, or nil when no guard is installed. A package
// whose TestMain claims to be guarded can assert on this being non-nil.
func ActiveServiceManagerGuard() *ServiceManagerGuard {
	activeMu.Lock()
	defer activeMu.Unlock()
	return activeGuard
}
