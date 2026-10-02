package health

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openProbeFile(path string) (*os.File, error) {
	entry, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	resolved, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !validWindowsProbeModes(entry.Mode(), resolved.Mode()) {
		return nil, os.ErrInvalid
	}
	// Resolve through a handle: Go 1.26 reports junctions as ModeIrregular,
	// which filepath.EvalSymlinks does not follow. This also resolves junctions
	// in parent directories and mount points on other volumes.
	target, err := openProbeHandle(path, 0, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return nil, err
	}
	defer target.Close()
	before, err := target.Stat()
	if err != nil {
		return nil, err
	}
	path, err = finalProbePath(windows.Handle(target.Fd()), windows.GetFinalPathNameByHandle)
	if err != nil {
		return nil, err
	}
	// Open the resolved entry itself; reject a reparse point substituted after
	// resolution by comparing the two handle identities and the final shape.
	f, err := openProbeHandle(path, windows.GENERIC_READ,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || (!after.IsDir() && !after.Mode().IsRegular()) || !os.SameFile(before, after) {
		f.Close()
		return nil, os.ErrInvalid
	}
	return f, nil
}

// An irregular entry can be a directory junction or mount point. Only the
// resolved directory shape admits it; pipes, devices and irregular file
// targets remain invalid.
func validWindowsProbeModes(entry, resolved os.FileMode) bool {
	if !resolved.IsDir() && !resolved.IsRegular() {
		return false
	}
	switch entry & os.ModeType {
	case 0, os.ModeDir, os.ModeSymlink:
		return true
	case os.ModeIrregular, os.ModeIrregular | os.ModeDir:
		return resolved.IsDir()
	default:
		return false
	}
}

func finalProbePath(handle windows.Handle, get func(windows.Handle, *uint16, uint32, uint32) (uint32, error)) (string, error) {
	buf := make([]uint16, 260)
	var flags uint32
	for {
		n, err := get(handle, &buf[0], uint32(len(buf)), flags)
		// A mounted local volume may have no drive letter. UNC shares use
		// DOS paths, so attempt the volume GUID only for this specific error.
		if errors.Is(err, windows.ERROR_PATH_NOT_FOUND) && flags == 0 {
			flags = 0x1 // VOLUME_NAME_GUID from GetFinalPathNameByHandleW.
			continue
		}
		if err != nil {
			return "", err
		}
		if n < uint32(len(buf)) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		if n >= 32768 {
			return "", os.ErrInvalid
		}
		buf = make([]uint16, n+1)
	}
}

func openProbeHandle(path string, access, flags uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// CreateFile does not wait for a named-pipe server to become available.
	h, err := windows.CreateFile(name, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return nil, err
	}
	kind, err := windows.GetFileType(h)
	if err != nil || kind != windows.FILE_TYPE_DISK {
		windows.CloseHandle(h)
		return nil, os.ErrInvalid
	}
	return os.NewFile(uintptr(h), path), nil
}

// Stat follows supported name-surrogate reparse points, including junctions,
// just as registry admission does. Reject other irregular objects in the
// common shape check. Capture the pre-open identity via fstat: Windows Stat
// can otherwise load its file ID lazily from a path replaced after this check.
func statProbeFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return info, err
	}
	f, err := openProbeFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}
