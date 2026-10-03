//go:build !windows

package atomicfile

import "os"

// OpenSharedRead opens path for reading. POSIX rename already replaces a
// directory entry while readers hold the previous file open.
func OpenSharedRead(path string) (*os.File, error) {
	return os.Open(path)
}

// ReplaceFile atomically replaces target with source.
func ReplaceFile(source, target string) error {
	return os.Rename(source, target)
}
