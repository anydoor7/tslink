package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/credentials"
)

type partialWriteLoginStore struct {
	*recordingCredentialStore
	failed bool
}

func (s *partialWriteLoginStore) Write(mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	if !s.failed {
		s.failed = true
		s.values[mode] = value
		return "", errors.New("synthetic post-write failure")
	}
	return s.recordingCredentialStore.Write(mode, value)
}

func TestLoginRollsBackWriteThatFailedAfterMutation(t *testing.T) {
	for _, mode := range []loginCredentialMode{loginCredentialModeAPIKey, loginCredentialModeClientSecret} {
		t.Run(string(mode), func(t *testing.T) {
			base := newRecordingCredentialStore("tskey-api-<test-only-FAKE-OLD>", "tskey-client-<testonly_FAKE>-<testonly_OLD>")
			base.withMeta(loginCredentialModeAPIKey, loginTestNow)
			base.withMeta(loginCredentialModeClientSecret, loginTestNow)
			store := &partialWriteLoginStore{recordingCredentialStore: base}
			candidate := "tskey-api-<test-only-FAKE-NEW>"
			if mode == loginCredentialModeClientSecret {
				candidate = "tskey-client-<testonly_FAKE>-<testonly_NEW>"
			}
			_, err := commitLoginCredentialValidated(context.Background(), store, mode, candidate, loginReplaceOptions{Now: loginTestNow}, true)
			if err == nil || !strings.Contains(err.Error(), "synthetic post-write failure") {
				t.Fatalf("write failure not returned: %v", err)
			}
			base.assertState(t, "tskey-api-<test-only-FAKE-OLD>", "tskey-client-<testonly_FAKE>-<testonly_OLD>")
			base.assertMetaMatchesValues(t)
		})
	}
}
