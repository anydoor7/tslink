//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

func openAuthHandoff(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
