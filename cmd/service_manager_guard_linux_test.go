//go:build linux

package cmd

import "github.com/anydoor7/tslink/internal/testenv"

// osServiceManagerSeams lists every systemd process exit this package owns.
// systemctl --user writes against the caller's live user manager, which no
// filesystem isolation can scope, so the exit itself is what has to be closed;
// loginctl is read-only but is the same kind of unscoped exit. See
// testenv.ErrServiceManagerBlocked for the incident this mirrors on darwin.
func osServiceManagerSeams() []testenv.ServiceManagerSeam {
	return []testenv.ServiceManagerSeam{
		{
			Manager: "systemctl",
			Get:     func() testenv.ServiceManagerCall { return systemctlCombinedOutput },
			Set:     func(fn testenv.ServiceManagerCall) { systemctlCombinedOutput = fn },
		},
		{
			Manager: "loginctl",
			Get:     func() testenv.ServiceManagerCall { return loginctlCombinedOutputFn },
			Set:     func(fn testenv.ServiceManagerCall) { loginctlCombinedOutputFn = fn },
		},
	}
}

// serviceManagerGuardProbe calls this platform's guarded seam the way an
// unisolated test would. The unit name is deliberately one that does not
// exist and `show` is read-only, so even a probe that escaped the guard could
// not mutate the caller's systemd user manager.
func serviceManagerGuardProbe() ([]byte, error) {
	return systemctlCombinedOutput("--user", "show", "tslink-guard-probe-invalid.service", "--property=LoadState")
}
