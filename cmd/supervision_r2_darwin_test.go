//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func launchctlFixture(t *testing.T, state string, pid int) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/launchctl/launchctl-print-prod.txt")
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("state = running"), []byte("state = "+state), 1)
	data = bytes.Replace(data, []byte("pid = 41564"), []byte(fmt.Sprintf("pid = %d", pid)), 1)
	return data
}

func TestBootstrapLaunchctlRealFixture(t *testing.T) {
	dir := isolateBootstrap(t)
	data, err := os.ReadFile("testdata/launchctl/launchctl-print-prod.txt")
	if err != nil {
		t.Fatal(err)
	}
	state, pid := parseLaunchAgentState(data)
	if state != "running" || pid != 41564 {
		t.Fatalf("real output state=%q pid=%d", state, pid)
	}
	disabled, err := os.ReadFile("testdata/launchctl/launchctl-print-disabled-prod.txt")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := supervisorPath()
	var plist bytes.Buffer
	if err := plistTemplate.Execute(&plist, plistData{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, plist.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	managerOutputFn = func(_ string, args ...string) ([]byte, error) {
		if args[0] == "print-disabled" {
			return disabled, nil
		}
		return data, nil
	}
	for _, runAtLoad := range []bool{true, false} {
		content := plist.Bytes()
		if !runAtLoad {
			content = bytes.Replace(content, []byte("<key>RunAtLoad</key>\n  <true/>"), []byte("<key>RunAtLoad</key>\n  <false/>"), 1)
		}
		// XML indentation is an implementation detail; use the actual template span.
		if !runAtLoad && bytes.Equal(content, plist.Bytes()) {
			content = bytes.Replace(plist.Bytes(), []byte("<key>RunAtLoad</key>\n    <true/>"), []byte("<key>RunAtLoad</key>\n    <false/>"), 1)
		}
		if !runAtLoad && bytes.Equal(content, plist.Bytes()) {
			t.Fatal("RunAtLoad negative fixture did not change")
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		s := detectSupervision(filepath.Join(dir, "tslink.pid"), true, 41564)
		if s.Manager != "launchd" || s.Autostart != runAtLoad || !s.RestartOnExit {
			t.Fatalf("RunAtLoad=%t supervision=%+v", runAtLoad, s)
		}
	}
}

func TestBootstrapLaunchctlRealDisabledFormat(t *testing.T) {
	isolateBootstrap(t)
	data, err := os.ReadFile("testdata/launchctl/launchctl-print-disabled-prod.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Preserve the real structure; substitute an existing label with ours.
	lines := strings.Split(string(data), "\n")
	for _, value := range []string{"enabled", "disabled", "false", "true"} {
		t.Run(value, func(t *testing.T) {
			variant := append([]string(nil), lines...)
			changed := false
			for i, line := range variant {
				key, _, ok := strings.Cut(line, "=>")
				if ok && !changed {
					prefix := key[:len(key)-len(strings.TrimLeft(key, " \t"))]
					variant[i] = prefix + `"com.tslink.daemon" => ` + value
					changed = true
				}
			}
			if !changed {
				t.Fatal("real fixture has no overrides")
			}
			managerOutputFn = func(string, ...string) ([]byte, error) { return []byte(strings.Join(variant, "\n")), nil }
			want := value == "enabled" || value == "false"
			if got := launchdAutostartEnabled("gui/fixture"); got != want {
				t.Fatalf("real format %s enabled=%t want=%t", value, got, want)
			}
		})
	}
}

func TestBootstrapExplicitInstallRefusesUnverifiedManagerPID(t *testing.T) {
	dir := isolateBootstrap(t)
	path, _ := supervisorPath()
	var plist bytes.Buffer
	if err := plistTemplate.Execute(&plist, plistData{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, plist.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	managerOutputFn = func(string, ...string) ([]byte, error) { return launchctlFixture(t, "running", 41564), nil }
	// capture is invoked before any bootout or plist replacement in install.
	if _, err := captureLaunchAgentPreviousState(context.Background(), path); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("unverified live supervisor accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, plist.Bytes()) {
		t.Fatal("definition changed on refusal")
	}
}

func TestBootstrapDoctorMissingPIDWithLiveManagerKeepsProbes(t *testing.T) {
	isolateBootstrap(t)
	env := newDoctorTestEnv(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000"}})
	if _, err := os.Stat(env.pidPath); !os.IsNotExist(err) {
		t.Fatal("PID must be absent")
	}
	isRunningFn = func(string) bool { return false }
	managerOutputFn = func(string, ...string) ([]byte, error) { return launchctlFixture(t, "running", 41564), nil }
	probes := 0
	doctorProbeTargetFn = func(context.Context, string, time.Duration) error { probes++; return syscall.ECONNREFUSED }
	result := buildDoctorResult(doctorOptions{})
	if probes != 1 || !result.Daemon.IdentityUnverified {
		t.Fatalf("missing pid hid live manager: probes=%d daemon=%+v", probes, result.Daemon)
	}
	assertDoctorFinding(t, result, inspect.WarningCodeDaemonIdentityUnverified)
	assertDoctorFinding(t, result, inspect.WarningCodeTargetProbeRefused)
	assertDoctorNoFinding(t, result, inspect.WarningCodeDaemonNotRunning)
	assertDoctorNoFinding(t, result, inspect.WarningCodeTargetProbeSkippedDaemon)
}
