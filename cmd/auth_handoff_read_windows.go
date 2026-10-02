package cmd

import (
	"errors"
	"os"
)

func openAuthHandoff(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("auth handoff must be a regular file")
	}
	return os.Open(path)
}
