package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestKeyringRotationReportsFallbackPathFailure(t *testing.T) {
	setup(t)
	const oldKey = "tskey-api-<test-only-FAKE-OLD>"
	if err := keyring.Set(keychainService, keychainAPIKey, oldKey); err != nil {
		t.Fatal(err)
	}
	pathErr := errors.New("synthetic fallback path failure")
	backend, err := storeCredentialWithBackendLocked("API key", keychainAPIKey, "tskey-api-<test-only-FAKE-NEW>", func() (string, error) { return "", pathErr })
	if !errors.Is(err, pathErr) || backend != "" {
		t.Fatalf("backend=%q err=%v, want path failure", backend, err)
	}
	got, getErr := keyring.Get(keychainService, keychainAPIKey)
	if getErr != nil || got != oldKey {
		t.Fatalf("path failure changed prior keyring credential: present=%t err=%v", got != "", getErr)
	}
}

func TestKeyringRotationMissingFallbackControl(t *testing.T) {
	setup(t)
	path := filepath.Join(t.TempDir(), "missing")
	backend, err := storeCredentialWithBackendLocked("API key", keychainAPIKey, "tskey-api-<test-only-FAKE-NEW>", func() (string, error) { return path, nil })
	if err != nil || backend != CredentialBackendKeyring {
		t.Fatalf("backend=%q err=%v", backend, err)
	}
}

func TestOAuthKeyringRotationPathErrorAndMissingFallback(t *testing.T) {
	setup(t)
	const oldSecret = "tskey-client-FAKE-OLD"
	if err := keyring.Set(keychainService, keychainClientSecret, oldSecret); err != nil {
		t.Fatal(err)
	}
	pathErr := errors.New("synthetic OAuth fallback path failure")
	backend, err := storeCredentialWithBackendLocked("OAuth client secret", keychainClientSecret, "tskey-client-FAKE-NEW", func() (string, error) { return "", pathErr })
	if backend != "" || !errors.Is(err, pathErr) {
		t.Fatalf("OAuth path error hidden: backend=%q err=%v", backend, err)
	}
	if got, getErr := keyring.Get(keychainService, keychainClientSecret); getErr != nil || got != oldSecret {
		t.Fatalf("OAuth path error changed prior keyring value: present=%t err=%v", got != "", getErr)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	backend, err = storeCredentialWithBackendLocked("OAuth client secret", keychainClientSecret, "tskey-client-FAKE-NEW", func() (string, error) { return missing, nil })
	if err != nil || backend != CredentialBackendKeyring {
		t.Fatalf("missing OAuth fallback control: backend=%q err=%v", backend, err)
	}
}

func TestKeyringRotationRollbackFailureReportsPartialState(t *testing.T) {
	setup(t)
	path := filepath.Join(t.TempDir(), "fallback")
	if err := os.WriteFile(path, []byte("tskey-api-<test-only-FAKE-OLD>"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRemove, oldDelete := credentialFileRemoveFunc, keyringDeleteFunc
	t.Cleanup(func() { credentialFileRemoveFunc, keyringDeleteFunc = oldRemove, oldDelete })
	removeErr := errors.New("synthetic unlink denied")
	deleteErr := errors.New("synthetic keyring rollback denied")
	credentialFileRemoveFunc = func(string) error { return removeErr }
	keyringDeleteFunc = func(string, string) error { return deleteErr }
	backend, err := storeCredentialWithBackendLocked("API key", keychainAPIKey, "tskey-api-<test-only-FAKE-NEW>", func() (string, error) { return path, nil })
	if backend != "" || !errors.Is(err, ErrCredentialWritePartial) || !errors.Is(err, removeErr) || !errors.Is(err, deleteErr) {
		t.Fatalf("partial commit not reported with both causes: backend=%q err=%v", backend, err)
	}
	if got, getErr := keyring.Get(keychainService, keychainAPIKey); getErr != nil || got != "tskey-api-<test-only-FAKE-NEW>" {
		t.Fatalf("partial state fixture did not retain new keyring value: present=%t err=%v", got != "", getErr)
	}
}

func TestKeyringRotationKeepsUsableValueIfCleanupReportedErrorAfterRemoval(t *testing.T) {
	setup(t)
	path := filepath.Join(t.TempDir(), "fallback")
	if err := os.WriteFile(path, []byte("tskey-api-<test-only-FAKE-OLD>"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRemove := credentialFileRemoveFunc
	t.Cleanup(func() { credentialFileRemoveFunc = oldRemove })
	removeErr := errors.New("synthetic unlink outcome error")
	credentialFileRemoveFunc = func(path string) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return removeErr
	}
	backend, err := storeCredentialWithBackendLocked("API key", keychainAPIKey, "tskey-api-<test-only-FAKE-NEW>", func() (string, error) { return path, nil })
	if backend != "" || !errors.Is(err, ErrCredentialWritePartial) || !errors.Is(err, removeErr) {
		t.Fatalf("uncertain cleanup not reported: backend=%q err=%v", backend, err)
	}
	if got, getErr := keyring.Get(keychainService, keychainAPIKey); getErr != nil || got != "tskey-api-<test-only-FAKE-NEW>" {
		t.Fatalf("new usable keyring value lost: present=%t err=%v", got != "", getErr)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("fixture did not remove fallback: %v", statErr)
	}
}

func TestKeyringRotationCleanupFailurePreservesPreviousCredential(t *testing.T) {
	for _, tc := range []struct {
		name, keychainKey, old, next string
		keyringBefore                bool
		set                          func(string) (CredentialBackend, error)
		get                          func() (string, error)
		setPath                      func(func() (string, error)) func()
	}{
		{"api_no_prior_keyring", keychainAPIKey, "tskey-api-<test-only-FAKE-OLD>", "tskey-api-<test-only-FAKE-NEW>", false, SetAPIKeyWithBackend, GetAPIKey,
			func(fn func() (string, error)) func() {
				old := apiKeyPathFunc
				apiKeyPathFunc = fn
				return func() { apiKeyPathFunc = old }
			}},
		{"oauth_prior_keyring", keychainClientSecret, "tskey-client-FAKE-OLD", "tskey-client-FAKE-NEW", true, SaveClientSecretWithBackend, GetClientSecret,
			func(fn func() (string, error)) func() {
				old := clientSecretPathFunc
				clientSecretPathFunc = fn
				return func() { clientSecretPathFunc = old }
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			path := filepath.Join(t.TempDir(), "fallback")
			if err := os.WriteFile(path, []byte(tc.old), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(tc.setPath(func() (string, error) { return path, nil }))
			if tc.keyringBefore {
				if err := keyring.Set(keychainService, tc.keychainKey, tc.old); err != nil {
					t.Fatal(err)
				}
			}
			oldRemove := credentialFileRemoveFunc
			removeErr := errors.New("synthetic unlink denied")
			credentialFileRemoveFunc = func(string) error { return removeErr }
			t.Cleanup(func() { credentialFileRemoveFunc = oldRemove })
			backend, err := tc.set(tc.next)
			if backend != "" || !errors.Is(err, removeErr) || !strings.Contains(err.Error(), "prior keyring value restored") {
				t.Fatalf("backend=%q err=%v, want explicit failed cleanup and restored prior value", backend, err)
			}
			got, getErr := keyring.Get(keychainService, tc.keychainKey)
			if tc.keyringBefore && (getErr != nil || got != tc.old) {
				t.Fatalf("keyring did not restore prior value: present=%t err=%v", got != "", getErr)
			}
			if !tc.keyringBefore && !errors.Is(getErr, keyring.ErrNotFound) {
				t.Fatalf("new keyring value survived failed cleanup: present=%t err=%v", got != "", getErr)
			}
			oldEnabled := keyringEnabledFunc
			keyringEnabledFunc = func() bool { return false }
			t.Cleanup(func() { keyringEnabledFunc = oldEnabled })
			fallback, getErr := tc.get()
			if getErr != nil || fallback != tc.old {
				t.Fatalf("unavailable-keyring fallback changed: present=%t err=%v", fallback != "", getErr)
			}
		})
	}
}
