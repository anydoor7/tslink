package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
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

// formatSupervision is the single place that answers "will it still be there
// after a reboot", so every scope the detectors can produce has to survive the
// render, and an unverified autostart has to render no scope at all. The
// detectors are covered where they actually run: the launchd login scope in
// supervision_darwin_test.go's ownership matrix, and systemd's boot/login/
// unknown against real lingering states in supervision_r3_linux_test.go.
//
// An earlier version of this test opened with "verified autostart must name a
// scope" over the live detectSupervision. On darwin, which is the only host
// that runs this file, the isolated environment makes that call return
// unmanagedSupervision with Autostart=false, so the guard never evaluated. It
// read like a cross-platform invariant and was an assertion that could not
// fail; the real coverage it claimed now lives in the two files named above.
func TestBootstrapSupervisionRendersEveryAutostartScope(t *testing.T) {
	// Whole-output equality rather than substring containment: it states what
	// each scope renders to, and by stating all of it, it also fails when a
	// scope is printed for an autostart nobody verified. An assertion that
	// "autostart_scope" is absent would instead fall silent the day the field
	// is renamed, and read identically to a passing gate while doing nothing.
	for _, tc := range []struct {
		name  string
		super Supervision
		want  string
	}{
		{"boot", Supervision{Manager: "systemd", Autostart: true, AutostartScope: autostartScopeBoot},
			"Supervision: systemd (autostart=true, autostart_scope=boot, restart_on_exit=false)\n"},
		{"login", Supervision{Manager: "launchd", Autostart: true, AutostartScope: autostartScopeLogin},
			"Supervision: launchd (autostart=true, autostart_scope=login, restart_on_exit=false)\n"},
		{"unknown", Supervision{Manager: "systemd", Autostart: true, AutostartScope: autostartScopeUnknown},
			"Supervision: systemd (autostart=true, autostart_scope=unknown, restart_on_exit=false)\n"},
		{"absent", Supervision{Manager: "none"},
			"Supervision: none (autostart=false, restart_on_exit=false)\n"},
		// A login scope is only useful with the command that changes it, and
		// that command is the detector's to name because it is platform
		// specific. This pins the carriage: whatever the detector wrote has to
		// reach the operator through this renderer, unwrapped and unedited.
		{"login_detail_reaches_the_operator", Supervision{Manager: "systemd", Autostart: true, AutostartScope: autostartScopeLogin,
			Detail: `systemd user unit verified; it starts when this user logs in. Lingering is disabled, so it does NOT start at boot while nobody is logged in; for that run: loginctl enable-linger "$USER". Undo: tslink uninstall`},
			"Supervision: systemd (autostart=true, autostart_scope=login, restart_on_exit=false)\n" +
				`systemd user unit verified; it starts when this user logs in. Lingering is disabled, so it does NOT start at boot while nobody is logged in; for that run: loginctl enable-linger "$USER". Undo: tslink uninstall` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			formatSupervision(tc.super, &out)
			if out.String() != tc.want {
				t.Fatalf("rendered %q, want %q", out.String(), tc.want)
			}
		})
	}
}

// The install banner is printed before the installer runs, when nothing about
// this host has been inspected, so it cannot answer the boot-versus-login
// question. It used to promise "Autostart persists across sign-in/reboot"
// unconditionally, which contradicted the scope this same command reports
// seconds later on every host without lingering. It now states what is being
// installed, where, and how to undo it, and routes the scope question to the
// renderer that answers it from a Supervision that was actually detected.
//
// The routing sentence is pinned positively on purpose. Asserting that the old
// promise is absent would pass forever the moment that promise is reworded,
// and its output would be indistinguishable from a working gate. Asserting the
// routing sentence is present fails as soon as it is dropped or replaced.
func TestBootstrapInstallBannerRoutesTheScopeQuestion(t *testing.T) {
	dir := isolateBootstrap(t)
	installDaemonFn = func(context.Context, io.Writer) error {
		isRunningFn = func(string) bool { return true }
		detectSupervisionFn = func(string, bool, int) Supervision {
			return Supervision{Manager: "systemd", Installed: true, RestartOnExit: true, Autostart: true, AutostartScope: autostartScopeLogin}
		}
		return nil
	}
	var log bytes.Buffer
	if err := ensureDaemonErr(context.Background(), &log, false); err != nil {
		t.Fatalf("err=%v log=%s", err, &log)
	}
	path, _ := supervisorPath()
	for _, want := range []string{
		supervisorName(), path, dir, "tslink uninstall",
		"Autostart scope is not known before this host is inspected",
		"'tslink status' reports whether it returns at boot or only at sign-in",
	} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("install banner missing %q: %s", want, &log)
		}
	}
}
