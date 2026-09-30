package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// errDaemonizeStubbed stops the daemon-mode run right after the point under
// test, the decision to daemonize.
var errDaemonizeStubbed = errors.New("daemonize stubbed by the test")

// badServiceEntries are services whose problem is their own. The daemon's sync
// isolates every one of them and reports it for that service with its stable
// code (internal/server TestDaemonReportsBadServiceEntriesPerService).
func badServiceEntries(t *testing.T) map[string]map[string]any {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "gone")
	return map[string]map[string]any{
		"file share whose directory is gone": {"name": "docs", "type": "file", "path": missing, "tags": []string{"tag:tsmain"}},
		"hand-edited link-local target":      {"name": "cam", "type": "proxy", "target": "http://169.254.10.10:80", "tags": []string{"tag:tsmain"}},
		"Funnel with an allow list":          {"name": "public-app", "type": "proxy", "target": "http://localhost:3000", "funnel": true, "public_ack": true, "allowed_users": []string{"alice@example.com"}, "tags": []string{"tag:tsmain"}},
		"Funnel with a control_url":          {"name": "public-app", "type": "proxy", "target": "http://localhost:3000", "funnel": true, "public_ack": true, "control_url": "https://headscale.example.com", "tags": []string{"tag:tsmain"}},
		"Funnel on a file share":             {"name": "public-files", "type": "file", "path": t.TempDir(), "funnel": true, "public_ack": true, "tags": []string{"tag:tsmain"}},
		"Funnel on a TCP service":            {"name": "public-db", "type": "tcp", "target": "localhost:5432", "port": 5432, "funnel": true, "public_ack": true, "tags": []string{"tag:tsmain"}},
		"invalid tag":                        {"name": "legacy", "type": "proxy", "target": "http://localhost:3000", "tags": []string{"tag:Bad"}},
	}
}

func writeRegistryWithBadEntry(t *testing.T, regPath string, bad map[string]any) {
	t.Helper()
	services := []map[string]any{
		{"name": "web", "type": "proxy", "target": "http://127.0.0.1:3000", "tags": []string{"tag:tsmain"}, "created_at": "2026-09-29T00:00:00Z"},
		bad,
	}
	bad["created_at"] = "2026-09-29T00:00:01Z"
	data, err := json.Marshal(map[string]any{"schema_version": 1, "services": services})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(regPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// serve refuses only problems of the registry document itself. A per-service
// problem passes to the daemon, which reports it for that service, so one bad
// entry does not keep every service down at the next login.
func TestServeStartsWhenOneServiceEntryIsBad(t *testing.T) {
	for name, bad := range badServiceEntries(t) {
		for _, daemonMode := range []bool{false, true} {
			mode := "foreground"
			if daemonMode {
				mode = "daemon"
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				dir := t.TempDir()
				mockServeDefaults(t, dir)
				writeRegistryWithBadEntry(t, filepath.Join(dir, "registry.json"), bad)
				serveDaemon = daemonMode
				mock := &mockServer{}
				serveNewServerFn = func(string, string) (serverRunner, error) { return mock, nil }
				daemonized := false
				serveDaemonizeFn = func(string, string, string, bool, bool, bool) (int, error) {
					daemonized = true
					return 0, errDaemonizeStubbed
				}

				cmd := findServeCmd(t)
				err := cmd.RunE(cmd, nil)
				if daemonMode {
					if !daemonized || !errors.Is(err, errDaemonizeStubbed) {
						t.Fatalf("serve --daemon refused before daemonizing: %v", err)
					}
					return
				}
				if err != nil || !mock.runCalled {
					t.Fatalf("serve error = %v, run = %v; want the daemon started", err, mock.runCalled)
				}
			})
		}
	}
}

// The document itself is still checked before anything starts, and before a
// stale PID file or the daemon child is touched.
func TestServeStillRefusesAMalformedRegistry(t *testing.T) {
	for _, daemonMode := range []bool{false, true} {
		dir := t.TempDir()
		mockServeDefaults(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "registry.json"), []byte(`{"schema_version":1,"services":[`), 0o600); err != nil {
			t.Fatal(err)
		}
		serveDaemon = daemonMode
		serveNewServerFn = func(string, string) (serverRunner, error) {
			t.Fatal("server constructed for a malformed registry")
			return nil, nil
		}
		serveDaemonizeFn = func(string, string, string, bool, bool, bool) (int, error) {
			t.Fatal("daemon child started for a malformed registry")
			return 0, nil
		}
		serveRemovePIDFn = func(string) { t.Fatal("stale PID removed before the registry was checked") }
		cmd := findServeCmd(t)
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Fatalf("serve (daemon=%v) started with a malformed registry.json", daemonMode)
		}
	}
}
