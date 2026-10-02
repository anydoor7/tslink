package registry

import (
	"fmt"
	"os"
)

func openPortalRegistry(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("portal registry must be a regular file")
	}
	return os.Open(path)
}
