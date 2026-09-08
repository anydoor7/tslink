package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/registry"
)

// A live PID that belongs to a different program is positive proof that our
// daemon is stopped, not an unanswered question about it. Reporting it as
// merely unverified suppressed both daemon_not_running and daemon_unsupervised
// and left a dead daemon behind two warnings. The test process is exactly such
// a program: same Go module, wrong argv.
func TestBootstrapDoctorForeignPIDIsAStoppedDaemon(t *testing.T) {
	env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	if err := os.WriteFile(env.pidPath, []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if !daemon.IsForeignProcessFromPIDFile(env.pidPath) {
		t.Fatal("the test process should be provably foreign to a tslink serve daemon")
	}
	isRunningFn = func(string) bool { return false }
	oldDetect := detectSupervisionFn
	t.Cleanup(func() { detectSupervisionFn = oldDetect })
	detectSupervisionFn = func(_ string, running bool, _ int) Supervision {
		return unmanagedSupervision(running, "test")
	}
	doctorProbeTargetFn = func(context.Context, string, time.Duration) error {
		t.Fatal("a daemon proven stopped must not be probed as if it might be serving")
		return nil
	}
	result := buildDoctorResult(doctorOptions{})
	if result.Daemon.IdentityUnverified {
		t.Fatalf("foreign PID reported as unverified identity: %+v", result.Daemon)
	}
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonIdentityUnverified)
	if f := assertDoctorFinding(t, result, inspect.WarningCodeDaemonNotRunning); f.Severity != "error" {
		t.Fatalf("not-running finding=%+v", f)
	}
	assertDoctorFinding(t, result, inspect.WarningCodeDaemonUnsupervised)
	assertDoctorFinding(t, result, inspect.WarningCodeTargetProbeSkippedDaemon)
}

// The scope field exists because a single autostart boolean cannot answer
// "will it be there after a reboot" for a per-user supervisor. Whatever the
// platform reports, a verified autostart has to say which of the two it means.
func TestBootstrapSupervisionDeclaresAutostartScope(t *testing.T) {
	isolateBootstrap(t)
	s := detectSupervision("", false, 0)
	if s.Autostart && s.AutostartScope == "" {
		t.Fatalf("verified autostart with no scope: %+v", s)
	}
	for _, tc := range []struct {
		name  string
		super Supervision
		want  string
	}{
		{"boot", Supervision{Manager: "systemd", Autostart: true, AutostartScope: autostartScopeBoot}, "autostart_scope=boot"},
		{"login", Supervision{Manager: "launchd", Autostart: true, AutostartScope: autostartScopeLogin}, "autostart_scope=login"},
		{"absent", Supervision{Manager: "none"}, "autostart=false, restart_on_exit=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			formatSupervision(tc.super, &out)
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("rendered %q, want %q", out.String(), tc.want)
			}
			if tc.want == "autostart=false, restart_on_exit=false" && strings.Contains(out.String(), "autostart_scope") {
				t.Fatalf("unverified autostart claimed a scope: %q", out.String())
			}
		})
	}
}
