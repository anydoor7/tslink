//go:build !unix && !windows

package health

import "os"

func openProbeFile(path string) (*os.File, error) { return os.Open(path) }
