//go:build unix

package logrotate

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// isAppendOnly reads the open file description's status flags. It asks the
// kernel about this exact descriptor rather than inferring from how the file
// looks on disk, because O_APPEND is a property of the open, not of the file:
// two descriptors on the same inode can disagree, and it is the one this
// process writes through that decides where a write lands.
func isAppendOnly(f *os.File) (bool, error) {
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrCannotVerifyAppend, err)
	}
	return flags&unix.O_APPEND != 0, nil
}

// truncateLog truncates through the descriptor the process writes to, which
// needs nothing beyond the write access it already has.
func truncateLog(f *os.File, _ string, _ os.FileInfo) error {
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("truncate the log through its own descriptor: %w", err)
	}
	return nil
}
