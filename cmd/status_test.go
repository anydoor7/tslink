package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

type statusURLsJSONResponse struct {
	OK      bool             `json:"ok"`
	Command string           `json:"command"`
	Data    StatusURLsResult `json:"data"`
}

func addStatusTestService(t *testing.T, regPath string, svc registry.Service) registry.Service {
	t.Helper()
	if _, err := registry.Add(regPath, svc); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	for _, existing := range reg.Services {
		if existing.Name == svc.Name {
			return existing
		}
	}
	t.Fatalf("service %q not found after add", svc.Name)
	return registry.Service{}
}

func TestStatusJSONReportsOwnershipProofWithoutNodeID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	addStatusTestService(t, regPath, registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	result, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Services) != 1 || result.Services[0].OwnershipProof {
		t.Fatalf("services = %+v, want ownership_proof=false before ledger write", result.Services)
	}
	withoutProof, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withoutProof), `"ownership_proof":false`) {
		t.Fatalf("status JSON = %s, want explicit false", withoutProof)
	}

	ledgerPath := filepath.Join(dir, "node-ownership.json")
	if err := tsruntime.RecordOwnedNode(ledgerPath, "private", "node-status-proof-fixture", time.Now()); err != nil {
		t.Fatal(err)
	}
	result, err = getStatus(pidPath, regPath)
	if err != nil {
		t.Fatal(err)
	}
	withProof, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Services[0].OwnershipProof || !strings.Contains(string(withProof), `"ownership_proof":true`) || strings.Contains(string(withProof), "node-status-proof-fixture") {
		t.Fatalf("status JSON = %s, want proof=true without NodeID", withProof)
	}
}

func TestOwnershipProofsDoNotDependOnRegistryPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	ledgerPath, err := config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := tsruntime.RecordOwnedNode(ledgerPath, "private", "node-private", time.Now()); err != nil {
		t.Fatal(err)
	}

	proofs, available := ownershipProofsForRegistry(filepath.Join(t.TempDir(), "noncanonical-registry.json"))
	if !available || !proofs["private"] {
		t.Fatalf("proofs = %v available=%t, want ownership path independent of registry path", proofs, available)
	}
}

func TestStatusDaemonStateIsAdditiveAndKeepsDaemonRunningSemantics(t *testing.T) {
	oldIsRunning, oldReadPID, oldAbsent := isRunningFn, readPIDFn, isProcessAbsentFromPIDFileFn
	t.Cleanup(func() {
		isRunningFn, readPIDFn, isProcessAbsentFromPIDFileFn = oldIsRunning, oldReadPID, oldAbsent
	})
	readPIDFn = func(string) (int, error) { return 4242, nil }

	for _, tc := range []struct {
		name        string
		running     bool
		absent      bool
		wantState   string
		wantRunning bool
		wantPID     int
	}{
		{name: "running", running: true, wantState: daemonStateRunning, wantRunning: true, wantPID: 4242},
		{name: "absent", absent: true, wantState: daemonStateAbsent},
		{name: "unknown", wantState: daemonStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isRunningFn = func(string) bool { return tc.running }
			isProcessAbsentFromPIDFileFn = func(string) bool { return tc.absent }
			result := baseStatus("isolated.pid")
			if result.DaemonState != tc.wantState || result.DaemonRunning != tc.wantRunning || result.DaemonPID != tc.wantPID {
				t.Fatalf("daemon status = state:%q running:%t pid:%d, want state:%q running:%t pid:%d", result.DaemonState, result.DaemonRunning, result.DaemonPID, tc.wantState, tc.wantRunning, tc.wantPID)
			}
			wire, err := json.Marshal(result)
			if err != nil || !bytes.Contains(wire, []byte(`"daemon_state":"`+tc.wantState+`"`)) {
				t.Fatalf("status JSON = %s, err=%v", wire, err)
			}
		})
	}
}

func TestStatusOwnershipProofAvailabilityDistinguishesMissingProofFromUnreadableLedger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	pidPath, err := config.PIDPath()
	if err != nil {
		t.Fatal(err)
	}
	addStatusTestService(t, regPath, registry.Service{Name: "private", Type: registry.TypeProxy, Target: "http://localhost:3000"})

	missing, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.OwnershipProofAvailable || missing.Services[0].OwnershipProof {
		t.Fatalf("missing proof status = available:%t services:%+v, want readable ledger with no proof", missing.OwnershipProofAvailable, missing.Services)
	}

	ledgerPath, err := config.NodeOwnershipPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, []byte(`{"schema_version":2,"nodes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatal(err)
	}
	if unreadable.OwnershipProofAvailable || unreadable.Services[0].OwnershipProof {
		t.Fatalf("unreadable proof status = available:%t services:%+v, want distinguishable unavailable ledger", unreadable.OwnershipProofAvailable, unreadable.Services)
	}
}

func TestStatusOwnershipProofPathComesFromConfigNotRegistrySibling(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, configDir)
	foreignDir := t.TempDir()
	foreignRegistryPath := filepath.Join(foreignDir, "registry.json")
	foreignLedgerPath := filepath.Join(foreignDir, "node-ownership.json")
	if err := tsruntime.RecordOwnedNode(foreignLedgerPath, "private", "node-foreign-sibling", time.Now()); err != nil {
		t.Fatal(err)
	}

	proofs, available := ownershipProofsForRegistry(foreignRegistryPath)
	if !available || proofs["private"] {
		t.Fatalf("proof availability=%t proofs=%v, want readable canonical ledger without consuming foreign sibling", available, proofs)
	}
}

func statusRegistryFingerprint(t *testing.T, regPath string) string {
	t.Helper()
	reg, err := registry.Load(regPath)
	if err != nil {
		t.Fatalf("registry.Load: %v", err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatalf("RegistryFingerprint: %v", err)
	}
	return fingerprint
}

func withStatusURLSeams(t *testing.T, running bool, pid int, lowerBound time.Time) {
	t.Helper()
	oldIsRunning := isRunningFn
	oldReadPID := readPIDFn
	oldGetAPIKey := getAPIKeyFn
	oldHasClientSecret := hasClientSecretFn
	oldPIDFileModTime := pidFileModTimeFn
	t.Cleanup(func() {
		isRunningFn = oldIsRunning
		readPIDFn = oldReadPID
		getAPIKeyFn = oldGetAPIKey
		hasClientSecretFn = oldHasClientSecret
		pidFileModTimeFn = oldPIDFileModTime
	})

	isRunningFn = func(string) bool { return running }
	readPIDFn = func(string) (int, error) { return pid, nil }
	getAPIKeyFn = func() (string, error) { return "", nil }
	hasClientSecretFn = func() bool { return false }
	pidFileModTimeFn = func(string) (time.Time, error) { return lowerBound, nil }
}

func findStatusService(t *testing.T, result StatusURLsResult, name string) StatusServiceView {
	t.Helper()
	for _, svc := range result.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("service %q not found in status result", name)
	return StatusServiceView{}
}

func hasStatusWarningCode(warnings []inspect.WarningView, code string) bool {
	for _, warning := range warnings {
		if warning.Code == code {
			return true
		}
	}
	return false
}

func TestStatusURLsExactSnapshotUsesRuntimeEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "web.tailnet.ts.net."},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if !result.RuntimeSnapshot.Exact || result.RuntimeSnapshot.Status != tsruntime.StatusExact {
		t.Fatalf("runtime freshness = %+v, want exact", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.Display != "https://web.tailnet.ts.net" || web.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want exact runtime URL", web.Endpoint)
	}
	if web.Exposure.Kind != inspect.ExposureTailnet {
		t.Fatalf("exposure = %+v, want tailnet", web.Exposure)
	}
}

func TestStatusAndListLifecycleFieldsExposeRFC3339DeadlineAndRemaining(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	expires := now.Add(2 * time.Hour)
	addStatusTestService(t, regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &expires})
	withStatusURLSeams(t, false, 0, time.Time{})
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return now }
	t.Cleanup(func() { statusNowFn = oldNow })

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	svc := findStatusService(t, result, "public")
	if svc.FunnelExpiresAt == nil || svc.FunnelExpiresAt.Format(time.RFC3339) != "2026-08-31T14:00:00Z" || svc.FunnelRemaining == nil || *svc.FunnelRemaining != "2h0m0s" {
		t.Fatalf("status lifecycle = expires %v remaining %v", svc.FunnelExpiresAt, svc.FunnelRemaining)
	}
	summary := listSummary(svc)
	if summary.FunnelExpiresAt == nil || summary.FunnelRemaining == nil || *summary.FunnelRemaining != "2h0m0s" {
		t.Fatalf("list lifecycle = %+v", summary)
	}
}

func TestExpiredFunnelWallClockOverridesPreDeadlineActiveSnapshot(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	deadline := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{Name: "public", Type: registry.TypeProxy, Target: "http://localhost:3000", Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline})
	started := deadline.Add(-time.Hour)
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, started, fingerprint, deadline.Add(-time.Second), []tsruntime.ServiceState{{Service: svc, RuntimeHost: "public.tailnet.ts.net."}})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, started)
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return deadline.Add(time.Second) }
	t.Cleanup(func() { statusNowFn = oldNow })
	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	got := findStatusService(t, result, "public")
	if got.FunnelRequested || got.FunnelActive || got.FunnelState != tsruntime.FunnelStateNotRequested || got.Exposure.Public || got.Endpoint.Kind != inspect.EndpointKindHTTPS {
		t.Fatalf("expired status = %+v, want wall-clock tailnet-only despite active snapshot", got)
	}
}

func TestStatusWithoutRuntimeUsesEffectiveExpiredFunnelState(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	now := time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(-time.Minute)
	if _, err := registry.Add(regPath, registry.Service{
		Name: "expired", Type: registry.TypeProxy, Target: "http://localhost:3000",
		Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline,
	}); err != nil {
		t.Fatal(err)
	}
	oldNow := statusNowFn
	statusNowFn = func() time.Time { return now }
	t.Cleanup(func() { statusNowFn = oldNow })
	result, err := getStatus(pidPath, regPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Services) != 1 || result.Services[0].FunnelRequested || result.Services[0].FunnelState != tsruntime.FunnelStateNotRequested {
		t.Fatalf("status = %+v, want effective tailnet-only state", result.Services)
	}
}

func TestStatusAndListExposeFunnelFailureState(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:      "public-app",
		Type:      registry.TypeProxy,
		Target:    "http://localhost:3000",
		Funnel:    true,
		PublicAck: true,
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{{
		Service:      svc,
		RuntimeState: tsruntime.ServiceRuntimeFailed,
		FunnelState:  tsruntime.FunnelStateCapabilityMissing,
		Error: &tsruntime.ServiceError{
			Code:    registry.CodeFunnelCapabilityMissing,
			Message: "nodeAttr funnel is missing",
			Next:    []string{"fix Access controls Funnel policy"},
			Provision: &registry.ProvisionOutcome{
				Attempted: true, Target: registry.FunnelTag, Changed: false,
				Reason: registry.ProvisionReasonNetmapTimeout, WriteOutcome: "unchanged",
			},
		},
	}})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if len(status.Services) != 1 {
		t.Fatalf("status services = %+v", status.Services)
	}
	statusService := status.Services[0]
	if statusService.Status != tsruntime.ServiceRuntimeFailed || !statusService.FunnelRequested || statusService.FunnelActive || statusService.FunnelState != tsruntime.FunnelStateCapabilityMissing || statusService.Error == nil || len(statusService.Error.Next) == 0 {
		t.Fatalf("status service = %+v, want actionable Funnel failure", statusService)
	}
	if statusService.Error.Provision == nil || statusService.Error.Provision.Reason != registry.ProvisionReasonNetmapTimeout || statusService.Error.Provision.Target != registry.FunnelTag {
		t.Fatalf("status provisioning = %+v, want machine-readable netmap timeout", statusService.Error.Provision)
	}

	urls, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	detailed := findStatusService(t, urls, svc.Name)
	if detailed.RuntimeState != tsruntime.ServiceRuntimeFailed || detailed.Endpoint.State != statusEndpointStateMissing || detailed.FunnelActive || detailed.FunnelState != tsruntime.FunnelStateCapabilityMissing || detailed.Error == nil {
		t.Fatalf("status --urls service = %+v", detailed)
	}

	listed, err := loadListResultForPaths(regPath, pidPath, snapshotPath, listOptions{})
	if err != nil {
		t.Fatalf("loadListResultForPaths: %v", err)
	}
	services, ok := listed.Services.([]ListServiceSummary)
	if !ok || len(services) != 1 {
		t.Fatalf("list services = %#v", listed.Services)
	}
	listedService := services[0]
	if listedService.State != tsruntime.ServiceRuntimeFailed || !listedService.FunnelRequested || listedService.FunnelActive || listedService.FunnelState != tsruntime.FunnelStateCapabilityMissing || listedService.Error == nil || listedService.URL != nil || !listedService.URLPending {
		t.Fatalf("list service = %+v, want no URL and actionable Funnel failure", listedService)
	}
	wire, err := json.Marshal(listedService)
	if err != nil {
		t.Fatalf("json.Marshal(list service): %v", err)
	}
	for _, field := range []string{"funnel_requested", "funnel_active", "funnel_state", "error", "provision", "attempted", "target", "changed", "reason", "write_outcome"} {
		if !strings.Contains(string(wire), `"`+field+`"`) {
			t.Fatalf("list JSON = %s, missing %s", wire, field)
		}
	}
}

func TestPollableStatusExposesGlobalRuntimeErrorWhenRegistryIsInvalid(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	if err := os.WriteFile(regPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	snapshot := tsruntime.NewPartialSnapshot(4242, startedAt, "sha256:last-good", startedAt.Add(time.Second), []tsruntime.ServiceState{{
		Service:      registry.Service{Name: "private-app", Type: registry.TypeProxy, Target: "http://localhost:3000"},
		RuntimeState: tsruntime.ServiceRuntimeRunning,
	}})
	snapshot.GlobalError = &tsruntime.ServiceError{
		Code:    registry.CodeRegistryReloadInvalid,
		Message: "registry reload remained invalid after bounded re-read",
		Next:    []string{"tslink registry check --json"},
	}
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus() error = %v", err)
	}
	if result.GlobalError == nil || result.GlobalError.Code != registry.CodeRegistryReloadInvalid || len(result.GlobalError.Next) != 1 {
		t.Fatalf("global_error = %+v", result.GlobalError)
	}
	if result.ServiceCount != 1 || len(result.Services) != 1 || result.Services[0].Name != "private-app" || result.Services[0].Status != tsruntime.ServiceRuntimeRunning {
		t.Fatalf("fallback status = %+v", result)
	}
	result.GlobalError.Next[0] = "mutated"
	loaded, err := tsruntime.Load(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.GlobalError.Next[0] != "tslink registry check --json" {
		t.Fatal("status result aliased persisted global_error recovery data")
	}
}

func TestStatusNotAuthenticatedIncludesMachineContinuation(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	addStatusTestService(t, regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	withStatusURLSeams(t, false, 0, time.Time{})

	status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatal(err)
	}
	if status.AuthStatus != authStatusNotAuthenticated || !reflect.DeepEqual(status.Next, []string{"tslink serve --json"}) {
		t.Fatalf("status = %+v, want not_authenticated with serve continuation", status)
	}
	urls, err := getStatusURLsWithAuth(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(urls.Next, status.Next) {
		t.Fatalf("status --urls next = %v, want %v", urls.Next, status.Next)
	}
	wire, err := json.Marshal(status)
	if err != nil || !bytes.Contains(wire, []byte(`"next":["tslink serve --json"]`)) {
		t.Fatalf("wire=%s err=%v", wire, err)
	}
}

func TestPollableStatusZeroCredentialTransitionsFromNeedsLoginToAuthenticated(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "web",
			AuthURL:       "https://login.tailscale.com/a/status-auth",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	needsLogin, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus(needs_login): %v", err)
	}
	if needsLogin.Authenticated || needsLogin.NodeAuthorized || needsLogin.AuthorizedServiceCount != 0 || needsLogin.AuthStatus != authStatusNeedsLogin {
		t.Fatalf("auth state = authenticated:%v status:%q, want needs_login", needsLogin.Authenticated, needsLogin.AuthStatus)
	}
	if needsLogin.AuthURL != "https://login.tailscale.com/a/status-auth" || needsLogin.ExpiresAt == nil {
		t.Fatalf("auth handoff = url:%q expiry:%v, want pollable URL and expiry", needsLogin.AuthURL, needsLogin.ExpiresAt)
	}
	if len(needsLogin.Services) != 1 || needsLogin.Services[0].Name != "web" || needsLogin.Services[0].Status != authStatusNeedsLogin {
		t.Fatalf("services = %+v, want web needs_login", needsLogin.Services)
	}

	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "web.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	authenticated, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus(authenticated): %v", err)
	}
	if !authenticated.Authenticated || !authenticated.NodeAuthorized || authenticated.AuthorizedServiceCount != 1 || authenticated.AuthStatus != authStatusAuthenticated {
		t.Fatalf("auth state = authenticated:%v status:%q, want authenticated", authenticated.Authenticated, authenticated.AuthStatus)
	}
	if authenticated.AuthURL != "" || authenticated.ExpiresAt != nil {
		t.Fatalf("completed auth leaked stale handoff: url:%q expiry:%v", authenticated.AuthURL, authenticated.ExpiresAt)
	}
	if len(authenticated.Services) != 1 || authenticated.Services[0].Status != "up" {
		t.Fatalf("services = %+v, want web up", authenticated.Services)
	}
}

func TestPollableStatusSurfacesPendingHandoffWithoutRunningDaemon(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000"})
	withStatusURLSeams(t, false, 0, startedAt)
	getAPIKeyFn = func() (string, error) { return "stored-credential-present", nil }

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "web",
			AuthURL:       "https://login.tailscale.com/a/daemon-stopped",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	result, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if result.DaemonRunning || result.Authenticated || !result.CredentialStored || result.NodeAuthorized || result.AuthorizedServiceCount != 0 {
		t.Fatalf("status distinctions = %+v", result)
	}
	if result.AuthStatus != authStatusNeedsLogin || result.AuthURL != "https://login.tailscale.com/a/daemon-stopped" || result.ExpiresAt == nil {
		t.Fatalf("pending handoff = %+v", result)
	}
}

func TestPollableStatusShowsEarlierServicesWhileNextNeedsLogin(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	first := addStatusTestService(t, regPath, registry.Service{
		Name: "first", Type: registry.TypeFile, Path: t.TempDir(),
	})
	addStatusTestService(t, regPath, registry.Service{
		Name: "second", Type: registry.TypeFile, Path: t.TempDir(),
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewPartialSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: first, RuntimeHost: "first.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "second",
			AuthURL:       "https://login.tailscale.com/a/second-auth",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	result, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	// A pending handoff for "second" must not erase the authorized evidence of
	// the already-running "first": the authorized count stays, and only the
	// handoff's own service falls back to needs_login.
	if !result.Authenticated || !result.NodeAuthorized || result.AuthorizedServiceCount != 1 || result.AuthStatus != authStatusNeedsLogin || result.AuthURL == "" {
		t.Fatalf("auth state = authenticated:%v node_authorized:%v count:%d status:%q url:%q, want first service retained with second handoff",
			result.Authenticated, result.NodeAuthorized, result.AuthorizedServiceCount, result.AuthStatus, result.AuthURL)
	}
	want := map[string]string{"first": "up", "second": authStatusNeedsLogin}
	for _, service := range result.Services {
		if service.Status != want[service.Name] {
			t.Fatalf("service %q status = %q, want %q (all=%+v)", service.Name, service.Status, want[service.Name], result.Services)
		}
	}
}

func TestStatusURLsPartialSnapshotUsesReportedServicesWithoutAuthoritativeOmissions(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	first := addStatusTestService(t, regPath, registry.Service{
		Name: "first", Type: registry.TypeFile, Path: t.TempDir(),
	})
	addStatusTestService(t, regPath, registry.Service{
		Name: "second", Type: registry.TypeFile, Path: t.TempDir(),
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	partial := tsruntime.NewPartialSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: first, RuntimeHost: "first.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, partial); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if result.RuntimeSnapshot.Exact || result.RuntimeSnapshot.Status != tsruntime.StatusPartial {
		t.Fatalf("runtime freshness = %+v, want non-authoritative partial", result.RuntimeSnapshot)
	}
	firstView := findStatusService(t, result, "first")
	if firstView.Endpoint.Display != "https://first.tailnet.ts.net" || firstView.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("first endpoint = %+v, want positive partial-snapshot evidence", firstView.Endpoint)
	}
	secondView := findStatusService(t, result, "second")
	if secondView.Endpoint.Display != "" || secondView.Endpoint.Host != "" || secondView.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("second endpoint = %+v, want not-yet-reported state without placeholder", secondView.Endpoint)
	}
}

func TestPollableStatusRejectsAuthHandoffFromDifferentDaemonPID(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{
		Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000",
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "web",
			AuthURL:       "https://login.tailscale.com/a/stale-daemon",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     31337,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	result, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if result.Authenticated || result.AuthStatus != authStatusNotAuthenticated {
		t.Fatalf("auth state = authenticated:%v status:%q, want stale handoff ignored", result.Authenticated, result.AuthStatus)
	}
	if result.AuthURL != "" || result.ExpiresAt != nil {
		t.Fatalf("stale daemon handoff leaked: url=%q expires_at=%v", result.AuthURL, result.ExpiresAt)
	}
}

func TestStatusURLsMissingSnapshotFallsBackToExpectedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotMissing {
		t.Fatalf("runtime snapshot = %+v, want missing code", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.Display != "" || web.Endpoint.Host != "" || web.Endpoint.State != statusEndpointStateMissing {
		t.Fatalf("endpoint = %+v, want missing state without placeholder", web.Endpoint)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}
}

// A pending node appended to the registry moves the snapshot into a
// registry_mismatch state. That must not erase the running evidence of the
// services the daemon is already serving, nor a pending handoff for one service
// suppress the authorized state of the others.
func TestStatusURLsRegistryMismatchKeepsRunningServiceEvidence(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	authorized := addStatusTestService(t, regPath, registry.Service{
		Name:   "tst-913a",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	addStatusTestService(t, regPath, registry.Service{
		Name:   "tst-913b",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3001",
	})
	// The snapshot only knows about the authorized service and was written for
	// the pre-pending-node registry, so its fingerprint no longer matches.
	snapshot := tsruntime.NewSnapshot(4242, startedAt, "sha256:stale-before-pending", startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: authorized, RuntimeHost: "tst-913a.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	oldLoadHandoff := statusLoadAuthHandoffFn
	statusLoadAuthHandoffFn = func(string) (authHandoffRecord, error) {
		return authHandoffRecord{
			SchemaVersion: authHandoffSchemaVersion,
			Status:        authStatusNeedsLogin,
			Service:       "tst-913b",
			AuthURL:       "https://login.tailscale.com/a/tst-913b",
			ExpiresAt:     startedAt.Add(authHandoffConservativeLifetime),
			Poll:          "tslink status --json",
			DaemonPID:     4242,
		}, nil
	}
	t.Cleanup(func() { statusLoadAuthHandoffFn = oldLoadHandoff })

	status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if !status.Authenticated || !status.NodeAuthorized || status.AuthorizedServiceCount != 1 {
		t.Fatalf("auth state = authenticated:%t node_authorized:%t count:%d, want authorized service retained",
			status.Authenticated, status.NodeAuthorized, status.AuthorizedServiceCount)
	}
	if status.AuthStatus != authStatusNeedsLogin || status.AuthURL == "" {
		t.Fatalf("pending handoff lost: status=%q url=%q", status.AuthStatus, status.AuthURL)
	}
	wants := map[string]string{"tst-913a": "up", "tst-913b": authStatusNeedsLogin}
	for _, svc := range status.Services {
		if svc.Status != wants[svc.Name] {
			t.Fatalf("service %q status = %q, want %q (all=%+v)", svc.Name, svc.Status, wants[svc.Name], status.Services)
		}
	}

	urls, err := getStatusURLsWithAuth(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getStatusURLsWithAuth: %v", err)
	}
	if urls.RuntimeSnapshot.Status != tsruntime.StatusRegistryMismatch {
		t.Fatalf("freshness = %+v, want registry_mismatch", urls.RuntimeSnapshot)
	}
	upView := findStatusService(t, urls, "tst-913a")
	if upView.RuntimeState != tsruntime.ServiceRuntimeRunning || upView.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("authorized service = %+v, want retained running evidence", upView)
	}
	if !hasStatusWarningCode(upView.Warnings, inspect.WarningCodeRuntimeSnapshotStale) {
		t.Fatalf("warnings = %+v, want stale warning alongside positive evidence", upView.Warnings)
	}
}

func TestStatusURLsStaleEvidenceFallsBackToExpectedEndpoint(t *testing.T) {
	cases := []struct {
		name              string
		snapshotPID       int
		snapshotStarted   time.Time
		snapshotUpdated   time.Time
		fingerprint       func(*testing.T, string) string
		wantStatus        string
		wantEndpointState string
	}{
		{
			name:            "registry fingerprint mismatch",
			snapshotPID:     4242,
			snapshotStarted: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			snapshotUpdated: time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
			fingerprint:     func(*testing.T, string) string { return "sha256:other" },
			wantStatus:      tsruntime.StatusRegistryMismatch,
			// A registry mismatch still carries positive running evidence for
			// services present in the snapshot: P3 keeps that evidence visible
			// rather than letting a newly added pending node erase it.
			wantEndpointState: inspect.EndpointStateExact,
		},
		{
			name:              "pid mismatch",
			snapshotPID:       9999,
			snapshotStarted:   time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
			snapshotUpdated:   time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
			fingerprint:       statusRegistryFingerprint,
			wantStatus:        tsruntime.StatusPIDMismatch,
			wantEndpointState: statusEndpointStateStale,
		},
		{
			name:              "stale freshness",
			snapshotPID:       4242,
			snapshotStarted:   time.Date(2026, 5, 17, 11, 59, 0, 0, time.UTC),
			snapshotUpdated:   time.Date(2026, 5, 17, 11, 59, 30, 0, time.UTC),
			fingerprint:       statusRegistryFingerprint,
			wantStatus:        tsruntime.StatusStale,
			wantEndpointState: statusEndpointStateStale,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			pidPath := filepath.Join(dir, "tslink.pid")
			snapshotPath := filepath.Join(dir, "runtime.json")
			lowerBound := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
			svc := addStatusTestService(t, regPath, registry.Service{
				Name:   "web",
				Type:   registry.TypeProxy,
				Target: "http://localhost:3000",
			})
			snapshot := tsruntime.NewSnapshot(tc.snapshotPID, tc.snapshotStarted, tc.fingerprint(t, regPath), tc.snapshotUpdated, []tsruntime.ServiceState{
				{Service: svc, RuntimeHost: "web.tailnet.ts.net"},
			})
			if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
				t.Fatalf("runtime.Save: %v", err)
			}
			withStatusURLSeams(t, true, 4242, lowerBound)

			result, err := getStatusURLs(pidPath, regPath, snapshotPath)
			if err != nil {
				t.Fatalf("getStatusURLs: %v", err)
			}
			if result.RuntimeSnapshot.Status != tc.wantStatus || result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotStale {
				t.Fatalf("runtime snapshot = %+v, want status %s stale code", result.RuntimeSnapshot, tc.wantStatus)
			}
			web := findStatusService(t, result, "web")
			if web.Endpoint.State != tc.wantEndpointState {
				t.Fatalf("endpoint = %+v, want state %q", web.Endpoint, tc.wantEndpointState)
			}
			if tc.wantEndpointState != inspect.EndpointStateExact && (web.Endpoint.Display != "" || web.Endpoint.Host != "") {
				t.Fatalf("endpoint = %+v, want no placeholder outside positive evidence", web.Endpoint)
			}
			if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotStale) {
				t.Fatalf("warnings = %+v, want runtime_snapshot_stale", web.Warnings)
			}
		})
	}
}

func TestStatusURLsUnreadableSnapshotFallsBackToExpectedEndpoint(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	addStatusTestService(t, regPath, registry.Service{
		Name:   "web",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
	})
	withStatusURLSeams(t, true, 4242, startedAt)
	oldLoad := runtimeLoadSnapshotFn
	runtimeLoadSnapshotFn = func(string) (*tsruntime.Snapshot, error) {
		return nil, &tsruntime.SnapshotError{
			Status: tsruntime.StatusUnreadable,
			Code:   inspect.WarningCodeRuntimeSnapshotUnreadable,
			Err:    os.ErrPermission,
		}
	}
	t.Cleanup(func() { runtimeLoadSnapshotFn = oldLoad })

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	if result.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotUnreadable {
		t.Fatalf("runtime snapshot = %+v, want unreadable code", result.RuntimeSnapshot)
	}
	web := findStatusService(t, result, "web")
	if web.Endpoint.State != statusEndpointStateUnknown {
		t.Fatalf("endpoint = %+v, want unknown state", web.Endpoint)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotUnreadable) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_unreadable", web.Warnings)
	}
}

func TestStatusURLsTCPRendersRawHostPort(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{
		Name:   "db",
		Type:   registry.TypeTCP,
		Target: "localhost:5432",
		Port:   5432,
	})
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, startedAt, fingerprint, startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: "db.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	result, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatalf("getStatusURLs: %v", err)
	}
	db := findStatusService(t, result, "db")
	if db.Endpoint.Display != "db.tailnet.ts.net:5432" || db.Endpoint.State != inspect.EndpointStateExact {
		t.Fatalf("endpoint = %+v, want raw exact TCP host:port", db.Endpoint)
	}
	if strings.Contains(db.Endpoint.Display, "https://") {
		t.Fatalf("TCP endpoint rendered as HTTPS: %+v", db.Endpoint)
	}
}

func TestStatusURLsJSONIncludesSchemaWarningsAndRedactedAllow(t *testing.T) {
	resetRootJSONFlag(t)
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	addStatusTestService(t, regPath, registry.Service{
		Name:         "web",
		Type:         registry.TypeProxy,
		Target:       "http://localhost:3000",
		Tags:         []string{"tag:tsmain"},
		AllowedUsers: []string{"alice@example.com", "bob@example.com"},
	})
	withStatusURLSeams(t, false, 0, time.Time{})

	oldPIDPath := statusPIDPathFn
	oldRegistryPath := statusRegistryPathFn
	oldSnapshotPath := statusRuntimeSnapshotPathFn
	t.Cleanup(func() {
		statusPIDPathFn = oldPIDPath
		statusRegistryPathFn = oldRegistryPath
		statusRuntimeSnapshotPathFn = oldSnapshotPath
		rootCmd.SetArgs(nil)
		_ = statusCmd.Flags().Set("urls", "false")
	})
	statusPIDPathFn = func() (string, error) { return pidPath, nil }
	statusRegistryPathFn = func() (string, error) { return regPath, nil }
	statusRuntimeSnapshotPathFn = func() (string, error) { return snapshotPath, nil }
	rootCmd.SetArgs([]string{"status", "--urls", "--json"})

	raw := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("root execute: %v", err)
		}
	})
	var resp statusURLsJSONResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal status --urls --json response: %v\nraw: %s", err, raw)
	}
	if !resp.OK || resp.Command != "status" {
		t.Fatalf("response envelope = %+v, want ok status", resp)
	}
	if resp.Data.SchemaVersion != inspect.SchemaVersion {
		t.Fatalf("schema_version = %q, want %q", resp.Data.SchemaVersion, inspect.SchemaVersion)
	}
	if resp.Data.RuntimeSnapshot.Code != inspect.WarningCodeRuntimeSnapshotMissing {
		t.Fatalf("runtime snapshot = %+v, want missing code", resp.Data.RuntimeSnapshot)
	}
	web := findStatusService(t, resp.Data, "web")
	if web.Allow.Count != 2 || !web.Allow.Redacted || len(web.Allow.Entries) != 0 {
		t.Fatalf("allow summary = %+v, want redacted count-only summary", web.Allow)
	}
	if !hasStatusWarningCode(web.Warnings, inspect.WarningCodeRuntimeSnapshotMissing) {
		t.Fatalf("warnings = %+v, want runtime_snapshot_missing", web.Warnings)
	}
}

func TestFormatStatusURLsHumanOutput(t *testing.T) {
	var buf bytes.Buffer
	formatStatusURLs(StatusURLsResult{
		DaemonRunning: true,
		DaemonPID:     4242,
		Authenticated: true,
		ServiceCount:  2,
		RuntimeSnapshot: StatusRuntimeSnapshotResult{
			Status: tsruntime.StatusStale,
			Code:   inspect.WarningCodeRuntimeSnapshotStale,
		},
		Services: []StatusServiceView{
			{
				Name: "web",
				Type: registry.TypeProxy,
				Endpoint: inspect.EndpointView{
					Display: "https://web.tailnet.ts.net",
					State:   inspect.EndpointStateExact,
				},
				Exposure: inspect.ExposureView{Kind: inspect.ExposureTailnetAllow},
				Allow:    inspect.SummaryView{Mode: "restricted", Count: 2, Redacted: true},
				Tags:     inspect.SummaryView{Mode: "configured", Entries: []string{"tag:web"}},
				Backend:  inspect.BackendView{Display: "http://localhost:3000"},
				Warnings: []inspect.WarningView{{Code: inspect.WarningCodeRuntimeSnapshotStale}},
			},
			{
				Name:     "docs",
				Type:     registry.TypeFile,
				Endpoint: inspect.EndpointView{},
				Exposure: inspect.ExposureView{},
				Allow:    inspect.SummaryView{Count: 0},
				Tags:     inspect.SummaryView{Mode: "configured"},
				Backend:  inspect.BackendView{},
			},
		},
	}, &buf)

	raw := buf.String()
	for _, want := range []string{
		"tslink: running (pid 4242)",
		"tailnet: authenticated",
		"services: 2 registered",
		"runtime snapshot: stale (runtime_snapshot_stale)",
		"NAME",
		"web",
		"restricted(2 redacted)",
		"configured:tag:web",
		inspect.WarningCodeRuntimeSnapshotStale,
		"docs",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("formatStatusURLs output missing %q:\n%s", want, raw)
		}
	}
}

func TestFormatStatusURLsNoServices(t *testing.T) {
	var buf bytes.Buffer
	formatStatusURLs(StatusURLsResult{
		RuntimeSnapshot: StatusRuntimeSnapshotResult{Status: tsruntime.StatusMissing},
	}, &buf)

	raw := buf.String()
	if !strings.Contains(raw, "runtime snapshot: missing") || !strings.Contains(raw, "service urls: none") {
		t.Fatalf("formatStatusURLs no-services output = %q, want runtime snapshot and none marker", raw)
	}
}

func TestStatusSummaryLabelAndWarningCodes(t *testing.T) {
	summaryCases := []struct {
		name string
		in   inspect.SummaryView
		want string
	}{
		{name: "redacted without mode", in: inspect.SummaryView{Count: 2, Redacted: true}, want: "2 redacted"},
		{name: "redacted with mode", in: inspect.SummaryView{Mode: "restricted", Count: 2, Redacted: true}, want: "restricted(2 redacted)"},
		{name: "entries without mode", in: inspect.SummaryView{Entries: []string{"a", "b"}}, want: "configured:a,b"},
		{name: "entries with mode", in: inspect.SummaryView{Mode: "tags", Entries: []string{"tag:a"}}, want: "tags:tag:a"},
		{name: "mode only", in: inspect.SummaryView{Mode: "all_tailnet"}, want: "all_tailnet"},
		{name: "count only", in: inspect.SummaryView{Count: 3}, want: "3"},
	}
	for _, tc := range summaryCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := summaryLabel(tc.in); got != tc.want {
				t.Fatalf("summaryLabel(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	if got := warningCodes(nil); got != "-" {
		t.Fatalf("warningCodes(nil) = %q, want -", got)
	}
	warnings := []inspect.WarningView{
		{Code: inspect.WarningCodeRuntimeSnapshotMissing},
		{Code: inspect.WarningCodeTCPHTTPACLNotApplicable},
	}
	if got := warningCodes(warnings); got != inspect.WarningCodeRuntimeSnapshotMissing+","+inspect.WarningCodeTCPHTTPACLNotApplicable {
		t.Fatalf("warningCodes() = %q, want joined codes", got)
	}
}

func TestStatusURLsReturnsRegistryLoadError(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(regPath, []byte("{bad"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withStatusURLSeams(t, false, 0, time.Time{})

	_, err := getStatusURLs(pidPath, regPath, snapshotPath)
	if err == nil {
		t.Fatal("getStatusURLs error = nil, want registry load error")
	}
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("getStatusURLs error = %T %[1]v, want JSON syntax error", err)
	}
}

func TestFilterStatusURLsResultByName(t *testing.T) {
	input := StatusURLsResult{
		ServiceCount: 2,
		Services: []StatusServiceView{
			{Name: "web", Type: registry.TypeProxy},
			{Name: "db", Type: registry.TypeTCP},
		},
	}
	got, err := filterStatusURLsResult(input, "db")
	if err != nil {
		t.Fatalf("filterStatusURLsResult: %v", err)
	}
	if got.ServiceCount != 1 || len(got.Services) != 1 || got.Services[0].Name != "db" {
		t.Fatalf("filtered result = %+v, want db only", got)
	}
	if _, err := filterStatusURLsResult(input, "missing"); err == nil {
		t.Fatal("missing name error = nil")
	}
	if _, err := filterStatusURLsResult(input, "BAD_NAME"); err == nil {
		t.Fatal("invalid name error = nil")
	}
}

// TestPollableStatusRemovalMismatchDoesNotInflateCount covers the mismatch
// window when the fingerprint mismatch was caused by a service REMOVAL: the
// snapshot still lists the removed service as running, but it must not count
// toward AuthorizedServiceCount or the authenticated flag because it is no
// longer in the registry.
func TestPollableStatusRemovalMismatchDoesNotInflateCount(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	handoffPath := filepath.Join(dir, "auth-handoff.json")
	startedAt := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	removed := addStatusTestService(t, regPath, registry.Service{
		Name:   "gone",
		Type:   registry.TypeProxy,
		Target: "http://localhost:3000",
		Tags:   []string{"tag:tsmain"},
	})
	// Remove it from the registry so the current registry no longer has it,
	// but keep the snapshot (written earlier) listing it as running.
	if _, err := registry.Remove(regPath, "gone"); err != nil {
		t.Fatalf("registry.Remove: %v", err)
	}
	snapshot := tsruntime.NewSnapshot(4242, startedAt, "sha256:stale-before-removal", startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: removed, RuntimeHost: "gone.tailnet.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)

	status, err := getPollableStatus(pidPath, regPath, snapshotPath, handoffPath)
	if err != nil {
		t.Fatalf("getPollableStatus: %v", err)
	}
	if status.Authenticated || status.NodeAuthorized || status.AuthorizedServiceCount != 0 {
		t.Fatalf("auth state = authenticated:%v node:%v count:%d, want removed service not counted", status.Authenticated, status.NodeAuthorized, status.AuthorizedServiceCount)
	}
	if len(status.Services) != 0 {
		t.Fatalf("services = %+v, want empty for a registry without services", status.Services)
	}
}
