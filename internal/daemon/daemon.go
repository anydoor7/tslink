package daemon

import (
	"fmt"
	"os"
	"path/filepath"
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
