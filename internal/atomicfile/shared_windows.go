//go:build windows

package atomicfile

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var setFileInformationFn = windows.SetFileInformationByHandle

// windowsExtendedPath preserves long-path support without changing Windows
// settings. The extended prefix requires an absolute, normalized path.
func windowsExtendedPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(abs, `\\?\`) || strings.HasPrefix(abs, `\\.\`) {
		return abs, nil
	}
	if strings.HasPrefix(abs, `\\`) {
		return `\\?\UNC\` + abs[2:], nil
	}
	return `\\?\` + abs, nil
}

// OpenSharedRead opens path for reading with read, write and delete sharing.
// Unlike os.Open on Go 1.26 and 1.27, the handle lets ReplaceFile swap the
// directory entry while this reader continues reading the old snapshot.
func OpenSharedRead(path string) (*os.File, error) {
	abs, err := windowsExtendedPath(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	wide, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := windows.CreateFile(wide, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// ReplaceFile atomically replaces target with source. It uses
// SetFileInformationByHandle(FileRenameInfoEx) with POSIX semantics, which
// succeeds while OpenSharedRead handles hold target; os.Rename (MoveFileEx)
// returns ERROR_ACCESS_DENIED in that case. Callers own any retry policy.
func ReplaceFile(source, target string) error {
	if err := replaceFileWindows(source, target); err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	return nil
}

func replaceFileWindows(source, target string) error {
	abs, err := windowsExtendedPath(target)
	if err != nil {
		return err
	}
	name, err := windows.UTF16FromString(abs)
	if err != nil {
		return err
	}
	src, err := windowsExtendedPath(source)
	if err != nil {
		return err
	}
	wide, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	h, err := windows.CreateFile(wide, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	// FileRenameInfoEx with POSIX semantics permits replacing a destination
	// still held by shared-delete readers; MoveFileEx does not on this path.
	// Compute the ABI offset for both 32-bit and 64-bit Windows handles.
	var header struct {
		Flags          uint32
		RootDirectory  windows.Handle
		FileNameLength uint32
	}
	nameOffset := int(unsafe.Offsetof(header.FileNameLength)) + 4
	buf := make([]byte, nameOffset+len(name)*2)
	binary.LittleEndian.PutUint32(buf, 0x1|0x2) // REPLACE_IF_EXISTS | POSIX_SEMANTICS
	// Keep the NUL for Win32 path conversion, while the counted name excludes
	// it. Without it, the Win32 wrapper can read beyond the counted buffer.
	binary.LittleEndian.PutUint32(buf[nameOffset-4:], uint32((len(name)-1)*2))
	for i, ch := range name {
		binary.LittleEndian.PutUint16(buf[nameOffset+2*i:], ch)
	}
	err = setFileInformationFn(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf)))
	_ = windows.CloseHandle(h)
	// Older Windows/filesystems can reject the extended rename class. Keep
	// their existing rename semantics; the caller's retry policy still applies.
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return os.Rename(source, target)
	}
	return err
}
