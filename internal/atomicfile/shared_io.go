package atomicfile

import (
	"errors"
	"io"
	"runtime"
	"syscall"
	"time"
)

// RetryFileOperation is the shared Windows state-file policy: seven attempts
// for sharing/access errors, with 1, 2, 4, 8, 16 and 32 ms sleeps (63 ms total).
// The filesystem operation itself and OS scheduling are not time-bounded.
func RetryFileOperation(operation func() error) error {
	if runtime.GOOS != "windows" {
		return operation()
	}
	return RetrySharingViolation(operation, time.Sleep)
}

// IsWindowsSharingError recognises Windows sharing and access-denied errors,
// including wrapped errors. Callers apply it only on Windows.
func IsWindowsSharingError(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(5))
}

// RetrySharingViolation implements the policy with an injectable sleep, so
// callers can test its budget without depending on the host scheduler.
func RetrySharingViolation(operation func() error, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		err := operation()
		if err == nil || !IsWindowsSharingError(err) || attempt == 6 {
			return err
		}
		sleep(time.Millisecond << attempt)
	}
}

// ReadFile reads one shared snapshot, settling a transient missing name before
// its caller interprets absence. Bounded/special-file readers use the same
// ReadSettled and RetryFileOperation wrappers around their guarded read.
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := ReadSettled(path, func() error {
		return RetryFileOperation(func() error {
			f, err := OpenSharedRead(path)
			if err != nil {
				return err
			}
			defer f.Close()
			data, err = io.ReadAll(f)
			return err
		})
	})
	return data, err
}

func replaceStateFile(source, target string) error {
	return RetryFileOperation(func() error { return ReplaceFile(source, target) })
}
