package atomicfile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func restoreAtomicFileHooks(t *testing.T) {
	t.Helper()
	origMkdirAll := mkdirAllFn
	origLstat := lstatFn
	origChmod := chmodFn
	origOpenFile := openFileFn
	origRename := renameFn
	origRemove := removeFn
	origRandomRead := randomReadFn
	origWriteAll := writeAllFn
	origSyncFile := syncFileFn
	origSyncDir := syncDirFn
	origCloseFile := closeFileFn
	t.Cleanup(func() {
		mkdirAllFn = origMkdirAll
		lstatFn = origLstat
		chmodFn = origChmod
		openFileFn = origOpenFile
		renameFn = origRename
		removeFn = origRemove
		randomReadFn = origRandomRead
		writeAllFn = origWriteAll
		syncFileFn = origSyncFile
		syncDirFn = origSyncDir
		closeFileFn = origCloseFile
	})
}

func TestWriteFilePreRenameFailuresPreserveOldJSONAndCleanTemp(t *testing.T) {
	failures := []struct {
		name   string
		inject func(target string)
	}{
		{
			name: "create",
			inject: func(target string) {
				openFileFn = func(name string, flag int, perm os.FileMode) (*os.File, error) {
					if strings.HasPrefix(filepath.Base(name), "."+filepath.Base(target)+".") {
						return nil, errors.New("injected create failure")
					}
					return os.OpenFile(name, flag, perm)
				}
			},
		},
		{
			name: "chmod",
			inject: func(target string) {
				chmodFn = func(name string, mode os.FileMode) error {
					if strings.HasPrefix(filepath.Base(name), "."+filepath.Base(target)+".") {
						return errors.New("injected chmod failure")
					}
					return os.Chmod(name, mode)
				}
			},
		},
		{
			name: "write",
			inject: func(string) {
				writeAllFn = func(*os.File, []byte) error {
					return errors.New("injected write failure")
				}
			},
		},
		{
			name: "file-sync",
			inject: func(target string) {
				syncFileFn = func(f *os.File) error {
					if strings.HasPrefix(filepath.Base(f.Name()), "."+filepath.Base(target)+".") {
						return errors.New("injected file sync failure")
					}
					return f.Sync()
				}
			},
		},
		{
			name: "rename",
			inject: func(string) {
				renameFn = func(string, string) error {
					return errors.New("injected rename failure")
				}
			},
		},
	}

	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			restoreAtomicFileHooks(t)
			dir := t.TempDir()
			target := filepath.Join(dir, "state.json")
			old := []byte(`{"version":"old"}` + "\n")
			if err := os.WriteFile(target, old, 0o600); err != nil {
				t.Fatalf("WriteFile(old) error = %v", err)
			}
			tc.inject(target)

			err := WriteFile(target, []byte(`{"version":"new"}`+"\n"))
			if err == nil {
				t.Fatal("WriteFile() error = nil, want injected failure")
			}
			assertJSONVersion(t, target, "old")
			assertNoOwnedTemps(t, dir, filepath.Base(target))
		})
	}
}

func TestWriteFileDirSyncFailureReportsAfterAtomicReplace(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte(`{"version":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}
	syncDirFn = func(string) error {
		return errors.New("injected dir sync failure")
	}

	err := WriteFile(target, []byte(`{"version":"new"}`+"\n"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want dir sync failure")
	}
	assertJSONVersion(t, target, "new")
	assertNoOwnedTemps(t, dir, filepath.Base(target))
}

func TestWriteFileConvergesModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode convergence is not a Windows DACL proof")
	}
	restoreAtomicFileHooks(t)
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte(`{"version":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	if err := WriteFile(target, []byte(`{"version":"new"}`+"\n")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	assertMode(t, dir, PrivateDirMode)
	assertMode(t, target, PrivateFileMode)
}

func TestWriteFileRejectsTargetSymlinkAndPreservesReferent(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	referent := filepath.Join(dir, "referent.json")
	if err := os.WriteFile(referent, []byte(`{"version":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(referent) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")
	if err := os.Symlink(referent, target); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	err := WriteFile(target, []byte(`{"version":"new"}`+"\n"))
	if err == nil {
		t.Fatal("WriteFile() error = nil, want symlink rejection")
	}
	assertJSONVersion(t, referent, "old")
	assertNoOwnedTemps(t, dir, filepath.Base(target))
}

func TestWriteFileDoesNotUsePredictableFixedTempSymlink(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	secret := filepath.Join(dir, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"version":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	if err := os.Symlink(secret, target+".tmp"); err != nil {
		t.Fatalf("Symlink(fixed tmp) error = %v", err)
	}

	if err := WriteFile(target, []byte(`{"version":"new"}`+"\n")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	assertJSONVersion(t, target, "new")
	assertJSONVersion(t, secret, "secret")
	assertNoOwnedTemps(t, dir, filepath.Base(target))
}

func assertJSONVersion(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	var got struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json at %s is not parseable after failure: %v\nraw=%s", path, err, data)
	}
	if got.Version != want {
		t.Fatalf("version at %s = %q, want %q; raw=%s", path, got.Version, want, data)
	}
}

func assertNoOwnedTemps(t *testing.T, dir, base string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	prefix := "." + base + "."
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".tmp") {
			t.Fatalf("owned temp residue remains: %s", filepath.Join(dir, name))
		}
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode(%s) = %o, want %o", path, got, want)
	}
}
