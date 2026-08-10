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

func checkParentPermissions(path, dir string, info os.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe parent for %s: %s is group- or world-writable (%04o); run 'chmod g-w,o-w %s' and retry", path, dir, numericFileMode(info.Mode()), dir)
	}
	return nil
}

func numericFileMode(mode os.FileMode) uint32 {
	numeric := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		numeric |= 0o4000
	}
	if mode&os.ModeSetgid != 0 {
		numeric |= 0o2000
	}
	if mode&os.ModeSticky != 0 {
		numeric |= 0o1000
	}
	return numeric
}

func syncDirectory(dir string) error {
	f, err := openFileFn(dir, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return syncFileFn(f)
}
