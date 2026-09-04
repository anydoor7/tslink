package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
)

// doctorInventoryAt builds an in-memory inventory where the api-key slot was
// stored so that it has daysLeft days remaining under the assumed maximum.
func doctorInventoryAt(daysLeft int, verified bool) func(credentials.SlotValues, time.Time) credentials.Inventory {
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

func TestDoctorCredentialPostureFindings(t *testing.T) {
	cases := []struct {
		name         string
		apiKey       string
		clientSecret string
		wantMode     string
		wantCode     string
		wantSeverity string
		wantExit     int
		absentCodes  []string
	}{
		{"mixed is recommended", doctorFixtureAPIKey, doctorFixtureClientSecret, doctorCredentialMixed, inspect.WarningCodeCredentialMixedRecommended, doctorSeverityInfo, output.ExitSuccess, []string{inspect.WarningCodeCredentialAPITokenOnly, inspect.WarningCodeCredentialOAuthClientOnly}},
		{"api token only warns", doctorFixtureAPIKey, "", doctorCredentialAPIToken, inspect.WarningCodeCredentialAPITokenOnly, doctorSeverityWarning, output.ExitWarning, []string{inspect.WarningCodeCredentialMixedRecommended, inspect.WarningCodeCredentialOAuthClientOnly}},
		{"oauth only informs", "", doctorFixtureClientSecret, doctorCredentialOAuthClientSecret, inspect.WarningCodeCredentialOAuthClientOnly, doctorSeverityInfo, output.ExitSuccess, []string{inspect.WarningCodeCredentialMixedRecommended, inspect.WarningCodeCredentialAPITokenOnly, inspect.WarningCodeCredentialAPITokenExpiring}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			env.writeExactSnapshot(t)
			doctorGetAPIKeyFn = func() (string, error) { return tc.apiKey, nil }
			doctorGetClientSecretFn = func() (string, error) { return tc.clientSecret, nil }

			var buf bytes.Buffer
			err := runDoctor(&buf, doctorOptions{}, true)
			if got := output.ExitCode(err); got != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; output=%s", got, tc.wantExit, buf.String())
			}
			result := decodeDoctorJSON(t, buf.String())
			if result.CredentialMode != tc.wantMode {
				t.Fatalf("credential_mode = %q, want %q", result.CredentialMode, tc.wantMode)
			}
			finding := assertDoctorFinding(t, result, tc.wantCode)
			if finding.Severity != tc.wantSeverity {
				t.Fatalf("%s severity = %q, want %q", tc.wantCode, finding.Severity, tc.wantSeverity)
			}
			for _, code := range tc.absentCodes {
				assertDoctorNoFinding(t, result, code)
			}
			assertDoctorCodesRegistered(t, result)
			assertDoctorOutputOmits(t, buf.String(), []string{doctorFixtureAPIKey, doctorFixtureClientSecret, "FAKE-fixture"})
		})
	}
}

func TestDoctorCredentialExpiryMatrix(t *testing.T) {
	cases := []struct {
		name         string
		daysLeft     int
		wantCode     string
		wantSeverity string
		wantExit     int
		absent       []string
	}{
		{"-1 day expired", -1, inspect.WarningCodeCredentialAPITokenExpired, doctorSeverityError, output.ExitCritical, []string{inspect.WarningCodeCredentialAPITokenExpiring}},
		{"0 days expiring", 0, inspect.WarningCodeCredentialAPITokenExpiring, doctorSeverityWarning, output.ExitWarning, []string{inspect.WarningCodeCredentialAPITokenExpired}},
		{"7 days expiring", 7, inspect.WarningCodeCredentialAPITokenExpiring, doctorSeverityWarning, output.ExitWarning, []string{inspect.WarningCodeCredentialAPITokenExpired}},
		{"13 days expiring", 13, inspect.WarningCodeCredentialAPITokenExpiring, doctorSeverityWarning, output.ExitWarning, nil},
		{"14 days expiring (threshold)", 14, inspect.WarningCodeCredentialAPITokenExpiring, doctorSeverityWarning, output.ExitWarning, nil},
		{"30 days ok", 30, inspect.WarningCodeCredentialMixedRecommended, doctorSeverityInfo, output.ExitSuccess, []string{inspect.WarningCodeCredentialAPITokenExpiring, inspect.WarningCodeCredentialAPITokenExpired}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			env.writeExactSnapshot(t)
			doctorCredentialInventoryFn = doctorInventoryAt(tc.daysLeft, true)

			var buf bytes.Buffer
			err := runDoctor(&buf, doctorOptions{}, true)
			if got := output.ExitCode(err); got != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; output=%s", got, tc.wantExit, buf.String())
			}
			result := decodeDoctorJSON(t, buf.String())
			finding := assertDoctorFinding(t, result, tc.wantCode)
			if finding.Severity != tc.wantSeverity {
				t.Fatalf("%s severity = %q, want %q", tc.wantCode, finding.Severity, tc.wantSeverity)
			}
			if tc.wantCode != inspect.WarningCodeCredentialMixedRecommended {
				if finding.Evidence["slot"] != credentials.SlotAPIKey || finding.Evidence["expires_at_source"] != credentials.ExpirySourceAssumedMax || finding.Evidence["days_left"] == "" || finding.Evidence["expires_at"] == "" {
					t.Fatalf("evidence = %v, want slot/expires_at/source/days_left", finding.Evidence)
				}
			}
			for _, code := range tc.absent {
				assertDoctorNoFinding(t, result, code)
			}
			assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialRemoteUnverified)
			assertDoctorCodesRegistered(t, result)
			assertDoctorOutputOmits(t, buf.String(), []string{doctorFixtureAPIKey, doctorFixtureClientSecret})
		})
	}
}

func TestDoctorCredentialMetadataStates(t *testing.T) {
	t.Run("never verified", func(t *testing.T) {
		env := newDoctorTestEnv(t, nil)
		env.writeExactSnapshot(t)
		doctorCredentialInventoryFn = doctorInventoryAt(60, false)
		result := buildDoctorResult(doctorOptions{})
		finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialRemoteUnverified)
		if finding.Severity != doctorSeverityInfo || !strings.Contains(finding.Evidence["slots"], credentials.SlotAPIKey) || !strings.Contains(finding.Evidence["slots"], credentials.SlotClientSecret) {
			t.Fatalf("finding = %+v, want info naming both slots", finding)
		}
		if got := output.ExitCode(doctorExit(result)); got != output.ExitSuccess {
			t.Fatalf("ExitCode = %d, want success for info-only", got)
		}
	})
	t.Run("backfilled", func(t *testing.T) {
		env := newDoctorTestEnv(t, nil)
		env.writeExactSnapshot(t)
		doctorCredentialInventoryFn = func(values credentials.SlotValues, now time.Time) credentials.Inventory {
			return credentials.DescribeSlotsWithMetadata(values, credentials.Metadata{}, nil, now)
		}
		result := buildDoctorResult(doctorOptions{})
		finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialMetaBackfilled)
		if finding.Severity != doctorSeverityInfo || finding.Evidence["slots"] != credentials.SlotAPIKey+","+credentials.SlotClientSecret {
			t.Fatalf("finding = %+v", finding)
		}
		assertDoctorFinding(t, result, inspect.WarningCodeCredentialRemoteUnverified)
		assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialExpiryUnknown)
	})
	t.Run("corrupt metadata is unknown warning", func(t *testing.T) {
		env := newDoctorTestEnv(t, nil)
		env.writeExactSnapshot(t)
		doctorCredentialInventoryFn = func(values credentials.SlotValues, now time.Time) credentials.Inventory {
			return credentials.DescribeSlotsWithMetadata(values, credentials.Metadata{}, errors.New("credential metadata file is unreadable or malformed: token tskey-api-FAKE-inside-error"), now)
		}
		var buf bytes.Buffer
		err := runDoctor(&buf, doctorOptions{}, true)
		if got := output.ExitCode(err); got != output.ExitWarning {
			t.Fatalf("ExitCode = %d, want warning", got)
		}
		result := decodeDoctorJSON(t, buf.String())
		finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialExpiryUnknown)
		if finding.Severity != doctorSeverityWarning || !strings.Contains(finding.Evidence["error"], "[redacted]") {
			t.Fatalf("finding = %+v, want warning with redacted evidence", finding)
		}
		assertDoctorOutputOmits(t, buf.String(), []string{"FAKE-inside-error"})
		assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialAPITokenExpiring)
	})
	t.Run("backfill persistence failure", func(t *testing.T) {
		env := newDoctorTestEnv(t, nil)
		env.writeExactSnapshot(t)
		doctorCredentialInventoryFn = func(values credentials.SlotValues, now time.Time) credentials.Inventory {
			inv := credentials.DescribeSlotsWithMetadata(values, credentials.Metadata{}, nil, now)
			inv.BackfillError = errors.New("read-only filesystem")
			return inv
		}
		result := buildDoctorResult(doctorOptions{})
		finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialExpiryUnknown)
		if !strings.Contains(finding.Message, "could not be persisted") {
			t.Fatalf("finding = %+v", finding)
		}
	})
	t.Run("no credentials produces no credential expiry findings", func(t *testing.T) {
		newDoctorTestEnv(t, nil)
		doctorGetAPIKeyFn = func() (string, error) { return "", nil }
		doctorGetClientSecretFn = func() (string, error) { return "", nil }
		doctorCredentialInventoryFn = func(credentials.SlotValues, time.Time) credentials.Inventory {
			t.Fatal("inventory ran without any stored credential")
			return credentials.Inventory{}
		}
		result := buildDoctorResult(doctorOptions{})
		for _, code := range []string{inspect.WarningCodeCredentialExpiryUnknown, inspect.WarningCodeCredentialRemoteUnverified, inspect.WarningCodeCredentialMetaBackfilled, inspect.WarningCodeCredentialMixedRecommended} {
			assertDoctorNoFinding(t, result, code)
		}
	})
}

func TestDoctorProbeRemoteRecordsEachSlotOutcome(t *testing.T) {
	cases := []struct {
		name         string
		results      map[string]string
		wantCode     string
		wantSeverity string
		wantExit     int
	}{
		{"both ok", map[string]string{credentials.SlotAPIKey: credentials.VerifyResultOK, credentials.SlotClientSecret: credentials.VerifyResultOK}, "", "", output.ExitSuccess},
		{"api key rejected", map[string]string{credentials.SlotAPIKey: credentials.VerifyResultUnauthorized, credentials.SlotClientSecret: credentials.VerifyResultOK}, inspect.WarningCodeCredentialAPITokenRejected, doctorSeverityError, output.ExitCritical},
		{"client secret forbidden", map[string]string{credentials.SlotAPIKey: credentials.VerifyResultOK, credentials.SlotClientSecret: credentials.VerifyResultForbidden}, inspect.WarningCodeCredentialRemoteForbidden, doctorSeverityWarning, output.ExitWarning},
		{"unreachable", map[string]string{credentials.SlotAPIKey: credentials.VerifyResultUnreachable, credentials.SlotClientSecret: credentials.VerifyResultUnreachable}, inspect.WarningCodeCredentialRemoteUnreachable, doctorSeverityWarning, output.ExitWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			env.writeExactSnapshot(t)
			doctorCredentialInventoryFn = doctorInventoryAt(60, false)
			probed := map[string]int{}
			doctorProbeCredentialFn = func(ctx context.Context, slot string, now time.Time) (credentials.ProbeOutcome, error) {
				probed[slot]++
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > doctorRemoteProbeTimeout {
					t.Fatalf("probe context deadline = %v ok=%v, want bounded by %s", deadline, ok, doctorRemoteProbeTimeout)
				}
				if !now.Equal(env.startedAt) {
					t.Fatalf("probe now = %s, want injected doctor clock %s", now, env.startedAt)
				}
				outcome := credentials.ProbeOutcome{Slot: slot, Present: true, Result: tc.results[slot], Recorded: true}
				if outcome.Result != credentials.VerifyResultOK {
					outcome.Cause = errors.New("synthetic probe failure for tskey-api-FAKE-cause")
				}
				return outcome, nil
			}

			var buf bytes.Buffer
			err := runDoctor(&buf, doctorOptions{ProbeRemote: true}, true)
			if got := output.ExitCode(err); got != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; output=%s", got, tc.wantExit, buf.String())
			}
			if probed[credentials.SlotAPIKey] != 1 || probed[credentials.SlotClientSecret] != 1 {
				t.Fatalf("probes = %v, want one per present slot", probed)
			}
			result := decodeDoctorJSON(t, buf.String())
			// --probe-remote replaces the never-verified info with real results.
			assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialRemoteUnverified)
			if tc.wantCode == "" {
				for _, code := range []string{inspect.WarningCodeCredentialAPITokenRejected, inspect.WarningCodeCredentialRemoteForbidden, inspect.WarningCodeCredentialRemoteUnreachable} {
					assertDoctorNoFinding(t, result, code)
				}
			} else {
				finding := assertDoctorFinding(t, result, tc.wantCode)
				if finding.Severity != tc.wantSeverity || finding.Evidence["result"] == "" || finding.Evidence["slot"] == "" {
					t.Fatalf("finding = %+v, want %s with slot/result evidence", finding, tc.wantSeverity)
				}
				if !strings.Contains(finding.Evidence["error"], "[redacted]") {
					t.Fatalf("evidence error = %q, want redacted token", finding.Evidence["error"])
				}
			}
			assertDoctorCodesRegistered(t, result)
			assertDoctorOutputOmits(t, buf.String(), []string{"FAKE-cause", doctorFixtureAPIKey, doctorFixtureClientSecret})
		})
	}
}

func TestDoctorProbeRemoteSkipsAbsentSlotsAndReportsReadFailures(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorCredentialInventoryFn = doctorInventoryAt(60, false)
	calls := 0
	doctorProbeCredentialFn = func(ctx context.Context, slot string, now time.Time) (credentials.ProbeOutcome, error) {
		calls++
		if slot != credentials.SlotAPIKey {
			t.Fatalf("probed absent slot %s", slot)
		}
		return credentials.ProbeOutcome{}, errors.New("keyring unavailable")
	}
	result := buildDoctorResult(doctorOptions{ProbeRemote: true})
	if calls != 1 {
		t.Fatalf("probe calls = %d, want 1", calls)
	}
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialReadFailed)
	if !strings.Contains(finding.Message, "remote probe") {
		t.Fatalf("finding = %+v", finding)
	}
}

func TestDoctorHelpAndFlagsDescribeCredentialLifecycle(t *testing.T) {
	for _, want := range []string{"--probe-remote", "credential-meta.json", "credential_api_token_expiring", "14 days", "credential_mixed_recommended"} {
		if !strings.Contains(doctorCmd.Long, want) {
			t.Fatalf("doctor help missing %q:\n%s", want, doctorCmd.Long)
		}
	}
	flag := doctorCmd.Flags().Lookup("probe-remote")
	if flag == nil || flag.DefValue != "false" || !strings.Contains(flag.Usage, "device-list read") {
		t.Fatalf("doctor --probe-remote flag = %+v, want documented default-off remote probe", flag)
	}
}

func TestDoctorRunEPassesProbeRemote(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	doctorCredentialInventoryFn = doctorInventoryAt(60, false)
	probed := false
	doctorProbeCredentialFn = func(context.Context, string, time.Time) (credentials.ProbeOutcome, error) {
		probed = true
		return credentials.ProbeOutcome{Present: true, Result: credentials.VerifyResultOK, Recorded: true}, nil
	}
	resetRootJSONFlag(t)
	resetCommandLocalFlags(t, doctorCmd)
	doctorCmd.SetOut(&bytes.Buffer{})
	t.Cleanup(func() { doctorCmd.SetOut(nil) })
	if err := doctorCmd.Flags().Set("probe-remote", "true"); err != nil {
		t.Fatal(err)
	}
	if err := doctorCmd.RunE(doctorCmd, nil); err != nil {
		t.Fatalf("doctor RunE error = %v", err)
	}
	if !probed {
		t.Fatal("--probe-remote did not trigger the remote probe")
	}
}
