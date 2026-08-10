// Package authmode persists credential-tier transitions that must be applied
// before existing tsnet node state is reused.
package authmode

import (
	"os"
	"path/filepath"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
)

const credentialUpgradeMarker = "credential-upgrade-pending.json"

var credentialUpgradeRecord = []byte("{\"schema_version\":1,\"from\":\"interactive\",\"to\":\"credentialed\"}\n")

// CredentialUpgradePath returns the private marker used to carry a Tier 1 to
// Tier 2 transition across the process restart required to replace node state.
func CredentialUpgradePath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, credentialUpgradeMarker), nil
}

// MarkCredentialUpgradePending records that existing interactive node state
// must not be reused by the next credentialed serve.
func MarkCredentialUpgradePending() error {
	path, err := CredentialUpgradePath()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, credentialUpgradeRecord)
}

// CredentialUpgradePending reports whether a Tier 1 to Tier 2 transition is
// waiting to be applied. Existing markers are validated as private regular
// files before they are trusted.
func CredentialUpgradePending() (bool, error) {
	path, err := CredentialUpgradePath()
	if err != nil {
		return false, err
	}
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ClearCredentialUpgradePending removes a transition only after all affected
// local node state has been removed.
func ClearCredentialUpgradePending() error {
	path, err := CredentialUpgradePath()
	if err != nil {
		return err
	}
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
