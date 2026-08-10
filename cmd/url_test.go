package cmd

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

func writeExactURLFixture(t *testing.T, name string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	regPath := filepath.Join(dir, "registry.json")
	pidPath := filepath.Join(dir, "tslink.pid")
	snapshotPath := filepath.Join(dir, "runtime.json")
	startedAt := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	svc := addStatusTestService(t, regPath, registry.Service{Name: name, Type: registry.TypeProxy, Target: "http://localhost:3000"})
	snapshot := tsruntime.NewSnapshot(4242, startedAt, statusRegistryFingerprint(t, regPath), startedAt.Add(time.Second), []tsruntime.ServiceState{
		{Service: svc, RuntimeHost: name + ".tailnet-example.ts.net"},
	})
	if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
		t.Fatalf("runtime.Save: %v", err)
	}
	withStatusURLSeams(t, true, 4242, startedAt)
	return pidPath, regPath, snapshotPath
}

func TestResolveServiceURLExactAndPending(t *testing.T) {
	pidPath, regPath, snapshotPath := writeExactURLFixture(t, "sample-service")
	got, err := resolveServiceURL(context.Background(), pidPath, regPath, snapshotPath, "sample-service", 0)
	if err != nil {
		t.Fatalf("resolveServiceURL: %v", err)
	}
	if got.URL != "https://sample-service.tailnet-example.ts.net" || got.State != inspect.EndpointStateExact {
		t.Fatalf("result = %+v, want exact URL", got)
	}

	if _, err := resolveServiceURL(context.Background(), pidPath, regPath, snapshotPath, "missing", 0); output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("missing ExitCode = %d, want %d (err=%v)", output.ExitCode(err), output.ExitNotFound, err)
	}

	dir := t.TempDir()
	pendingReg := filepath.Join(dir, "registry.json")
	if _, err := registry.Add(pendingReg, registry.Service{Name: "newapp", Type: registry.TypeProxy, Target: "http://localhost:3000"}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	_, err = resolveServiceURL(context.Background(), filepath.Join(dir, "pid"), pendingReg, filepath.Join(dir, "runtime.json"), "newapp", 0)
	if code, ok := registry.ErrorCode(err); !ok || code != registry.CodeURLNotReady || output.ExitCode(err) != output.ExitNotFound {
		t.Fatalf("pending err = %v code=%q ok=%v exit=%d", err, code, ok, output.ExitCode(err))
	}
	obj := output.ErrorObjectForError(output.ExitCode(err), err)
	if len(obj.Next) != 2 || obj.Next[1] != "tslink url newapp --wait=30s" {
		t.Fatalf("next = %v, want recovery commands", obj.Next)
	}
}

func TestURLCommandRawAndJSON(t *testing.T) {
	resetRootJSONFlag(t)
	pidPath, regPath, snapshotPath := writeExactURLFixture(t, "sample-service")
	oldPID, oldReg, oldSnapshot := urlPIDPathFn, urlRegistryPathFn, urlRuntimeSnapshotPathFn
	t.Cleanup(func() {
		urlPIDPathFn, urlRegistryPathFn, urlRuntimeSnapshotPathFn = oldPID, oldReg, oldSnapshot
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		_ = urlCmdFlag(t, "raw", "false")
		_ = urlCmdFlag(t, "wait", "0s")
	})
	urlPIDPathFn = func() (string, error) { return pidPath, nil }
	urlRegistryPathFn = func() (string, error) { return regPath, nil }
	urlRuntimeSnapshotPathFn = func() (string, error) { return snapshotPath, nil }

	rootCmd.SetArgs([]string{"url", "sample-service", "--raw"})
	var raw strings.Builder
	rootCmd.SetOut(&raw)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("url --raw: %v", err)
	}
	if raw.String() != "https://sample-service.tailnet-example.ts.net\n" || len(raw.String()) >= 60 {
		t.Fatalf("raw = %q (%d bytes)", raw.String(), len(raw.String()))
	}

	resetRootJSONFlag(t)
	_ = urlCmdFlag(t, "raw", "false")
	rootCmd.SetArgs([]string{"url", "sample-service", "--json"})
	encoded := captureStdout(t, func() {
		if err := rootCmd.Execute(); err != nil {
			t.Fatalf("url --json: %v", err)
		}
	})
	var envelope output.Result
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, encoded)
	}
	data, _ := json.Marshal(envelope.Data)
	var result URLResult
	if err := json.Unmarshal(data, &result); err != nil || result.URL != "https://sample-service.tailnet-example.ts.net" {
		t.Fatalf("result = %+v err=%v", result, err)
	}
}

func urlCmdFlag(t *testing.T, name, value string) error {
	t.Helper()
	urlCmd, _, err := rootCmd.Find([]string{"url"})
	if err != nil {
		return err
	}
	return urlCmd.Flags().Set(name, value)
}

func TestURLWaitFlagAcceptsOptionalValue(t *testing.T) {
	urlCmd, _, err := rootCmd.Find([]string{"url"})
	if err != nil {
		t.Fatalf("find url: %v", err)
	}
	flag := urlCmd.Flags().Lookup("wait")
	if flag == nil || flag.NoOptDefVal != "30s" {
		t.Fatalf("wait flag = %+v, want optional 30s value", flag)
	}
}
