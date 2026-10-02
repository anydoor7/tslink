//go:build !unix && !windows

package health

import "os"

func openProbeFile(path string) (*os.File, error) { return os.Open(path) }

func statProbeFile(path string) (os.FileInfo, error) { return os.Stat(path) }
