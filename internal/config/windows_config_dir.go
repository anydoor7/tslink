package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// CodeLegacyConfigDirPresent is the stable code of LegacyConfigDirError.
const CodeLegacyConfigDirPresent = "legacy_config_dir_present"

// LegacyConfigDirError refuses to run on a Windows install whose config
// directory is still at the old %USERPROFILE%\.config\tslink location.
// Resolving a path never moves the directory: it holds credential files, node
// keys and the ownership ledger, and every command resolves it, read-only ones
// included.
type LegacyConfigDirError struct {
	Legacy  string
	Current string
}

func (e *LegacyConfigDirError) Error() string {
	return fmt.Sprintf("TSLink's config directory is at the legacy location %q; move it to %q before running tslink again (tslink does not move it for you)", e.Legacy, e.Current)
}

func (e *LegacyConfigDirError) StableCode() string { return CodeLegacyConfigDirPresent }

// NextCommands returns the one manual command that moves the directory.
func (e *LegacyConfigDirError) NextCommands() []string {
	return []string{fmt.Sprintf("move %q %q", e.Legacy, e.Current)}
}

// resolveWindowsConfigDir decides which Windows config directory to use from
// the %AppData% candidate and the legacy profile candidate. It only stats
// them: the AppData one when the legacy one is absent, a refusal naming both
// when both exist, and a coded refusal with the manual move when only the
// legacy directory exists. It is platform-independent so it is tested on every
// platform; the Windows default is a thin wrapper around it.
func resolveWindowsConfigDir(current, legacy string, stat func(string) (os.FileInfo, error)) (string, error) {
	_, currentErr := stat(current)
	if currentErr != nil && !errors.Is(currentErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect Windows config directory %q: %w", current, currentErr)
	}
	legacyInfo, legacyErr := stat(legacy)
	if legacyErr != nil && !errors.Is(legacyErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect legacy Windows config directory %q: %w", legacy, legacyErr)
	}
	legacyExists := legacyErr == nil && legacyInfo.IsDir()
	if !legacyExists {
		return current, nil
	}
	if currentErr == nil {
		return "", fmt.Errorf("both Windows config directories exist; refusing to ignore either %q or %q", current, legacy)
	}
	return "", &LegacyConfigDirError{Legacy: legacy, Current: current}
}
