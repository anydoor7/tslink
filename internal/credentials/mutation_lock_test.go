package credentials

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestMigrationSerializesConcurrentLogin(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("old-legacy-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	readSnapshot := make(chan struct{})
	resumeMigration := make(chan struct{})
	var reads atomic.Int32
	stubKeyring(t, func(service, user string) (string, error) {
		value, err := keyring.Get(service, user)
		if reads.Add(1) == 1 {
			close(readSnapshot)
			<-resumeMigration
		}
		return value, err
	}, nil, nil)
	migrated := make(chan bool, 1)
	go func() { migrated <- MigrateFromLegacy() }()
	select {
	case <-readSnapshot:
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not reach the absent-slot keyring read")
	}
	loginDone := make(chan error, 1)
	go func() {
		_, err := SetAPIKeyWithBackend("new-login-key")
		loginDone <- err
	}()
	select {
	case err := <-loginDone:
		close(resumeMigration)
		t.Fatalf("login finished inside migration transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeMigration)
	select {
	case ok := <-migrated:
		if !ok {
			t.Fatal("migration did not complete after releasing its read")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not finish")
	}
	select {
	case err := <-loginDone:
		if err != nil {
			t.Fatalf("login failed after migration: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("login remained blocked after migration")
	}
	stored, err := keyring.Get(keychainService, keychainAPIKey)
	if err != nil || stored != "new-login-key" {
		t.Fatalf("concurrent login did not win after migration: preserved=%v err=%v", stored == "new-login-key", err)
	}
}

func TestMigrationDoesNotDeleteConcurrentFallback(t *testing.T) {
	setup(t)
	path := apiKeyPath(t)
	if err := os.WriteFile(path, []byte("old-legacy-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	readSnapshot := make(chan struct{})
	resumeMigration := make(chan struct{})
	var reads atomic.Int32
	stubKeyring(t, func(service, user string) (string, error) {
		value, err := keyring.Get(service, user)
		if reads.Add(1) == 1 {
			close(readSnapshot)
			<-resumeMigration
		}
		return value, err
	}, func(service, user, value string) error {
		if value == "new-fallback-key" {
			return errors.New("synthetic keyring write failure")
		}
		return keyring.Set(service, user, value)
	}, nil)
	migrated := make(chan bool, 1)
	go func() { migrated <- MigrateFromLegacy() }()
	select {
	case <-readSnapshot:
	case <-time.After(3 * time.Second):
		t.Fatal("migration did not reach keyring read")
	}
	loginDone := make(chan error, 1)
	go func() {
		backend, err := SetAPIKeyWithBackend("new-fallback-key")
		if err == nil && backend != CredentialBackendFile {
			err = errors.New("login did not use file fallback")
		}
		loginDone <- err
	}()
	select {
	case err := <-loginDone:
		close(resumeMigration)
		t.Fatalf("fallback login finished inside migration transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resumeMigration)
	select {
	case ok := <-migrated:
		if !ok {
			t.Fatal("migration did not finish")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("migration remained blocked")
	}
	select {
	case err := <-loginDone:
		if runtime.GOOS == "windows" {
			if err == nil || !strings.Contains(err.Error(), "file credential fallback is disabled on Windows") {
				t.Fatalf("Windows fallback policy was not enforced: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("fallback login failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fallback login remained blocked")
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != "new-fallback-key" {
		t.Fatalf("concurrent fallback credential not preserved: exists=%v err=%v", err == nil, err)
	}
	if _, err := keyring.Get(keychainService, keychainAPIKey); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("stale keyring credential still wins over file fallback: %v", err)
	}
}

func TestCredentialLockIgnoresConfigDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("TSLINK_DISABLE_KEYRING", "0")
	t.Setenv("TSLINK_CONFIG_DIR", filepath.Join(t.TempDir(), "one"))
	first, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TSLINK_CONFIG_DIR", filepath.Join(t.TempDir(), "two"))
	second, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same keyring user got different lock paths for config directories: %q != %q", first, second)
	}
	if first == "" {
		t.Fatal("enabled keyring has no account lock path")
	}
}

func TestFileOnlyCredentialLocksFollowConfigDirectory(t *testing.T) {
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	firstConfig := filepath.Join(t.TempDir(), "one")
	t.Setenv("TSLINK_CONFIG_DIR", firstConfig)
	first, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := filepath.Join(t.TempDir(), "two")
	t.Setenv("TSLINK_CONFIG_DIR", secondConfig)
	second, err := credentialMutationLockPathFunc()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || filepath.Dir(first) != firstConfig || filepath.Dir(second) != secondConfig {
		t.Fatalf("file-only lock did not follow isolated config directories: distinct=%v first_scoped=%v second_scoped=%v", first != second, filepath.Dir(first) == firstConfig, filepath.Dir(second) == secondConfig)
	}
}

func TestCredentialMutationLockChild(t *testing.T) {
	path := os.Getenv("TSLINK_TEST_CREDENTIAL_LOCK_CHILD")
	if path == "" {
		return
	}
	if os.Getenv("TSLINK_TEST_CREDENTIAL_LOCK_FILE_ONLY") != "1" {
		credentialMutationLockPathFunc = func() (string, error) { return path, nil }
	}
	if err := os.WriteFile(path+".attempt", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.WriteFile(path+".acquired", nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialMutationLockCrossProcess(t *testing.T) {
	assertCredentialMutationLockCrossProcess(t, false)
}

func TestCredentialMutationLockMixedKeyringModes(t *testing.T) {
	assertCredentialMutationLockCrossProcess(t, true)
}

func assertCredentialMutationLockCrossProcess(t *testing.T, childFileOnly bool) {
	t.Helper()
	t.Setenv("TSLINK_DISABLE_KEYRING", "0")
	t.Setenv("TSLINK_CONFIG_DIR", t.TempDir())
	path := filepath.Join(t.TempDir(), "credentials.lock")
	old := credentialMutationLockPathFunc
	credentialMutationLockPathFunc = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialMutationLockPathFunc = old })
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCredentialMutationLockChild$")
	cmd.Env = append(os.Environ(), "TSLINK_TEST_CREDENTIAL_LOCK_CHILD="+path)
	if childFileOnly {
		cmd.Env = append(cmd.Env, "TSLINK_TEST_CREDENTIAL_LOCK_FILE_ONLY=1", "TSLINK_DISABLE_KEYRING=1")
	}
	if err := cmd.Start(); err != nil {
		unlock()
		t.Fatal(err)
	}
	defer func() {
		if locked {
			unlock()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(path + ".attempt"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child never attempted to acquire credential lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(path + ".acquired"); !os.IsNotExist(err) {
		t.Fatalf("child acquired credential lock before release: %v", err)
	}
	unlock()
	locked = false
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child failed after lock release: %v", err)
	}
	if _, err := os.Stat(path + ".acquired"); err != nil {
		t.Fatalf("child did not acquire lock after release: %v", err)
	}
}
