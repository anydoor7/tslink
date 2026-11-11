package cmd

import (
	"context"
	"io"
	"os"
	"strconv"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
)

// Every reduced role must never contradict the owner doctor about the daemon,
// computed by the real owner code path on the same files, in every daemon
// state the status reader distinguishes and the ones only the owner doctor
// can see (a foreign live PID, a supervisor still holding a process).
func TestMCPReducedDoctorDaemonNeverContradictsTheOwner(t *testing.T) {
	type state struct {
		name       string
		services   []registry.Service
		running    bool
		pidFile    bool   // write the test process's PID: live, provably foreign
		supervisor []byte // OS manager answer to the owner's scope probe
		wantState  string
		// Control: what the real owner doctor concludes on these files.
		ownerUnverified bool
	}
	app := []registry.Service{{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}}
	supervisorHolds := []byte("pid = 777\nMainPID=777\n")
	states := []state{
		{name: "running", services: app, running: true, wantState: daemonStateRunning},
		{name: "absent", services: app, wantState: daemonStateAbsent},
		{name: "absent while the supervisor holds a process", services: app, supervisor: supervisorHolds, wantState: daemonStateAbsent, ownerUnverified: true},
		{name: "foreign live PID", services: app, pidFile: true, wantState: daemonStateUnknown},
		{name: "foreign live PID, zero services", pidFile: true, wantState: daemonStateUnknown},
		{name: "absent, zero services", wantState: daemonStateAbsent},
	}
	for _, st := range states {
		t.Run(st.name, func(t *testing.T) {
			env := newDoctorTestEnv(t, st.services)
			t.Setenv(config.ConfigDirEnv, env.dir)
			isRunningFn = func(string) bool { return st.running }
			if st.pidFile {
				if err := os.WriteFile(env.pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			oldOutput := managerOutputFn
			t.Cleanup(func() { managerOutputFn = oldOutput })
			managerOutputFn = func(context.Context, string, ...string) ([]byte, error) { return st.supervisor, nil }
			owner := buildDoctorResult(context.Background(), doctorOptions{RegistryPath: env.regPath, PIDPath: env.pidPath, RuntimeSnapshotPath: env.snapshotPath, AuthHandoffPath: env.authHandoff, ReadOnly: true})
			if owner.Daemon.IdentityUnverified != st.ownerUnverified {
				t.Fatalf("fixture control: owner identity_unverified = %t, want %t", owner.Daemon.IdentityUnverified, st.ownerUnverified)
			}
			actions := defaultMCPActions(sharePaths{Registry: env.regPath, PID: env.pidPath, Snapshot: env.snapshotPath, AuthHandoff: env.authHandoff}, io.Discard)
			for _, scope := range reducedReadScopes() {
				status := reducedToolPayload(t, actions, scope, "status")
				got := reducedToolPayload(t, actions, scope, "doctor")
				daemon, _ := got["daemon"].(map[string]any)
				if daemon["running"] != owner.Daemon.Running {
					t.Fatalf("%v: reduced daemon.running = %v, owner %v", scope, daemon["running"], owner.Daemon.Running)
				}
				if daemon["state"] != st.wantState || daemon["state"] != status["daemon_state"] {
					t.Fatalf("%v: reduced daemon.state = %v, status daemon_state %v, want %s", scope, daemon["state"], status["daemon_state"], st.wantState)
				}
				// The reduced doctor states only what the status reader states:
				// no identity verdict of its own, in either direction.
				for _, key := range []string{"identity_unverified", "pid", "executable", "build_version", "build_skew"} {
					if _, present := daemon[key]; present {
						t.Fatalf("%v: reduced daemon carries %s: %v", scope, key, daemon)
					}
				}
				for _, f := range got["findings"].([]any) {
					if code := f.(map[string]any)["code"]; code == "daemon_not_running" || code == "daemon_identity_unverified" {
						t.Fatalf("%v: reduced doctor asserted a daemon verdict: %v", scope, f)
					}
				}
				if got["findings_scope"] != mcpFindingsScopeAppHealth {
					t.Fatalf("findings_scope = %v", got["findings_scope"])
				}
				validateAgainstScopedOutputSchema(t, "doctor", scope, got)
			}
		})
	}
}
