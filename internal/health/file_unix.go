//go:build unix

package health

import (
	"os"
	"syscall"
)

func openProbeFile(path string) (*os.File, error) {
	// A file can become a FIFO after lstat. O_NONBLOCK prevents that open from
	// waiting for a writer; the common fstat check rejects the replacement.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
