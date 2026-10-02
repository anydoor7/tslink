package health

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func openProbeFile(path string) (*os.File, error) {
	// Preserve registered symlinks, but open the resolved final entry without
	// following a reparse point substituted after this resolution.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(resolved)
	if err != nil {
		return nil, err
	}
	// Open the entry itself rather than a newly substituted reparse point.
	// CreateFile does not wait for a named-pipe server to become available.
	h, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
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
