package registry

import (
	"errors"
	"io"
	"runtime"
	"syscall"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
)

// retryRegistryFileOperation handles Windows handles that briefly prevent a
// read or replacement. Seven attempts sleep 1, 2, 4, 8, 16 and 32 ms (63 ms
// total); permanent access denial still returns the last error. File operations
// themselves and OS scheduling are not bounded by this sleep budget.
func retryRegistryFileOperation(operation func() error) error {
	if runtime.GOOS != "windows" {
		return operation()
	}
	return retryRegistrySharingViolation(operation, time.Sleep)
}

func isWindowsSharingError(err error) bool {
	// Win32 ERROR_SHARING_VIOLATION (32) and ERROR_ACCESS_DENIED (5).
	// Numeric Errno values let the injected policy tests run on every OS;
	// retryRegistryFileOperation enables this classifier only on Windows.
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(5))
}

func retryRegistrySharingViolation(operation func() error, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		err := operation()
		if err == nil || !isWindowsSharingError(err) || attempt == 6 {
			return err
		}
		sleep(time.Millisecond << attempt)
	}
}

func readRegistryFileOnce(path string) ([]byte, error) {
	f, err := openRegistryFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func readRegistryBytes(path string) ([]byte, error) {
	var data []byte
	err := retryRegistryFileOperation(func() error {
		var err error
		data, err = readRegistryFile(path)
		return err
	})
	return data, err
}

func convergeRegistryFile(path string) error {
	return retryRegistryFileOperation(func() error { return atomicfile.ConvergePrivateFile(path) })
}

func writeRegistryFile(path string, data []byte) error {
	// Keep the same synced temporary file across replace attempts.
	return atomicfile.WriteFileWithReplace(path, data, func(source, target string) error {
		return retryRegistryFileOperation(func() error { return replaceRegistryFile(source, target) })
	})
}
