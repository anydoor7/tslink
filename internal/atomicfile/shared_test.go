package atomicfile

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteFileInExistingDirWithReplaceRetriesPreparedSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var sources []string
	attempt := func(source, target string) error {
		sources = append(sources, source)
		if data, err := os.ReadFile(source); err != nil || string(data) != "new" {
			t.Fatalf("unprepared source %q: %q, %v", source, data, err)
		}
		if len(sources) < 3 {
			return &os.LinkError{Op: "rename", Old: source, New: target, Err: syscall.Errno(32)}
		}
		return ReplaceFile(source, target)
	}
	policyCalls := 0
	err := WriteFileInExistingDirWithReplace(path, []byte("new"), 0600, func(source, target string) error {
		policyCalls++
		// A caller-owned bounded retry of the same prepared source.
		for i := 0; ; i++ {
			err := attempt(source, target)
			if !errors.Is(err, syscall.Errno(32)) || i == 4 {
				return err
			}
		}
	})
	if err != nil || policyCalls != 1 || len(sources) != 3 || sources[0] != sources[1] || sources[1] != sources[2] {
		t.Fatalf("err=%v policy_calls=%d sources=%v", err, policyCalls, sources)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("target = %q, %v", data, err)
	}
	assertNoOwnedTemps(t, dir, filepath.Base(path))
}

func TestWriteFileInExistingDirWithReplaceFailurePreservesTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	permanent := errors.New("permanent replace failure")
	err := WriteFileInExistingDirWithReplace(path, []byte("new"), 0600, func(string, string) error { return permanent })
	if !errors.Is(err, permanent) {
		t.Fatalf("err = %v, want permanent failure", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("target damaged = %q, %v", data, err)
	}
	assertNoOwnedTemps(t, dir, filepath.Base(path))

	// The parent must already exist; nothing is created and replace is not called.
	called := false
	missing := filepath.Join(dir, "missing", "state.json")
	if err := WriteFileInExistingDirWithReplace(missing, nil, 0600, func(string, string) error { called = true; return nil }); err == nil || called {
		t.Fatalf("missing parent: err=%v replace called=%v", err, called)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("parent was created: %v", err)
	}
}

func TestOpenSharedReadKeepsSnapshotAcrossReplaceFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenSharedRead(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := WriteFileInExistingDirWithReplace(path, []byte("new"), 0600, ReplaceFile); err != nil {
		t.Fatalf("replace while shared reader open: %v", err)
	}
	if data, err := io.ReadAll(f); err != nil || string(data) != "old" {
		t.Fatalf("held snapshot = %q, %v", data, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Fatalf("fresh read = %q, %v", data, err)
	}
}
