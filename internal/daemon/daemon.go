package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// WritePID writes the current process PID to path.
func WritePID(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data := []byte(strconv.Itoa(os.Getpid()) + "\n")
	return os.WriteFile(path, data, 0o600)
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
