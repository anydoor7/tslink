package cmd

import (
	"bytes"
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
)

// TestDoctorPristineDefaultTierIsHealthy pins R5-10. On a fresh install in the
// default tier (no stored credential, zero services, no daemon) doctor exited
// 64 because credential_none was a warning, although its own text says no
// credential is required; `tslink doctor && ...` failed on the documented
// first run. Once a service exists the same credential state is the info
// finding credential_tier1, so credential_none is info as well.
func TestDoctorPristineDefaultTierIsHealthy(t *testing.T) {
	newDoctorTestEnv(t, nil)
	doctorGetAPIKeyFn = func() (string, error) { return "", nil }
	doctorGetClientSecretFn = func() (string, error) { return "", nil }
	doctorReadFileFn = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	isRunningFn = func(string) bool { return false }

	var buf bytes.Buffer
	if err := runDoctor(&buf, doctorOptions{}, true); output.ExitCode(err) != output.ExitSuccess {
		t.Fatalf("pristine doctor ExitCode = %d (err=%v), want %d\n%s", output.ExitCode(err), err, output.ExitSuccess, buf.String())
	}
	result := decodeDoctorJSON(t, buf.String())
	if result.HealthStatus != doctorStatusOK || result.HealthExitCode != output.ExitSuccess {
		t.Fatalf("pristine doctor health = %q/%d, want %q/%d; findings=%+v", result.HealthStatus, result.HealthExitCode, doctorStatusOK, output.ExitSuccess, result.Findings)
	}
	finding := assertDoctorFinding(t, result, inspect.WarningCodeCredentialNone)
	if finding.Severity != doctorSeverityInfo {
		t.Fatalf("credential_none severity = %q, want %q like credential_tier1", finding.Severity, doctorSeverityInfo)
	}
	if tier1 := inspect.WarningCodeRegistry[inspect.WarningCodeCredentialTier1].Severity; finding.Severity != tier1 {
		t.Fatalf("credential_none severity %q differs from credential_tier1 %q", finding.Severity, tier1)
	}
}
