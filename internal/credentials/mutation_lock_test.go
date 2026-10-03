package credentials

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	case <-time.After(5 * time.Second):
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
	case <-time.After(5 * time.Second):
		t.Fatal("migration did not finish")
	}
	select {
	case err := <-loginDone:
		if err != nil {
			t.Fatalf("login failed after migration: %v", err)
		}
	case <-time.After(5 * time.Second):
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
	case <-time.After(5 * time.Second):
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
	case <-time.After(5 * time.Second):
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
	case <-time.After(5 * time.Second):
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
	// HOME and USERPROFILE do not isolate the account lock: it follows the OS
	// account. Vary them with the config directory and assert the path does
	// not move. Only the path is computed; nothing is opened.
	t.Setenv("TSLINK_DISABLE_KEYRING", "0")
	firstHome, secondHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", firstHome)
	t.Setenv("USERPROFILE", firstHome)
	t.Setenv("TSLINK_CONFIG_DIR", filepath.Join(t.TempDir(), "one"))
	first, err := defaultCredentialMutationLockPath()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", secondHome)
	t.Setenv("USERPROFILE", secondHome)
	t.Setenv("TSLINK_CONFIG_DIR", filepath.Join(t.TempDir(), "two"))
	second, err := defaultCredentialMutationLockPath()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("same keyring user got different lock paths for config directories: %q != %q", first, second)
	}
	if first == "" {
		t.Fatal("enabled keyring has no account lock path")
	}
	for _, home := range []string{firstHome, secondHome} {
		if pathInside(first, home) {
			t.Fatalf("account lock %s follows HOME/USERPROFILE %s instead of the OS account", first, home)
		}
	}
}

func TestFileOnlyCredentialLocksFollowConfigDirectory(t *testing.T) {
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	firstConfig := filepath.Join(t.TempDir(), "one")
	t.Setenv("TSLINK_CONFIG_DIR", firstConfig)
	first, err := defaultCredentialMutationLockPath()
	if err != nil {
		t.Fatal(err)
	}
	secondConfig := filepath.Join(t.TempDir(), "two")
	t.Setenv("TSLINK_CONFIG_DIR", secondConfig)
	second, err := defaultCredentialMutationLockPath()
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
	// This binary's TestMain gave the child a config directory of its own;
	// contend on the parent's, which the parent passes explicitly.
	t.Setenv("TSLINK_CONFIG_DIR", os.Getenv("TSLINK_TEST_CREDENTIAL_LOCK_CONFIG_DIR"))
	if os.Getenv("TSLINK_TEST_CREDENTIAL_LOCK_FILE_ONLY") != "1" {
		credentialMutationLockPathFunc = func() (string, error) { return path, nil }
	}
	oldTry := tryLockCredentialFileFunc
	blocked := 0
	var blockedFile *os.File
	tryLockCredentialFileFunc = func(f *os.File) (bool, error) {
		locked, err := oldTry(f)
		if !locked && err == nil {
			if blockedFile == nil {
				blockedFile = f
			}
			// Keyring-enabled writers take two different files. Only retries
			// on the first contended descriptor prove continued exclusion.
			if f == blockedFile && blocked < 2 {
				blocked++
				fmt.Println("blocked")
			}
		}
		return locked, err
	}
	t.Cleanup(func() { tryLockCredentialFileFunc = oldTry })
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("acquired")
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
	configDir := t.TempDir()
	t.Setenv("TSLINK_CONFIG_DIR", configDir)
	path := filepath.Join(t.TempDir(), "credentials.lock")
	old := credentialMutationLockPathFunc
	credentialMutationLockPathFunc = func() (string, error) { return path, nil }
	t.Cleanup(func() { credentialMutationLockPathFunc = old })
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCredentialMutationLockChild$")
	cmd.Env = append(os.Environ(), "TSLINK_TEST_CREDENTIAL_LOCK_CHILD="+path, "TSLINK_TEST_CREDENTIAL_LOCK_CONFIG_DIR="+configDir)
	if childFileOnly {
		cmd.Env = append(cmd.Env, "TSLINK_TEST_CREDENTIAL_LOCK_FILE_ONLY=1", "TSLINK_DISABLE_KEYRING=1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		unlock()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		unlock()
		t.Fatal(err)
	}
	const hangGuard = 5 * time.Second
	joined := make(chan struct{})
	var waitErr error
	startWait := sync.OnceFunc(func() {
		go func() { waitErr = cmd.Wait(); close(joined) }()
	})
	killAndJoin := func() {
		_ = cmd.Process.Kill()
		startWait()
		select {
		case <-joined:
		case <-time.After(hangGuard):
			t.Error("credential child did not exit after kill within the hang guard")
		}
	}
	defer func() {
		killAndJoin()
		if locked {
			unlock()
		}
	}()
	reader := bufio.NewReader(stdout)
	readEvent := func(want string) {
		t.Helper()
		type result struct {
			event string
			err   error
		}
		read := make(chan result, 1)
		go func() { event, err := reader.ReadString('\n'); read <- result{event, err} }()
		select {
		case got := <-read:
			if got.err != nil || got.event != want {
				t.Fatalf("credential child event=%q err=%v, want %q", got.event, got.err, want)
			}
		case <-time.After(hangGuard):
			killAndJoin()
			select {
			case <-read:
			case <-time.After(hangGuard):
				t.Fatal("credential IPC reader did not exit after child kill")
			}
			t.Fatalf("credential child did not report %q within the hang guard; killed and joined child", want)
		}
	}
	// Two failed OS attempts prove exclusion across a retry. A child that
	// ignores the first TryLock result reports "acquired" before the second.
	readEvent("blocked\n")
	readEvent("blocked\n")
	if _, err := os.Stat(path + ".acquired"); !os.IsNotExist(err) {
		t.Fatalf("child acquired credential lock before release: %v", err)
	}
	unlock()
	locked = false
	readEvent("acquired\n")
	startWait()
	select {
	case <-joined:
	case <-time.After(hangGuard):
		killAndJoin()
		t.Fatal("credential child did not exit after lock release within the hang guard")
	}
	if waitErr != nil {
		t.Fatalf("child failed after lock release: %v", waitErr)
	}
	if _, err := os.Stat(path + ".acquired"); err != nil {
		t.Fatalf("child did not acquire lock after release: %v", err)
	}
}
