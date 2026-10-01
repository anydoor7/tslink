package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
)

// TestDoctorTailscaleSSHIsInformationalInEveryOutcome pins the property the
// check exists to guarantee: it reports Tailscale SSH for discoverability and
// can never move doctor's status, counts, or exit code, including when the
// local Tailscale client cannot be read at all.
func TestDoctorTailscaleSSHIsInformationalInEveryOutcome(t *testing.T) {
	cases := []struct {
		name      string
		enabled   bool
		probeErr  error
		wantState string
		wantCode  string
		absent    []string
	}{
		{
			name:      "enabled",
			enabled:   true,
			wantState: doctorTailscaleSSHEnabled,
			wantCode:  inspect.WarningCodeTailscaleSSHEnabled,
			absent:    []string{inspect.WarningCodeTailscaleSSHDisabled, inspect.WarningCodeTailscaleSSHUnknown},
		},
		{
			name:      "disabled",
			wantState: doctorTailscaleSSHDisabled,
			wantCode:  inspect.WarningCodeTailscaleSSHDisabled,
			absent:    []string{inspect.WarningCodeTailscaleSSHEnabled, inspect.WarningCodeTailscaleSSHUnknown},
		},
		{
			name:      "local client unreachable",
			probeErr:  errors.New("dial local tailscaled: connection refused"),
			wantState: doctorTailscaleSSHUnknown,
			wantCode:  inspect.WarningCodeTailscaleSSHUnknown,
			absent:    []string{inspect.WarningCodeTailscaleSSHEnabled, inspect.WarningCodeTailscaleSSHDisabled},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			env.writeExactSnapshot(t)
			stubDoctorTailscaleSSH(t, tc.enabled, tc.probeErr)

			result := buildDoctorResult(doctorOptions{})
			if result.TailscaleSSH.State != tc.wantState {
				t.Fatalf("tailscale_ssh.state = %q, want %q", result.TailscaleSSH.State, tc.wantState)
			}
			if !result.TailscaleSSH.ACLRuleRequired {
				t.Fatal("tailscale_ssh.acl_rule_required = false; the tailnet ACL ssh rule is always the second half of the requirement")
			}

			finding := assertDoctorFinding(t, result, tc.wantCode)
			if finding.Severity != doctorSeverityInfo {
				t.Fatalf("finding severity = %q, want info so health can never change", finding.Severity)
			}
			if finding.Area != "tailscale_ssh" {
				t.Fatalf("finding area = %q, want tailscale_ssh", finding.Area)
			}
			if strings.TrimSpace(finding.Message) == "" {
				t.Fatalf("finding %s has no message; the check exists to be actionable", finding.Code)
			}
			for _, code := range tc.absent {
				assertDoctorNoFinding(t, result, code)
			}
			assertDoctorCodesRegistered(t, result)

			if result.Status != doctorStatusOK || result.HealthStatus != doctorStatusOK || result.HealthExitCode != output.ExitSuccess {
				t.Fatalf("result health = %+v, want an unchanged healthy verdict", result)
			}
			if result.Counts.Warnings != 0 || result.Counts.Errors != 0 || result.Counts.Critical != 0 {
				t.Fatalf("counts = %+v, want the SSH check counted only as info", result.Counts)
			}
			if err := doctorExit(result); err != nil {
				t.Fatalf("doctorExit = %v, want nil; the SSH check must never fail the run", err)
			}
		})
	}
}

func TestDoctorTailscaleSSHFindingsCarryActionableEvidence(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)

	stubDoctorTailscaleSSH(t, false, nil)
	disabled := assertDoctorFinding(t, buildDoctorResult(doctorOptions{}), inspect.WarningCodeTailscaleSSHDisabled)
	if disabled.Evidence["enable"] != "tailscale set --ssh" {
		t.Fatalf("disabled evidence = %v, want the exact enabling command", disabled.Evidence)
	}
	if !strings.Contains(disabled.Evidence["also_required"], "ACL ssh rule") {
		t.Fatalf("disabled evidence = %v, want the ACL requirement named", disabled.Evidence)
	}
	if !strings.Contains(disabled.Message, "tailscale set --ssh") || !strings.Contains(disabled.Message, "TSLink does not manage") {
		t.Fatalf("disabled message = %q, want the enabling step and the ownership disclaimer", disabled.Message)
	}

	stubDoctorTailscaleSSH(t, true, nil)
	enabled := assertDoctorFinding(t, buildDoctorResult(doctorOptions{}), inspect.WarningCodeTailscaleSSHEnabled)
	if !strings.Contains(enabled.Evidence["remote_command"], "tailscale ssh") || !strings.Contains(enabled.Evidence["remote_command"], "tslink") {
		t.Fatalf("enabled evidence = %v, want the concrete remote invocation", enabled.Evidence)
	}
}

// TestDoctorTailscaleSSHUnknownRedactsProbeEvidence keeps the local-client
// failure text on the same redaction path as every other doctor evidence value.
func TestDoctorTailscaleSSHUnknownRedactsProbeEvidence(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	stubDoctorTailscaleSSH(t, false, errors.New(`local api https://user:pass@127.0.0.1:1/localapi?token=tskey-api-secret-value failed`))

	var buf bytes.Buffer
	if err := runDoctor(&buf, doctorOptions{}, true); err != nil {
		t.Fatalf("runDoctor = %v, want nil for info-only findings", err)
	}
	assertDoctorOutputOmits(t, buf.String(), []string{"tskey-api-secret-value", "user:pass@"})

	finding := assertDoctorFinding(t, decodeDoctorJSON(t, buf.String()), inspect.WarningCodeTailscaleSSHUnknown)
	if finding.Evidence["error"] == "" {
		t.Fatalf("unknown finding = %+v, want a redacted reason", finding)
	}
}

func TestDoctorTailscaleSSHAppearsInHumanAndJSONOutput(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)
	stubDoctorTailscaleSSH(t, true, nil)

	var humanBuf bytes.Buffer
	if err := runDoctor(&humanBuf, doctorOptions{}, false); err != nil {
		t.Fatalf("runDoctor human = %v", err)
	}
	if !strings.Contains(humanBuf.String(), "Tailscale SSH (this node): enabled") {
		t.Fatalf("human doctor output missing the Tailscale SSH line:\n%s", humanBuf.String())
	}

	var jsonBuf bytes.Buffer
	if err := runDoctor(&jsonBuf, doctorOptions{}, true); err != nil {
		t.Fatalf("runDoctor json = %v", err)
	}
	result := decodeDoctorJSON(t, jsonBuf.String())
	if result.TailscaleSSH.State != doctorTailscaleSSHEnabled || !result.TailscaleSSH.ACLRuleRequired {
		t.Fatalf("json tailscale_ssh = %+v, want the enabled state and the ACL requirement", result.TailscaleSSH)
	}
	if !strings.Contains(jsonBuf.String(), `"tailscale_ssh"`) {
		t.Fatalf("doctor JSON is missing the tailscale_ssh object: %s", jsonBuf.String())
	}
}

// TestDoctorTailscaleSSHProbeIsBounded proves the informational read cannot
// stretch a doctor run: the seam always receives a context with a deadline no
// further out than doctorTailscaleSSHTimeout.
func TestDoctorTailscaleSSHProbeIsBounded(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	env.writeExactSnapshot(t)

	old := doctorTailscaleSSHFn
	t.Cleanup(func() { doctorTailscaleSSHFn = old })
	var deadline time.Time
	var hasDeadline bool
	doctorTailscaleSSHFn = func(ctx context.Context) (bool, error) {
		deadline, hasDeadline = ctx.Deadline()
		return false, nil
	}

	buildDoctorResult(doctorOptions{})
	if !hasDeadline {
		t.Fatal("Tailscale SSH probe received a context with no deadline")
	}
	if remaining := time.Until(deadline); remaining > doctorTailscaleSSHTimeout {
		t.Fatalf("probe deadline is %v away, want at most %v", remaining, doctorTailscaleSSHTimeout)
	}
}

// TestDefaultTailscaleSSHEnabledFailsClosedOnACancelledContext exercises the
// production seam's error path without contacting any tailscaled: the client
// comes from doctorLocalClientFn with a stub transport that, like a real one,
// refuses a request whose context is already cancelled. A zero local.Client
// would not get that far without the host: it looks up the LocalAPI token
// (lsof and a token file read on macOS) before the request even starts.
func TestDefaultTailscaleSSHEnabledFailsClosedOnACancelledContext(t *testing.T) {
	t.Setenv(doctorSkipTailscaleSSHEnv, "")
	requests := 0
	stubDoctorLocalClient(t, localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		t.Fatal("stub transport received a live request on a cancelled context")
		return nil, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	enabled, err := defaultTailscaleSSHEnabled(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("defaultTailscaleSSHEnabled on a cancelled context = %v, want context.Canceled", err)
	}
	if enabled {
		t.Fatal("defaultTailscaleSSHEnabled reported enabled without reading preferences")
	}
	if requests > 1 {
		t.Fatalf("stub saw %d requests, want at most one", requests)
	}
}
