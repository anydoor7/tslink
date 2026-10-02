//go:build !windows

package registry

import "os"

func openRegistryFile(path string) (*os.File, error) {
	return os.Open(path)
}

func replaceRegistryFile(source, target string) error {
	return os.Rename(source, target)
}
