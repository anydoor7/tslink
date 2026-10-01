package cmd

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func writeCompiledDoctorRegistry(t *testing.T, home string, reg registry.Registry) string {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "tslink")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	reg.SchemaVersion = registry.CurrentRegistrySchemaVersion
	if reg.Services == nil {
		reg.Services = []registry.Service{}
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	regPath := filepath.Join(cfgDir, "registry.json")
	if err := os.WriteFile(regPath, append(data, '\n'), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return regPath
}

func writeDoctorExactRuntime(t *testing.T, home string, reg registry.Registry) {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "tslink")
	pid := os.Getpid()
	pidPath := filepath.Join(cfgDir, "tslink.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatalf("write pid: %v", err)
	}
	lowerBound := time.Now().Add(-time.Minute)
	if err := os.Chtimes(pidPath, lowerBound, lowerBound); err != nil {
		t.Fatalf("chtimes pid: %v", err)
	}
	fp, err := tsruntime.RegistryFingerprint(&reg, nil)
	if err != nil {
		t.Fatalf("fingerprint registry: %v", err)
	}
	now := time.Now()
	snapshot := tsruntime.NewSnapshot(pid, now, fp, now, nil)
	if err := tsruntime.Save(filepath.Join(cfgDir, "runtime.json"), snapshot); err != nil {
		t.Fatalf("write runtime snapshot: %v", err)
	}
}

func writeDoctorCredential(t *testing.T, home, filename, value string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "tslink")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, filename), []byte(value), 0o600); err != nil {
		t.Fatalf("write credential fixture: %v", err)
	}
}

func decodeCompiledDoctor(t *testing.T, stdout string) DoctorResult {
	t.Helper()
	results := parseCompiledJSONLines(t, stdout)
	if len(results) != 1 {
		t.Fatalf("doctor stdout record count = %d, want 1", len(results))
	}
	if !results[0].OK || results[0].Command != "doctor" {
		t.Fatalf("doctor execution envelope = %+v, want a completed diagnosis", results[0])
	}
	dataBytes, err := json.Marshal(results[0].Data)
	if err != nil {
		t.Fatalf("marshal doctor data: %v", err)
	}
	var data DoctorResult
	if err := json.Unmarshal(dataBytes, &data); err != nil {
		t.Fatalf("decode doctor payload: %v", err)
	}
	// The envelope code is the process exit code (A3-5), which is the
	// health exit code the data reports; callers check it against the exit.
	if results[0].Code != data.HealthExitCode {
		t.Fatalf("doctor envelope code = %d, want the health exit code %d", results[0].Code, data.HealthExitCode)
	}
	return data
}

// closedLoopbackTCPTarget returns 127.0.0.1:<port> for a port this test bound
// and released, so a probe of it is refused on every host instead of reaching
// whatever the machine runs on a well-known port (localhost:5432 used to hit a
// contributor's PostgreSQL).
func closedLoopbackTCPTarget(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	target := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release the loopback port: %v", err)
	}
	return target
}

func TestCompiledDoctorJSONHealthFixtures(t *testing.T) {
	validReg := registry.Registry{Services: []registry.Service{}}
	errorReg := registry.Registry{Services: []registry.Service{{
		Name:         "db",
		Type:         registry.TypeTCP,
		Target:       closedLoopbackTCPTarget(t),
		AllowedUsers: []string{"alice@example.com"},
	}}}

	tests := []struct {
		name       string
		setup      func(t *testing.T, home string)
		wantExit   int
		wantHealth string
		wantCode   int
	}{
		{
			name: "healthy",
			setup: func(t *testing.T, home string) {
				writeCompiledDoctorRegistry(t, home, validReg)
				writeDoctorExactRuntime(t, home, validReg)
				// The recommended dual-slot state (api-key for invites plus a
				// client-secret for durable daemon auth) is the only credential
				// posture that stays info-level; an api-key-only store is now a
				// warning by design, so a genuinely healthy fixture needs both.
				writeDoctorCredential(t, home, "apikey", "present")
				writeDoctorCredential(t, home, "clientsecret", "present")
			},
			wantExit:   output.ExitSuccess,
			wantHealth: doctorStatusOK,
			wantCode:   output.ExitSuccess,
		},
		{
			name: "warning",
			setup: func(t *testing.T, home string) {
				writeCompiledDoctorRegistry(t, home, validReg)
				writeDoctorExactRuntime(t, home, validReg)
				writeDoctorCredential(t, home, "authkey", "legacy-present")
			},
			wantExit:   output.ExitWarning,
			wantHealth: doctorStatusWarning,
			wantCode:   output.ExitWarning,
		},
		{
			name: "error",
			setup: func(t *testing.T, home string) {
				writeCompiledDoctorRegistry(t, home, errorReg)
				writeDoctorExactRuntime(t, home, errorReg)
				writeDoctorCredential(t, home, "apikey", "present")
			},
			wantExit:   output.ExitCritical,
			wantHealth: doctorStatusError,
			wantCode:   output.ExitCritical,
		},
		{
			name: "critical",
			setup: func(t *testing.T, home string) {
				cfgDir := filepath.Join(home, ".config", "tslink")
				if err := os.MkdirAll(cfgDir, 0o700); err != nil {
					t.Fatalf("mkdir config dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(cfgDir, "registry.json"), []byte("{"), 0o600); err != nil {
					t.Fatalf("write malformed registry: %v", err)
				}
				writeDoctorCredential(t, home, "apikey", "present")
			},
			wantExit:   output.ExitCritical,
			wantHealth: doctorStatusCritical,
			wantCode:   output.ExitCritical,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			tc.setup(t, home)
			stdout, stderr, exitCode := runCompiledTSLink(t, home, "", "doctor", "--json")
			if exitCode != tc.wantExit {
				t.Fatalf("exit = %d, want %d\nstdout=%s\nstderr=%s", exitCode, tc.wantExit, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			result := decodeCompiledDoctor(t, stdout)
			if result.ExecutionStatus != doctorExecutionCompleted {
				t.Fatalf("execution_status = %q, want %q", result.ExecutionStatus, doctorExecutionCompleted)
			}
			if result.HealthStatus != tc.wantHealth || result.Status != tc.wantHealth {
				t.Fatalf("health/status = %q/%q, want %q", result.HealthStatus, result.Status, tc.wantHealth)
			}
			if result.HealthExitCode != tc.wantCode {
				t.Fatalf("health_exit_code = %d, want %d", result.HealthExitCode, tc.wantCode)
			}
			if result.SchemaVersion != inspect.SchemaVersion {
				t.Fatalf("schema_version = %v, want %v", result.SchemaVersion, inspect.SchemaVersion)
			}
			// The child inherited the test isolation's knob, so it never read
			// the local tailscaled of the machine running the tests.
			assertDoctorTailscaleSSHSkipped(t, result)
		})
	}
}
