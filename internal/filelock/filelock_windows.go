//go:build windows

package filelock

import (
	"os"

	"golang.org/x/sys/windows"
)

const (
	lockfileExclusiveLock = 0x00000002
)

func lock(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		lockfileExclusiveLock,
		0,
		1, 0,
		&ol,
	)
}

func unlock(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		1, 0,
		&ol,
	)
}
