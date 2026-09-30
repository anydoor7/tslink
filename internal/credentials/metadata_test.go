package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

var metaTestNow = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

func metaPath(t *testing.T) string {
	t.Helper()
	path, err := credentialMetaPathFunc()
	if err != nil {
		t.Fatalf("credentialMetaPathFunc() error = %v", err)
	}
	return path
}

func readMetaFile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(metaPath(t))
	if err != nil {
		t.Fatalf("ReadFile(credential-meta.json) error = %v", err)
	}
	return string(data)
}

func TestFingerprintIsShortStableAndValueFree(t *testing.T) {
	const value = "tskey-api-FAKE-fingerprint-input"
	fp := Fingerprint(value)
	if len(fp) != fingerprintHexLength {
		t.Fatalf("Fingerprint() = %q, want %d hex chars", fp, fingerprintHexLength)
	}
	if fp != Fingerprint("  "+value+"\n") {
		t.Fatal("Fingerprint() must trim surrounding whitespace")
	}
	if fp == Fingerprint(value+"x") {
		t.Fatal("Fingerprint() collided for different values")
	}
	if strings.Contains(value, fp) || strings.Contains(fp, "tskey") {
		t.Fatalf("Fingerprint() %q leaks value material", fp)
	}
	if Fingerprint("   ") != "" {
		t.Fatal("Fingerprint(blank) must be empty")
	}
}

func TestNewSlotMetadataDefaultsAndSources(t *testing.T) {
	apiMeta, err := NewSlotMetadata(SlotAPIKey, "tskey-api-FAKE", StoredOptions{Now: metaTestNow, Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	if apiMeta.Kind != KindAPIAccessToken || apiMeta.Fingerprint != Fingerprint("tskey-api-FAKE") || !apiMeta.StoredAt.Equal(metaTestNow) {
		t.Fatalf("api metadata = %+v", apiMeta)
	}
	if apiMeta.ExpiresAt == nil || !apiMeta.ExpiresAt.Equal(metaTestNow.Add(APIKeyAssumedMaxLifetime)) || apiMeta.ExpiresAtSource != ExpirySourceAssumedMax {
		t.Fatalf("api metadata expiry = %+v, want assumed max", apiMeta)
	}
	if apiMeta.LastVerifiedAt == nil || apiMeta.LastVerifiedResult != VerifyResultOK {
		t.Fatalf("api metadata verification = %+v, want ok now", apiMeta)
	}

	explicit := metaTestNow.Add(30 * 24 * time.Hour)
	userMeta, err := NewSlotMetadata(SlotAPIKey, "tskey-api-FAKE", StoredOptions{Now: metaTestNow, ExpiresAt: &explicit})
	if err != nil {
		t.Fatal(err)
	}
	if userMeta.ExpiresAt == nil || !userMeta.ExpiresAt.Equal(explicit) || userMeta.ExpiresAtSource != ExpirySourceUser {
		t.Fatalf("user expiry = %+v, want %s source user", userMeta, explicit)
	}
	if userMeta.LastVerifiedAt != nil {
		t.Fatal("unverified store must not record a verification")
	}

	secretMeta, err := NewSlotMetadata(SlotClientSecret, "tskey-client-FAKE", StoredOptions{Now: metaTestNow, ExpiresAt: &explicit})
	if err != nil {
		t.Fatal(err)
	}
	if secretMeta.Kind != KindOAuthClientSecret || secretMeta.ExpiresAt != nil || secretMeta.ExpiresAtSource != "" {
		t.Fatalf("client-secret metadata = %+v, want no expiry regardless of options", secretMeta)
	}

	if _, err := NewSlotMetadata("bogus", "x", StoredOptions{Now: metaTestNow}); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("unknown slot error = %v", err)
	}
	if _, err := NewSlotMetadata(SlotAPIKey, "x", StoredOptions{}); err == nil {
		t.Fatal("zero timestamp must be rejected")
	}
}

func TestMetadataRoundTripNeverStoresValue(t *testing.T) {
	setup(t)
	const value = "tskey-api-FAKE-roundtrip-value-9f3a"
	meta, previous, err := RecordCredentialStored(SlotAPIKey, value, StoredOptions{Now: metaTestNow, Verified: true})
	if err != nil {
		t.Fatalf("RecordCredentialStored() error = %v", err)
	}
	if previous != nil {
		t.Fatalf("previous = %+v, want nil on first store", previous)
	}
	raw := readMetaFile(t)
	if strings.Contains(raw, value) || strings.Contains(raw, "roundtrip-value") {
		t.Fatalf("credential-meta.json leaks the credential value:\n%s", raw)
	}
	if !strings.Contains(raw, meta.Fingerprint) || !strings.Contains(raw, `"schema_version": 1`) {
		t.Fatalf("credential-meta.json missing fingerprint/schema:\n%s", raw)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(metaPath(t))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential-meta.json mode = %o, want 0600", info.Mode().Perm())
		}
	}

	loaded, err := LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata() error = %v", err)
	}
	got, ok := loaded.Slots[SlotAPIKey]
	if !ok || got.Fingerprint != meta.Fingerprint || !got.StoredAt.Equal(meta.StoredAt) || got.ExpiresAt == nil || !got.ExpiresAt.Equal(*meta.ExpiresAt) {
		t.Fatalf("loaded = %+v, want %+v", got, meta)
	}

	// Same slot again: rotation returns the previous record.
	rotated, previous, err := RecordCredentialStored(SlotAPIKey, value+"-new", StoredOptions{Now: metaTestNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if previous == nil || previous.Fingerprint != meta.Fingerprint || rotated.Fingerprint == meta.Fingerprint {
		t.Fatalf("rotation previous=%+v rotated=%+v", previous, rotated)
	}
}

func TestLoadMetadataMissingIsEmptyNotError(t *testing.T) {
	setup(t)
	doc, err := LoadMetadata()
	if err != nil || len(doc.Slots) != 0 || doc.SchemaVersion != MetadataSchemaVersion {
		t.Fatalf("LoadMetadata() = %+v, %v, want empty document", doc, err)
	}
	meta, err := ReadSlotMetadata(SlotAPIKey)
	if err != nil || meta != nil {
		t.Fatalf("ReadSlotMetadata() = %+v, %v, want nil, nil", meta, err)
	}
}

func TestLoadMetadataCorruptVariants(t *testing.T) {
	cases := map[string]string{
		"not json":       "{not json",
		"wrong schema":   `{"schema_version":99,"slots":{}}`,
		"unknown slot":   `{"schema_version":1,"slots":{"bogus":{}}}`,
		"array document": `[]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			setup(t)
			if err := os.WriteFile(metaPath(t), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadMetadata()
			if !errors.Is(err, ErrMetadataCorrupt) {
				t.Fatalf("LoadMetadata() error = %v, want ErrMetadataCorrupt", err)
			}
			// Corrupt metadata classifies present slots as unknown and never blocks.
			inv := DescribeSlots(SlotValues{APIKey: "tskey-api-FAKE"}, metaTestNow, true)
			if inv.MetadataError == nil || inv.APIKey.ExpiryState != ExpiryStateUnknown || inv.ExpiryState != ExpiryStateUnknown {
				t.Fatalf("inventory = %+v, want unknown with metadata error", inv)
			}
			// A commit self-heals the corrupt file.
			if _, _, err := RecordCredentialStored(SlotAPIKey, "tskey-api-FAKE", StoredOptions{Now: metaTestNow}); err != nil {
				t.Fatalf("RecordCredentialStored() over corrupt file error = %v", err)
			}
			if _, err := LoadMetadata(); err != nil {
				t.Fatalf("LoadMetadata() after self-heal error = %v", err)
			}
		})
	}
}

func TestLoadMetadataRepairsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	setup(t)
	if _, _, err := RecordCredentialStored(SlotClientSecret, "tskey-client-FAKE", StoredOptions{Now: metaTestNow}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(metaPath(t), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMetadata(); err != nil {
		t.Fatalf("LoadMetadata() error = %v", err)
	}
	info, err := os.Stat(metaPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode after load = %o, want repaired 0600", info.Mode().Perm())
	}
}

func TestDeleteSlotMetadataAndRemoveFile(t *testing.T) {
	setup(t)
	for slot, value := range map[string]string{SlotAPIKey: "tskey-api-FAKE", SlotClientSecret: "tskey-client-FAKE"} {
		if _, _, err := RecordCredentialStored(slot, value, StoredOptions{Now: metaTestNow}); err != nil {
			t.Fatal(err)
		}
	}
	if err := DeleteSlotMetadata(SlotAPIKey); err != nil {
		t.Fatal(err)
	}
	doc, err := LoadMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if _, stale := doc.Slots[SlotAPIKey]; stale || len(doc.Slots) != 1 {
		t.Fatalf("slots after delete = %v, want only client-secret", doc.Slots)
	}
	// Deleting the last slot removes the file.
	if err := DeleteSlotMetadata(SlotClientSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath(t)); !os.IsNotExist(err) {
		t.Fatalf("file after deleting last slot: stat err = %v, want not exist", err)
	}
	// Deleting an absent slot and removing an absent file are no-ops.
	if err := DeleteSlotMetadata(SlotAPIKey); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMetadataFile(); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSlotMetadata("bogus"); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("DeleteSlotMetadata(bogus) error = %v", err)
	}
	// A corrupt file is removed wholesale on delete.
	if err := os.WriteFile(metaPath(t), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSlotMetadata(SlotAPIKey); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(metaPath(t)); !os.IsNotExist(err) {
		t.Fatalf("corrupt file survived delete: %v", err)
	}
}

func TestRecordVerificationOnlyTouchesKnownSlots(t *testing.T) {
	setup(t)
	if err := RecordVerification(SlotAPIKey, Fingerprint("tskey-api-FAKE"), VerifyResultOK, metaTestNow); err != nil {
		t.Fatalf("RecordVerification() without metadata error = %v", err)
	}
	if _, err := os.Stat(metaPath(t)); !os.IsNotExist(err) {
		t.Fatal("verification must not invent a metadata record")
	}
	if _, _, err := RecordCredentialStored(SlotAPIKey, "tskey-api-FAKE", StoredOptions{Now: metaTestNow}); err != nil {
		t.Fatal(err)
	}
	later := metaTestNow.Add(2 * time.Hour)
	for _, result := range []string{VerifyResultOK, VerifyResultUnauthorized, VerifyResultForbidden, VerifyResultUnreachable} {
		if err := RecordVerification(SlotAPIKey, Fingerprint("tskey-api-FAKE"), result, later); err != nil {
			t.Fatalf("RecordVerification(%s) error = %v", result, err)
		}
		meta, err := ReadSlotMetadata(SlotAPIKey)
		if err != nil || meta == nil || meta.LastVerifiedAt == nil || !meta.LastVerifiedAt.Equal(later) || meta.LastVerifiedResult != result {
			t.Fatalf("after %s: meta=%+v err=%v", result, meta, err)
		}
	}
	if err := RecordVerification(SlotAPIKey, Fingerprint("tskey-api-FAKE"), "maybe", later); err == nil {
		t.Fatal("unknown verification result must be rejected")
	}
	if err := RecordVerification("bogus", Fingerprint("tskey-api-FAKE"), VerifyResultOK, later); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("RecordVerification(bogus) error = %v", err)
	}
}

func TestExpiryStateThresholds(t *testing.T) {
	expiresAt := metaTestNow.Add(90 * 24 * time.Hour)
	meta := &SlotMetadata{ExpiresAt: &expiresAt}
	cases := []struct {
		name     string
		now      time.Time
		want     string
		wantDays int
	}{
		{"-1 day (expired yesterday)", expiresAt.Add(24 * time.Hour), ExpiryStateExpired, -1},
		{"exactly at expiry", expiresAt, ExpiryStateExpired, 0},
		{"12 hours past", expiresAt.Add(12 * time.Hour), ExpiryStateExpired, -1},
		{"0 days (1 hour left)", expiresAt.Add(-time.Hour), ExpiryStateExpiring, 0},
		{"7 days left", expiresAt.Add(-7 * 24 * time.Hour), ExpiryStateExpiring, 7},
		{"13 days left", expiresAt.Add(-13 * 24 * time.Hour), ExpiryStateExpiring, 13},
		{"14 days left (threshold inclusive)", expiresAt.Add(-14 * 24 * time.Hour), ExpiryStateExpiring, 14},
		{"14 days plus a minute", expiresAt.Add(-14*24*time.Hour - time.Minute), ExpiryStateOK, 14},
		{"30 days left", expiresAt.Add(-30 * 24 * time.Hour), ExpiryStateOK, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, days := ExpiryState(meta, true, tc.now)
			if state != tc.want || days == nil || *days != tc.wantDays {
				got := "nil"
				if days != nil {
					got = strconv.Itoa(*days)
				}
				t.Fatalf("ExpiryState() = %s/%s, want %s/%d", state, got, tc.want, tc.wantDays)
			}
		})
	}
	if state, days := ExpiryState(meta, false, metaTestNow); state != ExpiryStateNone || days != nil {
		t.Fatalf("absent slot = %s/%v, want none", state, days)
	}
	if state, days := ExpiryState(nil, true, metaTestNow); state != ExpiryStateUnknown || days != nil {
		t.Fatalf("present without metadata = %s/%v, want unknown", state, days)
	}
	if state, days := ExpiryState(&SlotMetadata{}, true, metaTestNow); state != ExpiryStateOK || days != nil {
		t.Fatalf("no expiry = %s/%v, want ok", state, days)
	}
}

func TestWorstExpiryStateOrdering(t *testing.T) {
	cases := []struct {
		states []string
		want   string
	}{
		{nil, ExpiryStateNone},
		{[]string{ExpiryStateNone, ExpiryStateOK}, ExpiryStateOK},
		{[]string{ExpiryStateOK, ExpiryStateUnknown}, ExpiryStateUnknown},
		{[]string{ExpiryStateUnknown, ExpiryStateExpiring}, ExpiryStateExpiring},
		{[]string{ExpiryStateExpiring, ExpiryStateExpired, ExpiryStateOK}, ExpiryStateExpired},
	}
	for _, tc := range cases {
		if got := WorstExpiryState(tc.states...); got != tc.want {
			t.Fatalf("WorstExpiryState(%v) = %s, want %s", tc.states, got, tc.want)
		}
	}
}

func TestDescribeSlotsNoCredentialsTouchesNoDisk(t *testing.T) {
	setup(t)
	old := credentialMetaPathFunc
	t.Cleanup(func() { credentialMetaPathFunc = old })
	credentialMetaPathFunc = func() (string, error) {
		t.Fatal("metadata path resolved although no credential is present")
		return "", nil
	}
	inv := DescribeSlots(SlotValues{}, metaTestNow, true)
	if inv.ExpiryState != ExpiryStateNone || inv.APIKey.Present || inv.ClientSecret.Present || inv.APIKey.ExpiryState != ExpiryStateNone {
		t.Fatalf("inventory = %+v, want none", inv)
	}
}

func TestDescribeSlotsBackfillPersistsOnceAndReanchorsOnFingerprintChange(t *testing.T) {
	setup(t)
	values := SlotValues{APIKey: "tskey-api-FAKE-backfill", ClientSecret: "tskey-client-FAKE-backfill"}
	// A backfill is persisted only for the value a slot stores (B6a-2), so
	// the fixture stores the values it describes, as status and doctor read
	// them from the store.
	if err := SetAPIKey(values.APIKey); err != nil {
		t.Fatal(err)
	}
	if err := SaveClientSecret(values.ClientSecret); err != nil {
		t.Fatal(err)
	}

	// Persist=false classifies but leaves the disk alone.
	preview := DescribeSlots(values, metaTestNow, false)
	if len(preview.Backfilled) != 2 || !preview.APIKey.Backfilled || !preview.ClientSecret.Backfilled {
		t.Fatalf("preview backfill = %+v", preview)
	}
	if _, err := os.Stat(metaPath(t)); !os.IsNotExist(err) {
		t.Fatal("persist=false wrote metadata")
	}

	first := DescribeSlots(values, metaTestNow, true)
	if len(first.Backfilled) != 2 || first.BackfillError != nil {
		t.Fatalf("first backfill = %+v", first)
	}
	if first.APIKey.Metadata == nil || first.APIKey.Metadata.ExpiresAtSource != ExpirySourceAssumedMax || !first.APIKey.Metadata.StoredAt.Equal(metaTestNow) || first.APIKey.Metadata.LastVerifiedAt != nil {
		t.Fatalf("api backfill metadata = %+v, want stored_at=now assumed_max unverified", first.APIKey.Metadata)
	}
	if first.ClientSecret.Metadata == nil || first.ClientSecret.Metadata.ExpiresAt != nil || first.ClientSecret.ExpiryState != ExpiryStateOK {
		t.Fatalf("client-secret backfill = %+v", first.ClientSecret)
	}
	if first.APIKey.ExpiryState != ExpiryStateOK || first.ExpiryState != ExpiryStateOK {
		t.Fatalf("states = %s/%s, want ok", first.APIKey.ExpiryState, first.ExpiryState)
	}
	raw := readMetaFile(t)
	if strings.Contains(raw, "FAKE-backfill") {
		t.Fatalf("backfilled metadata leaks value:\n%s", raw)
	}

	// Second call a day later: nothing to backfill, stored_at is unchanged.
	second := DescribeSlots(values, metaTestNow.Add(24*time.Hour), true)
	if len(second.Backfilled) != 0 || second.APIKey.Backfilled || !second.APIKey.Metadata.StoredAt.Equal(metaTestNow) {
		t.Fatalf("second inventory re-backfilled: %+v", second)
	}
	if second.APIKey.DaysLeft == nil || *second.APIKey.DaysLeft != 89 {
		t.Fatalf("days_left = %v, want 89", second.APIKey.DaysLeft)
	}

	// Rotating the value outside login (fingerprint mismatch) re-anchors.
	if err := SetAPIKey("tskey-api-FAKE-rotated"); err != nil {
		t.Fatal(err)
	}
	rotated := DescribeSlots(SlotValues{APIKey: "tskey-api-FAKE-rotated", ClientSecret: values.ClientSecret}, metaTestNow.Add(48*time.Hour), true)
	if len(rotated.Backfilled) != 1 || rotated.Backfilled[0] != SlotAPIKey || !rotated.APIKey.Metadata.StoredAt.Equal(metaTestNow.Add(48*time.Hour)) {
		t.Fatalf("rotated inventory = %+v, want api-key re-anchored", rotated)
	}
	if rotated.ClientSecret.Backfilled {
		t.Fatal("unchanged client-secret slot was re-anchored")
	}
}

func TestDescribeSlotsExpiryMatrix(t *testing.T) {
	setup(t)
	const key = "tskey-api-FAKE-matrix"
	storedAt := metaTestNow.Add(-80 * 24 * time.Hour) // assumed max => 10 days left
	if _, _, err := RecordCredentialStored(SlotAPIKey, key, StoredOptions{Now: storedAt}); err != nil {
		t.Fatal(err)
	}
	inv := DescribeSlots(SlotValues{APIKey: key}, metaTestNow, true)
	if inv.APIKey.ExpiryState != ExpiryStateExpiring || inv.ExpiryState != ExpiryStateExpiring || inv.APIKey.DaysLeft == nil || *inv.APIKey.DaysLeft != 10 {
		t.Fatalf("expiring inventory = %+v", inv.APIKey)
	}
	if len(inv.Backfilled) != 0 {
		t.Fatalf("existing metadata was backfilled: %v", inv.Backfilled)
	}
	expired := DescribeSlots(SlotValues{APIKey: key}, metaTestNow.Add(11*24*time.Hour), true)
	if expired.APIKey.ExpiryState != ExpiryStateExpired || expired.ExpiryState != ExpiryStateExpired || *expired.APIKey.DaysLeft != -1 {
		t.Fatalf("expired inventory = %+v", expired.APIKey)
	}
	// client-secret present alongside: worst state still wins.
	both := DescribeSlots(SlotValues{APIKey: key, ClientSecret: "tskey-client-FAKE"}, metaTestNow.Add(11*24*time.Hour), true)
	if both.ClientSecret.ExpiryState != ExpiryStateOK || both.ExpiryState != ExpiryStateExpired {
		t.Fatalf("mixed inventory = api=%s client=%s worst=%s", both.APIKey.ExpiryState, both.ClientSecret.ExpiryState, both.ExpiryState)
	}
}

func TestDescribeSlotsBackfillWriteFailureIsReportedNotFatal(t *testing.T) {
	setup(t)
	// The slot stores the value described, so the backfill reaches the write.
	if err := SetAPIKey("tskey-api-FAKE"); err != nil {
		t.Fatal(err)
	}
	old := metadataWriteFunc
	t.Cleanup(func() { metadataWriteFunc = old })
	metadataWriteFunc = func(string, []byte) error { return errors.New("disk full") }
	inv := DescribeSlots(SlotValues{APIKey: "tskey-api-FAKE"}, metaTestNow, true)
	if inv.BackfillError == nil || inv.APIKey.ExpiryState != ExpiryStateOK || len(inv.Backfilled) != 1 {
		t.Fatalf("inventory = %+v, want classified with backfill error", inv)
	}
}

func TestBackfillMetadataReadsStoredSlots(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-FAKE-stored"); err != nil {
		t.Fatal(err)
	}
	backfilled, err := BackfillMetadata(metaTestNow)
	if err != nil || len(backfilled) != 1 || backfilled[0] != SlotAPIKey {
		t.Fatalf("BackfillMetadata() = %v, %v", backfilled, err)
	}
	again, err := BackfillMetadata(metaTestNow.Add(time.Hour))
	if err != nil || len(again) != 0 {
		t.Fatalf("second BackfillMetadata() = %v, %v, want nothing", again, err)
	}
	raw := readMetaFile(t)
	if strings.Contains(raw, "FAKE-stored") {
		t.Fatalf("metadata leaks value:\n%s", raw)
	}
}

func TestDeleteStoredCredentialKindStrictRemovesOnlyThatSlot(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-FAKE"); err != nil {
		t.Fatal(err)
	}
	if err := SaveClientSecret("tskey-client-FAKE"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteStoredCredentialKindStrict(SlotAPIKey); err != nil {
		t.Fatalf("DeleteStoredCredentialKindStrict(api-key) error = %v", err)
	}
	if key, _ := GetAPIKey(); key != "" {
		t.Fatal("api key survived selective delete")
	}
	if secret, _ := GetClientSecret(); secret != "tskey-client-FAKE" {
		t.Fatalf("client secret = %q, want untouched", secret)
	}
	if err := DeleteStoredCredentialKindStrict("bogus"); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("DeleteStoredCredentialKindStrict(bogus) error = %v", err)
	}
}

func TestMetadataPathResolutionErrorPropagates(t *testing.T) {
	setup(t)
	old := credentialMetaPathFunc
	t.Cleanup(func() { credentialMetaPathFunc = old })
	credentialMetaPathFunc = func() (string, error) { return "", errors.New("no config dir") }
	if _, err := LoadMetadata(); err == nil || errors.Is(err, ErrMetadataCorrupt) {
		t.Fatalf("LoadMetadata() error = %v, want raw path error", err)
	}
	if err := SaveMetadata(Metadata{Slots: map[string]SlotMetadata{SlotAPIKey: {}}}); err == nil {
		t.Fatal("SaveMetadata() error = nil, want path error")
	}
	if err := RemoveMetadataFile(); err == nil {
		t.Fatal("RemoveMetadataFile() error = nil, want path error")
	}
	if _, _, err := RecordCredentialStored(SlotAPIKey, "tskey-api-FAKE", StoredOptions{Now: metaTestNow}); err == nil {
		t.Fatal("RecordCredentialStored() error = nil, want path error")
	}
	if err := WriteSlotMetadata(SlotAPIKey, SlotMetadata{}); err == nil {
		t.Fatal("WriteSlotMetadata() error = nil, want path error")
	}
	if _, err := ReadSlotMetadata(SlotAPIKey); err == nil {
		t.Fatal("ReadSlotMetadata() error = nil, want path error")
	}
	if err := DeleteSlotMetadata(SlotAPIKey); err == nil {
		t.Fatal("DeleteSlotMetadata() error = nil, want path error")
	}
	if err := RecordVerification(SlotAPIKey, Fingerprint("tskey-api-FAKE"), VerifyResultOK, metaTestNow); err == nil {
		t.Fatal("RecordVerification() error = nil, want path error")
	}
	if err := WriteSlotMetadata("bogus", SlotMetadata{}); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("WriteSlotMetadata(bogus) error = %v", err)
	}
	if _, err := ReadSlotMetadata("bogus"); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("ReadSlotMetadata(bogus) error = %v", err)
	}
}

func TestSlotKindAndValidSlot(t *testing.T) {
	if SlotKind(SlotAPIKey) != KindAPIAccessToken || SlotKind(SlotClientSecret) != KindOAuthClientSecret || SlotKind("x") != "" {
		t.Fatal("SlotKind mapping is wrong")
	}
	if !ValidSlot(SlotAPIKey) || !ValidSlot(SlotClientSecret) || ValidSlot("apikey") {
		t.Fatal("ValidSlot mapping is wrong")
	}
	if _, err := os.Stat(filepath.Dir(t.TempDir())); err != nil {
		t.Fatal(err)
	}
}
