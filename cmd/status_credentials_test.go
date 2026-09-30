package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
)

var statusTestNow = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

const (
	statusFixtureAPIKey       = "tskey-api-FAKE-status-fixture"
	statusFixtureClientSecret = "tskey-client-FAKE-status-fixture"
)

// withStatusCredentialSeams stubs the value readers and swaps the inventory for
// an in-memory classification so no test touches credential-meta.json.
func withStatusCredentialSeams(t *testing.T, apiKey, clientSecret string, inventory func(credentials.SlotValues, time.Time) credentials.Inventory) {
	t.Helper()
	oldGetAPIKey := getAPIKeyFn
	oldHasClientSecret := hasClientSecretFn
	oldGetClientSecret := statusGetClientSecretFn
	oldInventory := statusCredentialInventoryFn
	oldNow := statusNowFn
	oldIsRunning := isRunningFn
	t.Cleanup(func() {
		getAPIKeyFn = oldGetAPIKey
		hasClientSecretFn = oldHasClientSecret
		statusGetClientSecretFn = oldGetClientSecret
		statusCredentialInventoryFn = oldInventory
		statusNowFn = oldNow
		isRunningFn = oldIsRunning
	})
	getAPIKeyFn = func() (string, error) { return apiKey, nil }
	hasClientSecretFn = func() bool { return clientSecret != "" }
	statusGetClientSecretFn = func() (string, error) { return clientSecret, nil }
	statusNowFn = func() time.Time { return statusTestNow }
	isRunningFn = func(string) bool { return false }
	if inventory != nil {
		statusCredentialInventoryFn = inventory
	}
}

// statusInventoryAt anchors the api-key slot so it has daysLeft days remaining
// under the assumed maximum lifetime.
func statusInventoryAt(daysLeft int, verified bool) func(credentials.SlotValues, time.Time) credentials.Inventory {
	return func(values credentials.SlotValues, now time.Time) credentials.Inventory {
		doc := credentials.Metadata{SchemaVersion: credentials.MetadataSchemaVersion, Slots: map[string]credentials.SlotMetadata{}}
		// A positive sub-day offset only matters at the zero boundary, where the
		// slot must still read as "expiring today" (days_left 0, remaining > 0)
		// rather than already expired. For any positive daysLeft the remaining
		// duration must land exactly on daysLeft*24h so the raw-duration
		// threshold classifies the 14-day boundary as expiring, matching
		// ExpiryState's contract pinned in metadata_test.go (14d inclusive, 14d+1min ok).
		subDayOffset := time.Hour
		if daysLeft > 0 {
			subDayOffset = 0
		}
		storedAt := now.Add(-credentials.APIKeyAssumedMaxLifetime + time.Duration(daysLeft)*24*time.Hour + subDayOffset)
		for slot, value := range map[string]string{credentials.SlotAPIKey: values.APIKey, credentials.SlotClientSecret: values.ClientSecret} {
			if strings.TrimSpace(value) == "" {
				continue
			}
			meta, err := credentials.NewSlotMetadata(slot, value, credentials.StoredOptions{Now: storedAt, Verified: verified})
			if err != nil {
				panic(err)
			}
			doc.Slots[slot] = meta
		}
		return credentials.DescribeSlotsWithMetadata(values, doc, nil, now)
	}
}

func statusTestPaths(t *testing.T) (pidPath, regPath, snapshotPath, handoffPath string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath = filepath.Join(dir, "registry.json")
	addStatusTestService(t, regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	return filepath.Join(dir, "tslink.pid"), regPath, filepath.Join(dir, "runtime.json"), filepath.Join(dir, "auth-handoff.json")
}

func TestStatusCredentialExpiryMatrix(t *testing.T) {
	cases := []struct {
		name          string
		apiKey        string
		clientSecret  string
		daysLeft      int
		wantState     string
		wantAPIState  string
		wantDays      *int
		wantNext      bool
		wantHumanLine string
	}{
		{"none", "", "", 0, credentials.ExpiryStateNone, credentials.ExpiryStateNone, nil, false, ""},
		{"expired yesterday", statusFixtureAPIKey, statusFixtureClientSecret, -1, credentials.ExpiryStateExpired, credentials.ExpiryStateExpired, intPtr(-1), true, "api-key expired 1d ago (assumed max); client-secret ok (does not expire)"},
		{"expiring today", statusFixtureAPIKey, "", 0, credentials.ExpiryStateExpiring, credentials.ExpiryStateExpiring, intPtr(0), true, "api-key expires in 0d (assumed max)"},
		{"expiring 7", statusFixtureAPIKey, "", 7, credentials.ExpiryStateExpiring, credentials.ExpiryStateExpiring, intPtr(7), true, "api-key expires in 7d (assumed max)"},
		{"expiring 13", statusFixtureAPIKey, "", 13, credentials.ExpiryStateExpiring, credentials.ExpiryStateExpiring, intPtr(13), true, "api-key expires in 13d (assumed max)"},
		{"expiring 14 threshold", statusFixtureAPIKey, "", 14, credentials.ExpiryStateExpiring, credentials.ExpiryStateExpiring, intPtr(14), true, "api-key expires in 14d (assumed max)"},
		{"ok 30", statusFixtureAPIKey, statusFixtureClientSecret, 30, credentials.ExpiryStateOK, credentials.ExpiryStateOK, intPtr(30), false, "api-key expires in 30d (assumed max); client-secret ok (does not expire)"},
		{"client secret only", "", statusFixtureClientSecret, 30, credentials.ExpiryStateOK, credentials.ExpiryStateNone, nil, false, "client-secret ok (does not expire)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pidPath, regPath, snapshotPath, handoffPath := statusTestPaths(t)
			withStatusCredentialSeams(t, tc.apiKey, tc.clientSecret, statusInventoryAt(tc.daysLeft, true))

			status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
			if err != nil {
				t.Fatal(err)
			}
			if status.CredentialExpiryState != tc.wantState || status.Credentials.APIKey.ExpiryState != tc.wantAPIState {
				t.Fatalf("expiry state = %s (api %s), want %s (api %s)", status.CredentialExpiryState, status.Credentials.APIKey.ExpiryState, tc.wantState, tc.wantAPIState)
			}
			if !reflect.DeepEqual(status.Credentials.APIKey.DaysLeft, tc.wantDays) {
				t.Fatalf("days_left = %v, want %v", status.Credentials.APIKey.DaysLeft, tc.wantDays)
			}
			if status.Credentials.APIKey.Present != (tc.apiKey != "") || status.Credentials.ClientSecret.Present != (tc.clientSecret != "") {
				t.Fatalf("presence = %+v", status.Credentials)
			}
			if tc.apiKey != "" {
				slot := status.Credentials.APIKey
				if slot.Fingerprint != credentials.Fingerprint(tc.apiKey) || slot.ExpiresAt == nil || slot.ExpiresAtSource != credentials.ExpirySourceAssumedMax || slot.StoredAt == nil || slot.LastVerifiedAt == nil || slot.LastVerifiedResult != credentials.VerifyResultOK {
					t.Fatalf("api slot = %+v, want full value-free record", slot)
				}
			}
			if tc.clientSecret != "" && status.Credentials.ClientSecret.ExpiresAt != nil {
				t.Fatalf("client-secret slot = %+v, want no expiry", status.Credentials.ClientSecret)
			}
			if tc.wantNext {
				if !reflect.DeepEqual(status.Next, credentials.NextAPIKeyBootstrap()) {
					t.Fatalf("next = %v, want key bootstrap", status.Next)
				}
			} else if tc.apiKey != "" || tc.clientSecret != "" {
				if status.Next != nil {
					t.Fatalf("next = %v, want none for %s", status.Next, tc.wantState)
				}
			}

			wire, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{statusFixtureAPIKey, statusFixtureClientSecret, "FAKE-status"} {
				if bytes.Contains(wire, []byte(secret)) {
					t.Fatalf("status JSON leaked %q: %s", secret, wire)
				}
			}
			if !bytes.Contains(wire, []byte(`"credential_expiry_state":"`+tc.wantState+`"`)) || !bytes.Contains(wire, []byte(`"credentials":{"api_key":{"present":`)) {
				t.Fatalf("status JSON missing credential fields: %s", wire)
			}

			var human bytes.Buffer
			formatStatus(status, &human)
			if tc.wantHumanLine == "" {
				if strings.Contains(human.String(), "→ credentials:") {
					t.Fatalf("human status printed credentials without any slot:\n%s", human.String())
				}
			} else if !strings.Contains(human.String(), "→ credentials: "+tc.wantHumanLine) {
				t.Fatalf("human status = %q, want line %q", human.String(), tc.wantHumanLine)
			}
			if tc.wantNext && !strings.Contains(human.String(), "→ next: "+credentials.NextAPIKeyBootstrap()[0]) {
				t.Fatalf("human status lacks renewal steps:\n%s", human.String())
			}
			if strings.Contains(human.String(), "FAKE-status") {
				t.Fatalf("human status leaked credential:\n%s", human.String())
			}

			// status --urls carries the identical credential block.
			urls, err := getStatusURLsWithAuth(pidPath, regPath, snapshotPath, handoffPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(urls.Credentials, status.Credentials) || urls.CredentialExpiryState != status.CredentialExpiryState || !reflect.DeepEqual(urls.Next, status.Next) {
				t.Fatalf("status --urls credentials diverged: %+v vs %+v", urls.Credentials, status.Credentials)
			}
			var humanURLs bytes.Buffer
			formatStatusURLs(urls, &humanURLs)
			if tc.wantHumanLine != "" && !strings.Contains(humanURLs.String(), "→ credentials: "+tc.wantHumanLine) {
				t.Fatalf("human status --urls = %q, want line %q", humanURLs.String(), tc.wantHumanLine)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestStatusCredentialUnknownStatesAndMetadataError(t *testing.T) {
	t.Run("corrupt metadata", func(t *testing.T) {
		pidPath, regPath, snapshotPath, handoffPath := statusTestPaths(t)
		withStatusCredentialSeams(t, statusFixtureAPIKey, "", func(values credentials.SlotValues, now time.Time) credentials.Inventory {
			return credentials.DescribeSlotsWithMetadata(values, credentials.Metadata{}, errors.New("credential metadata file is unreadable or malformed: tskey-api-FAKE-in-error"), now)
		})
		status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
		if err != nil {
			t.Fatal(err)
		}
		if status.CredentialExpiryState != credentials.ExpiryStateUnknown || status.Credentials.APIKey.ExpiryState != credentials.ExpiryStateUnknown || status.Credentials.APIKey.Fingerprint != "" {
			t.Fatalf("status = %+v, want unknown without fingerprint", status.Credentials)
		}
		if status.Credentials.MetadataError == "" || strings.Contains(status.Credentials.MetadataError, "FAKE-in-error") {
			t.Fatalf("metadata_error = %q, want redacted reason", status.Credentials.MetadataError)
		}
		if status.Next != nil {
			t.Fatalf("next = %v, want none for unknown", status.Next)
		}
		var human bytes.Buffer
		formatStatus(status, &human)
		if !strings.Contains(human.String(), "→ credentials: api-key expiry unknown") {
			t.Fatalf("human status = %q", human.String())
		}
	})
	t.Run("client secret present but unreadable stays present", func(t *testing.T) {
		pidPath, regPath, snapshotPath, handoffPath := statusTestPaths(t)
		withStatusCredentialSeams(t, "", "", nil)
		hasClientSecretFn = func() bool { return true }
		statusGetClientSecretFn = func() (string, error) { return "", errors.New("keyring read failed") }
		status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
		if err != nil {
			t.Fatal(err)
		}
		if !status.CredentialStored || status.Authenticated || !status.Credentials.ClientSecret.Present || status.Credentials.ClientSecret.ExpiryState != credentials.ExpiryStateUnknown || status.CredentialExpiryState != credentials.ExpiryStateUnknown {
			t.Fatalf("status = %+v, want present/unknown client-secret", status.Credentials)
		}
	})
}

func TestStatusCredentialsDefaultInventoryPersistsBackfillInConfigDir(t *testing.T) {
	pidPath, regPath, snapshotPath, handoffPath := statusTestPaths(t)
	withStatusCredentialSeams(t, statusFixtureAPIKey, "", nil)
	status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.CredentialExpiryState != credentials.ExpiryStateOK || status.Credentials.APIKey.StoredAt == nil || !status.Credentials.APIKey.StoredAt.Equal(statusTestNow) {
		t.Fatalf("status = %+v, want backfilled at injected clock", status.Credentials.APIKey)
	}
	doc, err := credentials.LoadMetadata()
	if err != nil {
		t.Fatalf("LoadMetadata() error = %v", err)
	}
	meta, ok := doc.Slots[credentials.SlotAPIKey]
	if !ok || meta.Fingerprint != credentials.Fingerprint(statusFixtureAPIKey) || meta.ExpiresAtSource != credentials.ExpirySourceAssumedMax {
		t.Fatalf("persisted metadata = %+v", doc.Slots)
	}
}

func TestStatusHelpDocumentsCredentialLine(t *testing.T) {
	for _, want := range []string{"→ credentials:", "credential_expiry_state", "14 days", "Fingerprints only"} {
		if !strings.Contains(statusCmd.Long, want) {
			t.Fatalf("status help missing %q:\n%s", want, statusCmd.Long)
		}
	}
}
