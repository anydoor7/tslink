//go:build darwin

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// Keep these tests usable against the frozen detector: exercise its public
// result, not a new parser helper, so a baseline failure must reach an assertion.
func loadedPolicyEnvironment(t *testing.T, diskKeepAlive bool) (string, string) {
	t.Helper()
	dir := isolateBootstrap(t)
	oldHome, oldUID := userHomeDirFn, userUIDFn
	t.Cleanup(func() { userHomeDirFn, userUIDFn = oldHome, oldUID })
	home := filepath.Dir(filepath.Dir(dir))
	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 501 }
	configXML, err := xmlEscapeValue("ConfigDir", dir)
	if err != nil {
		t.Fatal(err)
	}
	// A valid disk definition with independently selectable desired KeepAlive.
	data := []byte(fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict>
<key>Label</key><string>com.tslink.daemon</string>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><%t/>
<key>EnvironmentVariables</key><dict><key>TSLINK_CONFIG_DIR</key><string>%s</string></dict>
</dict></plist>`, diskKeepAlive, configXML))
	values, err := launchdDefinition(data)
	if err != nil || values["KeepAlive"] != diskKeepAlive || !supervisorConfigMatches(data, dir) {
		t.Fatalf("invalid disk control: values=%v err=%v", values, err)
	}
	path, err := plistPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// Keep the consumer's real print structure (including nested coalitions).
// Change only the observed properties line; empty means absent, not disabled.
func loadedPolicyPrint(t *testing.T, state string, pid int, properties string) []byte {
	t.Helper()
	lines := strings.Split(string(launchctlFixture(t, state, pid)), "\n")
	found := 0
	for i, line := range lines {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "properties" {
			lines[i] = properties
			found++
		}
	}
	if found != 1 {
		t.Fatalf("expected one real properties line, got %d", found)
	}
	return []byte(strings.Join(lines, "\n"))
}

func loadedPolicyManager(t *testing.T, gui, user []byte, disabled string, disabledErr error) *int {
	t.Helper()
	queries := new(int)
	managerOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name != "launchctl" || len(args) != 2 {
			t.Fatalf("unexpected manager call %s %v", name, args)
		}
		if args[0] == "print-disabled" {
			if args[1] != "gui/501" && args[1] != "user/501" {
				t.Fatalf("unexpected disabled domain %v", args)
			}
			return []byte(disabled), disabledErr
		}
		if args[0] != "print" {
			t.Fatalf("unexpected manager action %v", args)
		}
		*queries++
		var output []byte
		switch args[1] {
		case "gui/501/com.tslink.daemon":
			output = gui
		case "user/501/com.tslink.daemon":
			output = bytes.ReplaceAll(user, []byte("gui/501"), []byte("user/501"))
		default:
			t.Fatalf("unexpected print domain %v", args)
		}
		if output == nil {
			return []byte("Could not find service"), errors.New("absent")
		}
		return output, nil
	}
	return queries
}

func TestSupervisionLoadedPolicyMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disk       bool
		properties string
		want       bool
	}{
		{"disktrue_loadedfalse", true, "properties = runatload", false},
		{"diskfalse_loadedtrue", false, "properties = keepalive | runatload", true},
		{"valid_loadedtrue", true, "properties = keepalive | runatload | inferred program", true},
		{"valid_with_environment_mapping", true, "properties = keepalive | runatload", true},
		{"missing", true, "", false},
		{"unknown", true, "properties = unknown", false},
		{"empty", true, "properties = ", false},
		{"substring_prefix", true, "properties = notkeepalive | runatload", false},
		{"substring_suffix", true, "properties = keepalive-disabled | runatload", false},
		{"substring_phrase", true, "properties = no keepalive | runatload", false},
		{"wrong_key", true, "otherproperties = keepalive | runatload", false},
		{"wrong_separator", true, "properties => keepalive", false},
		{"environment_mapping", true, "properties => runatload | keepalive", false},
		{"dictionary_not_tokens", true, "properties = { runatload | keepalive | }", false},
		{"comma_separator", true, "properties = keepalive, runatload", false},
		{"boolean_not_tokens", true, "properties = keepalive=true", false},
		{"quoted_token", true, `properties = "keepalive" | runatload`, false},
		{"case_mismatch", true, "properties = KeepAlive | runatload", false},
		{"empty_token", true, "properties = keepalive || runatload", false},
		{"duplicate_line", true, "properties = runatload\nproperties = keepalive", false},
		{"valid_whitespace", true, "\tproperties\t=\trunatload |  keepalive\t| inferred program", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, path := loadedPolicyEnvironment(t, tc.disk)
			output := loadedPolicyPrint(t, "running", 4242, tc.properties)
			if tc.name == "valid_with_environment_mapping" {
				withEnvironment := bytes.Replace(output, []byte("inherited environment = {\n"), []byte("inherited environment = {\n\t\tproperties => runatload | keepalive\n"), 1)
				if bytes.Equal(output, withEnvironment) {
					t.Fatal("real fixture environment mapping was not inserted")
				}
				output = withEnvironment
			}
			queries := loadedPolicyManager(t, output, nil, "disabled services = {\n}\n", nil)
			s := detectSupervision("", true, 4242)
			if *queries != 2 || s.Manager != "launchd" || !s.Installed || s.Path != path || !s.Autostart || s.AutostartScope != autostartScopeLogin {
				t.Fatalf("ownership/future-login control failed: queries=%d supervision=%+v", *queries, s)
			}
			if s.RestartOnExit != tc.want {
				t.Fatalf("loaded policy assertion: disk=%t properties=%q restart=%t want=%t", tc.disk, tc.properties, s.RestartOnExit, tc.want)
			}
			if !tc.want && !strings.Contains(s.Detail, "keepalive") {
				t.Fatalf("missing loaded-policy diagnostic: %+v", s)
			}
		})
	}
}

func TestSupervisionLoadedPolicyOwnershipDomains(t *testing.T) {
	for _, tc := range []struct {
		name              string
		guiPID, userPID   int // -1 means domain absent/error.
		guiKeep, userKeep bool
		wantManager       string
		wantRestart       bool
	}{
		{"gui_owned", 4242, -1, true, false, "launchd", true},
		{"user_owned", -1, 4242, false, true, "launchd", true},
		{"unmatched_user_cannot_donate", 4242, 9999, false, true, "launchd", false},
		{"unmatched_gui_cannot_donate", 9999, 4242, true, false, "launchd", false},
		{"unmatched_user_cannot_erase", 4242, 9999, true, false, "launchd", true},
		{"unmatched_gui_cannot_erase", 9999, 4242, false, true, "launchd", true},
		{"foreign_both", 9999, 9999, true, true, "manual", false},
		{"missing_both", -1, -1, true, true, "manual", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loadedPolicyEnvironment(t, true)
			printFor := func(pid int, keep bool) []byte {
				if pid < 0 {
					return nil
				}
				properties := "properties = runatload"
				if keep {
					properties += " | keepalive"
				}
				return loadedPolicyPrint(t, "running", pid, properties)
			}
			loadedPolicyManager(t, printFor(tc.guiPID, tc.guiKeep), printFor(tc.userPID, tc.userKeep), "disabled services = {\n}\n", nil)
			manager := managerOutputFn
			managerOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if args[0] == "print-disabled" {
					wantDomain := "gui/501"
					if tc.userPID == 4242 {
						wantDomain = "user/501"
					}
					if args[1] != wantDomain {
						t.Fatalf("disabled query escaped owning domain: %v want %s", args, wantDomain)
					}
				}
				return manager(ctx, name, args...)
			}
			s := detectSupervision("", true, 4242)
			if s.Manager != tc.wantManager || s.RestartOnExit != tc.wantRestart || s.Autostart != (tc.wantManager == "launchd") {
				t.Fatalf("ownership-domain policy assertion: %+v", s)
			}
		})
	}
}

func TestSupervisionLoadedPolicyPreservesBoundaries(t *testing.T) {
	for _, scenario := range []string{"disk_runatload_false", "disk_disabled", "override_disabled", "disabled_unknown", "disabled_error", "print_error", "state_not_running", "stopped_bound", "stopped_loadedfalse", "stopped_foreign_config", "legacy_running_no_config", "missing_plist", "invalid_plist"} {
		t.Run(scenario, func(t *testing.T) {
			_, path := loadedPolicyEnvironment(t, true)
			output := loadedPolicyPrint(t, "running", 4242, "properties = keepalive | runatload")
			disabled := "disabled services = {\n}\n"
			var disabledErr error
			running, wantOwned, wantAutostart, wantRestart := true, true, false, true
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "disk_runatload_false":
				data = bytes.Replace(data, []byte("<key>RunAtLoad</key><true/>"), []byte("<key>RunAtLoad</key><false/>"), 1)
			case "disk_disabled":
				data = bytes.Replace(data, []byte("<key>RunAtLoad</key>"), []byte("<key>Disabled</key><true/><key>RunAtLoad</key>"), 1)
			case "override_disabled":
				disabled = "disabled services = {\n\"com.tslink.daemon\" => disabled\n}\n"
			case "disabled_unknown":
				disabled = "unrecognized format"
			case "disabled_error":
				disabledErr = errors.New("access denied")
			case "print_error":
				wantOwned, wantRestart = false, false
			case "state_not_running":
				output = loadedPolicyPrint(t, "waiting", 4242, "properties = keepalive")
				wantOwned, wantRestart = false, false
			case "stopped_bound", "stopped_loadedfalse", "stopped_foreign_config":
				running, wantAutostart = false, true
				output = loadedPolicyPrint(t, "waiting", 0, "properties = keepalive | runatload")
				if scenario == "stopped_loadedfalse" {
					output = loadedPolicyPrint(t, "waiting", 0, "properties = runatload")
					wantRestart = false
				}
				if scenario == "stopped_foreign_config" {
					data = bytes.Replace(data, []byte("TSLINK_CONFIG_DIR"), []byte("OTHER_CONFIG_DIR"), 1)
					wantOwned, wantAutostart, wantRestart = false, false, false
				}
			case "legacy_running_no_config":
				data = bytes.Replace(data, []byte("TSLINK_CONFIG_DIR"), []byte("OTHER_CONFIG_DIR"), 1)
				wantAutostart = true
			case "missing_plist", "invalid_plist":
				wantOwned, wantRestart = false, false
				data = []byte("not a plist")
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "missing_plist" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			loadedPolicyManager(t, output, nil, disabled, disabledErr)
			if scenario == "print_error" {
				// Even recognizable output accompanying an error is not proof.
				managerOutputFn = func(context.Context, string, ...string) ([]byte, error) { return output, errors.New("access denied") }
			}
			s := detectSupervision("", running, 4242)
			if (s.Manager == "launchd") != wantOwned || s.Autostart != wantAutostart || s.RestartOnExit != wantRestart || (s.AutostartScope == autostartScopeLogin) != wantAutostart {
				t.Fatalf("boundary assertion: %+v owned=%t autostart=%t restart=%t", s, wantOwned, wantAutostart, wantRestart)
			}
		})
	}
}

func TestSupervisionLoadedPolicyFastPathRealDetect(t *testing.T) {
	for _, entry := range []string{"ensure", "default_add"} {
		for _, domain := range []string{"gui", "user"} {
			for _, keepAlive := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/loaded_%t", entry, domain, keepAlive), func(t *testing.T) {
					dir, path := loadedPolicyEnvironment(t, true)
					before, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					properties := "properties = runatload"
					if keepAlive {
						properties += " | keepalive | inferred program"
					}
					output := loadedPolicyPrint(t, "running", 4242, properties)
					var gui, user []byte
					if domain == "gui" {
						gui = output
					} else {
						user = output
					}
					queries := loadedPolicyManager(t, gui, user, "disabled services = {\n}\n", nil)
					isRunningFn = func(string) bool { return true }
					detectSupervisionFn = detectSupervisionContext // Real detector, not a Supervision stub.
					var out string
					if entry == "ensure" {
						err = ensureDaemon(context.Background(), io.Discard, false)
					} else {
						out, err = runAddCmdOutput(t, []string{"loaded-policy-app"}, map[string]string{"proxy": "localhost:3000"})
						reg, loadErr := registry.Load(filepath.Join(dir, "registry.json"))
						if loadErr != nil || len(reg.Services) != 1 || reg.Services[0].Name != "loaded-policy-app" {
							t.Fatalf("default add lost persisted configuration: %+v err=%v", reg, loadErr)
						}
					}
					if *queries == 0 {
						t.Fatal("fast path never reached real detector")
					}
					if keepAlive {
						if err != nil {
							t.Fatalf("valid installed job rejected: %v output=%q", err, out)
						}
					} else if code, _ := registry.ErrorCode(err); code != "daemon_supervision_unverified" {
						t.Fatalf("fast-path loaded-policy assertion: code=%q err=%v output=%q", code, err, out)
					}
					after, readErr := os.ReadFile(path)
					if readErr != nil || !bytes.Equal(before, after) {
						t.Fatal("fast-path check changed installed plist")
					}
				})
			}
		}
	}
}
