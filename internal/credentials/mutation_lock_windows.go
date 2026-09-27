//go:build windows

package credentials

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockCredentialFile(f *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()), 0x00000002|0x00000001, 0, 1, 0, &overlapped)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION):
		return false, nil
	default:
		return false, err
	}
}
