//go:build windows

package logrotate

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAccessInformation is FILE_INFORMATION_CLASS FileAccessInformation, which
// golang.org/x/sys/windows does not name.
const fileAccessInformation = 8

// isAppendOnly reads the access this handle was granted. Windows has no
// O_APPEND flag on an open; the equivalent is a handle that holds
// FILE_APPEND_DATA without FILE_WRITE_DATA, which the kernel only lets write at
// end-of-file. That is the handle os.OpenFile makes for O_APPEND, including the
// one internal/daemon opens for `serve --daemon`. A handle that also holds
// FILE_WRITE_DATA (cmd.exe's `2>>`, for one) writes at its own offset, so after
// a truncate it would leave the same sparse file O_APPEND prevents on unix, and
// it is reported as not append-only.
//
// Like the unix probe, this asks about the exact handle rather than about the
// file, because two handles on one file can hold different access.
func isAppendOnly(f *os.File) (bool, error) {
	var status windows.IO_STATUS_BLOCK
	var access uint32
	if err := windows.NtQueryInformationFile(windows.Handle(f.Fd()), &status,
		(*byte)(unsafe.Pointer(&access)), uint32(unsafe.Sizeof(access)), fileAccessInformation); err != nil {
		return false, fmt.Errorf("%w: %v", ErrCannotVerifyAppend, err)
	}
	return access&windows.FILE_APPEND_DATA != 0 && access&windows.FILE_WRITE_DATA == 0, nil
}

// truncateLog empties the log through a second handle. The append-only handle
// isAppendOnly accepts cannot do it itself: setting end-of-file needs
// FILE_WRITE_DATA, which is the very right whose absence makes the handle safe.
// The second handle is checked to be the file f writes to, so a path replaced
// since the caller's check is refused rather than truncated. f's next write
// still lands at the new end-of-file, because that is the only place it can
// write.
func truncateLog(f *os.File, target string, opened os.FileInfo) error {
	writer, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("truncate the log through a second handle: open: %w", err)
	}
	defer writer.Close()
	info, err := writer.Stat()
	if err != nil {
		return fmt.Errorf("truncate the log through a second handle: stat: %w", err)
	}
	if !os.SameFile(opened, info) {
		return errors.New("truncate the log through a second handle: the configured log file was replaced during rotation; it was not truncated")
	}
	if err := writer.Truncate(0); err != nil {
		return fmt.Errorf("truncate the log through a second handle: %w", err)
	}
	return nil
}
