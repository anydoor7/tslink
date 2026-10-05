package registry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"golang.org/x/sys/windows"
)

func TestRegistryReadSharingAllowsReplacementWithOpenSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := writeRegistryFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	f, err := openRegistryFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := writeRegistryFile(path, []byte("new")); err != nil {
		t.Fatalf("shared-delete reader blocks replacement: %v", err)
	}
	old, err := io.ReadAll(f)
	if err != nil || string(old) != "old" {
		t.Fatalf("open snapshot = %q, %v", old, err)
	}
	data, err := readRegistryBytes(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("new snapshot = %q, %v", data, err)
	}
}

func TestRegistryReadSharingWindowsLongUnicodePath(t *testing.T) {
	dir := t.TempDir()
	for len(dir) < 300 {
		dir = filepath.Join(dir, strings.Repeat("segment", 5))
	}
	path := filepath.Join(dir, "registry-\u96ea.json")
	if err := writeRegistryFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeRegistryFile(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	data, err := readRegistryBytes(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("long Unicode path = %q, %v", data, err)
	}
}

func TestRegistrySharingNativeReadAndReplaceRetry(t *testing.T) {
	for _, operation := range []string{"read", "replace"} {
		t.Run(operation, func(t *testing.T) {
			// Virtual time: the product's 1-32ms retry sleeps and the release delay
			// share the bubble clock, so the handle is released after the 4th failed
			// attempt (at 10ms, between the 7ms and 15ms retries) on every runner.
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "registry.json")
				if err := writeRegistryFile(path, []byte("old")); err != nil {
					t.Fatal(err)
				}
				var closeHandle func() error
				var raw, retried func() error
				if operation == "read" {
					wide, err := windows.UTF16PtrFromString(path)
					if err != nil {
						t.Fatal(err)
					}
					h, err := windows.CreateFile(wide, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
					if err != nil {
						t.Fatal(err)
					}
					closeHandle = func() error { return windows.CloseHandle(h) }
					raw = func() error { _, err := readRegistryFileOnce(path); return err }
					retried = func() error { _, err := readRegistryBytes(path); return err }
				} else {
					f, err := os.Open(path) // Deliberately model a legacy/third-party reader.
					if err != nil {
						t.Fatal(err)
					}
					closeHandle = f.Close
					raw = func() error { return atomicfile.WriteFile(path, []byte("new")) }
					retried = func() error { return writeRegistryFile(path, []byte("new")) }
				}
				// Prove the real Windows handle rejects the unretired operation.
				if err := raw(); !isWindowsSharingError(err) {
					_ = closeHandle()
					t.Fatalf("contention control must return 32 or 5: %v", err)
				}
				closed := make(chan error, 1)
				go func() { time.Sleep(10 * time.Millisecond); closed <- closeHandle() }()
				err := retried()
				if closeErr := <-closed; closeErr != nil {
					t.Fatal(closeErr)
				}
				if err != nil {
					t.Fatalf("transient %s did not recover: %v", operation, err)
				}
				data, err := readRegistryBytes(path)
				want := "old"
				if operation == "replace" {
					want = "new"
				}
				if err != nil || string(data) != want {
					t.Fatalf("durable contents: %q, %v", data, err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("temporary files leaked: %v, %v", entries, err)
				}
			})
		})
	}
}

func TestRegistrySharingPermanentReplacePreservesOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.json")
	if err := writeRegistryFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := writeRegistryFile(path, []byte("new")); !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("permanently held reader must return the original sharing error: %v", err)
	}
	data, err := io.ReadAll(f)
	if err != nil || string(data) != "old" {
		t.Fatalf("old file damaged: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files leaked: %v, %v", entries, err)
	}
}
