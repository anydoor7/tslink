package cmd

import (
	"errors"
	"github.com/anydoor7/tslink/internal/atomicfile"
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
	return atomicfile.OpenSharedRead(path)
}
