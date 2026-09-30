package credentials

import (
	"errors"
	"testing"
	"time"
)

// B6a-2 (audit X4-2). A status or doctor poll reads the stored credential,
// then loads credential-meta.json to decide whether to backfill. A login that
// commits between those two reads leaves both metadata reads describing its
// new credential, so comparing them cannot tell that the poll's value is the
// one the login replaced. The backfill is written only for a slot that, read
// back under the credential lock, still stores the value it describes.

// X4's reproduction: the login, with a user-stated expiry and a successful
// verification, finishes after status read the old value and before status
// loads the metadata.
func TestStatusBackfillOfAValueReadBeforeALoginKeepsTheLoginMetadata(t *testing.T) {
	setup(t)
	now := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	// Synthetic values only; none is printed.
	oldValue, newValue := "tskey-api-FAKE-before", "tskey-api-FAKE-after"
	if err := SetAPIKey(oldValue); err != nil {
		t.Fatal(err)
	}
	stale, err := StoredSlotValues()
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(30 * 24 * time.Hour)
	if err := WithMutationTransaction(func(tx *MutationTransaction) error {
		if _, err := tx.SetAPIKeyWithBackend(newValue); err != nil {
			return err
		}
		meta, err := NewSlotMetadata(SlotAPIKey, newValue, StoredOptions{Now: now, ExpiresAt: &expires, ExpiresAtSource: ExpirySourceUser, Verified: true})
		if err != nil {
			return err
		}
		return WriteSlotMetadataLocked(SlotAPIKey, meta)
	}); err != nil {
		t.Fatal(err)
	}

	inventory := DescribeSlots(stale, now, true)

	got, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || got == nil {
		t.Fatalf("api-key metadata = %+v, %v", got, err)
	}
	if got.Fingerprint != Fingerprint(newValue) || got.ExpiresAtSource != ExpirySourceUser || got.LastVerifiedResult != VerifyResultOK {
		t.Fatalf("stale status value overwrote the committed login metadata: fingerprint_is_new=%t expires_at_source=%q verified=%q",
			got.Fingerprint == Fingerprint(newValue), got.ExpiresAtSource, got.LastVerifiedResult)
	}
	if inventory.BackfillError != nil {
		t.Fatalf("BackfillError = %v, want a skipped backfill, not a failure", inventory.BackfillError)
	}
}

// The logout shape of the same window: status read a value, logout removed it,
// and the slot now stores nothing. The backfill must not recreate a record for
// the removed credential. The control runs the same poll without the logout
// and persists, so the refusal is the value check and not a broken backfill.
func TestStatusBackfillOfAValueReadBeforeALogoutWritesNoRecord(t *testing.T) {
	now := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		logout     bool
		wantRecord bool
	}{
		{name: "control: the value is still stored", logout: false, wantRecord: true},
		{name: "logout removed the value", logout: true, wantRecord: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			if err := SetAPIKey("tskey-api-FAKE-logout"); err != nil {
				t.Fatal(err)
			}
			stale, err := StoredSlotValues()
			if err != nil {
				t.Fatal(err)
			}
			if tc.logout {
				if err := DeleteAPIKeyChecked(); err != nil {
					t.Fatal(err)
				}
			}

			inventory := DescribeSlots(stale, now, true)

			got, err := ReadSlotMetadata(SlotAPIKey)
			if err != nil {
				t.Fatal(err)
			}
			if (got != nil) != tc.wantRecord {
				t.Fatalf("api-key record present = %t, want %t (%+v)", got != nil, tc.wantRecord, got)
			}
			if inventory.BackfillError != nil {
				t.Fatalf("BackfillError = %v", inventory.BackfillError)
			}
		})
	}
}

// A slot that cannot be read back under the lock is not backfilled, and the
// failure is reported instead of being mistaken for a matching value.
func TestStatusBackfillReportsASlotItCannotReadBack(t *testing.T) {
	setup(t)
	now := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := SetAPIKey("tskey-api-FAKE-unreadable"); err != nil {
		t.Fatal(err)
	}
	stale, err := StoredSlotValues()
	if err != nil {
		t.Fatal(err)
	}
	readFailure := errors.New("synthetic credential store read failure")
	oldEnabled, oldPath := keyringEnabledFunc, apiKeyPathFunc
	keyringEnabledFunc = func() bool { return false }
	apiKeyPathFunc = func() (string, error) { return "", readFailure }
	t.Cleanup(func() { keyringEnabledFunc, apiKeyPathFunc = oldEnabled, oldPath })

	inventory := DescribeSlots(stale, now, true)

	if !errors.Is(inventory.BackfillError, readFailure) {
		t.Fatalf("BackfillError = %v, want the read-back failure", inventory.BackfillError)
	}
	if got, err := ReadSlotMetadata(SlotAPIKey); err != nil || got != nil {
		t.Fatalf("api-key record = %+v, %v; want none written for a value that could not be confirmed", got, err)
	}
}
