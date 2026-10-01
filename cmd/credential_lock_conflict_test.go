package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/output"
)

func holdCmdCredentialTransaction(t *testing.T) {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- credentials.WithMutationTransaction(func(*credentials.MutationTransaction) error {
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
	t.Cleanup(func() {
		close(release)
		<-done
	})
}

// Lock contention is the CLI's retryable conflict: exit 4 with the stable
// "conflict" code, the lock path, and a retry hint, for login, logout and
// logout --kind alike.
func TestCredentialLockContentionIsRetryableConflictForLoginAndLogout(t *testing.T) {
	if got := output.StableErrorCode(output.ExitConflict); got != "conflict" {
		t.Fatalf("CLI conflict code = %q", got)
	}
	if info := errorCodeManifest()["conflict"]; info.ExitCode != output.ExitConflict {
		t.Fatalf("manifest conflict exit = %d, want %d", info.ExitCode, output.ExitConflict)
	}
	cases := map[string]func(dir string) error{
		"login": func(string) error {
			_, err := commitLoginCredential(context.Background(), defaultLoginCredentialStore{}, loginCredentialModeAPIKey, "tskey-api-FAKE-contended", loginReplaceOptions{Now: loginTestNow})
			return err
		},
		"logout": func(dir string) error {
			return logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), AuthKeyPath: filepath.Join(dir, "authkey"), NodesDir: filepath.Join(dir, "nodes"), ConfigDir: dir}, false, &bytes.Buffer{})
		},
		"logout --kind": func(dir string) error {
			return logoutUserWithOptions(logoutOptions{PIDPath: filepath.Join(dir, "pid"), Kind: credentials.SlotAPIKey}, false, &bytes.Buffer{})
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			dir := setupLoginTest(t)
			useRealLoginTransaction(t)
			mockAPIKeySuccess(t)
			if err := credentials.SetAPIKey("tskey-api-FAKE-before"); err != nil {
				t.Fatal(err)
			}
			lockPath := filepath.Join(dir, "credential-test.lock")
			t.Cleanup(credentials.SetMutationLockTimeoutForTesting(200 * time.Millisecond))
			holdCmdCredentialTransaction(t)

			err := run(dir)
			if err == nil {
				t.Fatalf("%s succeeded while another credential transaction held the lock", name)
			}
			if code := output.ExitCode(err); code != output.ExitConflict {
				t.Fatalf("%s exit = %d, want conflict %d (err=%v)", name, code, output.ExitConflict, err)
			}
			failure := output.NewFailureForError(name, err)
			if failure.Error == nil || failure.Error.Code != "conflict" || failure.Code != output.ExitConflict || len(failure.Error.Next) == 0 {
				t.Fatalf("%s envelope = %+v", name, failure.Error)
			}
			for _, want := range []string{lockPath, "another tslink login, logout, or serve is running", "retry"} {
				if !strings.Contains(failure.Error.Message, want) {
					t.Fatalf("%s message %q does not mention %q", name, failure.Error.Message, want)
				}
			}
			if key, err := credentials.GetAPIKey(); err != nil || key != "tskey-api-FAKE-before" {
				t.Fatalf("%s changed the stored credential under contention: kept=%v err=%v", name, key == "tskey-api-FAKE-before", err)
			}
		})
	}
}
