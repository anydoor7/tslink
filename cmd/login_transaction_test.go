package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
)

type recordingCredentialStore struct {
	values map[loginCredentialMode]string
	ops    []string
	failAt map[string]int
	counts map[string]int
}

func newRecordingCredentialStore(apiKey, clientSecret string) *recordingCredentialStore {
	return &recordingCredentialStore{
		values: map[loginCredentialMode]string{
			loginCredentialModeAPIKey:       apiKey,
			loginCredentialModeClientSecret: clientSecret,
		},
		failAt: map[string]int{},
		counts: map[string]int{},
	}
}

func (s *recordingCredentialStore) fail(op string) error {
	s.counts[op]++
	if s.failAt[op] == s.counts[op] {
		return fmt.Errorf("%s failed", op)
	}
	return nil
}

func (s *recordingCredentialStore) Read(mode loginCredentialMode) (string, error) {
	op := "read:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return "", err
	}
	return s.values[mode], nil
}

func (s *recordingCredentialStore) Write(mode loginCredentialMode, value string) (credentials.CredentialBackend, error) {
	op := "write:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return "", err
	}
	s.values[mode] = value
	return credentials.CredentialBackendKeyring, nil
}

func (s *recordingCredentialStore) Delete(mode loginCredentialMode) error {
	op := "delete:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return err
	}
	s.values[mode] = ""
	return nil
}

func (s *recordingCredentialStore) assertState(t *testing.T, apiKey, clientSecret string) {
	t.Helper()
	if got := s.values[loginCredentialModeAPIKey]; got != apiKey {
		t.Fatalf("api credential state mismatch; ops=%v", s.ops)
	}
	if got := s.values[loginCredentialModeClientSecret]; got != clientSecret {
		t.Fatalf("client-secret credential state mismatch; ops=%v", s.ops)
	}
}

func withLoginVerifier(t *testing.T, fn func(context.Context, string) error) {
	t.Helper()
	old := loginVerifyAPIKeyFn
	t.Cleanup(func() { loginVerifyAPIKeyFn = old })
	loginVerifyAPIKeyFn = fn
}

func withLoginClientSecretActivator(t *testing.T, fn func(context.Context, string) error) {
	t.Helper()
	old := loginActivateClientSecretFn
	t.Cleanup(func() { loginActivateClientSecretFn = old })
	loginActivateClientSecretFn = fn
}

func (s *recordingCredentialStore) hasMutationOps() bool {
	for _, op := range s.ops {
		if strings.HasPrefix(op, "write:") || strings.HasPrefix(op, "delete:") {
			return true
		}
	}
	return false
}

func TestReplaceLoginCredentialTransactionalSuccessModes(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return nil })
	withLoginClientSecretActivator(t, func(context.Context, string) error { return nil })

	tests := []struct {
		name      string
		apiBefore string
		csBefore  string
		mode      loginCredentialMode
		candidate string
		apiAfter  string
		csAfter   string
	}{
		{"api to api", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", "tskey-api-new", ""},
		{"secret to secret", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", "", "tskey-client-new"},
		{"api to secret", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", "", "tskey-client-new"},
		{"secret to api", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", "tskey-api-new", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newRecordingCredentialStore(tc.apiBefore, tc.csBefore)
			if _, err := replaceLoginCredential(context.Background(), store, tc.mode, tc.candidate); err != nil {
				t.Fatalf("replaceLoginCredential() error = %v", err)
			}
			store.assertState(t, tc.apiAfter, tc.csAfter)
		})
	}
}

func TestReplaceLoginCredentialRollbackFailurePoints(t *testing.T) {
	// Client-secret activation succeeds for these cases so the injected store
	// failure is what exercises rollback; activation-failure cases live in
	// TestReplaceLoginCredentialClientSecretActivationPreservesLastKnownGood.
	withLoginClientSecretActivator(t, func(context.Context, string) error { return nil })

	tests := []struct {
		name        string
		apiBefore   string
		csBefore    string
		mode        loginCredentialMode
		candidate   string
		failAt      map[string]int
		verifyError bool
		wantAPI     string
		wantCS      string
	}{
		{"read api fails before stage", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"read:api-key": 1}, false, "tskey-api-old", ""},
		{"read secret fails before stage", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"read:client-secret": 1}, false, "tskey-api-old", ""},
		{"api validation fails before write", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", nil, true, "tskey-api-old", ""},
		{"api write fails", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"write:api-key": 1}, false, "tskey-api-old", ""},
		{"api readback fails", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"read:api-key": 2}, false, "tskey-api-old", ""},
		{"secret write fails", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", map[string]int{"write:client-secret": 1}, false, "", "tskey-client-old"},
		{"secret readback fails", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", map[string]int{"read:client-secret": 2}, false, "", "tskey-client-old"},
		{"cross-mode delete old api fails", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", map[string]int{"delete:api-key": 1}, false, "tskey-api-old", ""},
		{"cross-mode delete old secret fails", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"delete:client-secret": 1}, false, "", "tskey-client-old"},
		{"verify old api inactive fails", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", map[string]int{"read:api-key": 2}, false, "tskey-api-old", ""},
		{"verify old secret inactive fails", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", map[string]int{"read:client-secret": 2}, false, "", "tskey-client-old"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newRecordingCredentialStore(tc.apiBefore, tc.csBefore)
			store.failAt = tc.failAt
			withLoginVerifier(t, func(context.Context, string) error {
				if tc.verifyError {
					return fmt.Errorf("candidate verification failed")
				}
				return nil
			})

			_, err := replaceLoginCredential(context.Background(), store, tc.mode, tc.candidate)
			if err == nil {
				t.Fatal("replaceLoginCredential() error = nil, want failure")
			}
			store.assertState(t, tc.wantAPI, tc.wantCS)
			if store.values[tc.mode] == tc.candidate {
				t.Fatalf("failed candidate remained active after rollback; ops=%v", store.ops)
			}
			if strings.Contains(err.Error(), tc.candidate) {
				t.Fatal("error leaked candidate credential material")
			}
		})
	}
}

// TestReplaceLoginCredentialClientSecretActivationPreservesLastKnownGood is the
// old-implementation-killing test. Before the fix, a
// syntactically valid but unusable client secret (typo/revoked/wrong-scope) was
// written and the working API key was deleted BEFORE any semantic proof, so the
// next `serve` failed auth with no fallback. The transaction must now run the
// semantic activation before any store mutation, so an activation failure keeps
// the previous credential active and never writes/deletes anything.
func TestReplaceLoginCredentialClientSecretActivationPreservesLastKnownGood(t *testing.T) {
	// The API verifier is irrelevant here; only client-secret activation runs.
	withLoginVerifier(t, func(context.Context, string) error { return nil })

	activationErrors := []struct {
		name string
		err  error
	}{
		{"invalid secret", fmt.Errorf("client secret failed activation: invalid key")},
		{"revoked secret", fmt.Errorf("client secret failed activation: key revoked")},
		{"wrong scope", fmt.Errorf("client secret failed activation: tag not authorized by scope")},
		{"control plane unreachable", fmt.Errorf("client secret failed activation: context deadline exceeded")},
	}

	for _, ae := range activationErrors {
		t.Run("cross-mode api->secret fails: "+ae.name, func(t *testing.T) {
			withLoginClientSecretActivator(t, func(context.Context, string) error { return ae.err })

			store := newRecordingCredentialStore("tskey-api-old", "")
			_, err := replaceLoginCredential(context.Background(), store, loginCredentialModeClientSecret, "tskey-client-unusable")
			if err == nil {
				t.Fatal("replaceLoginCredential() error = nil, want activation failure")
			}
			// The working API key must survive untouched; the candidate must not
			// be persisted.
			store.assertState(t, "tskey-api-old", "")
			// Ordering proof: activation runs before commit, so no write/delete
			// op may have executed on failure.
			if store.hasMutationOps() {
				t.Fatalf("credential store was mutated before activation succeeded; ops=%v", store.ops)
			}
			if strings.Contains(err.Error(), "tskey-client-unusable") {
				t.Fatal("error leaked candidate secret material")
			}
		})
	}

	t.Run("cross-mode secret->api unaffected by client-secret activation", func(t *testing.T) {
		// Switching TO api-key must not invoke client-secret activation at all.
		activatorCalled := false
		withLoginClientSecretActivator(t, func(context.Context, string) error {
			activatorCalled = true
			return nil
		})
		store := newRecordingCredentialStore("", "tskey-client-old")
		if _, err := replaceLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new"); err != nil {
			t.Fatalf("replaceLoginCredential() error = %v", err)
		}
		if activatorCalled {
			t.Fatal("client-secret activation ran for an api-key switch")
		}
		store.assertState(t, "tskey-api-new", "")
	})

	t.Run("successful activation commits and retires previous api key", func(t *testing.T) {
		activatorCalled := false
		withLoginClientSecretActivator(t, func(context.Context, string) error {
			activatorCalled = true
			return nil
		})
		store := newRecordingCredentialStore("tskey-api-old", "")
		if _, err := replaceLoginCredential(context.Background(), store, loginCredentialModeClientSecret, "tskey-client-good"); err != nil {
			t.Fatalf("replaceLoginCredential() error = %v", err)
		}
		if !activatorCalled {
			t.Fatal("client-secret activation was not invoked before commit")
		}
		// Only after a successful activation is the old API key retired.
		store.assertState(t, "", "tskey-client-good")
	})
}
