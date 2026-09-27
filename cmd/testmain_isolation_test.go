package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
)

// A zero-test child exercises TestMain's setup and teardown without running
// any command body or touching a service manager.
func TestCmdTestMainCleansIsolatedConfig(t *testing.T) {
	tmp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "TSLINK_CONFIG_DIR=", "TSLINK_STOP_LIVENESS_HELPER=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zero-test child failed: %v\n%s", err, out)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tslink-cmd-test-config-") {
			t.Fatalf("TestMain leaked config directory %s", entry.Name())
		}
	}
}

func TestCmdTestMainFailsClosedWhenTempUnavailable(t *testing.T) {
	badTmp := filepath.Join(t.TempDir(), "missing-parent")
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "TMPDIR="+badTmp, "TMP="+badTmp, "TEMP="+badTmp, "TSLINK_CONFIG_DIR=", "TSLINK_STOP_LIVENESS_HELPER=")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "cannot isolate cmd tests") {
		t.Fatalf("TestMain did not refuse missing temp directory: err=%v output=%s", err, out)
	}
}

func TestCmdTestMainOverridesInheritedConfigWithoutChangingIt(t *testing.T) {
	if os.Getenv("TSLINK_TESTMAIN_CONFIG_WRITE_HELPER") == "1" {
		dir, err := config.Dir()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child-sentinel"), []byte("child wrote through config.Dir"), 0o600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(os.Stdout, "isolated-config-write-ok")
		return
	}
	productionLike := t.TempDir()
	marker := filepath.Join(productionLike, "registry.json")
	const original = "production marker must remain byte-for-byte unchanged\n"
	if err := os.WriteFile(marker, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCmdTestMainOverridesInheritedConfigWithoutChangingIt$")
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp, "TSLINK_CONFIG_DIR="+productionLike,
		"TSLINK_STOP_LIVENESS_HELPER=", "TSLINK_SYSTEMD_E2E=", "TSLINK_TESTMAIN_CONFIG_WRITE_HELPER=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "isolated-config-write-ok") {
		t.Fatalf("config-writing child with inherited config failed: %v\n%s", err, out)
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != original {
		t.Fatalf("inherited config marker changed: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(productionLike, "child-sentinel")); !os.IsNotExist(err) {
		t.Fatalf("child wrote into inherited config: stat error=%v", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "tslink-cmd-test-config-") {
			t.Fatalf("inherited-config child leaked isolated config %s", entry.Name())
		}
	}
}

func TestDedicatedSystemdE2ERequiresExactSelector(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	t.Setenv("TSLINK_SYSTEMD_E2E", "1")
	os.Args = []string{"cmd.test", "-test.run=.*"}
	if dedicatedSystemdE2EInvocation() {
		t.Fatal("broad test selector bypassed ordinary config isolation")
	}
	if runtime.GOOS == "linux" {
		for _, args := range [][]string{
			{"cmd.test", "-test.run", "^TestSystemdInstallE2E$"},
			{"cmd.test", "-test.run=^TestSystemdInstallE2E$"},
			{"cmd.test", "--test.run=^TestSystemdInstallE2E$"},
		} {
			os.Args = args
			if !dedicatedSystemdE2EInvocation() {
				t.Fatalf("dedicated systemd E2E selector was not recognized: %q", args)
			}
		}
		for _, args := range [][]string{
			{"cmd.test", "-test.run=^TestSystemdInstallE2E$", "-test.run=.*"},
			{"cmd.test", "-test.run=.*", "-test.run=^TestSystemdInstallE2E$"},
			{"cmd.test", "-test.run=^TestSystemdInstallE2E$", "-test.run=^TestSystemdInstallE2E$"},
		} {
			os.Args = args
			if dedicatedSystemdE2EInvocation() {
				t.Fatalf("repeated test selector bypassed config isolation: %q", args)
			}
		}
	}
	t.Setenv("TSLINK_SYSTEMD_E2E", "")
	os.Args = []string{"cmd.test", "-test.run=^TestSystemdInstallE2E$"}
	if dedicatedSystemdE2EInvocation() {
		t.Fatal("exact selector without explicit E2E opt-in bypassed config isolation")
	}
}

// This helper is deliberately harmless. If a child test body starts, it only
// emits a marker and checks its config path; it never calls the real E2E test
// or the service-manager guard opt-out.
func TestCmdTestMainRejectsNonDedicatedSystemdE2E(t *testing.T) {
	const reached = "harmless E2E-scope child body reached"
	if os.Getenv("TSLINK_TESTMAIN_E2E_SCOPE_HELPER") == "1" {
		fmt.Fprintln(os.Stdout, reached)
		dir, err := config.Dir()
		if err != nil {
			t.Fatal(err)
		}
		if dir == os.Getenv("TSLINK_TESTMAIN_INHERITED_CONFIG") {
			t.Fatal("ordinary test body retained inherited config")
		}
		return
	}
	const thisTest = "^TestCmdTestMainRejectsNonDedicatedSystemdE2E$"
	for _, tc := range []struct {
		name    string
		args    []string
		enabled bool
	}{
		{name: "disabled opt-in ordinary control", args: []string{"-test.run=" + thisTest}},
		{name: "enabled other exact", args: []string{"-test.run=" + thisTest}, enabled: true},
		{name: "enabled broad", args: []string{"-test.run=^TestCmdTestMain"}, enabled: true},
		{name: "enabled repeated", args: []string{"-test.run=^TestSystemdInstallE2E$", "-test.run=" + thisTest}, enabled: true},
		{name: "enabled missing", args: []string{"-test.run=^$"}, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inherited := t.TempDir()
			cmd := exec.Command(os.Args[0], tc.args...)
			enabled := ""
			if tc.enabled {
				enabled = "1"
			}
			cmd.Env = append(os.Environ(), "TSLINK_SYSTEMD_E2E="+enabled,
				"TSLINK_CONFIG_DIR="+inherited, "TSLINK_TESTMAIN_INHERITED_CONFIG="+inherited,
				"TSLINK_TESTMAIN_E2E_SCOPE_HELPER=1", "TSLINK_STOP_LIVENESS_HELPER=")
			out, err := cmd.CombinedOutput()
			if tc.enabled {
				if err == nil || !strings.Contains(string(out), "systemd E2E requires Linux and one exact") || strings.Contains(string(out), reached) {
					t.Fatalf("non-dedicated E2E reached a test body or failed unclearly: err=%v output=%s", err, out)
				}
			} else if err != nil || !strings.Contains(string(out), reached) {
				t.Fatalf("disabled opt-in did not run isolated harmless control: err=%v output=%s", err, out)
			}
		})
	}
}
