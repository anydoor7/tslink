package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

type doctorTestEnv struct {
	dir          string
	regPath      string
	snapshotPath string
	authHandoff  string
	pidPath      string
	authKeyPath  string
	startedAt    time.Time
	pid          int
}

func newDoctorTestEnv(t *testing.T, services []registry.Service) doctorTestEnv {
	t.Helper()
	resetDoctorSeams(t)
	oldSupervision := detectSupervisionFn
	t.Cleanup(func() { detectSupervisionFn = oldSupervision })
	detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
		return Supervision{Manager: "systemd", Autostart: true, RestartOnExit: true, Detail: "isolated managed fixture"}
	}

	dir := t.TempDir()
	env := doctorTestEnv{
		dir:          dir,
		regPath:      filepath.Join(dir, "registry.json"),
		snapshotPath: filepath.Join(dir, "runtime.json"),
		authHandoff:  filepath.Join(dir, "auth-handoff.json"),
		pidPath:      filepath.Join(dir, "tslink.pid"),
		authKeyPath:  filepath.Join(dir, "authkey"),
		startedAt:    time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		pid:          4242,
	}
	writeDoctorRegistry(t, env.regPath, services)

	doctorConfigDirFn = func() (string, error) { return dir, nil }
	doctorRegistryPathFn = func() (string, error) { return env.regPath, nil }
	doctorRuntimeSnapshotPathFn = func() (string, error) { return env.snapshotPath, nil }
	doctorAuthHandoffPathFn = func() (string, error) { return env.authHandoff, nil }
	doctorPIDPathFn = func() (string, error) { return env.pidPath, nil }
	doctorAuthKeyPathFn = func() (string, error) { return env.authKeyPath, nil }
	doctorNodeOwnershipPathFn = func() (string, error) { return filepath.Join(dir, "node-ownership.json"), nil }
	doctorLoadGlobalConfigFn = func() (config.GlobalConfig, error) { return config.GlobalConfig{}, nil }
	// The default fixture models the recommended dual-slot state (api-key for
	// invites, client-secret for durable daemon auth) with fresh, verified
	// metadata so a healthy environment produces no credential warnings.
	doctorGetAPIKeyFn = func() (string, error) { return doctorFixtureAPIKey, nil }
	doctorGetClientSecretFn = func() (string, error) { return doctorFixtureClientSecret, nil }
	doctorNowFn = func() time.Time { return env.startedAt }
	doctorCredentialInventoryFn = doctorFixtureInventory
	doctorProbeCredentialFn = func(context.Context, string, time.Time) (credentials.ProbeOutcome, error) {
		t.Fatal("remote credential probe ran without --probe-remote")
		return credentials.ProbeOutcome{}, nil
	}
	doctorReadFileFn = os.ReadFile
	doctorStatFn = os.Stat
	doctorOpenPathFn = func(path string) (io.Closer, error) { return os.Open(path) }
	doctorProbeTargetFn = func(context.Context, string, time.Duration) error { return nil }
	doctorHTTPProbeFn = func(context.Context, registry.Service) string { return "" }
	doctorLoadAuthHandoffFn = loadAuthHandoff
	// The default fixture keeps Tailscale SSH deterministic and off the
	// machine's real tailscaled: no test process may perform the local-API
	// preferences read that production uses.
	doctorTailscaleSSHFn = func(context.Context) (bool, error) { return false, nil }

	isRunningFn = func(string) bool { return true }
	readPIDFn = func(string) (int, error) { return env.pid, nil }
	pidFileModTimeFn = func(string) (time.Time, error) { return env.startedAt, nil }
	runtimeLoadSnapshotFn = tsruntime.Load

	return env
}

func resetDoctorSeams(t *testing.T) {
	t.Helper()
	oldConfigDir := doctorConfigDirFn
	oldRegistryPath := doctorRegistryPathFn
	oldRuntimeSnapshotPath := doctorRuntimeSnapshotPathFn
	oldAuthHandoffPath := doctorAuthHandoffPathFn
	oldPIDPath := doctorPIDPathFn
	oldAuthKeyPath := doctorAuthKeyPathFn
	oldNodeOwnershipPath := doctorNodeOwnershipPathFn
	oldLoadGlobalConfig := doctorLoadGlobalConfigFn
	oldGetAPIKey := doctorGetAPIKeyFn
	oldGetClientSecret := doctorGetClientSecretFn
	oldReadFile := doctorReadFileFn
	oldStat := doctorStatFn
	oldOpenPath := doctorOpenPathFn
	oldProbe := doctorProbeTargetFn
	oldHTTPProbe := doctorHTTPProbeFn
	oldLoadAuthHandoff := doctorLoadAuthHandoffFn
	oldIsRunning := isRunningFn
	oldReadPID := readPIDFn
	oldPIDFileModTime := pidFileModTimeFn
	oldRuntimeLoad := runtimeLoadSnapshotFn
	oldNow := doctorNowFn
	oldInventory := doctorCredentialInventoryFn
	oldProbeCredential := doctorProbeCredentialFn
	oldTailscaleSSH := doctorTailscaleSSHFn
	t.Cleanup(func() {
		doctorTailscaleSSHFn = oldTailscaleSSH
		doctorNowFn = oldNow
		doctorCredentialInventoryFn = oldInventory
		doctorProbeCredentialFn = oldProbeCredential
		doctorConfigDirFn = oldConfigDir
		doctorRegistryPathFn = oldRegistryPath
		doctorRuntimeSnapshotPathFn = oldRuntimeSnapshotPath
		doctorAuthHandoffPathFn = oldAuthHandoffPath
		doctorPIDPathFn = oldPIDPath
		doctorAuthKeyPathFn = oldAuthKeyPath
		doctorNodeOwnershipPathFn = oldNodeOwnershipPath
		doctorLoadGlobalConfigFn = oldLoadGlobalConfig
		doctorGetAPIKeyFn = oldGetAPIKey
		doctorGetClientSecretFn = oldGetClientSecret
		doctorReadFileFn = oldReadFile
		doctorStatFn = oldStat
		doctorOpenPathFn = oldOpenPath
		doctorProbeTargetFn = oldProbe
		doctorHTTPProbeFn = oldHTTPProbe
		doctorLoadAuthHandoffFn = oldLoadAuthHandoff
		isRunningFn = oldIsRunning
		readPIDFn = oldReadPID
		pidFileModTimeFn = oldPIDFileModTime
		runtimeLoadSnapshotFn = oldRuntimeLoad
	})
}

// stubDoctorTailscaleSSH pins the informational Tailscale SSH check to a fixed
// outcome. Every test that reaches buildDoctorResult must install it (directly
// or through newDoctorTestEnv): the production seam performs a local-API
// preferences read against the machine's real tailscaled, which no test may do.
func stubDoctorTailscaleSSH(t *testing.T, enabled bool, err error) {
	t.Helper()
	old := doctorTailscaleSSHFn
	t.Cleanup(func() { doctorTailscaleSSHFn = old })
	doctorTailscaleSSHFn = func(context.Context) (bool, error) { return enabled, err }
}

const (
	doctorFixtureAPIKey       = "tskey-api-<test-only-secret-value>"
	doctorFixtureClientSecret = "tskey-client-FAKE-fixture-secret"
)

// doctorFixtureInventory classifies the fixture values against an in-memory
// metadata document (both slots stored a day ago and verified) so tests never
// touch a credential-meta.json on disk and see no backfill noise.
func doctorFixtureInventory(values credentials.SlotValues, now time.Time, _ bool) credentials.Inventory {
	doc := credentials.Metadata{SchemaVersion: credentials.MetadataSchemaVersion, Slots: map[string]credentials.SlotMetadata{}}
	storedAt := now.Add(-24 * time.Hour)
	for slot, value := range map[string]string{credentials.SlotAPIKey: values.APIKey, credentials.SlotClientSecret: values.ClientSecret} {
		if strings.TrimSpace(value) == "" {
			continue
		}
		meta, err := credentials.NewSlotMetadata(slot, value, credentials.StoredOptions{Now: storedAt, Verified: true})
		if err != nil {
			panic(err)
		}
		doc.Slots[slot] = meta
	}
	return credentials.DescribeSlotsWithMetadata(values, doc, nil, now)
}

func writeDoctorRegistry(t *testing.T, path string, services []registry.Service) {
	t.Helper()
	created := time.Date(2026, 5, 17, 11, 0, 0, 0, time.UTC)
	copied := append([]registry.Service(nil), services...)
	for i := range copied {
		if copied[i].CreatedAt.IsZero() {
			copied[i].CreatedAt = created.Add(time.Duration(i) * time.Second)
		}
	}
	data, err := json.MarshalIndent(registry.Registry{Services: copied}, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("WriteFile registry: %v", err)
	}
}

func (env doctorTestEnv) writeExactSnapshot(t *testing.T) {
	t.Helper()
	reg, err := registry.Load(env.regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg, nil)
	if err != nil {
		t.Fatalf("RegistryFingerprint: %v", err)
	}
	states := make([]tsruntime.ServiceState, 0, len(reg.Services))
	for _, svc := range reg.Services {
		states = append(states, tsruntime.ServiceState{
			Service:     svc,
			RuntimeHost: svc.Name + ".tailnet.ts.net",
		})
	}
	snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, fingerprint, env.startedAt.Add(time.Second), states)
	snapshot.AccessLog = &accesslog.Health{Enabled: true, Current: true}
	if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
}

func assertDoctorFinding(t *testing.T, result DoctorResult, code string) DoctorFinding {
	t.Helper()
	for _, finding := range result.Findings {
		if finding.Code == code {
			return finding
		}
	}
	t.Fatalf("finding %s not found in %+v", code, result.Findings)
	return DoctorFinding{}
}

func assertDoctorNoFinding(t *testing.T, result DoctorResult, code string) {
	t.Helper()
	for _, finding := range result.Findings {
		if finding.Code == code {
			t.Fatalf("unexpected finding %s in %+v", code, result.Findings)
		}
	}
}

func TestDoctorSupervisorBreakerVisibleWithEmptyRegistry(t *testing.T) {
	for _, state := range []string{"circuit_open", "failed"} {
		t.Run(state, func(t *testing.T) {
			newDoctorTestEnv(t, nil)
			isRunningFn = func(string) bool { return false }
			detectSupervisionFn = func(context.Context, string, bool, int) Supervision {
				return Supervision{Manager: "windows-task-scheduler", Installed: true, Autostart: true,
					RuntimeState: state, FailureReason: "historical_reason"}
			}
			var out bytes.Buffer
			if err := runDoctor(context.Background(), &out, doctorOptions{}, true); output.ExitCode(err) != output.ExitWarning {
				t.Fatalf("doctor exit=%v output=%s", err, out.String())
			}
			var envelope struct {
				OK   bool         `json:"ok"`
				Code int          `json:"code"`
				Data DoctorResult `json:"data"`
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			finding := assertDoctorFinding(t, envelope.Data, inspect.WarningCodeDaemonRestartUnavailable)
			if !envelope.OK || envelope.Code != int(output.ExitWarning) || envelope.Data.Counts.Services != 0 ||
				envelope.Data.Supervision.RuntimeState != state || !strings.Contains(finding.Message, "historical_reason") {
				t.Fatalf("doctor terminal evidence=%s", out.String())
			}
		})
	}
}

func assertDoctorCodesRegistered(t *testing.T, result DoctorResult) {
	t.Helper()
	for _, finding := range result.Findings {
		meta, ok := inspect.WarningCodeRegistry[finding.Code]
		if !ok {
			t.Fatalf("doctor emitted unregistered code %s", finding.Code)
		}
		if finding.Severity != meta.Severity {
			t.Fatalf("finding %s severity = %q, want registry severity %q", finding.Code, finding.Severity, meta.Severity)
		}
	}
}

func assertDoctorOutputOmits(t *testing.T, raw string, forbidden []string) {
	t.Helper()
	for _, value := range forbidden {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(raw, value) || strings.Contains(raw, string(encoded[1:len(encoded)-1])) {
			t.Fatalf("doctor output leaked %q: %s", value, raw)
		}
	}
}

func decodeDoctorJSON(t *testing.T, raw string) DoctorResult {
	t.Helper()

	assertExactTopLevelJSONKeys(t, raw, "type", "ok", "schema_version", "command", "code", "data")
	var envelope output.Result
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("unmarshal doctor envelope: %v\nraw: %s", err, raw)
	}
	if envelope.Type != output.SchemaType {
		t.Fatalf("type = %q, want %q\nraw: %s", envelope.Type, output.SchemaType, raw)
	}
	if !envelope.OK {
		t.Fatalf("ok = false, want true\nraw: %s", raw)
	}
	if envelope.SchemaVersion != output.SchemaVersion {
		t.Fatalf("top-level schema_version = %d, want %d\nraw: %s", envelope.SchemaVersion, output.SchemaVersion, raw)
	}
	if envelope.Command != "doctor" {
		t.Fatalf("command = %q, want %q", envelope.Command, "doctor")
	}
	dataBytes, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatalf("marshal doctor data: %v", err)
	}
	var data DoctorResult
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("unmarshal doctor data: %v\nraw data: %s", err, dataBytes)
	}
	if data.SchemaVersion == 0 || data.ExecutionStatus == "" {
		t.Fatalf("flat doctor data incomplete\nraw data: %s", dataBytes)
	}
	// The envelope code is the process exit code (A3-5), which is the
	// health exit code the data reports.
	if envelope.Code != data.HealthExitCode {
		t.Fatalf("code = %d, want the health exit code %d", envelope.Code, data.HealthExitCode)
	}
	return data
}

func TestDoctorExitCodes(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	if err := runDoctor(context.Background(), io.Discard, doctorOptions{}, false); err != nil {
		t.Fatalf("healthy runDoctor error = %v", err)
	}

	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	if err := os.WriteFile(env.authKeyPath, []byte("tskey-auth-secret"), 0o600); err != nil {
		t.Fatalf("write authkey: %v", err)
	}
	var warningBuf bytes.Buffer
	err := runDoctor(context.Background(), &warningBuf, doctorOptions{}, true)
	if output.ExitCode(err) != output.ExitWarning {
		t.Fatalf("legacy authkey ExitCode = %d, want %d", output.ExitCode(err), output.ExitWarning)
	}
	if !output.IsSilent(err) {
		t.Fatalf("legacy authkey err = %T, want silent exit", err)
	}
	warningResult := decodeDoctorJSON(t, warningBuf.String())
	if warningResult.Status != doctorStatusWarning {
		t.Fatalf("legacy authkey status = %q, want %q", warningResult.Status, doctorStatusWarning)
	}
	if warningResult.HealthStatus != doctorStatusWarning || warningResult.HealthExitCode != output.ExitWarning {
		t.Fatalf("legacy authkey health = %q/%d, want warning/%d", warningResult.HealthStatus, warningResult.HealthExitCode, output.ExitWarning)
	}

	doctorGetAPIKeyFn = func() (string, error) { return "", errors.New("credential backend unavailable") }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	var criticalBuf bytes.Buffer
	err = runDoctor(context.Background(), &criticalBuf, doctorOptions{}, true)
	if output.ExitCode(err) != output.ExitCritical {
		t.Fatalf("credential read failure ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
	}
	if !output.IsSilent(err) {
		t.Fatalf("credential read failure err = %T, want silent exit", err)
	}
	criticalResult := decodeDoctorJSON(t, criticalBuf.String())
	if criticalResult.Status != doctorStatusError {
		t.Fatalf("credential read failure status = %q, want %q", criticalResult.Status, doctorStatusError)
	}
	if criticalResult.HealthStatus != doctorStatusError || criticalResult.HealthExitCode != output.ExitCritical {
		t.Fatalf("credential read failure health = %q/%d, want error/%d", criticalResult.HealthStatus, criticalResult.HealthExitCode, output.ExitCritical)
	}
}

// The pristine default tier is healthy (R5-10): credential_none is reported,
// but as info, and doctor exits 0.
func TestDoctorMissingCredentialsFindingIsInfo(t *testing.T) {
	newDoctorTestEnv(t, nil)
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	isRunningFn = func(string) bool { return false }

	var buf bytes.Buffer
	err := runDoctor(context.Background(), &buf, doctorOptions{}, false)
	if output.ExitCode(err) != output.ExitSuccess {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitSuccess)
	}
	result := buildDoctorResult(context.Background(), doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialNone)
	if finding.Severity != doctorSeverityInfo {
		t.Fatalf("credential_none severity = %q, want info", finding.Severity)
	}
	assertDoctorNoFinding(t, result, inspect.WarningCodeRuntimeSnapshotMissing)
	assertDoctorCodesRegistered(t, result)
	if strings.Contains(buf.String(), "tskey-") {
		t.Fatalf("human doctor output leaked credential-looking value: %s", buf.String())
	}
	for _, want := range []string{"Credential tier: Tier 1", "tslink serve", "optional Tier 2"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("human doctor output = %q, want %q", buf.String(), want)
		}
	}
}

func TestDoctorTier1StateMatrix(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	cases := []struct {
		name             string
		services         []registry.Service
		setup            func(t *testing.T, env doctorTestEnv)
		wantCode         string
		wantSeverity     string
		wantExit         int
		wantSnapshotMiss bool
	}{
		{
			name:         "no enrollment and no daemon",
			wantCode:     inspect.WarningCodeCredentialNone,
			wantSeverity: doctorSeverityInfo,
			wantExit:     output.ExitSuccess,
			setup: func(t *testing.T, env doctorTestEnv) {
				isRunningFn = func(string) bool { return false }
			},
		},
		{
			name:         "interactive enrollment pending",
			services:     []registry.Service{service},
			wantCode:     inspect.WarningCodeCredentialTier1,
			wantSeverity: doctorSeverityInfo,
			wantExit:     output.ExitWarning,
			setup: func(t *testing.T, env doctorTestEnv) {
				record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/doctor-test", env.pid)
				if err := saveAuthHandoff(env.authHandoff, record); err != nil {
					t.Fatalf("saveAuthHandoff: %v", err)
				}
			},
		},
		{
			name:         "interactive enrollment completed",
			services:     []registry.Service{service},
			wantCode:     inspect.WarningCodeCredentialTier1,
			wantSeverity: doctorSeverityInfo,
			wantExit:     output.ExitSuccess,
			setup: func(t *testing.T, env doctorTestEnv) {
				env.writeExactSnapshot(t)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, tc.services)
			doctorGetAPIKeyFn = func() (string, error) { return "", nil }
			doctorGetClientSecretFn = func() (string, error) { return "", nil }
			doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
			if tc.setup != nil {
				tc.setup(t, env)
			}

			result := buildDoctorResult(context.Background(), doctorOptions{})
			if result.CredentialTier != doctorCredentialTier1 || result.CredentialMode != doctorCredentialNone {
				t.Fatalf("credential state = %s/%s, want tier1/none", result.CredentialTier, result.CredentialMode)
			}
			finding := assertDoctorFinding(t, result, tc.wantCode)
			if finding.Severity != tc.wantSeverity {
				t.Fatalf("%s severity = %q, want %q", tc.wantCode, finding.Severity, tc.wantSeverity)
			}
			if tc.wantSnapshotMiss {
				assertDoctorFinding(t, result, inspect.WarningCodeRuntimeSnapshotMissing)
			} else {
				assertDoctorNoFinding(t, result, inspect.WarningCodeRuntimeSnapshotMissing)
			}
			if got := output.ExitCode(doctorExit(result)); got != tc.wantExit {
				t.Fatalf("ExitCode = %d, want %d; findings=%+v", got, tc.wantExit, result.Findings)
			}
			assertDoctorCodesRegistered(t, result)
		})
	}
}

func TestDoctorTier1RunningWithoutEnrollmentWarns(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	newDoctorTestEnv(t, []registry.Service{service})
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }

	result := buildDoctorResult(context.Background(), doctorOptions{})
	tierFinding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialTier1)
	if tierFinding.Severity != doctorSeverityInfo {
		t.Fatalf("credential_tier1 severity = %q, want %q", tierFinding.Severity, doctorSeverityInfo)
	}
	snapshotFinding := assertDoctorFinding(t, result, inspect.WarningCodeRuntimeSnapshotMissing)
	if snapshotFinding.Severity != doctorSeverityWarning {
		t.Fatalf("runtime_snapshot_missing severity = %q, want %q", snapshotFinding.Severity, doctorSeverityWarning)
	}
	if got := output.ExitCode(doctorExit(result)); got != output.ExitWarning {
		t.Fatalf("ExitCode = %d, want %d; findings=%+v", got, output.ExitWarning, result.Findings)
	}
}

func TestDoctorTier1CompletedEnrollmentMessage(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	env := newDoctorTestEnv(t, []registry.Service{service})
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	env.writeExactSnapshot(t)

	result := buildDoctorResult(context.Background(), doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialTier1)
	if !strings.Contains(finding.Message, "has produced authorized runtime state") {
		t.Fatalf("credential_tier1 message = %q, want completed-enrollment evidence", finding.Message)
	}
	if got := output.ExitCode(doctorExit(result)); got != output.ExitSuccess {
		t.Fatalf("ExitCode = %d, want %d; findings=%+v", got, output.ExitSuccess, result.Findings)
	}
}

// A lingering auth-handoff file must not keep doctor reporting a pending
// enrollment after the daemon has already produced authorized runtime state.
// Authorized snapshot evidence outranks the stale handoff.
func TestDoctorTier1CompletedEnrollmentOutranksStaleHandoff(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	env := newDoctorTestEnv(t, []registry.Service{service})
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	env.writeExactSnapshot(t)
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/lingering", env.pid)
	if err := saveAuthHandoff(env.authHandoff, record); err != nil {
		t.Fatalf("saveAuthHandoff: %v", err)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialTier1)
	if !strings.Contains(finding.Message, "has produced authorized runtime state") {
		t.Fatalf("credential_tier1 message = %q, want completed-enrollment evidence despite lingering handoff", finding.Message)
	}
	if strings.Contains(finding.Message, "is pending") {
		t.Fatalf("credential_tier1 message = %q, must not report pending after authorized runtime state", finding.Message)
	}
}

func TestDoctorCredentialBackendFailureClassifiesTierUnknown(t *testing.T) {
	newDoctorTestEnv(t, nil)
	doctorGetAPIKeyFn = func() (string, error) { return "", errors.New("credential backend unavailable") }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }

	result := buildDoctorResult(context.Background(), doctorOptions{})
	if result.CredentialTier != doctorCredentialTierUnknown {
		t.Fatalf("credential tier = %q, want %q", result.CredentialTier, doctorCredentialTierUnknown)
	}
	assertDoctorFinding(t, result, inspect.WarningCodeCredentialReadFailed)
	assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialTier1)
	assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialNone)
	if got := output.ExitCode(doctorExit(result)); got != output.ExitCritical {
		t.Fatalf("ExitCode = %d, want %d; findings=%+v", got, output.ExitCritical, result.Findings)
	}
}

func TestDoctorLegacyAuthKeyWarning(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	if err := os.WriteFile(env.authKeyPath, []byte("tskey-auth-secret"), 0o600); err != nil {
		t.Fatalf("write authkey: %v", err)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	if result.CredentialMode != doctorCredentialLegacyAuthKey {
		t.Fatalf("credential mode = %q, want %q", result.CredentialMode, doctorCredentialLegacyAuthKey)
	}
	assertDoctorFinding(t, result, inspect.WarningCodeCredentialLegacyAuthKey)
	assertDoctorNoFinding(t, result, inspect.WarningCodeCredentialNone)
	assertDoctorCodesRegistered(t, result)
	if err := doctorExit(result); output.ExitCode(err) != output.ExitWarning {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitWarning)
	}
}

func TestDoctorHealthyLoopbackCanExitZero(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	}})
	env.writeExactSnapshot(t)

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorCodesRegistered(t, result)
	if result.Status != doctorStatusOK || result.Counts.Warnings != 0 || result.Counts.Errors != 0 || result.Counts.Critical != 0 {
		t.Fatalf("doctor result = %+v, want clean ok", result)
	}
	if err := doctorExit(result); err != nil {
		t.Fatalf("doctorExit = %v, want nil", err)
	}
}

func TestDoctorJSONSchemaCountsAndRedaction(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		AllowedUsers: []string{"alice@example.com", "bob@example.com"},
	}})
	env.writeExactSnapshot(t)

	var buf bytes.Buffer
	if err := runDoctor(context.Background(), &buf, doctorOptions{}, true); err != nil {
		t.Fatalf("runDoctor JSON returned %v, want nil for info-only findings", err)
	}
	raw := buf.String()
	for _, secret := range []string{"test-only-secret-value", "alice@example.com", "bob@example.com"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("doctor JSON leaked %q: %s", secret, raw)
		}
	}
	result := decodeDoctorJSON(t, raw)
	if result.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %v, want %v", result.SchemaVersion, inspect.SchemaVersion)
	}
	if result.Counts.Findings != len(result.Findings) || result.Counts.Info == 0 {
		t.Fatalf("counts = %+v findings=%d, want info finding counted", result.Counts, len(result.Findings))
	}
	assertDoctorFinding(t, result, inspect.WarningCodeIdentityResolutionUnknown)
	assertDoctorCodesRegistered(t, result)
}

func TestDoctorEvidenceErrorRedactsSecretBearingValues(t *testing.T) {
	evidence := evidenceError(errors.New(`invalid URL "https://user:pass@example.com?auth=tskey-test-secret": token tskey-other-secret`))
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	assertDoctorOutputOmits(t, string(raw), []string{
		"user",
		"pass",
		"https://user:pass@example.com",
		"auth=",
		"tskey-",
	})
	if evidence["error"] == "" {
		t.Fatalf("sanitized evidence error is empty")
	}
}

func TestDoctorRuntimeSnapshotWarnings(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, env doctorTestEnv)
		code  string
	}{
		{
			name: "missing",
			code: inspect.WarningCodeRuntimeSnapshotMissing,
		},
		{
			name: "stale",
			setup: func(t *testing.T, env doctorTestEnv) {
				reg, err := registry.Load(env.regPath)
				if err != nil {
					t.Fatalf("registry.Load: %v", err)
				}
				fingerprint, err := tsruntime.RegistryFingerprint(reg, nil)
				if err != nil {
					t.Fatalf("RegistryFingerprint: %v", err)
				}
				oldStart := env.startedAt.Add(-time.Minute)
				snapshot := tsruntime.NewSnapshot(env.pid, oldStart, fingerprint, oldStart.Add(time.Second), nil)
				if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
					t.Fatalf("runtime.Save: %v", err)
				}
			},
			code: inspect.WarningCodeRuntimeSnapshotStale,
		},
		{
			name: "unreadable",
			setup: func(t *testing.T, env doctorTestEnv) {
				runtimeLoadSnapshotFn = func(string) (*tsruntime.Snapshot, error) {
					return nil, &tsruntime.SnapshotError{
						Status: tsruntime.StatusUnreadable,
						Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
						Err:    os.ErrPermission,
					}
				}
			},
			code: inspect.WarningCodeRuntimeSnapshotUnreadable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			if tc.setup != nil {
				tc.setup(t, env)
			}
			result := buildDoctorResult(context.Background(), doctorOptions{})
			assertDoctorFinding(t, result, tc.code)
			assertDoctorCodesRegistered(t, result)
			if err := doctorExit(result); output.ExitCode(err) != output.ExitWarning {
				t.Fatalf("ExitCode = %d, want %d", output.ExitCode(doctorExit(result)), output.ExitWarning)
			}
		})
	}
}

func TestDoctorTCPBoundaryWarning(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	}})
	env.writeExactSnapshot(t)

	var buf bytes.Buffer
	err := runDoctor(context.Background(), &buf, doctorOptions{}, false)
	if output.ExitCode(err) != output.ExitWarning {
		t.Fatalf("ExitCode = %d, want warning", output.ExitCode(err))
	}
	raw := buf.String()
	if strings.Contains(raw, "HTTP ACL applies") {
		t.Fatalf("doctor output overclaimed TCP ACL behavior: %s", raw)
	}
	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorFinding(t, result, inspect.WarningCodeTCPHTTPACLNotApplicable)
	assertDoctorCodesRegistered(t, result)
}

func TestDoctorExternalTargetsSkipByDefaultAndProbeWhenRequested(t *testing.T) {
	cases := []struct {
		name            string
		svc             registry.Service
		nonLoopbackCode string
	}{
		{
			name: "proxy",
			svc: registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://10.0.0.5:3000",
			},
			nonLoopbackCode: inspect.WarningCodeProxyNonLoopbackTarget,
		},
		{
			name: "tcp",
			svc: registry.Service{
				Name:   "db",
				Type:   registry.TypeTCP,
				Target: "10.0.0.5:5432",
				Port:   5432,
			},
			nonLoopbackCode: inspect.WarningCodeTCPNonLoopbackTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, []registry.Service{tc.svc})
			env.writeExactSnapshot(t)
			called := 0
			doctorProbeTargetFn = func(context.Context, string, time.Duration) error {
				called++
				return nil
			}

			result := buildDoctorResult(context.Background(), doctorOptions{})
			if called != 0 {
				t.Fatalf("probe called by default for external target")
			}
			assertDoctorFinding(t, result, tc.nonLoopbackCode)
			assertDoctorFinding(t, result, inspect.WarningCodeTargetProbeSkippedExternal)
			assertDoctorCodesRegistered(t, result)

			called = 0
			result = buildDoctorResult(context.Background(), doctorOptions{ProbeExternal: true})
			if called != 1 {
				t.Fatalf("probe calls with --probe-external = %d, want 1", called)
			}
			assertDoctorFinding(t, result, tc.nonLoopbackCode)
			assertDoctorNoFinding(t, result, inspect.WarningCodeTargetProbeSkippedExternal)
			assertDoctorCodesRegistered(t, result)
		})
	}
}

func TestDoctorProbeFailureCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{name: "timeout", err: context.DeadlineExceeded, code: inspect.WarningCodeTargetProbeTimeout},
		{name: "refused", err: syscall.ECONNREFUSED, code: inspect.WarningCodeTargetProbeRefused},
		{name: "windows refused", err: &net.OpError{Err: windowsWSAECONNREFUSED}, code: inspect.WarningCodeTargetProbeRefused},
		{name: "windows reset", err: &net.OpError{Err: windowsWSAECONNRESET}, code: inspect.WarningCodeTargetProbeRefused},
		{name: "failed", err: errors.New("boom"), code: inspect.WarningCodeTargetProbeFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, []registry.Service{{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
			}})
			env.writeExactSnapshot(t)
			doctorProbeTargetFn = func(context.Context, string, time.Duration) error {
				return tc.err
			}
			result := buildDoctorResult(context.Background(), doctorOptions{})
			assertDoctorFinding(t, result, tc.code)
			assertDoctorCodesRegistered(t, result)
			if err := doctorExit(result); output.ExitCode(err) != output.ExitCritical {
				t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
			}
		})
	}
}

func TestDoctorFunnelGlobalControlURLWarning(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:      "public-web",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3000",
		Funnel:    true,
		PublicAck: true,
	}})
	env.writeExactSnapshot(t)
	doctorLoadGlobalConfigFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{ControlURL: "https://headscale.example.com"}, nil
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorFinding(t, result, inspect.WarningCodeFunnelGlobalControlURLUnknownCompat)
	assertDoctorCodesRegistered(t, result)
	if err := doctorExit(result); output.ExitCode(err) != output.ExitWarning {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitWarning)
	}
}

func TestDoctorFunnelGuardrailValidationCodes(t *testing.T) {
	cases := []struct {
		name string
		svc  registry.Service
		code string
	}{
		{
			name: "allow conflict",
			svc: registry.Service{
				Name:         "public-web",
				Type:         registry.TypeProxy,
				Target:       "http://localhost:3000",
				Funnel:       true,
				AllowedUsers: []string{"alice@example.com"},
			},
			code: inspect.WarningCodeFunnelAllowConflict,
		},
		{
			name: "control url conflict",
			svc: registry.Service{
				Name:       "public-web",
				Type:       registry.TypeProxy,
				Target:     "http://localhost:3000",
				Funnel:     true,
				ControlURL: "https://headscale.example.com",
			},
			code: inspect.WarningCodeFunnelControlURLConflict,
		},
		{
			name: "type conflict",
			svc: registry.Service{
				Name:   "public-db",
				Type:   registry.TypeTCP,
				Target: "localhost:5432",
				Port:   5432,
				Funnel: true,
			},
			code: inspect.WarningCodeFunnelTypeConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, []registry.Service{tc.svc})
			env.writeExactSnapshot(t)
			result := buildDoctorResult(context.Background(), doctorOptions{})
			assertDoctorFinding(t, result, tc.code)
			assertDoctorCodesRegistered(t, result)
			if err := doctorExit(result); output.ExitCode(err) != output.ExitCritical {
				t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
			}
		})
	}
}

func TestDoctorInvalidControlURLs(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name:       "web",
		Type:       registry.TypeProxy,
		Target:     "http://localhost:3000",
		ControlURL: "not-a-url",
	}})
	env.writeExactSnapshot(t)
	doctorLoadGlobalConfigFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{ControlURL: "ftp://headscale.example.com"}, nil
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorFinding(t, result, inspect.WarningCodeControlURLInvalid)
	assertDoctorCodesRegistered(t, result)
	if err := doctorExit(result); output.ExitCode(err) != output.ExitCritical {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
	}
}

func TestDoctorGlobalInvalidControlURLRedactsRawOutputs(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	doctorLoadGlobalConfigFn = func() (config.GlobalConfig, error) {
		return config.GlobalConfig{ControlURL: "ftp://user:pass@example.com?auth=tskey-test-secret"}, nil
	}
	forbidden := []string{
		// "user:pass" targets the leaked URL userinfo specifically. A bare "user"
		// token would collide with benign credential-posture prose such as the
		// mixed-recommended finding's "user-owned API access token"; the real
		// secret material stays covered by pass, the full URL, auth=, and tskey-.
		"user:pass",
		"pass",
		"ftp://user:pass@example.com",
		"auth=",
		"tskey-",
	}

	var jsonBuf bytes.Buffer
	err := runDoctor(context.Background(), &jsonBuf, doctorOptions{}, true)
	if output.ExitCode(err) != output.ExitCritical {
		t.Fatalf("JSON ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
	}
	assertDoctorOutputOmits(t, jsonBuf.String(), forbidden)

	result := decodeDoctorJSON(t, jsonBuf.String())
	finding := assertDoctorFinding(t, result, inspect.WarningCodeControlURLInvalid)
	evidenceRaw, err := json.Marshal(finding.Evidence)
	if err != nil {
		t.Fatalf("marshal finding evidence: %v", err)
	}
	assertDoctorOutputOmits(t, string(evidenceRaw), forbidden)
	assertDoctorCodesRegistered(t, result)

	var humanBuf bytes.Buffer
	err = runDoctor(context.Background(), &humanBuf, doctorOptions{}, false)
	if output.ExitCode(err) != output.ExitCritical {
		t.Fatalf("human ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
	}
	assertDoctorOutputOmits(t, humanBuf.String(), forbidden)
}

func TestDoctorFileServicePathChecks(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{
		Name: "docs",
		Type: registry.TypeFile,
		Path: filepath.Join(t.TempDir(), "missing"),
	}})
	env.writeExactSnapshot(t)

	result := buildDoctorResult(context.Background(), doctorOptions{})
	assertDoctorFinding(t, result, inspect.WarningCodeFilePathMissing)
	assertDoctorCodesRegistered(t, result)
	if err := doctorExit(result); output.ExitCode(err) != output.ExitCritical {
		t.Fatalf("ExitCode = %d, want %d", output.ExitCode(err), output.ExitCritical)
	}
}

func TestClassifyProbeErrorWithNetOpError(t *testing.T) {
	err := &net.OpError{Err: syscall.ECONNREFUSED}
	if code := classifyProbeError(err); code != inspect.WarningCodeTargetProbeRefused {
		t.Fatalf("classifyProbeError = %s, want %s", code, inspect.WarningCodeTargetProbeRefused)
	}
}

func TestDoctorAuthorityHasExplicitPort(t *testing.T) {
	cases := []struct {
		authority string
		want      bool
	}{
		{"localhost:3000", true},
		{"localhost", false},
		{"[::1]:443", true},
		{"[::1]", false},
		{"2001:db8::1", false},
		{"user:pass@localhost:5432", false},
	}

	for _, tc := range cases {
		if got := authorityHasExplicitPort(tc.authority); got != tc.want {
			t.Fatalf("authorityHasExplicitPort(%q) = %v, want %v", tc.authority, got, tc.want)
		}
	}
}

func TestDoctorClassifyHostPortTargetBranches(t *testing.T) {
	cases := []struct {
		name          string
		authority     string
		defaultPort   string
		wantHost      string
		wantPort      string
		wantProbeAddr string
		wantExternal  bool
		wantErr       string
	}{
		{
			name:          "default local host port",
			authority:     "localhost",
			defaultPort:   "80",
			wantHost:      "localhost",
			wantPort:      "80",
			wantProbeAddr: "localhost:80",
		},
		{
			name:          "unspecified address probes loopback",
			authority:     "0.0.0.0:8080",
			wantHost:      "0.0.0.0",
			wantPort:      "8080",
			wantProbeAddr: "127.0.0.1:8080",
		},
		{
			name:          "bracketed ipv6 external",
			authority:     "[2001:db8::1]:443",
			wantHost:      "2001:db8::1",
			wantPort:      "443",
			wantProbeAddr: "[2001:db8::1]:443",
			wantExternal:  true,
		},
		{
			name:      "missing required port",
			authority: "localhost",
			wantErr:   "missing port in address",
		},
		{
			name:        "explicit malformed port is not defaulted",
			authority:   "localhost:http",
			defaultPort: "80",
			wantErr:     `invalid target port "http"`,
		},
		{
			name:      "empty explicit port",
			authority: "localhost:",
			wantErr:   "missing target port",
		},
		{
			name:      "port out of range",
			authority: "localhost:70000",
			wantErr:   `invalid target port "70000"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyHostPortTarget(tc.authority, tc.defaultPort)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("classifyHostPortTarget() error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("classifyHostPortTarget() error = %v", err)
			}
			if got.Host != tc.wantHost || got.Port != tc.wantPort || got.ProbeAddress != tc.wantProbeAddr || got.External != tc.wantExternal {
				t.Fatalf("doctor target = %+v, want host %q port %q probe %q external %v", got, tc.wantHost, tc.wantPort, tc.wantProbeAddr, tc.wantExternal)
			}
		})
	}
}

func TestDiagnoseFileTargetDirectPathFindings(t *testing.T) {
	resetDoctorSeams(t)

	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	filePath := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	unreadableDir := filepath.Join(dir, "unreadable")
	if err := os.Mkdir(unreadableDir, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	doctorOpenPathFn = func(path string) (io.Closer, error) {
		if path == unreadableDir {
			return nil, errors.New("open blocked")
		}
		return os.Open(path)
	}

	cases := []struct {
		name     string
		path     string
		wantCode string
	}{
		{name: "empty path", wantCode: inspect.WarningCodeFilePathMissing},
		{name: "missing path", path: missing, wantCode: inspect.WarningCodeFilePathMissing},
		{name: "path is file", path: filePath, wantCode: inspect.WarningCodeFilePathUnreadable},
		{name: "directory open error", path: unreadableDir, wantCode: inspect.WarningCodeFilePathUnreadable},
		{name: "readable directory", path: dir},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := DoctorResult{}
			diagnoseFileTarget(&result, registry.Service{
				Name: "docs",
				Type: registry.TypeFile,
				Path: tc.path,
			})
			if tc.wantCode == "" {
				if len(result.Findings) != 0 {
					t.Fatalf("findings = %+v, want none", result.Findings)
				}
				return
			}
			finding := assertDoctorFinding(t, result, tc.wantCode)
			if finding.Service != "docs" || finding.Area != "file" {
				t.Fatalf("finding = %+v, want service docs area file", finding)
			}
		})
	}
}

// TestDoctorTier1CompletedEnrollmentUnderRegistryMismatch reproduces the
// cross-env retest finding: a new service in the registry makes the snapshot
// registry_mismatch, and a lingering handoff used to keep doctor reporting a
// pending enrollment even though the snapshot carries positive runtime
// evidence for the authorized service. completedEnrollment must still outrank
// the stale handoff in that state.
func TestDoctorTier1CompletedEnrollmentUnderRegistryMismatch(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	env := newDoctorTestEnv(t, []registry.Service{service})
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	// Snapshot with a stale fingerprint: simulate a newer registry (e.g. a
	// newly added service awaiting authorization) while the daemon keeps
	// serving the authorized service.
	stale := []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:9999"}}
	states := make([]tsruntime.ServiceState, 0, len(stale))
	for _, svc := range stale {
		states = append(states, tsruntime.ServiceState{
			Service:     svc,
			RuntimeHost: svc.Name + ".tailnet.ts.net",
		})
	}
	snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, "stale-fingerprint-0000", env.startedAt.Add(time.Second), states)
	if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	record := newAuthHandoffRecord("web", "https://login.tailscale.com/a/lingering", env.pid)
	if err := saveAuthHandoff(env.authHandoff, record); err != nil {
		t.Fatalf("saveAuthHandoff: %v", err)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialTier1)
	if !strings.Contains(finding.Message, "has produced authorized runtime state") {
		t.Fatalf("credential_tier1 message = %q, want completed-enrollment evidence despite registry mismatch + lingering handoff", finding.Message)
	}
	if strings.Contains(finding.Message, "is pending") {
		t.Fatalf("credential_tier1 message = %q, must not report pending under registry mismatch after authorized runtime state", finding.Message)
	}
}

// TestDoctorTier1FailedSnapshotIsNotCompletedEnrollment covers a defect an
// earlier review found: a snapshot whose only entry is RuntimeState=failed
// must not count as completed enrollment evidence, mirroring status's
// running-only rule.
func TestDoctorTier1FailedSnapshotIsNotCompletedEnrollment(t *testing.T) {
	service := registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	env := newDoctorTestEnv(t, []registry.Service{service})
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	states := []tsruntime.ServiceState{{
		Service:      service,
		RuntimeHost:  service.Name + ".tailnet.ts.net",
		RuntimeState: tsruntime.ServiceRuntimeFailed,
	}}
	states[0].Error = &tsruntime.ServiceError{Code: "service_start_timeout", Message: "timed out"}
	snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, statusRegistryFingerprint(t, env.regPath), env.startedAt.Add(time.Second), states)
	if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	result := buildDoctorResult(context.Background(), doctorOptions{})
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialTier1)
	if strings.Contains(finding.Message, "has produced authorized runtime state") {
		t.Fatalf("credential_tier1 message = %q, failed service must not count as completed enrollment", finding.Message)
	}
	if !strings.Contains(finding.Message, "waiting for interactive enrollment evidence") {
		t.Fatalf("credential_tier1 message = %q, want running-daemon waiting evidence", finding.Message)
	}
}
