//go:build windows

package logrotate

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// openRemovableSupervisedLog opens the append-only handle os.OpenFile makes for
// O_APPEND, but also shares delete. os.OpenFile never shares delete, so with
// its handle open Windows refuses to remove or rename the log, and the
// degraded tests could not reach their state at all. A supervisor that does
// share delete can have its log removed or replaced under it, which is the
// Windows form of the unix scenario those tests describe.
func openRemovableSupervisedLog(t *testing.T, path string, content []byte) *os.File {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	// The access syscall.Open grants for O_APPEND|O_WRONLY: everything
	// GENERIC_WRITE carries except FILE_WRITE_DATA.
	const appendOnly = windows.FILE_APPEND_DATA | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA |
		windows.STANDARD_RIGHTS_WRITE | windows.SYNCHRONIZE
	handle, err := windows.CreateFile(name, appendOnly,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open %s append-only sharing delete: %v", path, err)
	}
	file := os.NewFile(uintptr(handle), path)
	t.Cleanup(func() { _ = file.Close() })
	return file
}
