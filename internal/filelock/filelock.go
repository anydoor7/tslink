// Package filelock provides cross-platform file locking.
package filelock

import "os"

// Lock places an exclusive advisory lock on the given file.
// It blocks until the lock is acquired.
func Lock(f *os.File) error {
	return lock(f)
}

// TryLock attempts the same exclusive lock without waiting. A false result
// means another holder owns it; callers must leave protected state unchanged.
func TryLock(f *os.File) (bool, error) {
	return tryLock(f)
}

// Unlock releases the advisory lock on the given file.
func Unlock(f *os.File) error {
	return unlock(f)
}
