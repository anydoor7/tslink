package registry

import (
	"io"
	"os"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
)

// retryRegistryFileOperation handles Windows handles that briefly prevent a
// read or replacement. Seven attempts sleep 1, 2, 4, 8, 16 and 32 ms (63 ms
// total); permanent access denial still returns the last error. File operations
// themselves and OS scheduling are not bounded by this sleep budget.
func retryRegistryFileOperation(operation func() error) error {
	return atomicfile.RetryFileOperation(operation)
}

func isWindowsSharingError(err error) bool {
	// Win32 ERROR_SHARING_VIOLATION (32) and ERROR_ACCESS_DENIED (5).
	// Numeric Errno values let the injected policy tests run on every OS;
	// retryRegistryFileOperation enables this classifier only on Windows.
	return atomicfile.IsWindowsSharingError(err)
}

func retryRegistrySharingViolation(operation func() error, sleep func(time.Duration)) error {
	return atomicfile.RetrySharingViolation(operation, sleep)
}

// Registry readers allow delete sharing and replacements use POSIX semantics
// on Windows, so a reader holding the old snapshot cannot block the daemon's
// writer. atomicfile owns both primitives; other platforms use os.Open/Rename.
func openRegistryFile(path string) (*os.File, error) {
	return atomicfile.OpenSharedRead(path)
}

func replaceRegistryFile(source, target string) error {
	return atomicfile.ReplaceFile(source, target)
}

func readRegistryFileOnce(path string) ([]byte, error) {
	f, err := openRegistryFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// readRegistryBytes retries Windows sharing errors (32/5) and, through
// atomicfile.ReadSettled, rereads a not-exist result while a replacement of
// path is in progress, so loaders that map "missing" to an empty registry do
// not report a configured registry as absent mid-replace.
func readRegistryBytes(path string) ([]byte, error) {
	var data []byte
	err := atomicfile.ReadSettled(path, func() error {
		return retryRegistryFileOperation(func() error {
			var err error
			data, err = readRegistryFile(path)
			return err
		})
	})
	return data, err
}

// ReadFile reads a registry file (registry.json or a tentative mark) with the
// same sharing, retry and replacement-settle behavior as the loaders. Callers
// outside this package use it instead of os.ReadFile.
func ReadFile(path string) ([]byte, error) {
	return readRegistryBytes(path)
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
