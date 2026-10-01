package cmd

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/authmode"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/registry"
)

var loginTestNow = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

type recordingCredentialStore struct {
	values map[loginCredentialMode]string
	meta   map[loginCredentialMode]*credentials.SlotMetadata
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
		meta:   map[loginCredentialMode]*credentials.SlotMetadata{},
		failAt: map[string]int{},
		counts: map[string]int{},
	}
}

// withMeta seeds a slot record the way a previous login would have left it.
func (s *recordingCredentialStore) withMeta(mode loginCredentialMode, storedAt time.Time) *recordingCredentialStore {
	meta, err := credentials.NewSlotMetadata(string(mode), s.values[mode], credentials.StoredOptions{Now: storedAt, Verified: true})
	if err != nil {
		panic(err)
	}
	s.meta[mode] = &meta
	return s
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

func (s *recordingCredentialStore) ReadMeta(mode loginCredentialMode) (*credentials.SlotMetadata, error) {
	op := "read-meta:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return nil, err
	}
	if meta := s.meta[mode]; meta != nil {
		copied := *meta
		return &copied, nil
	}
	return nil, nil
}

func (s *recordingCredentialStore) WriteMeta(mode loginCredentialMode, meta credentials.SlotMetadata) error {
	op := "write-meta:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return err
	}
	copied := meta
	s.meta[mode] = &copied
	return nil
}

func (s *recordingCredentialStore) DeleteMeta(mode loginCredentialMode) error {
	op := "delete-meta:" + string(mode)
	s.ops = append(s.ops, op)
	if err := s.fail(op); err != nil {
		return err
	}
	delete(s.meta, mode)
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

// assertMetaMatchesValues checks the metadata invariant: every populated slot
// has a record whose fingerprint matches the value, and every empty slot has no
// record. The transaction must leave the store in this state on success and
// after every rollback.
func (s *recordingCredentialStore) assertMetaMatchesValues(t *testing.T) {
	t.Helper()
	for _, mode := range []loginCredentialMode{loginCredentialModeAPIKey, loginCredentialModeClientSecret} {
		value := s.values[mode]
		meta := s.meta[mode]
		switch {
		case value == "" && meta != nil:
			t.Fatalf("%s has metadata without a value; ops=%v", mode, s.ops)
		case value != "" && meta == nil:
			t.Fatalf("%s has a value without metadata; ops=%v", mode, s.ops)
		case value != "" && meta.Fingerprint != credentials.Fingerprint(value):
			t.Fatalf("%s metadata fingerprint %q does not match value fingerprint %q; ops=%v", mode, meta.Fingerprint, credentials.Fingerprint(value), s.ops)
		}
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

func withLoginClock(t *testing.T, now time.Time) {
	t.Helper()
	old := loginNowFn
	t.Cleanup(func() { loginNowFn = old })
	loginNowFn = func() time.Time { return now }
}

func (s *recordingCredentialStore) hasMutationOps() bool {
	for _, op := range s.ops {
		if strings.HasPrefix(op, "write") || strings.HasPrefix(op, "delete") {
			return true
		}
	}
	return false
}

func TestReplaceLoginCredentialTransactionalSuccessModes(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return nil })
	withLoginClientSecretActivator(t, func(context.Context, string) error { return nil })
	withLoginClock(t, loginTestNow)

	tests := []struct {
		name        string
		apiBefore   string
		csBefore    string
		mode        loginCredentialMode
		candidate   string
		retireOther bool
		apiAfter    string
		csAfter     string
		wantRetired loginCredentialMode
		wantRotated bool
	}{
		{"api to api", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, "tskey-api-new", "", "", true},
		{"secret to secret", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", false, "", "tskey-client-new", "", true},
		// Dual-slot default: adding the other kind keeps the existing one.
		{"api plus secret keeps api", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", false, "tskey-api-old", "tskey-client-new", "", false},
		{"secret plus api keeps secret", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", false, "tskey-api-new", "tskey-client-old", "", false},
		// Explicit --retire-other restores the legacy single-slot swap.
		{"api to secret retire", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", true, "", "tskey-client-new", loginCredentialModeAPIKey, false},
		{"secret to api retire", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", true, "tskey-api-new", "", loginCredentialModeClientSecret, false},
		// --retire-other with an empty other slot retires nothing and says so.
		{"retire with nothing to retire", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", true, "tskey-api-new", "", "", true},
		// Re-committing the same value re-anchors metadata without a rotation.
		{"same value is not a rotation", "tskey-api-same", "", loginCredentialModeAPIKey, "tskey-api-same", false, "tskey-api-same", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newRecordingCredentialStore(tc.apiBefore, tc.csBefore)
			if tc.apiBefore != "" {
				store.withMeta(loginCredentialModeAPIKey, loginTestNow.Add(-24*time.Hour))
			}
			if tc.csBefore != "" {
				store.withMeta(loginCredentialModeClientSecret, loginTestNow.Add(-24*time.Hour))
			}
			result, err := commitLoginCredential(context.Background(), store, tc.mode, tc.candidate, loginReplaceOptions{RetireOther: tc.retireOther, Now: loginTestNow})
			if err != nil {
				t.Fatalf("commitLoginCredential() error = %v", err)
			}
			store.assertState(t, tc.apiAfter, tc.csAfter)
			store.assertMetaMatchesValues(t)
			if result.Retired != tc.wantRetired {
				t.Fatalf("retired = %q, want %q; ops=%v", result.Retired, tc.wantRetired, store.ops)
			}
			if result.Rotated != tc.wantRotated {
				t.Fatalf("rotated = %v, want %v; ops=%v", result.Rotated, tc.wantRotated, store.ops)
			}
			if result.Metadata.Fingerprint != credentials.Fingerprint(tc.candidate) || !result.Metadata.StoredAt.Equal(loginTestNow) {
				t.Fatalf("metadata = %+v, want fingerprint of candidate stored at %s", result.Metadata, loginTestNow)
			}
			if result.Metadata.LastVerifiedAt == nil || result.Metadata.LastVerifiedResult != credentials.VerifyResultOK {
				t.Fatalf("metadata verification = %+v, want ok at commit", result.Metadata)
			}
			if tc.mode == loginCredentialModeAPIKey {
				if result.Metadata.ExpiresAt == nil || result.Metadata.ExpiresAtSource != credentials.ExpirySourceAssumedMax || !result.Metadata.ExpiresAt.Equal(loginTestNow.Add(credentials.APIKeyAssumedMaxLifetime)) {
					t.Fatalf("api-key metadata expiry = %+v, want assumed max 90d", result.Metadata)
				}
			} else if result.Metadata.ExpiresAt != nil {
				t.Fatalf("client-secret metadata expiry = %v, want none", result.Metadata.ExpiresAt)
			}
			if tc.wantRotated && result.PreviousFingerprint == "" {
				t.Fatal("rotation did not report the previous fingerprint")
			}
			if strings.Contains(result.PreviousFingerprint, "tskey") || strings.Contains(result.Metadata.Fingerprint, "tskey") {
				t.Fatalf("fingerprints leak credential shape: %+v", result)
			}
		})
	}
}

func TestReplaceLoginCredentialWrapperKeepsOtherSlot(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return nil })
	withLoginClock(t, loginTestNow)
	store := newRecordingCredentialStore("", "tskey-client-old").withMeta(loginCredentialModeClientSecret, loginTestNow)
	backend, err := replaceLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new")
	if err != nil || backend != credentials.CredentialBackendKeyring {
		t.Fatalf("replaceLoginCredential() = %q, %v", backend, err)
	}
	store.assertState(t, "tskey-api-new", "tskey-client-old")
	store.assertMetaMatchesValues(t)
}

func TestCommitLoginCredentialRecordsOperatorExpiry(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return nil })
	expires := loginTestNow.Add(30 * 24 * time.Hour)
	store := newRecordingCredentialStore("", "")
	oldMark := loginMarkCredentialUpgradeFn
	loginMarkCredentialUpgradeFn = func() error { return nil }
	t.Cleanup(func() { loginMarkCredentialUpgradeFn = oldMark })

	result, err := commitLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new", loginReplaceOptions{Now: loginTestNow, ExpiresAt: &expires, ExpiresAtSource: credentials.ExpirySourceUser})
	if err != nil {
		t.Fatalf("commitLoginCredential() error = %v", err)
	}
	if result.Metadata.ExpiresAt == nil || !result.Metadata.ExpiresAt.Equal(expires) || result.Metadata.ExpiresAtSource != credentials.ExpirySourceUser {
		t.Fatalf("metadata = %+v, want operator expiry %s source user", result.Metadata, expires)
	}
}

func TestReplaceLoginCredentialFromZeroTierRecordsUpgrade(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	withLoginVerifier(t, func(context.Context, string) error { return nil })

	store := newRecordingCredentialStore("", "")
	if _, err := replaceLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new"); err != nil {
		t.Fatalf("replaceLoginCredential() error = %v", err)
	}
	store.assertState(t, "tskey-api-new", "")
	store.assertMetaMatchesValues(t)
	pending, err := authmode.CredentialUpgradePending()
	if err != nil {
		t.Fatalf("CredentialUpgradePending() error = %v", err)
	}
	if !pending {
		t.Fatal("credential upgrade marker is absent after zero-to-credential commit")
	}
}

func TestReplaceLoginCredentialUpgradeMarkerFailureRollsBack(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return nil })
	oldMark := loginMarkCredentialUpgradeFn
	loginMarkCredentialUpgradeFn = func() error { return fmt.Errorf("marker unavailable") }
	t.Cleanup(func() { loginMarkCredentialUpgradeFn = oldMark })

	store := newRecordingCredentialStore("", "")
	_, err := replaceLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new")
	if err == nil || !strings.Contains(err.Error(), "record Tier 1 to Tier 2 transition") {
		t.Fatalf("replaceLoginCredential() error = %v, want transition marker failure", err)
	}
	store.assertState(t, "", "")
	store.assertMetaMatchesValues(t)
}

func TestReplaceLoginCredentialRollbackFailurePoints(t *testing.T) {
	// Client-secret activation succeeds for these cases so the injected store
	// failure is what exercises rollback; activation-failure cases live in
	// TestReplaceLoginCredentialClientSecretActivationPreservesLastKnownGood.
	withLoginClientSecretActivator(t, func(context.Context, string) error { return nil })
	withLoginClock(t, loginTestNow)

	tests := []struct {
		name        string
		apiBefore   string
		csBefore    string
		mode        loginCredentialMode
		candidate   string
		retire      bool
		failAt      map[string]int
		verifyError bool
		wantAPI     string
		wantCS      string
	}{
		{"read api fails before stage", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"read:api-key": 1}, false, "tskey-api-old", ""},
		{"read secret fails before stage", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"read:client-secret": 1}, false, "tskey-api-old", ""},
		{"read api meta fails before stage", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"read-meta:api-key": 1}, false, "tskey-api-old", ""},
		{"api validation fails before write", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, nil, true, "tskey-api-old", ""},
		{"api write fails", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"write:api-key": 1}, false, "tskey-api-old", ""},
		{"api readback fails", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"read:api-key": 2}, false, "tskey-api-old", ""},
		{"api metadata write fails", "tskey-api-old", "", loginCredentialModeAPIKey, "tskey-api-new", false, map[string]int{"write-meta:api-key": 1}, false, "tskey-api-old", ""},
		{"secret write fails", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", false, map[string]int{"write:client-secret": 1}, false, "", "tskey-client-old"},
		{"secret readback fails", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", false, map[string]int{"read:client-secret": 2}, false, "", "tskey-client-old"},
		{"secret metadata write fails", "", "tskey-client-old", loginCredentialModeClientSecret, "tskey-client-new", false, map[string]int{"write-meta:client-secret": 1}, false, "", "tskey-client-old"},
		{"retire delete old api fails", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", true, map[string]int{"delete:api-key": 1}, false, "tskey-api-old", ""},
		{"retire delete old secret fails", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", true, map[string]int{"delete:client-secret": 1}, false, "", "tskey-client-old"},
		{"retire verify old api inactive fails", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", true, map[string]int{"read:api-key": 2}, false, "tskey-api-old", ""},
		{"retire verify old secret inactive fails", "", "tskey-client-old", loginCredentialModeAPIKey, "tskey-api-new", true, map[string]int{"read:client-secret": 2}, false, "", "tskey-client-old"},
		{"retire delete old api meta fails", "tskey-api-old", "", loginCredentialModeClientSecret, "tskey-client-new", true, map[string]int{"delete-meta:api-key": 1}, false, "tskey-api-old", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newRecordingCredentialStore(tc.apiBefore, tc.csBefore)
			if tc.apiBefore != "" {
				store.withMeta(loginCredentialModeAPIKey, loginTestNow.Add(-time.Hour))
			}
			if tc.csBefore != "" {
				store.withMeta(loginCredentialModeClientSecret, loginTestNow.Add(-time.Hour))
			}
			store.failAt = tc.failAt
			withLoginVerifier(t, func(context.Context, string) error {
				if tc.verifyError {
					return fmt.Errorf("candidate verification failed")
				}
				return nil
			})

			_, err := commitLoginCredential(context.Background(), store, tc.mode, tc.candidate, loginReplaceOptions{RetireOther: tc.retire, Now: loginTestNow})
			if err == nil {
				t.Fatal("commitLoginCredential() error = nil, want failure")
			}
			store.assertState(t, tc.wantAPI, tc.wantCS)
			store.assertMetaMatchesValues(t)
			if store.values[tc.mode] == tc.candidate {
				t.Fatalf("failed candidate remained active after rollback; ops=%v", store.ops)
			}
			if meta := store.meta[tc.mode]; meta != nil && meta.Fingerprint == credentials.Fingerprint(tc.candidate) {
				t.Fatalf("failed candidate metadata remained after rollback; ops=%v", store.ops)
			}
			if strings.Contains(err.Error(), tc.candidate) {
				t.Fatal("error leaked candidate credential material")
			}
		})
	}
}

func TestLoginVerifyFailureCodesNonAuthErrorsAsLoginVerifyFailed(t *testing.T) {
	withLoginVerifier(t, func(context.Context, string) error { return fmt.Errorf("dial tcp: connection refused") })
	err := validateLoginCredentialCandidate(context.Background(), loginCredentialModeAPIKey, "tskey-api-FAKE-unreachable")
	code, ok := registry.ErrorCode(err)
	if !ok || code != registry.CodeLoginVerifyFailed {
		t.Fatalf("error = %v code=%q, want %s", err, code, registry.CodeLoginVerifyFailed)
	}
	var next interface{ NextCommands() []string }
	if !asNextCarrier(err, &next) || len(next.NextCommands()) == 0 || !strings.Contains(strings.Join(next.NextCommands(), "\n"), credentials.KeysPageURL) {
		t.Fatalf("next = %v, want key bootstrap steps", err)
	}
	if strings.Contains(err.Error(), "FAKE-unreachable") {
		t.Fatalf("error leaked candidate: %v", err)
	}

	// A coded 401 from the verifier passes through unchanged.
	withLoginVerifier(t, func(context.Context, string) error {
		return &registry.StableCodeError{Code: registry.CodeAPITokenUnauthorized, Err: fmt.Errorf("rejected")}
	})
	err = validateLoginCredentialCandidate(context.Background(), loginCredentialModeAPIKey, "tskey-api-FAKE-expired")
	if code, _ := registry.ErrorCode(err); code != registry.CodeAPITokenUnauthorized {
		t.Fatalf("coded verifier error was rewrapped: %v", err)
	}
}

func asNextCarrier(err error, target *interface{ NextCommands() []string }) bool {
	for err != nil {
		if carrier, ok := err.(interface{ NextCommands() []string }); ok {
			*target = carrier
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
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
	withLoginClock(t, loginTestNow)

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

			store := newRecordingCredentialStore("tskey-api-old", "").withMeta(loginCredentialModeAPIKey, loginTestNow)
			_, err := commitLoginCredential(context.Background(), store, loginCredentialModeClientSecret, "tskey-client-unusable", loginReplaceOptions{RetireOther: true, Now: loginTestNow})
			if err == nil {
				t.Fatal("commitLoginCredential() error = nil, want activation failure")
			}
			// The working API key must survive untouched; the candidate must not
			// be persisted.
			store.assertState(t, "tskey-api-old", "")
			store.assertMetaMatchesValues(t)
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
		store := newRecordingCredentialStore("", "tskey-client-old").withMeta(loginCredentialModeClientSecret, loginTestNow)
		if _, err := commitLoginCredential(context.Background(), store, loginCredentialModeAPIKey, "tskey-api-new", loginReplaceOptions{RetireOther: true, Now: loginTestNow}); err != nil {
			t.Fatalf("commitLoginCredential() error = %v", err)
		}
		if activatorCalled {
			t.Fatal("client-secret activation ran for an api-key switch")
		}
		store.assertState(t, "tskey-api-new", "")
		store.assertMetaMatchesValues(t)
	})

	t.Run("successful activation commits and retires previous api key only with retire-other", func(t *testing.T) {
		activatorCalled := false
		withLoginClientSecretActivator(t, func(context.Context, string) error {
			activatorCalled = true
			return nil
		})
		store := newRecordingCredentialStore("tskey-api-old", "").withMeta(loginCredentialModeAPIKey, loginTestNow)
		result, err := commitLoginCredential(context.Background(), store, loginCredentialModeClientSecret, "tskey-client-good", loginReplaceOptions{RetireOther: true, Now: loginTestNow})
		if err != nil {
			t.Fatalf("commitLoginCredential() error = %v", err)
		}
		if !activatorCalled {
			t.Fatal("client-secret activation was not invoked before commit")
		}
		// Only after a successful activation is the old API key retired.
		store.assertState(t, "", "tskey-client-good")
		store.assertMetaMatchesValues(t)
		if result.Retired != loginCredentialModeAPIKey {
			t.Fatalf("retired = %q, want api-key", result.Retired)
		}
	})
}
