//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

var renameConfigDir = os.Rename

func platformDefaultConfigDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve Windows user config directory: %w", err)
	}
	current := filepath.Join(root, "tslink")

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Windows user profile for legacy config migration: %w", err)
	}
	legacy := filepath.Join(home, ".config", "tslink")

	_, currentErr := os.Stat(current)
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return "", fmt.Errorf("inspect Windows config directory %q: %w", current, currentErr)
	}
	legacyInfo, legacyErr := os.Stat(legacy)
	if legacyErr != nil && !os.IsNotExist(legacyErr) {
		return "", fmt.Errorf("inspect legacy Windows config directory %q: %w", legacy, legacyErr)
	}

	currentExists := currentErr == nil
	legacyExists := legacyErr == nil
	if currentExists && legacyExists {
		return "", fmt.Errorf("both Windows config directories exist; refusing to ignore either %q or %q", current, legacy)
	}
	if currentExists || !legacyExists {
		return current, nil
	}
	if !legacyInfo.IsDir() {
		return "", fmt.Errorf("legacy Windows config path %q is not a directory", legacy)
	}

	// Prefer an atomic move into %AppData%. If a redirected profile makes the
	// move impossible, keep using the legacy directory rather than losing access
	// to an existing user's configuration.
	if err := os.MkdirAll(filepath.Dir(current), 0o700); err != nil {
		return legacy, nil
	}
	if err := renameConfigDir(legacy, current); err != nil {
		// Another process may have won the migration race.
		currentAfter, currentAfterErr := os.Stat(current)
		_, legacyAfterErr := os.Stat(legacy)
		if currentAfterErr == nil && currentAfter.IsDir() && os.IsNotExist(legacyAfterErr) {
			return current, nil
		}
		return legacy, nil
	}
	return current, nil
}
