//go:build !windows

package atomicfile

import "os"

func renameFile(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }
