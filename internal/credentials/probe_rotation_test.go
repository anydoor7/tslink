package credentials

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// RV-B: doctor --probe-remote reads the stored value, calls the API (up to
// 10 s), then records the verdict. A rotation that commits during the call
// must not receive the old credential's verdict.
func TestProbeVerdictIsNotRecordedOnCredentialRotatedDuringProbe(t *testing.T) {
	setup(t)
	const oldKey, newKey = "tskey-api-FAKE-OLD", "tskey-api-FAKE-NEW"
	if err := SetAPIKey(oldKey); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordCredentialStored(SlotAPIKey, oldKey, StoredOptions{Now: metaTestNow}); err != nil {
		t.Fatal(err)
	}
	rotated := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A login rotates the slot while the old token's request is in flight.
		rotated <- WithMutationTransaction(func(tx *MutationTransaction) error {
			if _, err := tx.SetAPIKeyWithBackend(newKey); err != nil {
				return err
			}
			meta, err := NewSlotMetadata(SlotAPIKey, newKey, StoredOptions{Now: metaTestNow.Add(time.Minute), Verified: true})
			if err != nil {
				return err
			}
			return WriteSlotMetadataLocked(SlotAPIKey, meta)
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"status":401,"message":"synthetic"}`))
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	withProbeClientFactory(t, baseURL, server.Client())

	outcome, err := ProbeStoredCredential(context.Background(), SlotAPIKey, metaTestNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("ProbeStoredCredential() error = %v", err)
	}
	if rotateErr := <-rotated; rotateErr != nil {
		t.Fatalf("rotation during probe: %v", rotateErr)
	}
	if outcome.Result != VerifyResultUnauthorized {
		t.Fatalf("probe result = %q, want the old token rejected", outcome.Result)
	}
	meta, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || meta == nil {
		t.Fatalf("metadata after probe = %+v, %v", meta, err)
	}
	if meta.Fingerprint != Fingerprint(newKey) {
		t.Fatalf("metadata fingerprint is not the rotated credential's")
	}
	if meta.LastVerifiedResult != VerifyResultOK {
		t.Fatalf("STALE VERDICT: 401 for the old token recorded on the new token's metadata (last_verified_result=%q)", meta.LastVerifiedResult)
	}
}

func TestRecordVerificationSkipsSlotHoldingAnotherFingerprint(t *testing.T) {
	setup(t)
	const key = "tskey-api-FAKE-current"
	if _, _, err := RecordCredentialStored(SlotAPIKey, key, StoredOptions{Now: metaTestNow, Verified: true}); err != nil {
		t.Fatal(err)
	}
	later := metaTestNow.Add(time.Hour)
	if err := RecordVerification(SlotAPIKey, Fingerprint("tskey-api-FAKE-other"), VerifyResultUnauthorized, later); err != nil {
		t.Fatalf("RecordVerification(other fingerprint) error = %v", err)
	}
	meta, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || meta == nil || meta.LastVerifiedResult != VerifyResultOK || !meta.LastVerifiedAt.Equal(metaTestNow) {
		t.Fatalf("verdict for another credential changed the record: %+v %v", meta, err)
	}
	// Control: the matching fingerprint is recorded.
	if err := RecordVerification(SlotAPIKey, Fingerprint(key), VerifyResultUnauthorized, later); err != nil {
		t.Fatal(err)
	}
	meta, err = ReadSlotMetadata(SlotAPIKey)
	if err != nil || meta == nil || meta.LastVerifiedResult != VerifyResultUnauthorized || !meta.LastVerifiedAt.Equal(later) {
		t.Fatalf("matching verdict not recorded: %+v %v", meta, err)
	}
}
