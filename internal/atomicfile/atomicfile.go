// Package atomicfile provides permission-safe local state file helpers.
package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	PrivateDirMode  os.FileMode = 0o700
	PrivateFileMode os.FileMode = 0o600
)

var (
	mkdirAllFn   = os.MkdirAll
	lstatFn      = os.Lstat
	statFn       = os.Stat
	chmodFn      = os.Chmod
	openFileFn   = os.OpenFile
	renameFn     = os.Rename
	removeFn     = os.Remove
	randomReadFn = rand.Read
	writeAllFn   = writeAll
	syncFileFn   = func(f *os.File) error { return f.Sync() }
	syncDirFn    = syncDirectory
	closeFileFn  = func(f *os.File) error { return f.Close() }
)

// EnsurePrivateDir creates dir if needed and converges existing directories to
// owner-only permissions where POSIX modes are meaningful. It rejects symlinks,
// non-directories, and unsafe owner conditions when the platform exposes them.
func EnsurePrivateDir(dir string) error {
	if err := mkdirAllFn(dir, PrivateDirMode); err != nil {
		return err
	}
	info, err := lstatFn(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe state directory %s: symlink", dir)
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe state directory %s: not a directory", dir)
	}
	if err := checkOwner(dir, info); err != nil {
		return err
	}
	if info.Mode().Perm() != PrivateDirMode {
		if err := chmodFn(dir, PrivateDirMode); err != nil {
			return err
		}
	}
	return nil
}

// ConvergePrivateFile validates an existing sensitive file and converges its
// permissions. Missing files are left untouched.
func ConvergePrivateFile(path string) error {
	info, err := lstatFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe state file %s: symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe state file %s: not a regular file", path)
	}
	if err := checkOwner(path, info); err != nil {
		return err
	}
	if info.Mode().Perm() != PrivateFileMode {
		if err := chmodFn(path, PrivateFileMode); err != nil {
			return err
		}
	}
	return nil
}

// WriteFile writes data by creating a random exclusive temp file in the same
// directory, fsyncing it, atomically replacing path, and fsyncing the parent
// directory where the platform supports it. Pre-rename failures leave the old
// file in place and remove the temp file.
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	return writeFile(path, data, PrivateFileMode)
}

// WriteFileInExistingDir atomically writes a file without creating or changing
// the parent directory. It validates that the existing parent is a directory,
// is owned by the current user where ownership is available, and is not
// world-writable. Parent-directory symlinks are followed and their referent is
// validated, while a symlink at path itself is rejected.
func WriteFileInExistingDir(path string, data []byte, mode os.FileMode) error {
	if err := validateExistingParent(path); err != nil {
		return err
	}
	return writeFile(path, data, mode.Perm())
}

func validateExistingParent(path string) error {
	dir := filepath.Dir(path)
	info, err := lstatFn(dir)
	if err != nil {
		return fmt.Errorf("validate parent for %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		info, err = statFn(dir)
		if err != nil {
			return fmt.Errorf("validate parent for %s: %w", path, err)
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe parent for %s: %s is not a directory", path, dir)
	}
	if err := checkOwner(dir, info); err != nil {
		return err
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("unsafe parent for %s: %s is world-writable (%04o)", path, dir, info.Mode().Perm())
	}
	return nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := validateReplaceTarget(path); err != nil {
		return err
	}

	tmpPath, f, err := createTemp(dir, filepath.Base(path), mode)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = removeFn(tmpPath)
		}
	}()

	if err := chmodFn(tmpPath, mode); err != nil {
		_ = closeFileFn(f)
		return err
	}
	if err := writeAllFn(f, data); err != nil {
		_ = closeFileFn(f)
		return err
	}
	if err := syncFileFn(f); err != nil {
		_ = closeFileFn(f)
		return err
	}
	if err := closeFileFn(f); err != nil {
		return err
	}
	if err := renameFn(tmpPath, path); err != nil {
		return err
	}
	renamed = true
	return syncDirFn(dir)
}

func validateReplaceTarget(path string) error {
	info, err := lstatFn(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe state file %s: symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe state file %s: not a regular file", path)
	}
	return checkOwner(path, info)
}

func createTemp(dir, base string, mode os.FileMode) (string, *os.File, error) {
	var lastErr error
	for range 128 {
		name, err := randomTempName(base)
		if err != nil {
			return "", nil, err
		}
		path := filepath.Join(dir, name)
		f, err := openFileFn(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return path, f, nil
		}
		if !os.IsExist(err) {
			cause := err
			var pathErr *os.PathError
			if errors.As(err, &pathErr) {
				cause = pathErr.Err
			}
			return "", nil, fmt.Errorf("create temp for %s: %w", filepath.Join(dir, base), cause)
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("temp name collision")
	}
	return "", nil, fmt.Errorf("create temp for %s: %w", filepath.Join(dir, base), lastErr)
}

func randomTempName(base string) (string, error) {
	var b [16]byte
	if _, err := randomReadFn(b[:]); err != nil {
		return "", err
	}
	return "." + base + "." + hex.EncodeToString(b[:]) + ".tmp", nil
}

func writeAll(f *os.File, data []byte) error {
	n, err := f.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
