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
	origStat := statFn
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
		statFn = origStat
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

func TestWriteFileInExistingDirPreservesSharedDirectoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode preservation is not a Windows DACL proof")
	}
	restoreAtomicFileHooks(t)
	dir := filepath.Join(t.TempDir(), "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(shared) error = %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("Chmod(shared) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte(`{"version":"old"}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	if err := WriteFileInExistingDir(target, []byte(`{"version":"new"}`+"\n"), PrivateFileMode); err != nil {
		t.Fatalf("WriteFileInExistingDir() error = %v", err)
	}
	assertMode(t, dir, 0o755)
	assertMode(t, target, PrivateFileMode)
	assertJSONVersion(t, target, "new")
}

func TestWriteFileInExistingDirSupportsSymlinkedParent(t *testing.T) {
	restoreAtomicFileHooks(t)
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(real) error = %v", err)
	}
	linkedDir := filepath.Join(root, "linked")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable on this Windows runner: %v", err)
		}
		t.Fatalf("Symlink(parent) error = %v", err)
	}
	target := filepath.Join(linkedDir, "state.json")

	if err := WriteFileInExistingDir(target, []byte(`{"version":"new"}`+"\n"), PrivateFileMode); err != nil {
		t.Fatalf("WriteFileInExistingDir() error = %v", err)
	}
	assertJSONVersion(t, filepath.Join(realDir, "state.json"), "new")
	// Writing through the symlinked parent is the property this test is named
	// for, and it holds on every platform, so the test keeps running on Windows.
	// The mode assertion does not: Windows synthesizes 0666 from the read-only
	// attribute, so it proves nothing about privacy there. Every other
	// assertMode caller in this file skips the whole test on Windows; this one
	// drops only the POSIX claim and keeps the traversal coverage.
	if runtime.GOOS != "windows" {
		assertMode(t, filepath.Join(realDir, "state.json"), PrivateFileMode)
	}
}

func TestWriteFileInExistingDirRejectsTargetSymlink(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	referent := filepath.Join(dir, "referent.json")
	if err := os.WriteFile(referent, []byte(`{"version":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(referent) error = %v", err)
	}
	target := filepath.Join(dir, "state.json")
	if err := os.Symlink(referent, target); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink unavailable on this Windows runner: %v", err)
		}
		t.Fatalf("Symlink(target) error = %v", err)
	}

	err := WriteFileInExistingDir(target, []byte(`{"version":"new"}`+"\n"), PrivateFileMode)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("WriteFileInExistingDir(symlink) error = %v, want symlink rejection", err)
	}
	assertJSONVersion(t, referent, "old")
}

func TestWriteFileInExistingDirPreRenameFailurePreservesOldFile(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte(`{"version":"old"}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}
	renameFn = func(string, string) error { return errors.New("injected rename failure") }

	err := WriteFileInExistingDir(target, []byte(`{"version":"new"}`+"\n"), PrivateFileMode)
	if err == nil {
		t.Fatal("WriteFileInExistingDir() error = nil, want injected rename failure")
	}
	assertJSONVersion(t, target, "old")
	assertNoOwnedTemps(t, dir, filepath.Base(target))
}

func TestWriteFileInExistingDirCreatesTempWithRequestedMode(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	wantMode := os.FileMode(0o640)
	var createMode os.FileMode
	origOpenFile := openFileFn
	openFileFn = func(name string, flag int, mode os.FileMode) (*os.File, error) {
		if flag&os.O_CREATE != 0 && strings.HasPrefix(filepath.Base(name), ".state.json.") {
			createMode = mode
		}
		return origOpenFile(name, flag, mode)
	}

	if err := WriteFileInExistingDir(target, []byte("state\n"), wantMode); err != nil {
		t.Fatalf("WriteFileInExistingDir() error = %v", err)
	}
	if createMode != wantMode {
		t.Fatalf("temp create mode = %04o, want requested %04o", createMode, wantMode)
	}
}

func TestWriteFileInExistingDirHonorsCallerMode(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	wantMode := os.FileMode(0o644)
	var chmodMode os.FileMode
	origChmod := chmodFn
	chmodFn = func(name string, mode os.FileMode) error {
		if strings.HasPrefix(filepath.Base(name), ".state.json.") {
			chmodMode = mode
		}
		return origChmod(name, mode)
	}

	if err := WriteFileInExistingDir(target, []byte("state\n"), wantMode); err != nil {
		t.Fatalf("WriteFileInExistingDir() error = %v", err)
	}
	if chmodMode != wantMode {
		t.Fatalf("temp chmod mode = %04o, want caller mode %04o", chmodMode, wantMode)
	}
}

func TestWriteFileInExistingDirMissingParentErrorNamesTarget(t *testing.T) {
	restoreAtomicFileHooks(t)
	root := t.TempDir()
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("Lstat(root) error = %v", err)
	}
	dir := filepath.Join(root, "removed-parent")
	target := filepath.Join(dir, "state.json")
	origLstat := lstatFn
	lstatFn = func(path string) (os.FileInfo, error) {
		if path == dir {
			return info, nil
		}
		return origLstat(path)
	}

	err = WriteFileInExistingDir(target, []byte("state\n"), PrivateFileMode)
	if err == nil {
		t.Fatal("WriteFileInExistingDir() error = nil, want missing-parent failure")
	}
	if !strings.Contains(err.Error(), target) {
		t.Fatalf("missing-parent error = %q, want real target %q", err, target)
	}
	if strings.Contains(err.Error(), ".state.json.") || strings.Contains(err.Error(), ".tmp") {
		t.Fatalf("missing-parent error exposes random temp path: %q", err)
	}
}

func TestConvergePrivateFileMissingIsNoOp(t *testing.T) {
	restoreAtomicFileHooks(t)
	target := filepath.Join(t.TempDir(), "missing.json")

	if err := ConvergePrivateFile(target); err != nil {
		t.Fatalf("ConvergePrivateFile(missing) error = %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("Lstat(missing) error = %v, want not-exist", err)
	}
}

func TestConvergePrivateFileConvergesModeWithoutChangingContents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode convergence is not a Windows DACL proof")
	}
	restoreAtomicFileHooks(t)
	target := filepath.Join(t.TempDir(), "state.json")
	want := []byte(`{"version":"preserved"}` + "\n")
	if err := os.WriteFile(target, want, 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}

	if err := ConvergePrivateFile(target); err != nil {
		t.Fatalf("ConvergePrivateFile() error = %v", err)
	}
	assertMode(t, target, PrivateFileMode)
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target) error = %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("ConvergePrivateFile() content = %q, want unchanged %q", got, want)
	}
}

func TestConvergePrivateFileRejectsUnsafeTypes(t *testing.T) {
	restoreAtomicFileHooks(t)
	dir := t.TempDir()
	t.Run("directory", func(t *testing.T) {
		err := ConvergePrivateFile(dir)
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("ConvergePrivateFile(directory) error = %v, want not-regular rejection", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		referent := filepath.Join(dir, "referent.json")
		want := []byte(`{"version":"preserved"}` + "\n")
		if err := os.WriteFile(referent, want, 0o600); err != nil {
			t.Fatalf("WriteFile(referent) error = %v", err)
		}
		target := filepath.Join(dir, "state.json")
		if err := os.Symlink(referent, target); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink unavailable on this Windows runner: %v", err)
			}
			t.Fatalf("Symlink() error = %v", err)
		}

		err := ConvergePrivateFile(target)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("ConvergePrivateFile(symlink) error = %v, want symlink rejection", err)
		}
		got, err := os.ReadFile(referent)
		if err != nil {
			t.Fatalf("ReadFile(referent) error = %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("referent content = %q, want unchanged %q", got, want)
		}
	})
}

func TestConvergePrivateFileReportsChmodFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode convergence is not a Windows DACL proof")
	}
	restoreAtomicFileHooks(t)
	target := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(target, []byte("private\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	wantErr := errors.New("injected chmod failure")
	chmodFn = func(string, os.FileMode) error { return wantErr }

	err := ConvergePrivateFile(target)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ConvergePrivateFile() error = %v, want %v", err, wantErr)
	}
	assertMode(t, target, 0o644)
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
