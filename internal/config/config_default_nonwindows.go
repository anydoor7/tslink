//go:build !windows

package config

import (
	"os"
	"path/filepath"
)

func platformDefaultConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "tslink"), nil
}
