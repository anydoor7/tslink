//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
	"syscall"
)

func checkOwner(path string, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("unsafe owner for %s: uid %d does not match current uid %d", path, stat.Uid, os.Geteuid())
	}
	return nil
}

func syncDirectory(dir string) error {
	f, err := openFileFn(dir, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncFileFn(f)
}
