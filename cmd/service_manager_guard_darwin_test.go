//go:build darwin

package cmd

import (
	"context"
	"github.com/anydoor7/tslink/internal/testenv"
)

// osServiceManagerSeams lists every launchd process exit this package owns.
// launchctl writes (bootout/bootstrap) address gui/<uid>, a namespace shared
// with the operator's running daemon and unreachable by HOME or t.TempDir()
// isolation, so the exit itself is what has to be closed. See
// testenv.ErrServiceManagerBlocked for the incident.
func osServiceManagerSeams() []testenv.ServiceManagerSeam {
	return []testenv.ServiceManagerSeam{
		contextManagerSeam("launchctl",
			func() func(context.Context, ...string) ([]byte, error) { return launchctlCombinedOutput },
			func(fn func(context.Context, ...string) ([]byte, error)) { launchctlCombinedOutput = fn }),
	}
}

// serviceManagerGuardProbe calls this platform's guarded seam the way an
// unisolated test would. The target label is deliberately one that does not
// exist and the verb is read-only, so even a probe that escaped the guard
// could not mutate the operator's launchd domain.
func serviceManagerGuardProbe() ([]byte, error) {
	return launchctlCombinedOutput(context.Background(), "print", launchctlDomain()+"/com.tslink.guard-probe.invalid")
}
