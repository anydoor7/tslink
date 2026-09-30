package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/atomicfile"
	"github.com/monody0007/tslink/internal/config"
)

// Value-free credential metadata. The file records what TSLink knows about each
// stored credential without ever holding the credential itself: a short sha256
// fingerprint, when it was stored, when it is expected to expire, and the last
// remote verification result. A missing or corrupt file never blocks a command;
// readers report the affected slot as unknown and doctor surfaces a warning.
const (
	MetadataSchemaVersion = 1

	SlotAPIKey       = "api-key"
	SlotClientSecret = "client-secret"

	KindAPIAccessToken    = "api_access_token"
	KindOAuthClientSecret = "oauth_client_secret"

	ExpirySourceUser       = "user"
	ExpirySourceAssumedMax = "assumed_max"

	VerifyResultOK           = "ok"
	VerifyResultUnauthorized = "unauthorized"
	VerifyResultForbidden    = "forbidden"
	VerifyResultUnreachable  = "unreachable"

	ExpiryStateNone     = "none"
	ExpiryStateOK       = "ok"
	ExpiryStateExpiring = "expiring"
	ExpiryStateExpired  = "expired"
	ExpiryStateUnknown  = "unknown"

	// APIKeyAssumedMaxLifetime is Tailscale's documented upper bound for a
	// user-owned access token. When the operator does not state the real expiry
	// at login time, TSLink assumes this maximum and records the assumption as
	// expires_at_source=assumed_max so consumers can tell it apart from fact.
	APIKeyAssumedMaxLifetime = 90 * 24 * time.Hour

	// ExpiringSoonThreshold is the window before expires_at in which status and
	// doctor report the api-key slot as expiring rather than ok.
	ExpiringSoonThreshold = 14 * 24 * time.Hour

	fingerprintHexLength = 8
)

// ErrMetadataCorrupt reports that credential-meta.json exists but cannot be
// trusted. Callers treat the affected slots as unknown and continue.
var ErrMetadataCorrupt = errors.New("credential metadata file is unreadable or malformed")

// ErrUnknownCredentialSlot rejects slot names outside the two supported slots.
var ErrUnknownCredentialSlot = errors.New("unknown credential slot")

// Testable seams for the metadata file.
var (
	credentialMetaPathFunc = config.CredentialMetaPath
	metadataWriteFunc      = atomicfile.WriteFile
)

// SlotMetadata is the per-slot bookkeeping record. It never carries the
// credential value.
type SlotMetadata struct {
	Kind               string     `json:"kind"`
	Fingerprint        string     `json:"fingerprint"`
	StoredAt           time.Time  `json:"stored_at"`
	ExpiresAt          *time.Time `json:"expires_at"`
	ExpiresAtSource    string     `json:"expires_at_source,omitempty"`
	LastVerifiedAt     *time.Time `json:"last_verified_at"`
	LastVerifiedResult string     `json:"last_verified_result,omitempty"`
}

// Metadata is the on-disk document.
type Metadata struct {
	SchemaVersion int                     `json:"schema_version"`
	Slots         map[string]SlotMetadata `json:"slots"`
}

// StoredOptions describes what login learned about a freshly committed
// credential.
type StoredOptions struct {
	Now             time.Time
	ExpiresAt       *time.Time
	ExpiresAtSource string
	// Verified records that the login path proved the credential against the
	// remote API before committing it.
	Verified bool
}

// SlotValues carries the currently stored values only long enough to compute
// presence and fingerprints. Callers must not persist or print it.
type SlotValues struct {
	APIKey       string
	ClientSecret string
}

// SlotView is the value-free report for one slot.
type SlotView struct {
	Slot        string
	Present     bool
	Metadata    *SlotMetadata
	Backfilled  bool
	ExpiryState string
	DaysLeft    *int
}

// Inventory is the value-free report for both slots plus metadata health.
type Inventory struct {
	APIKey       SlotView
	ClientSecret SlotView
	// MetadataError is non-nil when credential-meta.json exists but is
	// unreadable or malformed; present slots then report unknown.
	MetadataError error
	// Backfilled lists slots whose metadata was created or re-anchored during
	// this call because a credential existed without matching bookkeeping.
	Backfilled []string
	// BackfillError is non-nil when a backfill could not be persisted.
	BackfillError error
	// ExpiryState is the worst per-slot state.
	ExpiryState string
}

// Fingerprint returns the first eight hex characters of sha256(value). It is
// safe to print: it identifies a credential without revealing it.
func Fingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:fingerprintHexLength]
}

// ValidSlot reports whether slot names a supported credential slot.
func ValidSlot(slot string) bool {
	return slot == SlotAPIKey || slot == SlotClientSecret
}

// SlotKind maps a slot to its credential kind label.
func SlotKind(slot string) string {
	switch slot {
	case SlotAPIKey:
		return KindAPIAccessToken
	case SlotClientSecret:
		return KindOAuthClientSecret
	default:
		return ""
	}
}

func emptyMetadata() Metadata {
	return Metadata{SchemaVersion: MetadataSchemaVersion, Slots: map[string]SlotMetadata{}}
}

// LoadMetadata reads credential-meta.json. A missing file yields an empty
// document and no error. A malformed or unreadable file yields ErrMetadataCorrupt.
func LoadMetadata() (Metadata, error) {
	path, err := credentialMetaPathFunc()
	if err != nil {
		return emptyMetadata(), err
	}
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return emptyMetadata(), fmt.Errorf("%w: %v", ErrMetadataCorrupt, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return emptyMetadata(), nil
		}
		return emptyMetadata(), fmt.Errorf("%w: %v", ErrMetadataCorrupt, err)
	}
	var doc Metadata
	if err := json.Unmarshal(data, &doc); err != nil {
		return emptyMetadata(), fmt.Errorf("%w: %v", ErrMetadataCorrupt, err)
	}
	if doc.SchemaVersion != MetadataSchemaVersion {
		return emptyMetadata(), fmt.Errorf("%w: unsupported schema_version %d", ErrMetadataCorrupt, doc.SchemaVersion)
	}
	if doc.Slots == nil {
		doc.Slots = map[string]SlotMetadata{}
	}
	for slot := range doc.Slots {
		if !ValidSlot(slot) {
			return emptyMetadata(), fmt.Errorf("%w: unknown slot %q", ErrMetadataCorrupt, slot)
		}
	}
	return doc, nil
}

// SaveMetadata atomically writes credential-meta.json with owner-only
// permissions. An empty slot map removes the file instead.
func SaveMetadata(doc Metadata) error {
	path, err := credentialMetaPathFunc()
	if err != nil {
		return err
	}
	if len(doc.Slots) == 0 {
		return removeMetadataPath(path)
	}
	doc.SchemaVersion = MetadataSchemaVersion
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode credential metadata: %w", err)
	}
	data = append(data, '\n')
	return metadataWriteFunc(path, data)
}

// RemoveMetadataFile deletes credential-meta.json under the credential
// mutation lock. Missing files are not errors.
func RemoveMetadataFile() error {
	return withCredentialMutationLock(RemoveMetadataFileLocked)
}

// RemoveMetadataFileLocked is RemoveMetadataFile for a caller that already
// holds the credential mutation lock inside WithMutationTransaction.
func RemoveMetadataFileLocked() error {
	path, err := credentialMetaPathFunc()
	if err != nil {
		return err
	}
	return removeMetadataPath(path)
}

func removeMetadataPath(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// NewSlotMetadata builds the bookkeeping record for a credential that was just
// stored. The api-key slot defaults to the assumed 90-day maximum when the
// operator gives no expiry; the client-secret slot never expires.
func NewSlotMetadata(slot, value string, opts StoredOptions) (SlotMetadata, error) {
	if !ValidSlot(slot) {
		return SlotMetadata{}, fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	now := opts.Now.UTC()
	if now.IsZero() {
		return SlotMetadata{}, errors.New("credential metadata requires an explicit timestamp")
	}
	meta := SlotMetadata{
		Kind:        SlotKind(slot),
		Fingerprint: Fingerprint(value),
		StoredAt:    now,
	}
	if slot == SlotAPIKey {
		switch {
		case opts.ExpiresAt != nil:
			expires := opts.ExpiresAt.UTC()
			meta.ExpiresAt = &expires
			meta.ExpiresAtSource = opts.ExpiresAtSource
			if meta.ExpiresAtSource == "" {
				meta.ExpiresAtSource = ExpirySourceUser
			}
		default:
			expires := now.Add(APIKeyAssumedMaxLifetime)
			meta.ExpiresAt = &expires
			meta.ExpiresAtSource = ExpirySourceAssumedMax
		}
	}
	if opts.Verified {
		verified := now
		meta.LastVerifiedAt = &verified
		meta.LastVerifiedResult = VerifyResultOK
	}
	return meta, nil
}

// RecordCredentialStored writes the slot record after a credential commit and
// returns the previous record when one existed. A corrupt metadata file is
// replaced: the new commit is the only fact TSLink can still vouch for. The
// read-modify-write runs under the credential mutation lock.
func RecordCredentialStored(slot, value string, opts StoredOptions) (SlotMetadata, *SlotMetadata, error) {
	meta, err := NewSlotMetadata(slot, value, opts)
	if err != nil {
		return SlotMetadata{}, nil, err
	}
	var previous *SlotMetadata
	err = withCredentialMutationLock(func() error {
		doc, loadErr := LoadMetadata()
		if loadErr != nil && !errors.Is(loadErr, ErrMetadataCorrupt) {
			return loadErr
		}
		if existing, ok := doc.Slots[slot]; ok {
			copied := existing
			previous = &copied
		}
		doc.Slots[slot] = meta
		return SaveMetadata(doc)
	})
	if err != nil {
		return SlotMetadata{}, nil, err
	}
	return meta, previous, nil
}

// WriteSlotMetadata replaces one slot record verbatim under the credential
// mutation lock.
func WriteSlotMetadata(slot string, meta SlotMetadata) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	return withCredentialMutationLock(func() error { return WriteSlotMetadataLocked(slot, meta) })
}

// WriteSlotMetadataLocked is WriteSlotMetadata for a caller that already holds
// the credential mutation lock inside WithMutationTransaction. Login uses it
// to record a commit and to restore the previous record on rollback.
func WriteSlotMetadataLocked(slot string, meta SlotMetadata) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	doc, loadErr := LoadMetadata()
	if loadErr != nil && !errors.Is(loadErr, ErrMetadataCorrupt) {
		return loadErr
	}
	doc.Slots[slot] = meta
	return SaveMetadata(doc)
}

// ReadSlotMetadata returns the record for one slot, or nil when absent.
func ReadSlotMetadata(slot string) (*SlotMetadata, error) {
	if !ValidSlot(slot) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	doc, err := LoadMetadata()
	if err != nil {
		return nil, err
	}
	meta, ok := doc.Slots[slot]
	if !ok {
		return nil, nil
	}
	return &meta, nil
}

// DeleteSlotMetadata removes one slot record under the credential mutation
// lock. When the file is corrupt or no slots remain, the whole file is removed.
func DeleteSlotMetadata(slot string) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	return withCredentialMutationLock(func() error { return DeleteSlotMetadataLocked(slot) })
}

// DeleteSlotMetadataLocked is DeleteSlotMetadata for a caller that already
// holds the credential mutation lock inside WithMutationTransaction.
func DeleteSlotMetadataLocked(slot string) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	doc, loadErr := LoadMetadata()
	if loadErr != nil {
		if errors.Is(loadErr, ErrMetadataCorrupt) {
			return RemoveMetadataFileLocked()
		}
		return loadErr
	}
	if _, ok := doc.Slots[slot]; !ok {
		return nil
	}
	delete(doc.Slots, slot)
	return SaveMetadata(doc)
}

// RecordVerification stores the outcome of a remote probe of the credential
// with the given fingerprint. Under the credential mutation lock it records
// the verdict only if the slot's metadata still describes that credential: a
// slot without metadata, or one rotated while the probe ran, is left alone so
// a probe can never invent a stored_at or stamp a verdict on another value.
func RecordVerification(slot, fingerprint, result string, now time.Time) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	switch result {
	case VerifyResultOK, VerifyResultUnauthorized, VerifyResultForbidden, VerifyResultUnreachable:
	default:
		return fmt.Errorf("unknown verification result %q", result)
	}
	return withCredentialMutationLock(func() error {
		doc, err := LoadMetadata()
		if err != nil {
			return err
		}
		meta, ok := doc.Slots[slot]
		if !ok || fingerprint == "" || meta.Fingerprint != fingerprint {
			return nil
		}
		verified := now.UTC()
		meta.LastVerifiedAt = &verified
		meta.LastVerifiedResult = result
		doc.Slots[slot] = meta
		return SaveMetadata(doc)
	})
}

// ExpiryState classifies one slot. A present slot without trusted metadata is
// unknown; an absent slot is none; a slot with no expires_at is ok.
func ExpiryState(meta *SlotMetadata, present bool, now time.Time) (string, *int) {
	if !present {
		return ExpiryStateNone, nil
	}
	if meta == nil {
		return ExpiryStateUnknown, nil
	}
	if meta.ExpiresAt == nil {
		return ExpiryStateOK, nil
	}
	remaining := meta.ExpiresAt.Sub(now)
	days := int(math.Floor(remaining.Hours() / 24))
	switch {
	case remaining <= 0:
		return ExpiryStateExpired, &days
	case remaining <= ExpiringSoonThreshold:
		return ExpiryStateExpiring, &days
	default:
		return ExpiryStateOK, &days
	}
}

var expiryStateSeverity = map[string]int{
	ExpiryStateNone:     0,
	ExpiryStateOK:       1,
	ExpiryStateUnknown:  2,
	ExpiryStateExpiring: 3,
	ExpiryStateExpired:  4,
}

// WorstExpiryState folds per-slot states into one headline state.
func WorstExpiryState(states ...string) string {
	worst := ExpiryStateNone
	for _, state := range states {
		if expiryStateSeverity[state] > expiryStateSeverity[worst] {
			worst = state
		}
	}
	return worst
}

// DescribeSlots computes the value-free inventory for the given stored values.
// When a slot holds a credential but has no matching metadata (or the stored
// fingerprint no longer matches), the slot is backfilled with stored_at=now and
// the assumed maximum lifetime; persist controls whether that backfill is
// written to disk, which happens only while the slot still stores that value.
// No disk access happens when neither slot is present.
func DescribeSlots(values SlotValues, now time.Time, persist bool) Inventory {
	if strings.TrimSpace(values.APIKey) == "" && strings.TrimSpace(values.ClientSecret) == "" {
		return DescribeSlotsWithMetadata(values, emptyMetadata(), nil, now)
	}
	doc, loadErr := LoadMetadata()
	inventory := DescribeSlotsWithMetadata(values, doc, loadErr, now)
	if persist && loadErr == nil && len(inventory.Backfilled) > 0 {
		inventory = persistBackfill(values, doc, inventory, now)
	}
	return inventory
}

// persistBackfill saves backfilled slot records under the credential mutation
// lock. It reloads the metadata there and re-checks each slot's fingerprint
// against the document the backfill was computed from: a slot another writer
// (a login) recorded in between keeps that writer's record, and the returned
// inventory reports it instead of the discarded backfill.
//
// Two reads of the metadata cannot say whether values is still what the slot
// stores. A login that commits before the first read leaves both reads
// describing its new credential while values may still hold the one it
// replaced, and a backfill computed from that stale value would erase the
// login's expiry and verification. So each slot is also read back here, under
// the lock every login and logout holds, and a backfill is written only for a
// slot that still stores the value it describes.
func persistBackfill(values SlotValues, seen Metadata, inventory Inventory, now time.Time) Inventory {
	var readErrs []error
	err := withCredentialMutationLock(func() error {
		current, err := LoadMetadata()
		if err != nil {
			return err
		}
		fresh := DescribeSlotsWithMetadata(values, current, nil, now)
		save := false
		for _, slot := range fresh.Backfilled {
			before, hadBefore := seen.Slots[slot]
			after, hasAfter := current.Slots[slot]
			if hadBefore != hasAfter || before.Fingerprint != after.Fingerprint {
				continue
			}
			view, value, readStored := fresh.APIKey, values.APIKey, GetAPIKey
			if slot == SlotClientSecret {
				view, value, readStored = fresh.ClientSecret, values.ClientSecret, GetClientSecret
			}
			stored, err := readStored()
			if err != nil {
				readErrs = append(readErrs, err)
				continue
			}
			if Fingerprint(stored) != Fingerprint(value) {
				continue
			}
			if view.Metadata != nil {
				current.Slots[slot] = *view.Metadata
				save = true
			}
		}
		inventory = fresh
		if !save {
			return nil
		}
		return SaveMetadata(current)
	})
	if err := errors.Join(append([]error{err}, readErrs...)...); err != nil {
		inventory.BackfillError = err
	}
	return inventory
}

// DescribeSlotsWithMetadata is the pure classification step behind
// DescribeSlots: it never touches disk, so callers (and tests) can evaluate an
// inventory against an in-memory document. loadErr is the LoadMetadata error
// for doc; when non-nil every present slot is reported as unknown.
func DescribeSlotsWithMetadata(values SlotValues, doc Metadata, loadErr error, now time.Time) Inventory {
	now = now.UTC()
	inventory := Inventory{
		APIKey:       SlotView{Slot: SlotAPIKey, Present: strings.TrimSpace(values.APIKey) != ""},
		ClientSecret: SlotView{Slot: SlotClientSecret, Present: strings.TrimSpace(values.ClientSecret) != ""},
	}
	if loadErr != nil {
		inventory.MetadataError = loadErr
	}
	if doc.Slots == nil {
		doc.Slots = map[string]SlotMetadata{}
	}
	describe := func(view *SlotView, value string) {
		if !view.Present {
			view.ExpiryState = ExpiryStateNone
			return
		}
		if loadErr != nil {
			view.ExpiryState = ExpiryStateUnknown
			return
		}
		existing, ok := doc.Slots[view.Slot]
		if ok && existing.Fingerprint == Fingerprint(value) {
			meta := existing
			view.Metadata = &meta
		} else {
			meta, err := NewSlotMetadata(view.Slot, value, StoredOptions{Now: now})
			if err != nil {
				view.ExpiryState = ExpiryStateUnknown
				return
			}
			view.Metadata = &meta
			view.Backfilled = true
			inventory.Backfilled = append(inventory.Backfilled, view.Slot)
		}
		view.ExpiryState, view.DaysLeft = ExpiryState(view.Metadata, true, now)
	}
	describe(&inventory.APIKey, values.APIKey)
	describe(&inventory.ClientSecret, values.ClientSecret)
	inventory.ExpiryState = WorstExpiryState(inventory.APIKey.ExpiryState, inventory.ClientSecret.ExpiryState)
	return inventory
}

// StoredSlotValues reads both credential slots for inventory purposes. Read
// failures are returned so callers can report them without guessing presence.
func StoredSlotValues() (SlotValues, error) {
	apiKey, apiErr := GetAPIKey()
	clientSecret, secretErr := GetClientSecret()
	return SlotValues{APIKey: apiKey, ClientSecret: clientSecret}, errors.Join(apiErr, secretErr)
}

// BackfillMetadata records metadata for any stored credential that lacks it.
// It is the daemon-startup migration counterpart of MigrateFromLegacy and
// returns the slots it backfilled.
func BackfillMetadata(now time.Time) ([]string, error) {
	values, err := StoredSlotValues()
	if err != nil {
		return nil, err
	}
	inventory := DescribeSlots(values, now, true)
	return inventory.Backfilled, errors.Join(inventory.MetadataError, inventory.BackfillError)
}

// DeleteStoredCredentialKindStrict removes exactly one credential slot from
// every supported store and reads back each store, mirroring
// DeleteStoredCredentialsStrict for selective logout.
func DeleteStoredCredentialKindStrict(slot string) error {
	if !ValidSlot(slot) {
		return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	unlock, err := acquireCredentialMutationLock()
	if err != nil {
		return err
	}
	defer unlock()
	return DeleteStoredCredentialKindStrictLocked(slot)
}

// DeleteStoredCredentialKindStrictLocked is DeleteStoredCredentialKindStrict
// for a caller that already holds the credential mutation lock, such as
// logout removing a slot and its metadata in one transaction.
func DeleteStoredCredentialKindStrictLocked(slot string) error {
	switch slot {
	case SlotAPIKey:
		return deleteCredentialStrict("API key", keychainAPIKey, apiKeyPathFunc)
	case SlotClientSecret:
		return deleteCredentialStrict("OAuth client secret", keychainClientSecret, clientSecretPathFunc)
	}
	return fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
}
