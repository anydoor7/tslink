//go:build !windows

package credentials

import (
	"errors"
	"os"
	"syscall"
)

// credentialFileLockUnsupported reports flock errors that mean the
// filesystem cannot lock at all rather than that another holder has it.
func credentialFileLockUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOLCK) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.EINVAL)
}

func tryLockCredentialFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK), errors.Is(err, syscall.EAGAIN):
		return false, nil
	default:
		return false, err
	}
}
