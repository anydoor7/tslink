package credentials

import (
	"errors"
	"os"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestLegacyMigrationPreservesCurrentKeyringCredential(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := keyring.Set(keychainService, keychainAPIKey, "current-key"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("conflicting legacy file was reported migrated")
	}
	current, err := keyring.Get(keychainService, keychainAPIKey)
	if err != nil || current != "current-key" {
		t.Fatalf("current keyring credential changed: %q, %v", current, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("conflicting legacy file must be preserved: %v", err)
	}
}

func TestLegacyMigrationRefusesUncertainKeyringRead(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	setCalls := 0
	stubKeyring(t,
		func(string, string) (string, error) { return "", errors.New("keyring unavailable") },
		func(string, string, string) error { setCalls++; return nil }, nil,
	)
	if migrated := MigrateFromLegacy(); migrated || setCalls != 0 {
		t.Fatalf("uncertain keyring read: migrated=%v writes=%d", migrated, setCalls)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file removed on uncertain read: %v", err)
	}
}

func TestLegacyMigrationVerifiesKeyringWriteBeforeRemovingFile(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("legacy-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	stubKeyring(t,
		func(string, string) (string, error) {
			reads++
			if reads == 1 {
				return "", keyring.ErrNotFound
			}
			return "different-key", nil
		},
		func(string, string, string) error { return nil }, nil,
	)
	if migrated := MigrateFromLegacy(); migrated {
		t.Fatal("unverified keyring write was reported migrated")
	}
	if reads < 2 {
		t.Fatalf("keyring readback never ran: reads=%d", reads)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file removed despite bad readback: %v", err)
	}
}

func TestLegacyMigrationRemovesMatchingFileWithoutOverwritingKeyring(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("same-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(keychainService, keychainAPIKey, "same-key"); err != nil {
		t.Fatal(err)
	}
	writes := 0
	stubKeyring(t, nil, func(string, string, string) error { writes++; return nil }, nil)
	if migrated := MigrateFromLegacy(); !migrated {
		t.Fatal("matching duplicate file was not cleaned")
	}
	if writes != 0 {
		t.Fatalf("matching keyring value was overwritten: writes=%d", writes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("matching duplicate file remains: %v", err)
	}
}
