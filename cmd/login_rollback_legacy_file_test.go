package cmd

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
)

// RV-F: migration keeps a legacy API key file that disagrees with the
// keyring. An api-key login removes that file after its keyring write; if
// the login then rolls back, the file must come back with it.
func TestRolledBackLoginRestoresConflictingLegacyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows never writes credential files (file fallback disabled)")
	}
	setupLoginTest(t)
	useRealLoginTransaction(t)
	mockAPIKeySuccess(t)
	if err := credentials.SetAPIKey("tskey-api-FAKE-RING"); err != nil {
		t.Fatal(err)
	}
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tskey-api-FAKE-FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if credentials.MigrateFromLegacy() {
		t.Fatal("conflicting legacy file was migrated")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("migration did not preserve the conflicting file: %v", err)
	}

	writeMeta := loginWriteSlotMetaFn
	t.Cleanup(func() { loginWriteSlotMetaFn = writeMeta })
	loginWriteSlotMetaFn = func(string, credentials.SlotMetadata) error {
		return errors.New("synthetic metadata failure")
	}
	_, err = commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-NEW", loginReplaceOptions{Now: loginTestNow})
	if err == nil || !strings.Contains(err.Error(), "synthetic metadata failure") {
		t.Fatalf("login did not fail through the metadata rollback path: %v", err)
	}
	if strings.Contains(err.Error(), "rollback failed") {
		t.Fatalf("rollback failed: %v", err)
	}
	if key, err := credentials.GetAPIKey(); err != nil || key != "tskey-api-FAKE-RING" {
		t.Fatalf("keyring restored=%v err=%v", key == "tskey-api-FAKE-RING", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("rolled-back login lost the conflicting legacy file: %v", err)
	}
	if string(data) != "tskey-api-FAKE-FILE" {
		t.Fatal("rolled-back login changed the conflicting legacy file's content")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("restored legacy file mode = %v, want 0600 (err=%v)", info.Mode().Perm(), err)
	}
}

// Control: a login that commits still removes the stale file copy, as before.
func TestCommittedLoginStillRemovesLegacyFileCopy(t *testing.T) {
	setupLoginTest(t)
	useRealLoginTransaction(t)
	mockAPIKeySuccess(t)
	if err := credentials.SetAPIKey("tskey-api-FAKE-RING"); err != nil {
		t.Fatal(err)
	}
	path, err := config.APIKeyPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tskey-api-FAKE-FILE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-NEW", loginReplaceOptions{Now: loginTestNow}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("committed login kept the stale file copy: %v", err)
	}
}
