package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/testenv"
	"github.com/monody0007/tslink/internal/testenv/localapitest"
	"tailscale.com/client/local"
)

// stubDoctorLocalClient makes defaultTailscaleSSHEnabled read through rt.
func stubDoctorLocalClient(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	old := doctorLocalClientFn
	t.Cleanup(func() { doctorLocalClientFn = old })
	doctorLocalClientFn = func() *local.Client { return localapitest.NewClient(rt) }
}

// assertDoctorTailscaleSSHSkipped checks the doctor payload of a run with
// doctorSkipTailscaleSSHEnv=1: state unknown, one info finding that names the
// variable.
func assertDoctorTailscaleSSHSkipped(t *testing.T, result DoctorResult) {
	t.Helper()
	if result.TailscaleSSH.State != doctorTailscaleSSHUnknown || !result.TailscaleSSH.ACLRuleRequired {
		t.Fatalf("tailscale_ssh = %+v, want state %q with acl_rule_required", result.TailscaleSSH, doctorTailscaleSSHUnknown)
	}
	for _, finding := range result.Findings {
		if finding.Code != inspect.WarningCodeTailscaleSSHUnknown {
			continue
		}
		if finding.Evidence["skipped"] != doctorSkipTailscaleSSHEnv+"=1" || finding.Severity != "info" || !strings.Contains(finding.Message, "skipped") {
			t.Fatalf("tailscale_ssh_unknown finding = %+v, want an info finding that says skipped with evidence skipped=%s=1", finding, doctorSkipTailscaleSSHEnv)
		}
		return
	}
	t.Fatalf("findings = %+v, want a %s finding for the skipped check", result.Findings, inspect.WarningCodeTailscaleSSHUnknown)
}

// TestDefaultTailscaleSSHEnabledReadsPrefsFromTheLocalAPI pins the release
// behaviour with the knob unset (or set to anything but 1): one GET
// /localapi/v0/prefs through the local client, answered here by a stub.
func TestDefaultTailscaleSSHEnabledReadsPrefsFromTheLocalAPI(t *testing.T) {
	for _, tc := range []struct {
		knob string
		body string
		want bool
	}{
		{knob: "", body: `{"RunSSH":true}`, want: true},
		{knob: "", body: `{"RunSSH":false}`, want: false},
		{knob: "0", body: `{"RunSSH":true}`, want: true},
		{knob: "true", body: `{"RunSSH":true}`, want: true},
	} {
		t.Run(tc.knob+tc.body, func(t *testing.T) {
			t.Setenv(doctorSkipTailscaleSSHEnv, tc.knob)
			var requests []string
			stubDoctorLocalClient(t, localapitest.RoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}))
			enabled, err := defaultTailscaleSSHEnabled(context.Background())
			if err != nil || enabled != tc.want {
				t.Fatalf("defaultTailscaleSSHEnabled() = %v, %v; want %v, nil", enabled, err, tc.want)
			}
			if strings.Join(requests, ",") != "GET /localapi/v0/prefs" {
				t.Fatalf("local API requests = %v, want exactly GET /localapi/v0/prefs", requests)
			}
		})
	}
}

// TestDefaultTailscaleSSHEnabledSkipsTheLocalReadWhenTheKnobIsSet: with the
// knob at 1 no client is built, so nothing reaches tailscaled.
func TestDefaultTailscaleSSHEnabledSkipsTheLocalReadWhenTheKnobIsSet(t *testing.T) {
	t.Setenv(doctorSkipTailscaleSSHEnv, "1")
	old := doctorLocalClientFn
	t.Cleanup(func() { doctorLocalClientFn = old })
	doctorLocalClientFn = func() *local.Client {
		t.Error("a local client was built although the knob skips the read")
		return localapitest.NewClient(nil)
	}
	enabled, err := defaultTailscaleSSHEnabled(context.Background())
	if !errors.Is(err, errTailscaleSSHCheckSkipped) || enabled {
		t.Fatalf("defaultTailscaleSSHEnabled() = %v, %v; want false, errTailscaleSSHCheckSkipped", enabled, err)
	}
}

// TestDoctorReportsASkippedTailscaleSSHCheckWithItsReason runs doctor's
// report with the production probe and the knob set.
func TestDoctorReportsASkippedTailscaleSSHCheckWithItsReason(t *testing.T) {
	t.Setenv(doctorSkipTailscaleSSHEnv, "1")
	old := doctorTailscaleSSHFn
	t.Cleanup(func() { doctorTailscaleSSHFn = old })
	doctorTailscaleSSHFn = defaultTailscaleSSHEnabled

	var result DoctorResult
	diagnoseTailscaleSSH(&result)
	result.finalize()
	assertDoctorTailscaleSSHSkipped(t, result)
	if result.Counts.Warnings != 0 || result.Counts.Errors != 0 || result.Counts.Critical != 0 {
		t.Fatalf("counts = %+v; a skipped informational check must not change doctor's status", result.Counts)
	}
}

// TestNewDoctorLocalClientIsThePlatformDefaultClient: the release default is
// the same zero client doctor always used (platform socket, host auth).
func TestNewDoctorLocalClientIsThePlatformDefaultClient(t *testing.T) {
	client := newDoctorLocalClient()
	if client.OmitAuth || client.Transport != nil || client.Dial != nil || client.Socket != "" || client.UseSocketOnly {
		t.Fatalf("newDoctorLocalClient() = %+v, want the zero local.Client", client)
	}
}

// TestDoctorSkipKnobIsTheOneTestIsolationSets keeps the product name and the
// name internal/testenv exports to compiled children in step.
func TestDoctorSkipKnobIsTheOneTestIsolationSets(t *testing.T) {
	if doctorSkipTailscaleSSHEnv != testenv.DoctorSkipTailscaleSSHEnv {
		t.Fatalf("doctor reads %s but test isolation sets %s", doctorSkipTailscaleSSHEnv, testenv.DoctorSkipTailscaleSSHEnv)
	}
}
