package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWave2WindowsReadAfterSharingRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := WriteFile(path, []byte("snapshot")); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	old := readAttemptFn
	t.Cleanup(func() { readAttemptFn = old })
	blocked := 0
	readAttemptFn = func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		if blocked == 0 {
			if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				t.Fatalf("real exclusive handle did not block reading: %v", err)
			}
			blocked++
			if err := windows.CloseHandle(handle); err != nil {
				t.Fatal(err)
			}
		}
		return data, err
	}
	data, err := ReadFile(path)
	if err != nil || string(data) != "snapshot" || blocked != 1 {
		t.Fatalf("read after release: %q %v, blocked=%d", data, err, blocked)
	}
}

func TestWave2WindowsReadPersistentError(t *testing.T) {
	old := readAttemptFn
	t.Cleanup(func() { readAttemptFn = old })
	for _, cause := range []error{windows.ERROR_SHARING_VIOLATION, windows.ERROR_FILE_NOT_FOUND} {
		calls := 0
		readAttemptFn = func(path string) ([]byte, error) {
			calls++
			return nil, &os.PathError{Op: "open", Path: path, Err: cause}
		}
		data, err := ReadFile("missing")
		want := 25
		if cause == windows.ERROR_FILE_NOT_FOUND {
			want = 1
		}
		if data != nil || !errors.Is(err, cause) || calls != want {
			t.Fatalf("persistent error: %q %v calls=%d want=%d", data, err, calls, want)
		}
	}
}
