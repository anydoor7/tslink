package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
)

func useRealLoginTransaction(t *testing.T) {
	t.Helper()
	old := loginMutationTransactionFn
	loginMutationTransactionFn = credentials.WithMutationTransaction
	t.Cleanup(func() { loginMutationTransactionFn = old })
}

// A login that commits while logout runs must keep its metadata: logout
// removes values and metadata inside one credential transaction, so the login
// either finishes before logout starts deleting or starts after it is done.
func TestLogoutCannotRemoveMetadataOfLoginCommittedDuringLogout(t *testing.T) {
	dir := setupLoginTest(t)
	useRealLoginTransaction(t)
	mockAPIKeySuccess(t)
	if _, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-old", loginReplaceOptions{Now: loginTestNow}); err != nil {
		t.Fatal(err)
	}

	userExpiry := loginTestNow.Add(30 * 24 * time.Hour)
	var loginErr error
	var loginStarted atomic.Bool
	loginFinished := make(chan struct{})
	originalDelete := deleteStoredCredentialsFn
	t.Cleanup(func() { deleteStoredCredentialsFn = originalDelete })
	deleteStoredCredentialsFn = func() error {
		err := originalDelete()
		loginStarted.Store(true)
		go func() {
			_, loginErr = commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-new",
				loginReplaceOptions{Now: loginTestNow, ExpiresAt: &userExpiry, ExpiresAtSource: credentials.ExpirySourceUser})
			close(loginFinished)
		}()
		select {
		case <-loginFinished:
			t.Log("login committed between logout's value delete and its metadata removal")
		case <-time.After(300 * time.Millisecond):
		}
		return err
	}
	// go-keyring's in-memory mock is not goroutine-safe (the real stores are
	// separate processes). Logout's final read-back waits for the concurrent
	// login so the two never touch the mock map at once; the ordering of
	// logout's own delete and metadata removal is unaffected.
	originalInspect := inspectStoredCredentialsFn
	t.Cleanup(func() { inspectStoredCredentialsFn = originalInspect })
	inspectStoredCredentialsFn = func() (credentials.StoredCredentialStatus, error) {
		if loginStarted.Load() {
			select {
			case <-loginFinished:
			case <-time.After(10 * time.Second):
				t.Error("concurrent login did not finish")
			}
		}
		return originalInspect()
	}

	// Logout's final read-back may see the concurrent login's credential and
	// honestly report it; either outcome is correct for logout itself.
	var out bytes.Buffer
	if err := logoutUser(filepath.Join(dir, "pid"), filepath.Join(dir, "authkey"), filepath.Join(dir, "nodes"), dir, false, &out); err != nil && !strings.Contains(err.Error(), "credential still present after cleanup") {
		t.Fatalf("logout: %v", err)
	}
	select {
	case <-loginFinished:
		if loginErr != nil {
			t.Fatalf("concurrent login: %v", loginErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent login did not finish")
	}

	key, err := credentials.GetAPIKey()
	if err != nil || key != "tskey-api-FAKE-new" {
		t.Fatalf("stored api key is new=%v err=%v, want the concurrent login's key", key == "tskey-api-FAKE-new", err)
	}
	meta, err := credentials.ReadSlotMetadata(credentials.SlotAPIKey)
	if err != nil || meta == nil {
		t.Fatalf("LOST UPDATE: logout removed the metadata of a credential committed after its value delete: meta=%+v err=%v", meta, err)
	}
	if meta.Fingerprint != credentials.Fingerprint("tskey-api-FAKE-new") || meta.ExpiresAtSource != credentials.ExpirySourceUser {
		t.Fatalf("metadata = %+v, want the concurrent login's record", meta)
	}
}

// Logout, logout --kind and login each take the credential lock exactly once;
// a nested acquisition would wait out the 5 s lock timeout and fail.
func TestLoginLogoutAndLogoutKindCompleteWithoutLockTimeout(t *testing.T) {
	dir := setupLoginTest(t)
	useRealLoginTransaction(t)
	mockAPIKeySuccess(t)
	step := func(name string, fn func() error) {
		t.Helper()
		// A nested acquisition returns ErrMutationLockBusy. Require success,
		// regardless of filesystem and scheduler latency outside the lock.
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	login := func() error {
		_, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-cycle", loginReplaceOptions{Now: loginTestNow})
		return err
	}
	opts := logoutOptions{PIDPath: filepath.Join(dir, "pid"), AuthKeyPath: filepath.Join(dir, "authkey"), NodesDir: filepath.Join(dir, "nodes"), ConfigDir: dir}
	step("login", login)
	kindOpts := opts
	kindOpts.Kind = credentials.SlotAPIKey
	step("logout --kind api-key", func() error { return logoutUserWithOptions(kindOpts, false, &bytes.Buffer{}) })
	if meta, err := credentials.ReadSlotMetadata(credentials.SlotAPIKey); err != nil || meta != nil {
		t.Fatalf("logout --kind left api-key metadata: %+v %v", meta, err)
	}
	step("login again", login)
	step("logout", func() error { return logoutUserWithOptions(opts, false, &bytes.Buffer{}) })
	if key, err := credentials.GetAPIKey(); err != nil || key != "" {
		t.Fatalf("api key still stored after logout: present=%v err=%v", key != "", err)
	}
	if meta, err := credentials.ReadSlotMetadata(credentials.SlotAPIKey); err != nil || meta != nil {
		t.Fatalf("logout left api-key metadata: %+v %v", meta, err)
	}
}
