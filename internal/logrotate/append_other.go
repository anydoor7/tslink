//go:build !unix

package logrotate

import "os"

// isAppendOnly has no portable implementation off unix. Reporting "cannot
// verify" rather than guessing keeps RotateStderrLog on its refusing path: a
// wrong "yes" here is a truncate that produces a sparse file, which is the
// exact failure this package exists to prevent.
func isAppendOnly(*os.File) (bool, error) {
	return false, ErrCannotVerifyAppend
}
