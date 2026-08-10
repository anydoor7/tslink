//go:build windows

package atomicfile

import "os"

func checkOwner(string, os.FileInfo) error {
	return nil
}

// checkParentPermissions intentionally does not validate a Windows DACL.
// Go synthesizes os.FileMode from FILE_ATTRIBUTE_READONLY on Windows, so its
// group and world write bits do not describe the directory's access control.
func checkParentPermissions(string, string, os.FileInfo) error {
	return nil
}

func syncDirectory(string) error {
	return nil
}
