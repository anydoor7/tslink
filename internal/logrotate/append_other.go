//go:build !unix && !windows

package logrotate

import "os"

// isAppendOnly has no implementation on this platform. Reporting "cannot
// verify" rather than guessing keeps RotateStderrLog on its refusing path: a
// wrong "yes" here is a truncate that produces a sparse file, which is the
// exact failure this package exists to prevent.
func isAppendOnly(*os.File) (bool, error) {
	return false, ErrCannotVerifyAppend
}

// truncateLog is unreachable here, because isAppendOnly never accepts.
func truncateLog(f *os.File, _ string, _ os.FileInfo) error {
	return f.Truncate(0)
}
