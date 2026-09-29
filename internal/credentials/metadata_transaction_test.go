package credentials

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"
)

// RV-A: a status/doctor backfill that loaded credential-meta.json before a
// login wrote its metadata must not save its stale document after the login
// committed. The login side mirrors commitLoginCredentialValidated: write the
// value, then write verified metadata with a user-stated expiry, all inside
// one credential transaction.
func TestStatusBackfillCannotReplaceLoginMetadataCommittedInsideTransaction(t *testing.T) {
	setup(t)
	const oldKey, newKey = "tskey-api-FAKE-K1", "tskey-api-FAKE-K2"
	if err := SetAPIKey(oldKey); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordCredentialStored(SlotAPIKey, oldKey, StoredOptions{Now: metaTestNow}); err != nil {
		t.Fatal(err)
	}

	var metaPathCalls atomic.Int32
	oldPath := credentialMetaPathFunc
	credentialMetaPathFunc = func() (string, error) {
		metaPathCalls.Add(1)
		return oldPath()
	}
	statusAtSave := make(chan struct{})
	releaseStatus := make(chan struct{})
	var paused atomic.Bool
	oldWrite := metadataWriteFunc
	metadataWriteFunc = func(path string, data []byte) error {
		// Hold only the status backfill's write for the new key.
		if bytes.Contains(data, []byte(Fingerprint(newKey))) && bytes.Contains(data, []byte(`"expires_at_source": "assumed_max"`)) && paused.CompareAndSwap(false, true) {
			close(statusAtSave)
			<-releaseStatus
		}
		return oldWrite(path, data)
	}
	t.Cleanup(func() {
		credentialMetaPathFunc = oldPath
		metadataWriteFunc = oldWrite
	})

	userExpiry := metaTestNow.Add(30 * 24 * time.Hour)
	statusDone := make(chan Inventory, 1)
	statusStarted := metaPathCalls.Load()
	txErr := WithMutationTransaction(func(tx *MutationTransaction) error {
		if _, err := tx.SetAPIKeyWithBackend(newKey); err != nil {
			return err
		}
		go func() {
			values, _ := StoredSlotValues()
			statusDone <- DescribeSlots(values, metaTestNow, true)
		}()
		deadline := time.Now().Add(3 * time.Second)
		for metaPathCalls.Load() == statusStarted {
			if time.Now().After(deadline) {
				t.Error("status never loaded credential metadata")
				break
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case <-statusAtSave:
			t.Log("status backfill reached SaveMetadata while login holds the credential lock")
		case <-time.After(300 * time.Millisecond):
		}
		if len(statusDone) != 0 {
			t.Error("status finished before login wrote metadata; the interleaving was not exercised")
		}
		meta, err := NewSlotMetadata(SlotAPIKey, newKey, StoredOptions{Now: metaTestNow, ExpiresAt: &userExpiry, ExpiresAtSource: ExpirySourceUser, Verified: true})
		if err != nil {
			return err
		}
		return WriteSlotMetadataLocked(SlotAPIKey, meta)
	})
	close(releaseStatus)
	if txErr != nil {
		t.Fatalf("login transaction: %v", txErr)
	}
	var inventory Inventory
	select {
	case inventory = <-statusDone:
	case <-time.After(10 * time.Second):
		t.Fatal("status backfill did not finish")
	}

	got, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || got == nil {
		t.Fatalf("api-key metadata after status and login = %+v, %v", got, err)
	}
	if got.Fingerprint != Fingerprint(newKey) || got.ExpiresAtSource != ExpirySourceUser || got.LastVerifiedResult != VerifyResultOK {
		t.Fatalf("LOST UPDATE: login committed expires_at_source=user verified=ok; file now has fingerprint_is_new=%v expires_at_source=%q verified=%q",
			got.Fingerprint == Fingerprint(newKey), got.ExpiresAtSource, got.LastVerifiedResult)
	}
	// Status reports the record login committed, not its discarded backfill.
	if inventory.APIKey.Metadata == nil || inventory.APIKey.Metadata.ExpiresAtSource != ExpirySourceUser || len(inventory.Backfilled) != 0 || inventory.BackfillError != nil {
		t.Fatalf("status inventory = %+v backfilled=%v err=%v, want the committed login record", inventory.APIKey.Metadata, inventory.Backfilled, inventory.BackfillError)
	}
}

// A backfill whose slot record is unchanged under the lock is still saved.
func TestStatusBackfillStillPersistsWhenNoWriterIntervenes(t *testing.T) {
	setup(t)
	const key = "tskey-api-FAKE-quiet"
	if err := SetAPIKey(key); err != nil {
		t.Fatal(err)
	}
	inventory := DescribeSlots(SlotValues{APIKey: key}, metaTestNow, true)
	if len(inventory.Backfilled) != 1 || inventory.BackfillError != nil {
		t.Fatalf("inventory backfilled=%v err=%v, want one persisted backfill", inventory.Backfilled, inventory.BackfillError)
	}
	got, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || got == nil || got.Fingerprint != Fingerprint(key) || got.ExpiresAtSource != ExpirySourceAssumedMax {
		t.Fatalf("persisted backfill = %+v, %v", got, err)
	}
}
