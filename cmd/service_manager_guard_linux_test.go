//go:build linux

package cmd

import (
	"context"
	"github.com/anydoor7/tslink/internal/testenv"
)

// osServiceManagerSeams lists every systemd process exit this package owns.
// systemctl --user writes against the caller's live user manager, which no
// filesystem isolation can scope, so the exit itself is what has to be closed;
// loginctl is read-only but is the same kind of unscoped exit. See
// testenv.ErrServiceManagerBlocked for the incident this mirrors on darwin.
func osServiceManagerSeams() []testenv.ServiceManagerSeam {
	return []testenv.ServiceManagerSeam{
		contextManagerSeam("systemctl",
			func() func(context.Context, ...string) ([]byte, error) { return systemctlCombinedOutput },
			func(fn func(context.Context, ...string) ([]byte, error)) { systemctlCombinedOutput = fn }),
		contextManagerSeam("loginctl",
			func() func(context.Context, ...string) ([]byte, error) { return loginctlCombinedOutputFn },
			func(fn func(context.Context, ...string) ([]byte, error)) { loginctlCombinedOutputFn = fn }),
	}
}

// serviceManagerGuardProbe calls this platform's guarded seam the way an
// unisolated test would. The unit name is deliberately one that does not
// exist and `show` is read-only, so even a probe that escaped the guard could
// not mutate the caller's systemd user manager.
func serviceManagerGuardProbe() ([]byte, error) {
	return systemctlCombinedOutput(context.Background(), "--user", "show", "tslink-guard-probe-invalid.service", "--property=LoadState")
}
