package registry

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var setRegistryFileInformation = windows.SetFileInformationByHandle

// registryWindowsPath preserves long-path support without changing Windows
// settings. The extended prefix requires an absolute, normalized path.
func registryWindowsPath(path string) (string, error) {
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

func openRegistryFile(path string) (*os.File, error) {
	abs, err := registryWindowsPath(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	wide, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// Unlike os.Open on Go 1.26 and 1.27, allow a writer to replace the
	// directory entry while this handle continues reading the old snapshot.
	h, err := windows.CreateFile(wide, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

func replaceRegistryFile(source, target string) error {
	err := replaceRegistryFileWindows(source, target)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: source, New: target, Err: err}
	}
	return nil
}

func replaceRegistryFileWindows(source, target string) error {
	abs, err := registryWindowsPath(target)
	if err != nil {
		return err
	}
	name, err := windows.UTF16FromString(abs)
	if err != nil {
		return err
	}
	src, err := registryWindowsPath(source)
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
	err = setRegistryFileInformation(h, windows.FileRenameInfoEx, &buf[0], uint32(len(buf)))
	_ = windows.CloseHandle(h)
	// Older Windows/filesystems can reject the extended rename class. Keep
	// their existing rename semantics, still under the same bounded retry.
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return os.Rename(source, target)
	}
	return err
}
