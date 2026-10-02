package health

import (
	"context"
	"os"
)

func probeFile(ctx context.Context, path string, directory bool) string {
	return probeFileWithOpen(ctx, path, directory, openProbeFile)
}

// The opener is passed by value so a replacement-race test needs no global
// seam. Check before open, then check the actual opened object and its identity.
func probeFileWithOpen(ctx context.Context, path string, directory bool, open func(string) (*os.File, error)) string {
	if ctx.Err() != nil {
		return "health_timeout"
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "health_file_unavailable"
	}
	if before.Mode()&os.ModeSymlink != 0 {
		before, err = os.Stat(path)
		if err != nil {
			return "health_file_unavailable"
		}
	}
	valid := func(info os.FileInfo) bool {
		return (directory && info.IsDir()) || (!directory && info.Mode().IsRegular())
	}
	if !valid(before) {
		return "health_file_invalid"
	}
	f, err := open(path)
	if err != nil {
		return "health_file_unavailable"
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !valid(after) || !os.SameFile(before, after) {
		return "health_file_invalid"
	}
	if ctx.Err() != nil {
		return "health_timeout"
	}
	return ""
}
