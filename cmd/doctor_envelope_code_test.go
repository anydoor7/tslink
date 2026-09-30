package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// TestDoctorEnvelopeCodeIsTheExitCode is A3-5's probe: doctor --json printed
// {"ok":true,"code":0} while the process exited 65, so a script reading .code
// saw success. The envelope's code is now the process exit code, as on every
// other command; ok stays true because the diagnosis itself completed, and
// data.health_exit_code carries the same number.
func TestDoctorEnvelopeCodeIsTheExitCode(t *testing.T) {
	seen := map[int]bool{}
	for name, setup := range map[string]func(t *testing.T){
		"warnings only": func(t *testing.T) {
			newDoctorTestEnv(t, []registry.Service{{Name: "web", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}})
		},
		"invalid entry": func(t *testing.T) {
			env := newDoctorTestEnv(t, nil)
			raw := `{"schema_version":1,"services":[{"name":"docs","type":"proxy","created_at":"2026-05-17T12:00:00Z"}]}`
			if err := os.WriteFile(env.regPath, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			setup(t)
			var out bytes.Buffer
			err := runDoctor(&out, doctorOptions{}, true)
			exit := output.ExitCode(err)
			var envelope struct {
				OK   bool `json:"ok"`
				Code int  `json:"code"`
				Data struct {
					HealthExitCode int `json:"health_exit_code"`
				} `json:"data"`
			}
			if jsonErr := json.Unmarshal(out.Bytes(), &envelope); jsonErr != nil {
				t.Fatalf("doctor --json output %q: %v", out.String(), jsonErr)
			}
			if envelope.Code != exit || envelope.Data.HealthExitCode != exit || !envelope.OK {
				t.Fatalf("envelope ok=%v code=%d health_exit_code=%d, process exit %d; want ok and the exit code in both", envelope.OK, envelope.Code, envelope.Data.HealthExitCode, exit)
			}
			seen[exit] = true
		})
	}
	// Control: the cases reach two different non-zero exits (warning and
	// critical), which the envelope used to report as 0.
	if len(seen) < 2 || seen[output.ExitSuccess] {
		t.Fatalf("exits seen = %v, want two different non-zero exits", seen)
	}
}
