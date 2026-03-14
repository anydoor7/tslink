// Package filelock provides cross-platform file locking.
package filelock

import "os"

// Lock places an exclusive advisory lock on the given file.
// It blocks until the lock is acquired.
func Lock(f *os.File) error {
	return lock(f)
}

// Unlock releases the advisory lock on the given file.
func Unlock(f *os.File) error {
	return unlock(f)
}
