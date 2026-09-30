//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

func platformDefaultConfigDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve Windows user config directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Windows user profile to check for a legacy config directory: %w", err)
	}
	return resolveWindowsConfigDir(filepath.Join(root, "tslink"), filepath.Join(home, ".config", "tslink"), os.Stat)
}
