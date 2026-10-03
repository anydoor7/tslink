package credentials

import (
	"errors"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// assertWaitsForCredentialTransaction holds the credential mutation lock in a
// transaction, starts op, and requires op to stay blocked until the
// transaction ends and to succeed afterwards.
func assertWaitsForCredentialTransaction(t *testing.T, name string, op func() error) {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- WithMutationTransaction(func(*MutationTransaction) error {
			close(held)
			<-release
			return nil
		})
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("holder transaction never acquired the credential lock")
	}
	opDone := make(chan error, 1)
	go func() { opDone <- op() }()
	select {
	case err := <-opDone:
		close(release)
		<-txDone
		t.Fatalf("%s finished while another credential transaction held the lock: %v", name, err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-txDone; err != nil {
		t.Fatalf("holder transaction: %v", err)
	}
	select {
	case err := <-opDone:
		if err != nil {
			t.Fatalf("%s after the lock was released: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s remained blocked after the lock was released", name)
	}
}

func storeBothCredentials(t *testing.T) {
	t.Helper()
	if err := SetAPIKey("tskey-api-FAKE-logout"); err != nil {
		t.Fatal(err)
	}
	if err := SaveClientSecret("tskey-client-FAKE-logout"); err != nil {
		t.Fatal(err)
	}
}

func assertKeyringSlotEmpty(t *testing.T, name, user string) {
	t.Helper()
	if _, err := keyring.Get(keychainService, user); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("%s left keyring slot %s: %v", name, user, err)
	}
}

func TestLogoutDeletesWaitForCredentialTransaction(t *testing.T) {
	cases := []struct {
		name    string
		op      func() error
		removed []string
	}{
		{"DeleteStoredCredentialsStrict", DeleteStoredCredentialsStrict, []string{keychainAPIKey, keychainClientSecret}},
		{"DeleteStoredCredentialKindStrict", func() error { return DeleteStoredCredentialKindStrict(SlotAPIKey) }, []string{keychainAPIKey}},
		{"DeleteAPIKeyChecked", DeleteAPIKeyChecked, []string{keychainAPIKey}},
		{"DeleteClientSecretChecked", DeleteClientSecretChecked, []string{keychainClientSecret}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			storeBothCredentials(t)
			assertWaitsForCredentialTransaction(t, tc.name, tc.op)
			for _, user := range tc.removed {
				assertKeyringSlotEmpty(t, tc.name, user)
			}
		})
	}
}
