package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
)

func restoreInviteCommandSeams(t *testing.T) {
	t.Helper()
	oldRegistryPath := inviteRegistryPathFn
	oldPIDPath := invitePIDPathFn
	oldSnapshotPath := inviteRuntimeSnapshotPathFn
	oldLoadRegistry := inviteLoadRegistryFn
	oldLoadSnapshot := inviteLoadSnapshotFn
	oldIsRunning := inviteIsRunningFn
	oldReadPID := inviteReadPIDFn
	oldPIDModTime := invitePIDModTimeFn
	oldCreateUser := inviteCreateUserFn
	oldCreateDevice := inviteCreateDeviceFn
	oldList := inviteListFn
	oldRevoke := inviteRevokeFn
	oldResend := inviteResendFn
	t.Cleanup(func() {
		inviteRegistryPathFn = oldRegistryPath
		invitePIDPathFn = oldPIDPath
		inviteRuntimeSnapshotPathFn = oldSnapshotPath
		inviteLoadRegistryFn = oldLoadRegistry
		inviteLoadSnapshotFn = oldLoadSnapshot
		inviteIsRunningFn = oldIsRunning
		inviteReadPIDFn = oldReadPID
		invitePIDModTimeFn = oldPIDModTime
		inviteCreateUserFn = oldCreateUser
		inviteCreateDeviceFn = oldCreateDevice
		inviteListFn = oldList
		inviteRevokeFn = oldRevoke
		inviteResendFn = oldResend
	})
}

func configureExactInviteRuntime(t *testing.T, services []registry.Service, nodeIDs map[string]string) (regPath, pidPath, snapshotPath string) {
	t.Helper()
	restoreInviteCommandSeams(t)
	dir := t.TempDir()
	regPath = filepath.Join(dir, "registry.json")
	pidPath = filepath.Join(dir, "tslink.pid")
	snapshotPath = filepath.Join(dir, "runtime.json")
	reg := &registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: services}
	registryData, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, registryData, 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	snapshotServices := make([]tsruntime.ServiceSnapshot, 0, len(services))
	for _, service := range services {
		snapshotServices = append(snapshotServices, tsruntime.ServiceSnapshot{Name: service.Name, Type: service.Type, NodeID: nodeIDs[service.Name], RuntimeState: tsruntime.ServiceRuntimeRunning})
	}
	snapshot := &tsruntime.Snapshot{
		SchemaVersion:       tsruntime.SchemaVersion,
		DaemonPID:           4242,
		DaemonStartedAt:     started,
		RegistryFingerprint: fingerprint,
		UpdatedAt:           started.Add(time.Minute),
		Services:            snapshotServices,
	}
	inviteRegistryPathFn = func() (string, error) { return regPath, nil }
	invitePIDPathFn = func() (string, error) { return pidPath, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return snapshotPath, nil }
	inviteLoadRegistryFn = func(path string) (*registry.Registry, error) {
		if path != regPath {
			t.Fatalf("registry path = %q, want %q", path, regPath)
		}
		return reg, nil
	}
	inviteLoadSnapshotFn = func(path string) (*tsruntime.Snapshot, error) {
		if path != snapshotPath {
			t.Fatalf("snapshot path = %q, want production fallback %q", path, snapshotPath)
		}
		return snapshot, nil
	}
	inviteIsRunningFn = func(string) bool { return true }
	inviteReadPIDFn = func(string) (int, error) { return 4242, nil }
	invitePIDModTimeFn = func(string) (time.Time, error) { return started, nil }
	return regPath, pidPath, snapshotPath
}

func TestInviteDeviceTargetsRequireFreshRuntimeNodeID(t *testing.T) {
	service := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	regPath, pidPath, snapshotPath := configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"app": "n-app-owned"})

	targets, err := inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].NodeID != "n-app-owned" || targets[0].Hostname != "app" {
		t.Fatalf("targets = %+v, want service hostname plus exact runtime node ID", targets)
	}

	inviteReadPIDFn = func(string) (int, error) { return 9999, nil }
	targets, err = inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].NodeID != "" {
		t.Fatalf("stale snapshot target = %+v, must not retain node ID across PID mismatch", targets[0])
	}

	inviteReadPIDFn = func(string) (int, error) { return 4242, nil }
	snapshot, err := inviteLoadSnapshotFn(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Services[0].RuntimeState = tsruntime.ServiceRuntimeFailed
	targets, err = inviteDeviceTargetsForPaths(regPath, pidPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if targets[0].NodeID != "" {
		t.Fatalf("non-running snapshot target = %+v, must not retain node ID", targets[0])
	}
}

func TestInviteUserJSONCarriesDeliveryFactURLAndAuditPlan(t *testing.T) {
	restoreInviteCommandSeams(t)
	inviteCreateUserFn = func(_ context.Context, email, role string, printLink bool) (tailapi.Invite, error) {
		if email != "alice@example.com" || role != tailapi.InviteRoleAuditor || !printLink {
			t.Fatalf("create args = %q %q print=%v", email, role, printLink)
		}
		return tailapi.Invite{Kind: tailapi.InviteKindUser, ID: "70001", Recipient: email, Role: role, InviteURL: "https://login.tailscale.com/uinv/verbatim-json", Emailed: false}, nil
	}
	var out bytes.Buffer
	if err := inviteUserRun(context.Background(), &out, "alice@example.com", tailapi.InviteRoleAuditor, true, true); err != nil {
		t.Fatal(err)
	}
	var envelope output.Result
	if err := json.NewDecoder(&out).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(envelope.Data)
	if err != nil {
		t.Fatal(err)
	}
	var result InviteMutationResult
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK || envelope.Command != "invite user" || result.InviteURL != "https://login.tailscale.com/uinv/verbatim-json" || result.Emailed || result.RemoteSideEffectPlan.ID != "remote.invite.user.create" {
		t.Fatalf("envelope=%+v result=%+v", envelope, result)
	}
	for _, resource := range result.RemoteSideEffectPlan.Resources {
		if strings.Contains(resource, "login.tailscale.com") || strings.Contains(resource, "@") {
			t.Fatalf("audit resources leaked bearer-like invite URL or recipient PII: %v", result.RemoteSideEffectPlan.Resources)
		}
	}
}

func TestInviteRoleRejectedBeforeRunWithStableUsageEnvelope(t *testing.T) {
	userCmd, _, err := rootCmd.Find([]string{"invite", "user"})
	if err != nil {
		t.Fatal(err)
	}
	resetCommandLocalFlags(t, userCmd)
	if err := userCmd.Flags().Set("role", "owner"); err != nil {
		t.Fatal(err)
	}
	err = userCmd.PreRunE(userCmd, []string{"alice@example.com"})
	if err == nil || output.ExitCode(err) != output.ExitUsage {
		t.Fatalf("error = %v exit=%d, want parse-time usage failure", err, output.ExitCode(err))
	}
	failure := output.NewFailureForError("invite user", err)
	if failure.Error == nil || failure.Error.Code != registry.CodeInviteRoleInvalid || len(failure.Error.Next) == 0 {
		t.Fatalf("failure = %+v, want stable role code and populated next[]", failure)
	}
}

func TestInviteMutationValidationPrecedesRegistryAndUsesStableUsageCodes(t *testing.T) {
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) {
		t.Fatal("invalid mutation input reached registry resolution")
		return "", errors.New("unreachable")
	}

	tests := []struct {
		name     string
		kind     string
		id       string
		wantCode string
	}{
		{name: "traversal_id", kind: tailapi.InviteKindUser, id: "../device/nodeid-VICTIM", wantCode: registry.CodeInviteIDInvalid},
		{name: "missing_kind", kind: "", id: "12346", wantCode: registry.CodeInviteKindInvalid},
		{name: "unknown_kind", kind: "both", id: "12346", wantCode: registry.CodeInviteKindInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := inviteRevokeRun(context.Background(), &out, tc.kind, tc.id, true)
			if output.ExitCode(err) != output.ExitUsage {
				t.Fatalf("error=%v exit=%d, want usage/2", err, output.ExitCode(err))
			}
			failure := output.NewFailureForError("invite revoke", err)
			if failure.Error == nil || failure.Error.Code != tc.wantCode || len(failure.Error.Next) == 0 {
				t.Fatalf("failure = %+v, want stable %s with next[]", failure, tc.wantCode)
			}
		})
	}
}

func TestInviteAPIListRequiresExplicitShowURLs(t *testing.T) {
	regPath, pidPath, _ := configureExactInviteRuntime(t, nil, nil)
	h := &apiHandler{regPath: regPath, pidPath: pidPath}
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{
			Complete:      true,
			UserInvites:   []tailapi.Invite{{Kind: tailapi.InviteKindUser, ID: "71101", InviteURL: "https://login.tailscale.com/uinv/api-list-placeholder"}},
			DeviceInvites: []tailapi.Invite{},
			Count:         1,
		}, nil
	}

	for _, tc := range []struct {
		name     string
		showURLs bool
		wantURL  bool
	}{
		{name: "default_redacted"},
		{name: "explicit_show", showURLs: true, wantURL: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			result := h.handle(APIRequest{Action: apiActionInviteList, ShowURLs: tc.showURLs}, &out)
			if !result.OK || !strings.Contains(out.String(), `"complete":true`) || !strings.Contains(out.String(), `"device_invites":[]`) || !strings.Contains(out.String(), `"device_targets":[]`) || strings.Contains(out.String(), "api-list-placeholder") != tc.wantURL || strings.Contains(out.String(), `"invite_url"`) != tc.wantURL {
				t.Fatalf("show_urls=%t result=%+v output=%s", tc.showURLs, result, out.String())
			}
		})
	}
}

func TestInviteAPIMutationsRejectTraversalBeforeTargetsOrMutation(t *testing.T) {
	restoreInviteCommandSeams(t)
	inviteLoadRegistryFn = func(string) (*registry.Registry, error) {
		t.Fatal("traversal API input reached target resolution")
		return nil, errors.New("unreachable")
	}
	inviteRevokeFn = func(context.Context, string, string, []tailapi.DeviceTarget) (tailapi.Invite, error) {
		t.Fatal("traversal API input reached revoke mutation seam")
		return tailapi.Invite{}, errors.New("unreachable")
	}
	inviteResendFn = func(context.Context, string, string, []tailapi.DeviceTarget) (tailapi.Invite, error) {
		t.Fatal("traversal API input reached resend mutation seam")
		return tailapi.Invite{}, errors.New("unreachable")
	}
	h := &apiHandler{}

	for _, request := range []APIRequest{
		{Action: apiActionInviteRevoke, Kind: tailapi.InviteKindUser, InviteID: "../device/nodeid-VICTIM"},
		{Action: apiActionInviteResend, Kind: tailapi.InviteKindDevice, InviteID: "../tailnet/-/keys/kVICTIM"},
	} {
		var out bytes.Buffer
		result := h.handle(request, &out)
		if result.OK || result.Code != output.ExitUsage || result.Error == nil || result.Error.Code != registry.CodeInviteIDInvalid || len(result.Error.Next) == 0 {
			t.Fatalf("action=%s result=%+v output=%s", request.Action, result, out.String())
		}
	}
}

func TestInviteAPIMutationsRejectMissingOrBogusKindBeforeTargetsOrMutation(t *testing.T) {
	restoreInviteCommandSeams(t)
	targetCalls := 0
	revokeCalls := 0
	resendCalls := 0
	inviteLoadRegistryFn = func(string) (*registry.Registry, error) {
		targetCalls++
		return &registry.Registry{}, nil
	}
	inviteRevokeFn = func(context.Context, string, string, []tailapi.DeviceTarget) (tailapi.Invite, error) {
		revokeCalls++
		return tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "12346"}, nil
	}
	inviteResendFn = func(context.Context, string, string, []tailapi.DeviceTarget) (tailapi.Invite, error) {
		resendCalls++
		return tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "12346", Email: "placeholder@example.com"}, nil
	}
	h := &apiHandler{}
	for _, action := range []string{apiActionInviteRevoke, apiActionInviteResend} {
		for _, kind := range []string{"", "both"} {
			t.Run(action+"_"+kind, func(t *testing.T) {
				var out bytes.Buffer
				result := h.handle(APIRequest{Action: action, Kind: kind, InviteID: "12346"}, &out)
				if result.OK || result.Code != output.ExitUsage || result.Error == nil || result.Error.Code != registry.CodeInviteKindInvalid || len(result.Error.Next) == 0 {
					t.Fatalf("result=%+v raw=%s, want invite_kind_invalid/2 with next[]", result, out.String())
				}
				if !strings.Contains(out.String(), `"code":"invite_kind_invalid"`) {
					t.Fatalf("raw API output = %s, want stable kind error", out.String())
				}
			})
		}
	}
	if targetCalls != 0 || revokeCalls != 0 || resendCalls != 0 {
		t.Fatalf("invalid kinds reached targets/revoke/resend: %d/%d/%d", targetCalls, revokeCalls, resendCalls)
	}
}

func TestInviteAPIActionsDriveAllOperationsWithoutArgv(t *testing.T) {
	service := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	regPath, pidPath, snapshotPath := configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"app": "n-app-owned"})
	fallbackCalls := 0
	inviteRuntimeSnapshotPathFn = func() (string, error) {
		fallbackCalls++
		return snapshotPath, nil
	}
	h := &apiHandler{regPath: regPath, pidPath: pidPath}

	inviteCreateUserFn = func(context.Context, string, string, bool) (tailapi.Invite, error) {
		return tailapi.Invite{Kind: tailapi.InviteKindUser, ID: "71001", Recipient: "alice@example.com", InviteURL: "https://login.tailscale.com/uinv/api", Emailed: true}, nil
	}
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, _ string, _, _, _ bool) (tailapi.Invite, error) {
		if target.Service != "app" || target.NodeID != "n-app-owned" {
			t.Fatalf("device target = %+v, want exact app ownership", target)
		}
		return tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "71002", Service: "app", Recipient: "bob@example.com", InviteURL: "https://login.tailscale.com/admin/invite/api", Emailed: false}, nil
	}
	inviteListFn = func(_ context.Context, targets []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		if len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("list targets = %+v, want production-shape ownership target", targets)
		}
		return tailapi.InviteList{Complete: true, UserInvites: []tailapi.Invite{}, DeviceInvites: []tailapi.Invite{}, DeviceTargets: []tailapi.InviteTargetStatus{}, Count: 0}, nil
	}
	inviteRevokeFn = func(_ context.Context, kind, id string, targets []tailapi.DeviceTarget) (tailapi.Invite, error) {
		if kind != tailapi.InviteKindDevice || id != "71003" || len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("revoke kind=%q id=%q targets=%+v", kind, id, targets)
		}
		return tailapi.Invite{Kind: kind, ID: id, Service: "app"}, nil
	}
	inviteResendFn = func(_ context.Context, kind, id string, targets []tailapi.DeviceTarget) (tailapi.Invite, error) {
		if kind != tailapi.InviteKindDevice || id != "71004" || len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("resend kind=%q id=%q targets=%+v", kind, id, targets)
		}
		return tailapi.Invite{Kind: kind, ID: id, Service: "app", Email: "alice@example.com", InviteURL: "https://login.tailscale.com/uinv/api", Emailed: true}, nil
	}

	requests := []APIRequest{
		{Action: apiActionInviteUser, Email: "alice@example.com"},
		{Action: apiActionInviteDevice, Service: "app", Email: "bob@example.com", PrintLink: true},
		{Action: apiActionInviteList},
		{Action: apiActionInviteRevoke, Kind: tailapi.InviteKindDevice, InviteID: "71003"},
		{Action: apiActionInviteResend, Kind: tailapi.InviteKindDevice, InviteID: "71004"},
	}
	for _, request := range requests {
		var out bytes.Buffer
		result := h.handle(request, &out)
		if !result.OK || result.Command != request.Action || result.Code != output.ExitSuccess {
			t.Fatalf("action %s result = %+v raw=%s", request.Action, result, out.String())
		}
		var envelope output.Result
		if err := json.NewDecoder(&out).Decode(&envelope); err != nil || !envelope.OK || envelope.Command != request.Action {
			t.Fatalf("action %s envelope=%+v err=%v raw=%s", request.Action, envelope, err, out.String())
		}
	}
	if fallbackCalls != 4 {
		t.Fatalf("production runtime-snapshot fallback calls = %d, want device/list/revoke/resend = 4", fallbackCalls)
	}
}

func TestInviteAPIErrorEnvelopePreservesStableCodeExitAndNext(t *testing.T) {
	service := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	regPath, pidPath, _ := configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"app": "n-app-owned"})
	h := &apiHandler{regPath: regPath, pidPath: pidPath}
	inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
		return tailapi.Invite{}, registry.CodedError{
			Code:        registry.CodeInviteDeviceAmbiguous,
			Message:     "matched devices: app (nodeId=n-one, id=1), app-2 (nodeId=n-two, id=2)",
			Next:        []string{"tslink status --urls --name app --json", "Resolve duplicate tailnet device hostnames before retrying"},
			MessageOnly: true,
		}
	}
	var out bytes.Buffer
	result := h.handle(APIRequest{Action: apiActionInviteDevice, Service: "app", Email: "bob@example.com"}, &out)
	if result.OK || result.Code != output.ExitConflict || result.Error == nil || result.Error.Code != registry.CodeInviteDeviceAmbiguous || len(result.Error.Next) != 2 {
		t.Fatalf("result = %+v, want distinct conflict code and exact next[]", result)
	}
	if !strings.Contains(result.Error.Message, "app-2") {
		t.Fatalf("message = %q, want matched device names", result.Error.Message)
	}
}

func TestInviteAPIKeyRequiredEnvelopeUsesAuthExit(t *testing.T) {
	restoreInviteCommandSeams(t)
	h := &apiHandler{}
	inviteCreateUserFn = func(context.Context, string, string, bool) (tailapi.Invite, error) {
		return tailapi.Invite{}, registry.CodedError{
			Code:        registry.CodeInviteAPIKeyRequired,
			Message:     "only OAuth is configured; user-owned tskey-api- token required",
			Next:        []string{"tslink login --api-key-stdin"},
			MessageOnly: true,
		}
	}
	var out bytes.Buffer
	result := h.handle(APIRequest{Action: apiActionInviteUser, Email: "alice@example.com"}, &out)
	if result.OK || result.Code != output.ExitAuth || result.Error == nil || result.Error.Code != registry.CodeInviteAPIKeyRequired || !strings.Contains(result.Error.Message, "user-owned tskey-api-") || len(result.Error.Next) != 1 {
		t.Fatalf("result = %+v, want auth exit, actionable type, and next[]", result)
	}
}

func TestInviteTargetServiceMissingUsesNotFoundExit(t *testing.T) {
	regPath, pidPath, snapshotPath := configureExactInviteRuntime(t, nil, nil)
	_, err := inviteDeviceTargetForService(regPath, pidPath, snapshotPath, "missing")
	if !errors.As(err, new(*output.CodeError)) || output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("error = %v exit=%d, want not_found/5", err, output.ExitCode(err))
	}
}

func TestInviteCLIRunsAllHumanAndJSONOperationsWithExactTargets(t *testing.T) {
	service := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	configureExactInviteRuntime(t, []registry.Service{service}, map[string]string{"app": "n-app-owned"})

	var deviceCalls int
	inviteCreateDeviceFn = func(_ context.Context, target tailapi.DeviceTarget, email string, printLink, multiUse, allowExitNode bool) (tailapi.Invite, error) {
		deviceCalls++
		if target.Service != "app" || target.Hostname != "app" || target.NodeID != "n-app-owned" || email != "bob@example.com" || !multiUse || !allowExitNode {
			t.Fatalf("device create args = target=%+v email=%q print=%t multi=%t exit=%t", target, email, printLink, multiUse, allowExitNode)
		}
		invite := tailapi.Invite{Kind: tailapi.InviteKindDevice, ID: "72001", Recipient: email, Service: "app", DeviceID: 11055, MultiUse: true, AllowExitNode: true}
		if printLink {
			invite.InviteURL = "https://login.tailscale.com/admin/invite/verbatim-device"
			return invite, nil
		}
		invite.Email = email
		invite.Emailed = true
		return invite, nil
	}

	var out bytes.Buffer
	if err := inviteDeviceRun(context.Background(), &out, "app", "bob@example.com", false, true, true, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Created device invite 72001") || !strings.Contains(got, "email bob@example.com") {
		t.Fatalf("device email output = %q", got)
	}
	out.Reset()
	if err := inviteDeviceRun(context.Background(), &out, "app", "bob@example.com", true, true, true, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "https://login.tailscale.com/admin/invite/verbatim-device" {
		t.Fatalf("device self-delivery output = %q, want verbatim API URL", got)
	}
	if deviceCalls != 2 {
		t.Fatalf("device create calls = %d, want 2", deviceCalls)
	}

	inviteListFn = func(_ context.Context, targets []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		if len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("list targets = %+v, want exact owned node", targets)
		}
		return tailapi.InviteList{
			Complete:      true,
			UserInvites:   []tailapi.Invite{{Kind: tailapi.InviteKindUser, ID: "73001", Email: "alice@example.com", Emailed: true, InviteURL: "https://login.tailscale.com/uinv/verbatim-list"}},
			DeviceInvites: []tailapi.Invite{{Kind: tailapi.InviteKindDevice, ID: "73002", Service: "app", Emailed: false, InviteURL: "https://login.tailscale.com/admin/invite/verbatim-list"}},
			DeviceTargets: []tailapi.InviteTargetStatus{},
			Count:         2,
		}, nil
	}
	out.Reset()
	if err := inviteListRun(context.Background(), &out, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"KIND", "73001", "alice@example.com", "73002", "app"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human list output = %q, want %q", out.String(), want)
		}
	}
	if strings.Contains(out.String(), "INVITE URL") || strings.Contains(out.String(), "verbatim-list") {
		t.Fatalf("default human list leaked bearer URLs: %q", out.String())
	}
	out.Reset()
	if err := inviteListRun(context.Background(), &out, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "INVITE URL") || !strings.Contains(out.String(), "verbatim-list") {
		t.Fatalf("--show-urls human list = %q, want explicit URL column", out.String())
	}
	out.Reset()
	if err := inviteListRun(context.Background(), &out, false, true); err != nil {
		t.Fatal(err)
	}
	listRaw := out.String()
	var listEnvelope output.Result
	if err := json.NewDecoder(strings.NewReader(listRaw)).Decode(&listEnvelope); err != nil || !listEnvelope.OK || listEnvelope.Command != "invite list" {
		t.Fatalf("list envelope = %+v err=%v raw=%s", listEnvelope, err, listRaw)
	}
	if strings.Contains(listRaw, "invite_url") || strings.Contains(listRaw, "verbatim-list") {
		t.Fatalf("default JSON list leaked bearer URLs: %s", listRaw)
	}
	out.Reset()
	if err := inviteListRun(context.Background(), &out, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"invite_url"`) || !strings.Contains(out.String(), "verbatim-list") {
		t.Fatalf("--show-urls JSON list = %s, want explicit URLs", out.String())
	}

	inviteRevokeFn = func(_ context.Context, kind, id string, targets []tailapi.DeviceTarget) (tailapi.Invite, error) {
		if kind != tailapi.InviteKindDevice || id != "73003" || len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("revoke kind=%q id=%q targets=%+v", kind, id, targets)
		}
		return tailapi.Invite{Kind: kind, ID: id, Service: "app", DeviceID: 11055}, nil
	}
	out.Reset()
	if err := inviteRevokeRun(context.Background(), &out, tailapi.InviteKindDevice, "73003", true); err != nil {
		t.Fatal(err)
	}
	var revokeEnvelope output.Result
	if err := json.NewDecoder(&out).Decode(&revokeEnvelope); err != nil || !revokeEnvelope.OK || revokeEnvelope.Command != "invite revoke" {
		t.Fatalf("revoke envelope = %+v err=%v raw=%s", revokeEnvelope, err, out.String())
	}
	out.Reset()
	if err := inviteRevokeRun(context.Background(), &out, tailapi.InviteKindDevice, "73003", false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Revoked device invite 73003") {
		t.Fatalf("revoke output = %q", got)
	}

	inviteResendFn = func(_ context.Context, kind, id string, targets []tailapi.DeviceTarget) (tailapi.Invite, error) {
		if kind != tailapi.InviteKindDevice || id != "73003" || len(targets) != 1 || targets[0].NodeID != "n-app-owned" {
			t.Fatalf("resend kind=%q id=%q targets=%+v", kind, id, targets)
		}
		return tailapi.Invite{Kind: kind, ID: id, Service: "app", Email: "bob@example.com", Emailed: true, InviteURL: "https://login.tailscale.com/admin/invite/verbatim-resend"}, nil
	}
	out.Reset()
	if err := inviteResendRun(context.Background(), &out, tailapi.InviteKindDevice, "73003", true); err != nil {
		t.Fatal(err)
	}
	resendRaw := out.String()
	var resendEnvelope output.Result
	if err := json.NewDecoder(strings.NewReader(resendRaw)).Decode(&resendEnvelope); err != nil || !resendEnvelope.OK || resendEnvelope.Command != "invite resend" {
		t.Fatalf("resend envelope = %+v err=%v raw=%s", resendEnvelope, err, resendRaw)
	}
	if strings.Contains(resendRaw, "invite_url") || strings.Contains(resendRaw, "verbatim-resend") {
		t.Fatalf("resend JSON echoed bearer URL: %s", resendRaw)
	}
	out.Reset()
	if err := inviteResendRun(context.Background(), &out, tailapi.InviteKindDevice, "73003", false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "Resent device invite 73003 to bob@example.com") {
		t.Fatalf("resend output = %q", got)
	}
}

func TestInviteListHumanEmptyStateIsExplicit(t *testing.T) {
	configureExactInviteRuntime(t, nil, nil)
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{Complete: true, UserInvites: []tailapi.Invite{}, DeviceInvites: []tailapi.Invite{}, DeviceTargets: []tailapi.InviteTargetStatus{}, Count: 0}, nil
	}
	var out bytes.Buffer
	if err := inviteListRun(context.Background(), &out, false, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "No open invites\n" {
		t.Fatalf("empty list output = %q", got)
	}
}

func TestInviteListHumanDistinguishesIncompleteDeviceChecks(t *testing.T) {
	configureExactInviteRuntime(t, nil, nil)
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{
			UserInvites:   []tailapi.Invite{},
			DeviceInvites: []tailapi.Invite{},
			DeviceTargets: []tailapi.InviteTargetStatus{{
				Service: "never-started",
				Error: &tailapi.InviteTargetError{
					Code:    registry.CodeInviteNotFound,
					Message: "no matching tailnet device",
				},
			}},
			Count: 0,
		}, nil
	}
	var out bytes.Buffer
	if err := inviteListRun(context.Background(), &out, false, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"some device checks were incomplete", "never-started", registry.CodeInviteNotFound, "no matching tailnet device"} {
		if !strings.Contains(got, want) {
			t.Fatalf("incomplete list output = %q, want %q", got, want)
		}
	}
}

func TestInviteListCompletenessIsVisibleWithReturnedInvites(t *testing.T) {
	configureExactInviteRuntime(t, nil, nil)
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return tailapi.InviteList{
			Complete:      false,
			UserInvites:   []tailapi.Invite{{Kind: tailapi.InviteKindUser, ID: "74001", Email: "alice@example.com", Emailed: true}},
			DeviceInvites: []tailapi.Invite{},
			DeviceTargets: []tailapi.InviteTargetStatus{{
				Service: "app",
				Error: &tailapi.InviteTargetError{
					Code:    registry.CodeInviteOwnershipUnproven,
					Message: "exact node ID is unavailable",
					Next:    []string{"Restart the TSLink daemon with the current binary"},
				},
			}},
			Count: 1,
		}, nil
	}

	var human bytes.Buffer
	if err := inviteListRun(context.Background(), &human, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"74001", "alice@example.com", "Device invite check incomplete", "app", registry.CodeInviteOwnershipUnproven, "exact node ID is unavailable"} {
		if !strings.Contains(human.String(), want) {
			t.Fatalf("human output = %q, want %q", human.String(), want)
		}
	}

	var machine bytes.Buffer
	if err := inviteListRun(context.Background(), &machine, false, true); err != nil {
		t.Fatal(err)
	}
	raw := machine.String()
	for _, want := range []string{`"ok":true`, `"complete":false`, `"id":"74001"`, `"device_invites":[]`, `"device_targets":[`, `"code":"invite_device_ownership_unproven"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("JSON output = %s, want %s", raw, want)
		}
	}
}

func TestInviteAPIInputGuardsDefaultDenyBeforeCreate(t *testing.T) {
	restoreInviteCommandSeams(t)
	inviteCreateUserFn = func(context.Context, string, string, bool) (tailapi.Invite, error) {
		t.Fatal("invalid invite_user input reached create")
		return tailapi.Invite{}, errors.New("unreachable")
	}
	inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
		t.Fatal("invalid invite_device input reached create")
		return tailapi.Invite{}, errors.New("unreachable")
	}

	for _, tc := range []struct {
		name     string
		request  APIRequest
		wantCode string
	}{
		{name: "user_missing_email", request: APIRequest{Action: apiActionInviteUser}, wantCode: "usage_error"},
		{name: "user_invalid_role", request: APIRequest{Action: apiActionInviteUser, Email: "alice@example.com", Role: "owner"}, wantCode: registry.CodeInviteRoleInvalid},
		{name: "device_missing_service", request: APIRequest{Action: apiActionInviteDevice, Email: "alice@example.com"}, wantCode: "usage_error"},
		{name: "device_missing_email", request: APIRequest{Action: apiActionInviteDevice, Service: "app"}, wantCode: "usage_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			result := (&apiHandler{}).handle(tc.request, &out)
			if result.OK || result.Code != output.ExitUsage || result.Error == nil || result.Error.Code != tc.wantCode {
				t.Fatalf("result=%+v raw=%s, want default-deny usage code %s", result, out.String(), tc.wantCode)
			}
		})
	}
}

func TestInviteAPIDeviceUnknownServiceStopsBeforeCreate(t *testing.T) {
	regPath, pidPath, _ := configureExactInviteRuntime(t, nil, nil)
	inviteCreateDeviceFn = func(context.Context, tailapi.DeviceTarget, string, bool, bool, bool) (tailapi.Invite, error) {
		t.Fatal("unknown service reached device invite creation")
		return tailapi.Invite{}, errors.New("unreachable")
	}
	var out bytes.Buffer
	result := (&apiHandler{regPath: regPath, pidPath: pidPath}).handle(APIRequest{Action: apiActionInviteDevice, Service: "missing", Email: "alice@example.com"}, &out)
	if result.OK || result.Code != output.ExitNotFound || result.Error == nil || result.Error.Code != "not_found" {
		t.Fatalf("result=%+v raw=%s, want service not_found before create", result, out.String())
	}
}

func TestInviteAPIListRejectsMissingOrBlankRegistryAsIncompleteState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content *string
	}{
		{name: "missing"},
		{name: "empty", content: new(string)},
		{name: "whitespace", content: func() *string { value := " \n\t"; return &value }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreInviteCommandSeams(t)
			dir := t.TempDir()
			regPath := filepath.Join(dir, "registry.json")
			pidPath := filepath.Join(dir, "tslink.pid")
			snapshotPath := filepath.Join(dir, "runtime.json")
			inviteRegistryPathFn = func() (string, error) { return regPath, nil }
			invitePIDPathFn = func() (string, error) { return pidPath, nil }
			inviteRuntimeSnapshotPathFn = func() (string, error) { return snapshotPath, nil }
			if tc.content != nil {
				if err := os.WriteFile(regPath, []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
				t.Fatal("unavailable registry reached invite listing")
				return tailapi.InviteList{}, errors.New("unreachable")
			}
			var out bytes.Buffer
			result := (&apiHandler{regPath: regPath, pidPath: pidPath}).handle(APIRequest{Action: apiActionInviteList}, &out)
			if result.OK || result.Code != output.ExitConflict || result.Error == nil || result.Error.Code != "conflict" || strings.Contains(out.String(), `"complete":true`) {
				t.Fatalf("result=%+v raw=%s, want explicit incomplete-state failure", result, out.String())
			}
			out.Reset()
			if err := inviteListRun(context.Background(), &out, false, true); err == nil || output.ExitCode(err) != output.ExitConflict {
				t.Fatalf("CLI invite list error=%v exit=%d, want the same incomplete-state conflict", err, output.ExitCode(err))
			}
		})
	}
}

func TestInviteListHumanAndJSONAgreeOnUncheckedTargetWithoutError(t *testing.T) {
	configureExactInviteRuntime(t, nil, nil)
	libraryResult := tailapi.InviteList{
		Complete:      false,
		UserInvites:   []tailapi.Invite{},
		DeviceInvites: []tailapi.Invite{},
		DeviceTargets: []tailapi.InviteTargetStatus{
			{Service: "clean", Checked: true},
			{Service: "pending", Checked: false},
		},
	}
	inviteListFn = func(context.Context, []tailapi.DeviceTarget) (tailapi.InviteList, error) {
		return libraryResult, nil
	}

	var human bytes.Buffer
	if err := inviteListRun(context.Background(), &human, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "some device checks were incomplete") || !strings.Contains(human.String(), "pending") || strings.Contains(human.String(), "incomplete for clean") || strings.Contains(human.String(), "No open invites\n") {
		t.Fatalf("human output = %q, want Complete=false semantics and no bogus clean-target error", human.String())
	}

	var machine bytes.Buffer
	if err := inviteListRun(context.Background(), &machine, false, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(machine.String(), `"complete":false`) || !strings.Contains(machine.String(), `"service":"pending"`) {
		t.Fatalf("JSON output = %s, want same library completeness result", machine.String())
	}
}

func TestInviteCLIUserRevokeDoesNotResolveDeviceTargets(t *testing.T) {
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) {
		t.Fatal("user revoke attempted device-target registry resolution")
		return "", errors.New("unreachable")
	}
	inviteRevokeFn = func(_ context.Context, kind, id string, targets []tailapi.DeviceTarget) (tailapi.Invite, error) {
		if kind != tailapi.InviteKindUser || id != "75001" || targets != nil {
			t.Fatalf("kind=%q id=%q targets=%+v, want user revoke without device targets", kind, id, targets)
		}
		return tailapi.Invite{Kind: kind, ID: id}, nil
	}
	var out bytes.Buffer
	if err := inviteRevokeRun(context.Background(), &out, tailapi.InviteKindUser, "75001", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Revoked user invite 75001") {
		t.Fatalf("output = %q, want successful user revoke", out.String())
	}
}
