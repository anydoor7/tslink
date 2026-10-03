package atomicfile

import (
	"io"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/windows"
)

func TestStateFileDefaultWriterKeepsHeldSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	f, err := OpenSharedRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := WriteFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if data, err := io.ReadAll(f); err != nil || string(data) != "old" {
		t.Fatalf("held snapshot = %q, %v", data, err)
	}
	if data, err := ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("published snapshot = %q, %v", data, err)
	}
}

func TestStateFileReadRetriesExclusiveHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("snapshot")); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(3 * time.Millisecond)
			if err := windows.CloseHandle(h); err != nil {
				t.Error(err)
			}
		}()
		data, err := ReadFile(path)
		<-done
		if err != nil || string(data) != "snapshot" {
			t.Fatalf("read after exclusive handle release = %q, %v", data, err)
		}
	})
}
