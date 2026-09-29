package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/credentials"
)

// Logout and logout --kind must wait for a credential transaction that is
// already running (for example a login) instead of deleting inside it.
func TestLogoutWaitsForHeldCredentialLock(t *testing.T) {
	for _, kind := range []string{"", credentials.SlotAPIKey} {
		name := "full"
		if kind != "" {
			name = "kind=" + kind
		}
		t.Run(name, func(t *testing.T) {
			dir := setupLoginTest(t)
			useRealLoginTransaction(t)
			mockAPIKeySuccess(t)
			if _, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-held", loginReplaceOptions{Now: loginTestNow}); err != nil {
				t.Fatal(err)
			}
			held := make(chan struct{})
			release := make(chan struct{})
			txDone := make(chan error, 1)
			go func() {
				txDone <- credentials.WithMutationTransaction(func(*credentials.MutationTransaction) error {
					close(held)
					<-release
					return nil
				})
			}()
			select {
			case <-held:
			case <-time.After(3 * time.Second):
				t.Fatal("holder transaction never acquired the credential lock")
			}
			opts := logoutOptions{PIDPath: filepath.Join(dir, "pid"), AuthKeyPath: filepath.Join(dir, "authkey"), NodesDir: filepath.Join(dir, "nodes"), ConfigDir: dir, Kind: kind}
			logoutDone := make(chan error, 1)
			go func() { logoutDone <- logoutUserWithOptions(opts, false, &bytes.Buffer{}) }()
			select {
			case err := <-logoutDone:
				close(release)
				<-txDone
				t.Fatalf("logout finished while another credential transaction held the lock: %v", err)
			case <-time.After(150 * time.Millisecond):
			}
			close(release)
			if err := <-txDone; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-logoutDone:
				if err != nil {
					t.Fatalf("logout after the lock was released: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("logout remained blocked after the lock was released")
			}
			if key, err := credentials.GetAPIKey(); err != nil || key != "" {
				t.Fatalf("api key still stored after logout: present=%v err=%v", key != "", err)
			}
		})
	}
}
