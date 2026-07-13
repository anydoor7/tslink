//go:build windows

package atomicfile

import "os"

func checkOwner(string, os.FileInfo) error {
	return nil
}

func syncDirectory(string) error {
	return nil
}
