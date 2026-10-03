package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWave2AtomicReplaceAfterWindowsReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	t.Logf("isolated target: %s", path)
	if err := WriteFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	oldAttempt := renameAttemptFn
	t.Cleanup(func() { renameAttemptFn = oldAttempt })
	blocked := 0
	renameAttemptFn = func(oldpath, newpath string) error {
		err := os.Rename(oldpath, newpath)
		if err != nil && blocked == 0 {
			blocked++
			if closeErr := reader.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		return err
	}
	if err := WriteFile(path, []byte("new")); err != nil {
		t.Fatalf("replace after reader release: %v", err)
	}
	if blocked != 1 {
		t.Fatalf("reader did not block initial rename: %d", blocked)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("published bytes %q: %v", data, err)
	}
}

func TestWave2WindowsRenameRetryBounds(t *testing.T) {
	for _, cause := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_DISK_FULL, nil} {
		t.Run("cause="+fmtError(cause), func(t *testing.T) {
			attempts, waits := 0, 0
			err := retryRename("old", "new", func(string, string) error {
				attempts++
				if cause == nil {
					return nil
				}
				return &os.LinkError{Op: "rename", Old: "old", New: "new", Err: cause}
			}, func(d time.Duration) {
				if d != 10*time.Millisecond {
					t.Fatalf("retry interval %s", d)
				}
				waits++
			})
			want := 25
			if cause == nil || cause == windows.ERROR_DISK_FULL {
				want = 1
			}
			if !errors.Is(err, cause) || attempts != want || waits != want-1 {
				t.Fatalf("attempts=%d waits=%d err=%v; want %d %v", attempts, waits, err, want, cause)
			}
		})
	}
}

func fmtError(err error) string {
	if err == nil {
		return "success"
	}
	return err.Error()
}

func TestWave2WindowsPersistentReaderPreservesOldFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := WriteFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := WriteFile(path, []byte("new")); err == nil {
		t.Fatal("persistent reader unexpectedly allowed replacement")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatalf("unpublished write changed old file: %q %v", data, err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatalf("unpublished temp file leaked: %v %v", files, err)
	}
}
