package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

func TestAccessLogCLIAndMCP(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	dir, _ := config.Dir()
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s, err := accesslog.New(dir, accesslog.Options{}, func() time.Time { return at })
	if err != nil {
		t.Fatal(err)
	}
	s.Record(accesslog.Event{App: "photos", Kind: "http", Identity: accesslog.Identity{Login: "alice"}, Method: "GET", Path: "/secret?token=never-store", Status: 200, Decision: "allowed"})
	s.Record(accesslog.Event{App: "docs", Kind: "guest", Identity: accesslog.Identity{Login: "bob"}, Status: 403, Decision: "denied", Reason: "expired"})
	s.Close()
	<-s.Done()
	original := accessLogNowFn
	accessLogNowFn = func() time.Time { return at }
	t.Cleanup(func() { accessLogNowFn = original })
	r, err := readAccessLog(accessLogArguments{App: "photos", Who: "alice", Since: "24h", Limit: 1})
	if err != nil || len(r.Events) != 1 || r.Events[0].Path != "/secret" {
		t.Fatalf("CLI %+v %v", r, err)
	}
	var out bytes.Buffer
	formatAccessLog(r, &out)
	if !strings.Contains(out.String(), "person alice: 1") || !strings.Contains(out.String(), "app photos: 1 events, last seen 2030-01-01") {
		t.Fatal(out.String())
	}
	paths := mcpSharePaths(t)
	paths.Registry = filepath.Join(dir, "registry.json")
	actions := defaultMCPActions(paths, &out)
	before := configTreeDigest(t, dir)
	for _, tool := range []string{"access_log", "access_summary"} {
		stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+tool+`","arguments":{"decision":"denied"}}}`), actions)
		frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))
		result := frame["result"].(map[string]any)
		if result["isError"] == true {
			t.Fatal(stdout)
		}
		content := result["structuredContent"].(map[string]any)
		if tool == "access_log" {
			if len(content["events"].([]any)) != 1 || content["summary"].(map[string]any)["count"] != float64(1) {
				t.Fatal(stdout)
			}
		} else {
			if content["count"] != float64(1) || content["denied"] != float64(1) {
				t.Fatal(stdout)
			}
		}
	}
	after := configTreeDigest(t, dir)
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if !bytes.Equal(a, b) {
		t.Fatal("read-only access tools mutated files")
	}
	h := accessHealthForRegistry(paths.Registry, filepath.Join(dir, "tslink.pid"), filepath.Join(dir, "runtime.json"))
	if h.Size <= 0 || h.LastWrite == nil || h.Drops != 0 {
		t.Fatalf("status health %+v", h)
	}
	out.Reset()
	formatAccessHealth(h, &out)
	if !strings.Contains(out.String(), "drops=0") {
		t.Fatal(out.String())
	}
}
func TestAccessLogArgumentValidation(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, a := range []accessLogArguments{{Since: "bad"}, {Since: "0s"}, {Since: "-1h"}, {Until: "24h"}, {Decision: "bad"}, {Limit: 10001}, {Since: "2031-01-01T00:00:00Z", Until: "2030-01-01T00:00:00Z"}} {
		if _, err := accessLogFilter(a, now); err == nil {
			t.Fatalf("invalid args admitted %+v", a)
		}
	}
	for _, a := range []accessLogArguments{{Since: "24h"}, {Since: "2029-12-31T19:00:00-05:00", Until: "2030-01-01T01:00:00Z"}} {
		f, e := accessLogFilter(a, now)
		if e != nil || f.Since == nil || f.Since.Location() != time.UTC {
			t.Fatalf("valid filter %+v %v", f, e)
		}
	}
}
func TestAccessLogConfigStrictAndCLI(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	var out bytes.Buffer
	for key, value := range map[string]string{"access-log-enabled": "false", "access-log-path": "false", "access-log-path-mode": "full", "access-log-retention-days": "7", "access-log-max-bytes": "65536", "access-log-queue-size": "1"} {
		if err := configSet(key, value, &out, false); err != nil {
			t.Fatal(err)
		}
		cfg, _ := config.LoadGlobalConfig()
		actual, set := accessLogOptionValue(cfg, key)
		if !set || actual != value {
			t.Fatalf("config %s %s %t", key, actual, set)
		}
		if err := configGet(key, &out, false); err != nil {
			t.Fatal(err)
		}
	}
	for key, values := range map[string][]string{"access-log-enabled": {"TRUE", "1"}, "access-log-path": {"yes"}, "access-log-retention-days": {"-1", "3651", "x"}, "access-log-max-bytes": {"1", "1073741825"}, "access-log-queue-size": {"0", "65537"}} {
		for _, value := range values {
			if err := configSet(key, value, &out, false); err == nil {
				t.Fatalf("invalid config %s=%s", key, value)
			}
		}
	}
	for _, key := range validConfigKeys[1:] {
		if err := configSet(key, "", &out, false); err != nil {
			t.Fatal(err)
		}
		cfg, _ := config.LoadGlobalConfig()
		if _, set := accessLogOptionValue(cfg, key); set {
			t.Fatalf("reset %s ignored", key)
		}
	}
	path, _ := config.ConfigPath()
	for _, raw := range []string{`{"access_log":{"path_mode":"unsafe"}}`, `{"access_log":{"path_mode":false}}`, `{"access_log":{"unknown":true}}`, `{"access_log":{"queue_size":-1}}`, `{"access_log":{"max_bytes":1}}`, `{"access_log":{"retention_days":3651}}`, `{"access_log":{"record_path":"false"}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadGlobalConfig(); err == nil {
			t.Fatalf("strict config accepted %s", raw)
		}
	}
}

func TestAccessCommandsThroughCobra(t *testing.T) {
	stubDoctorTailscaleSSH(t, false, nil)
	testenv.SetHome(t, t.TempDir())
	dir, _ := config.Dir()
	regPath, _ := config.RegistryPath()
	if _, err := registry.Add(regPath, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://127.0.0.1:3000"}); err != nil {
		t.Fatal(err)
	}
	pathCmd, _, err := rootCmd.Find([]string{"access", "path"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	pathCmd.SetOut(&out)
	t.Cleanup(func() { pathCmd.SetOut(nil) })
	for _, v := range []string{"false", "true", "inherit"} {
		if err = pathCmd.RunE(pathCmd, []string{"photos", v}); err != nil {
			t.Fatal(err)
		}
		reg, _, e := registry.Preflight(regPath)
		if e != nil {
			t.Fatal(e)
		}
		p := reg.Services[0].AccessLogPath
		if v == "inherit" && p != nil || v != "inherit" && (p == nil || *p != (v == "true")) {
			t.Fatalf("path opt-out %s %+v", v, p)
		}
	}
	if err = pathCmd.RunE(pathCmd, []string{"photos", "bad"}); err == nil {
		t.Fatal("invalid path setting accepted")
	}
	if err = pathCmd.RunE(pathCmd, []string{"missing", "false"}); err == nil {
		t.Fatal("missing app ignored")
	}
	logCmd, _, err := rootCmd.Find([]string{"access", "log"})
	if err != nil {
		t.Fatal(err)
	}
	logCmd.SetOut(&out)
	t.Cleanup(func() {
		logCmd.SetOut(nil)
		logCmd.Flags().Set("limit", "100")
		logCmd.Flags().Set("decision", "")
		logCmd.Flags().Set("since", "")
		logCmd.Flags().Set("who", "")
		logCmd.Flags().Set("app", "")
	})
	logCmd.Flags().Set("limit", "0")
	if err = logCmd.RunE(logCmd, nil); err == nil {
		t.Fatal("zero limit accepted")
	}
	logCmd.Flags().Set("limit", "100")
	logCmd.Flags().Set("since", "bad")
	if err = logCmd.RunE(logCmd, nil); err == nil {
		t.Fatal("invalid CLI since accepted")
	}
	logCmd.Flags().Set("since", "")
	if err = logCmd.RunE(logCmd, nil); err != nil {
		t.Fatal(err)
	}
	// Actual JSON commands, including per-app mutations, use the established envelope.
	rootCmd.PersistentFlags().Set("json", "true")
	t.Cleanup(func() { rootCmd.PersistentFlags().Set("json", "false") })
	text := captureStdout(t, func() {
		if e := logCmd.RunE(logCmd, nil); e != nil {
			t.Error(e)
		}
	})
	var envelope map[string]any
	if e := json.Unmarshal([]byte(text), &envelope); e != nil || envelope["command"] != "access log" || envelope["schema_version"] != float64(1) {
		t.Fatalf("log JSON %s %v", text, e)
	}
	text = captureStdout(t, func() {
		if e := pathCmd.RunE(pathCmd, []string{"photos", "false"}); e != nil {
			t.Error(e)
		}
	})
	if e := json.Unmarshal([]byte(text), &envelope); e != nil || envelope["data"].(map[string]any)["record_path"] != false {
		t.Fatalf("path JSON %s %v", text, e)
	}
	os.MkdirAll(filepath.Join(dir, "access-log"), 0700)
	os.WriteFile(filepath.Join(dir, "access-log", "health.json"), []byte(`{"enabled":true,"last_write":null,"drops":3,"size_bytes":42,"updated_at":"2030-01-01T00:00:00Z","error":"access_log_io_failed"}`), 0600)
	opts := doctorOptions{}
	r := buildDoctorResult(context.Background(), opts)
	hasDrop, hasUnavailable := false, false
	for _, f := range r.Findings {
		hasDrop = hasDrop || f.Code == inspect.WarningCodeAccessLogDrops
		hasUnavailable = hasUnavailable || f.Code == inspect.WarningCodeAccessLogUnavailable
	}
	if r.AccessLog.Drops != 3 || !hasDrop || !hasUnavailable {
		t.Fatalf("doctor access health %+v", r.AccessLog)
	}
}
func TestAccessTextUnknownDeniedAndOptionUnknown(t *testing.T) {
	if value, set := accessLogOptionValue(config.GlobalConfig{}, "access-log-path"); value != "" || set {
		t.Fatal("absent access config reported an explicit path setting")
	}
	var out bytes.Buffer
	formatAccessLog(accesslog.Result{Events: []accesslog.Event{{Kind: "tcp_close", Identity: accesslog.Identity{Node: "node"}}, {Kind: "guest", Identity: accesslog.Identity{Remote: "192.168.1.0/24"}}, {Kind: "mcp", MCP: &accesslog.MCPAudit{Principal: "osuser:operator"}}}, Summary: accesslog.Summary{Apps: []accesslog.Count{{Key: "app", Denied: 1}}}}, &out)
	if !strings.Contains(out.String(), "node") || !strings.Contains(out.String(), "192.168.1.0/24") || !strings.Contains(out.String(), "osuser:operator") || !strings.Contains(out.String(), "last seen never") {
		t.Fatal(out.String())
	}
	if err := updateAccessLogOption(&config.GlobalConfig{}, "unknown", "value"); err == nil {
		t.Fatal("unknown option accepted")
	}
}
