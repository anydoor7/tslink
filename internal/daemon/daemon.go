package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/filelock"
)

// daemonServeArgs builds the child argv for the re-executed foreground serve
// process. controlURL and manageACL opt-ins observed by the parent must be
// forwarded to the child exactly once, otherwise the documented
// `serve --daemon --manage-acl` opt-in is silently dropped in daemon mode. It
// is shared by the Unix and Windows Daemonize implementations so both platforms
// forward identical flags.
func daemonServeArgs(controlURL string, manageACL bool) []string {
	args := []string{"serve"}
	if controlURL != "" {
		args = append(args, "--control-url", controlURL)
	}
	if manageACL {
		args = append(args, "--manage-acl")
	}
	return args
}

// WritePID writes the current process PID to path.
func WritePID(path string) error {
	return WritePIDForProcess(path, os.Getpid())
}

// WritePIDForProcess writes pid to path.
func WritePIDForProcess(path string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID %d", pid)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data := []byte(strconv.Itoa(pid) + "\n")
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// WithPIDLock holds the PID-file lock while fn runs.
func WithPIDLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := filelock.Lock(lockFile); err != nil {
		return err
	}
	defer filelock.Unlock(lockFile)

	return fn()
}

// ReadPID reads and parses a PID file.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse PID in %s: %w", path, err)
	}

	return pid, nil
}

// RemovePID removes the PID file on a best-effort basis.
func RemovePID(path string) {
	_ = os.Remove(path)
}

func verifyProcessIdentity(pid int) error {
	expected, err := executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	actual, err := processExecutable(pid)
	if err != nil {
		return fmt.Errorf("inspect process %d: %w", pid, err)
	}
	if !sameExecutable(actual, expected) {
		return fmt.Errorf("process %d is %q, not %q", pid, actual, expected)
	}
	return nil
}

func sameExecutable(actual, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if actual == "" || expected == "" {
		return false
	}

	if filepath.IsAbs(actual) && filepath.IsAbs(expected) {
		return samePath(cleanExecutablePath(actual), cleanExecutablePath(expected))
	}

	return samePath(filepath.Base(actual), filepath.Base(expected))
}

func cleanExecutablePath(path string) string {
	path = strings.TrimSuffix(path, " (deleted)")
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
