package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
)

// TestDoctorReportsAnIsolatedEntryBeforeAValidOne is the B1 review's binary
// probe: with registry [docs (proxy, no target), web (proxy 127.0.0.1:8080)],
// doctor saw docs with web's target, so it probed that target for both and
// never reported docs as invalid.
func TestDoctorReportsAnIsolatedEntryBeforeAValidOne(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	raw := `{"schema_version":1,"services":[
{"name":"docs","type":"proxy","created_at":"2026-05-17T12:00:00Z"},
{"name":"web","type":"proxy","target":"http://127.0.0.1:8080","tags":["tag:tsmain"],"created_at":"2026-05-17T12:00:00Z"}
]}`
	if err := os.WriteFile(env.regPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var probed []string
	doctorProbeTargetFn = func(_ context.Context, address string, _ time.Duration) error {
		probed = append(probed, address)
		return nil
	}
	result := buildDoctorResult(doctorOptions{})
	invalid := map[string]bool{}
	for _, finding := range result.Findings {
		if finding.Code == inspect.WarningCodeRegistryServiceInvalid {
			invalid[finding.Service] = true
		}
	}
	if !invalid["docs"] {
		t.Fatalf("doctor did not report %s for docs; findings = %+v", inspect.WarningCodeRegistryServiceInvalid, result.Findings)
	}
	if invalid["web"] {
		t.Fatalf("doctor reported web invalid; findings = %+v", result.Findings)
	}
	// Only web has a target to probe; docs inherited it before the fix.
	if len(probed) != 1 || probed[0] != "127.0.0.1:8080" {
		t.Fatalf("probed targets = %v, want only web's 127.0.0.1:8080", probed)
	}
}
