//go:build windows

package atomicfile

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSharedReplaceKeepsHeldSnapshotWhereRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenSharedRead(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Control: Go's rename (MoveFileEx) is refused while the shared-delete
	// reader holds the destination. This is the hosted CI failure mode.
	control := filepath.Join(dir, "control.tmp")
	if err := os.WriteFile(control, []byte("control"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(control, target); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("os.Rename control with held destination = %v, want ERROR_ACCESS_DENIED", err)
	}
	source := filepath.Join(dir, "source.tmp")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(source, target); err != nil {
		t.Fatalf("POSIX replacement with held destination: %v", err)
	}
	old, err := io.ReadAll(f)
	if err != nil || string(old) != "old" {
		t.Fatalf("held snapshot = %q, %v", old, err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("replaced target = %q, %v", data, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source remains after replacement: %v", err)
	}
}

func TestSharedReplaceLegacyReaderStillBlocks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(target) // No FILE_SHARE_DELETE: a legacy/third-party reader.
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	source := filepath.Join(dir, "source.tmp")
	if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	err = ReplaceFile(source, target)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("legacy reader must surface a sharing error for the caller's retry: %v", err)
	}
	var link *os.LinkError
	if !errors.As(err, &link) || link.Old != source || link.New != target {
		t.Fatalf("error is not a rename LinkError for source/target: %#v", err)
	}
	if data, _ := os.ReadFile(source); string(data) != "new" {
		t.Fatalf("failed replacement lost source = %q", data)
	}
	if data, _ := io.ReadAll(f); string(data) != "old" {
		t.Fatalf("failed replacement damaged target = %q", data)
	}
}

func TestSharedReplaceWindowsRenameFallbackAndErrors(t *testing.T) {
	old := setFileInformationFn
	t.Cleanup(func() { setFileInformationFn = old })
	for _, code := range []error{windows.ERROR_INVALID_PARAMETER, windows.ERROR_INVALID_FUNCTION, windows.ERROR_NOT_SUPPORTED, windows.ERROR_ACCESS_DENIED} {
		t.Run(code.Error(), func(t *testing.T) {
			dir := t.TempDir()
			source, target := filepath.Join(dir, "temp"), filepath.Join(dir, "target")
			if err := os.WriteFile(source, []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			setFileInformationFn = func(h windows.Handle, class uint32, data *byte, length uint32) error {
				calls++
				buf := unsafe.Slice(data, length)
				if class != windows.FileRenameInfoEx || binary.LittleEndian.Uint32(buf) != 3 {
					t.Fatalf("rename class/flags = %d/%d", class, binary.LittleEndian.Uint32(buf))
				}
				if len(buf) < 2 || binary.LittleEndian.Uint16(buf[len(buf)-2:]) != 0 {
					t.Fatal("rename path must retain its NUL terminator for Win32 conversion")
				}
				// FILE_RENAME_INFO ABI, independent of the implementation's
				// offset arithmetic: Flags(4) + pad + RootDirectory(handle) +
				// FileNameLength(4), then the name: 20 bytes on 64-bit, 12 on 32-bit.
				nameOffset := 20
				if unsafe.Sizeof(uintptr(0)) == 4 {
					nameOffset = 12
				}
				extended, err := windowsExtendedPath(target)
				if err != nil {
					t.Fatal(err)
				}
				wantName, err := windows.UTF16FromString(extended)
				if err != nil {
					t.Fatal(err)
				}
				if len(buf) != nameOffset+2*len(wantName) {
					t.Fatalf("buffer length = %d, want %d", len(buf), nameOffset+2*len(wantName))
				}
				// FileNameLength counts bytes and excludes the NUL terminator.
				if got, want := binary.LittleEndian.Uint32(buf[nameOffset-4:]), uint32(2*(len(wantName)-1)); got != want {
					t.Fatalf("FileNameLength = %d, want %d", got, want)
				}
				for i, ch := range wantName {
					if got := binary.LittleEndian.Uint16(buf[nameOffset+2*i:]); got != ch {
						t.Fatalf("FileName[%d] = %#x, want %#x", i, got, ch)
					}
				}
				return code
			}
			err := ReplaceFile(source, target)
			want := "new"
			if code == windows.ERROR_ACCESS_DENIED {
				want = "old"
				if !errors.Is(err, code) {
					t.Fatalf("non-capability error masked: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unsupported-class fallback: %v", err)
			}
			if calls != 1 {
				t.Fatalf("rename calls = %d", calls)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != want {
				t.Fatalf("target = %q, %v", data, err)
			}
		})
	}
	if err := ReplaceFile("bad\x00source", "valid"); err == nil {
		t.Fatal("NUL source accepted")
	}
	if err := ReplaceFile("valid", "bad\x00target"); err == nil {
		t.Fatal("NUL target accepted")
	}
	if err := ReplaceFile(filepath.Join(t.TempDir(), "missing"), "valid"); !os.IsNotExist(err) {
		t.Fatalf("missing source: %v", err)
	}
	if _, err := OpenSharedRead(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing shared read: %v", err)
	}
}

func TestSharedReplaceWindowsLongUnicodePath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 300 {
		dir = filepath.Join(dir, strings.Repeat("segment", 5))
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "state-\u96ea.json")
	for _, content := range []string{"first", "second"} {
		if err := WriteFileWithReplace(target, []byte(content), ReplaceFile); err != nil {
			t.Fatal(err)
		}
	}
	f, err := OpenSharedRead(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if data, err := io.ReadAll(f); err != nil || string(data) != "second" {
		t.Fatalf("long Unicode path = %q, %v", data, err)
	}
	for _, p := range []string{`C:\state\file.json`, `\\server\share\file.json`, `\\?\C:\state\file.json`, `\\.\pipe\synthetic`} {
		abs, err := windowsExtendedPath(p)
		if err != nil || (!strings.HasPrefix(abs, `\\?\`) && !strings.HasPrefix(abs, `\\.\`)) {
			t.Fatalf("extended path = %q, %v", abs, err)
		}
	}
}
