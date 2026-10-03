package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

func TestRequestLimitsFlagsAndAddAdmission(t *testing.T) {
	cmd := &cobra.Command{}
	addRequestLimitFlags(cmd)
	if requestLimitsFromFlags(cmd) != nil {
		t.Fatal("absent flags persist settings")
	}
	if err := cmd.ParseFlags([]string{"--max-request-body", "unlimited", "--ack-unlimited-request-body", "--request-header-timeout", "15s", "--request-read-timeout", "2m", "--idle-timeout", "90s"}); err != nil {
		t.Fatal(err)
	}
	limits := requestLimitsFromFlags(cmd)
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	svc, err := buildService(AddParams{Name: "photos", Proxy: "localhost:3000", RequestLimits: limits})
	if err != nil {
		t.Fatal(err)
	}
	if svc.EffectiveRequestLimits().MaxBodyBytes != -1 {
		t.Fatal("flags did not reach service")
	}
	for _, params := range []AddParams{{Name: "photos", Proxy: "localhost:3000", RequestLimits: &registry.RequestLimits{MaxBody: "unlimited"}}, {Name: "db", TCP: "localhost:5432", RequestLimits: limits}} {
		if _, err := buildService(params); err == nil {
			t.Fatal("invalid request limits admitted")
		}
	}
	for _, name := range []string{"add", "share"} {
		c, _, err := rootCmd.Find([]string{name})
		if err != nil || c.Flags().Lookup("max-request-body") == nil {
			t.Fatalf("%s missing upload flags", name)
		}
	}
	if requestLimitsLabel(svc.EffectiveRequestLimits()) != "body=unlimited header=15s read-idle=2m0s idle=1m30s" || requestLimitsLabel(nil) != "-" {
		t.Fatal("incorrect limit labels")
	}
}

func TestRequestLimitsMCPTranslationAndValidation(t *testing.T) {
	t.Setenv(config.ConfigDirEnv, t.TempDir())
	limits := registry.RecommendedUploadLimits()
	for _, name := range []string{"add", "share"} {
		t.Run(name, func(t *testing.T) {
			called := false
			actions := mcpActions{
				add: func(ctx context.Context, p AddParams, _ bool) (any, error) {
					called = true
					svc, err := buildService(p)
					if err != nil {
						return nil, err
					}
					if svc.RequestLimits.MaxBody != "20GiB" {
						t.Fatal("MCP add lost settings")
					}
					return map[string]any{"request_limits": svc.EffectiveRequestLimits()}, nil
				},
				share: func(ctx context.Context, req shareRequest) (ShareResult, error) {
					called = true
					spec, err := applyShareExposure(shareTargetSpec{Service: registry.Service{Type: registry.TypeProxy}}, req)
					if err != nil {
						return ShareResult{}, err
					}
					if spec.Service.RequestLimits.MaxBody != "20GiB" {
						t.Fatal("MCP share lost settings")
					}
					return ShareResult{Status: "ready", RequestLimits: spec.Service.EffectiveRequestLimits()}, nil
				},
			}
			args := map[string]any{"target": "localhost:3000", "request_limits": limits}
			if name == "add" {
				args["name"] = "photos"
				args["type"] = "proxy"
			}
			raw, _ := json.Marshal(args)
			result, err := callMCPTool(context.Background(), actions, name, raw)
			if err != nil || result.IsError || !called {
				t.Fatalf("result=%+v err=%v called=%v", result, err, called)
			}
			args["request_limits"] = map[string]any{"max_body": "unlimited"}
			raw, _ = json.Marshal(args)
			result, err = callMCPTool(context.Background(), actions, name, raw)
			if err != nil || !result.IsError {
				t.Fatalf("missing unlimited acknowledgement accepted: %+v %v", result, err)
			}
			for _, def := range mcpToolDefinitions {
				if def.Name == name {
					props := def.InputSchema["properties"].(map[string]any)
					if props["request_limits"] == nil {
						t.Fatal("MCP schema omits settings")
					}
				}
			}
		})
	}
}

func TestRequestLimitsShareReuseAndOutput(t *testing.T) {
	original := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000"}
	candidate := original
	candidate.RequestLimits = registry.RecommendedUploadLimits()
	if sameShareTarget(original, candidate) {
		t.Fatal("share silently reused incompatible limits")
	}
	same := original
	same.RequestLimits = &registry.RequestLimits{MaxBody: "32MiB"}
	if !sameShareTarget(original, same) {
		t.Fatal("share refused equivalent defaults")
	}
	result := withShareFunnelState(ShareResult{Status: "ready"}, shareRegistration{Service: candidate})
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), `"max_body_bytes":21474836480`) {
		t.Fatalf("share JSON=%s", raw)
	}
	_, err := applyShareExposure(shareTargetSpec{Service: original}, shareRequest{RequestLimits: &registry.RequestLimits{ReadTimeout: "0"}})
	if err == nil {
		t.Fatal("invalid share read timeout admitted")
	}
}

func TestRequestLimitsShareConflictDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name, flag, before, after string
		limits                    registry.RequestLimits
	}{
		{"body", "--max-request-body", "33554432B", "21474836480B", registry.RequestLimits{MaxBody: "20GiB"}},
		{"header", "--request-header-timeout", "10s", "15s", registry.RequestLimits{HeaderTimeout: "15s"}},
		{"read", "--request-read-timeout", "30s", "2m0s", registry.RequestLimits{ReadTimeout: "2m"}},
		{"idle", "--idle-timeout", "1m0s", "1m30s", registry.RequestLimits{IdleTimeout: "90s"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, dir)
			regPath := filepath.Join(dir, "registry.json")
			original := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:2283"}
			if _, err := registry.Add(regPath, original); err != nil {
				t.Fatal(err)
			}
			candidate := original
			candidate.RequestLimits = &registry.RequestLimits{MaxBody: "32MiB", ReadTimeout: "30s", HeaderTimeout: "10s", IdleTimeout: "60s"}
			control, err := registerShareWithOutcome(regPath, shareTargetSpec{NameBase: "photos", Service: candidate}, "photos")
			if err != nil || control.Service.Name != "photos" || control.Created {
				t.Fatalf("equivalent effective limits did not reuse: %+v %v", control, err)
			}
			candidate.RequestLimits = &tc.limits
			_, err = registerShareWithOutcome(regPath, shareTargetSpec{NameBase: "photos", Service: candidate}, "photos")
			if err == nil {
				t.Fatal("different limits accepted")
			}
			for _, text := range []string{"request limits", tc.flag, tc.before, tc.after, "reconfigure"} {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("conflict %q omits %q", err, text)
				}
			}
			t.Log(err)
		})
	}
}

func TestRequestLimitsStatusListAndDoctorWarnings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	started := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:3000", RequestLimits: registry.RecommendedUploadLimits()})
	warnings := []inspect.WarningView{{Code: registry.CodeRequestBodyLimit, Severity: "warning", Source: "http.request_limits", Message: `Service "photos" hit request_body_limit (21474836480); adjust --max-request-body on add or share.`}}
	fingerprint := statusRegistryFingerprint(t, regPath)
	snapshot := tsruntime.NewSnapshot(4242, started, fingerprint, started.Add(time.Second), []tsruntime.ServiceState{{Service: svc, RuntimeHost: "photos.tailnet.ts.net", Warnings: warnings}})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	withStatusURLSeams(t, true, 4242, started)
	got, err := getStatusURLs(context.Background(), pidPath, regPath, snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	service := findStatusService(t, got, "photos")
	if service.RequestLimits == nil || service.RequestLimits.MaxBodyBytes != 21474836480 || !hasStatusWarningCode(service.Warnings, registry.CodeRequestBodyLimit) {
		t.Fatalf("status=%+v", service)
	}
	var human bytes.Buffer
	formatStatusURLs(got, &human)
	if !strings.Contains(human.String(), "read-idle=2m0s") || !strings.Contains(human.String(), registry.CodeRequestBodyLimit) {
		t.Fatalf("status human=%s", human.String())
	}
	result, err := loadListResultForPaths(context.Background(), regPath, pidPath, snapshotPath, listOptions{Verbose: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if !strings.Contains(string(raw), `"max_body_bytes":21474836480`) || !strings.Contains(string(raw), registry.CodeRequestBodyLimit) {
		t.Fatalf("list JSON=%s", raw)
	}
	human.Reset()
	if err := listServicesWithOptions(context.Background(), regPath, &human, listOptions{Verbose: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "read-idle=2m0s") || !strings.Contains(human.String(), "--max-request-body") {
		t.Fatalf("verbose list=%s", human.String())
	}
	doctor := DoctorResult{Paths: DoctorPaths{RuntimeSnapshot: snapshotPath, PID: pidPath}, Daemon: DoctorDaemon{Running: true, PID: 4242}}
	diagnoseRuntimeSnapshot(&doctor, fingerprint, false)
	found := false
	for _, finding := range doctor.Findings {
		if finding.Code == registry.CodeRequestBodyLimit && finding.Service == "photos" && finding.Evidence["suggested_flag"] == "--max-request-body" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor findings=%+v", doctor.Findings)
	}
}
