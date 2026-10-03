//go:build !windows

package atomicfile

import "os"

// ReadFile reads a snapshot of an atomically replaced state file.
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
