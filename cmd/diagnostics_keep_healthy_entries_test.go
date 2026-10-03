package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestDiagnosticsKeepHealthyEntriesBesideUnknownField(t *testing.T) {
	env := newDoctorTestEnv(t, nil)
	t.Setenv(config.ConfigDirEnv, env.dir)
	raw := []byte(`{"schema_version":1,"services":[{"name":"bad","type":"proxy","target":"http://127.0.0.1:8081","typo":true},{"name":"good","type":"proxy","target":"http://127.0.0.1:8080"}]}`)
	if err := os.WriteFile(env.regPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reg, issues, err := registry.LoadForRuntime(env.regPath)
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Name != "good" || len(issues) != 1 {
		t.Fatalf("runtime control: reg=%+v issues=%+v err=%v", reg, issues, err)
	}
	fp, err := tsruntime.CurrentRegistryFingerprint(env.regPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := tsruntime.NewSnapshot(env.pid, env.startedAt, fp, env.startedAt.Add(time.Second), []tsruntime.ServiceState{{Service: reg.Services[0], RuntimeHost: "good.tailnet.ts.net"}})
	if err := tsruntime.Save(env.snapshotPath, snapshot); err != nil {
		t.Fatal(err)
	}
	t.Run("status", func(t *testing.T) {
		result, err := getPollableStatus(context.Background(), env.pidPath, env.regPath, env.snapshotPath, env.authHandoff)
		if err != nil {
			t.Fatalf("status hides healthy entry: %v", err)
		}
		if len(result.Services) != 2 || result.Services[0].Name != "bad" || result.Services[0].Error == nil || result.Services[0].Error.Code != registry.CodeUnknownConfigKey || result.Services[1].Name != "good" || result.Services[1].Status != "up" {
			t.Fatalf("status must name bad error and healthy good: %+v", result.Services)
		}
	})
	t.Run("list and URLs", func(t *testing.T) {
		result, err := loadListResultForPaths(context.Background(), env.regPath, env.pidPath, env.snapshotPath, listOptions{})
		if err != nil {
			t.Fatalf("list hides healthy entry: %v", err)
		}
		data, err := json.Marshal(result.Services)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{`"name":"bad"`, `"code":"unknown_config_key"`, `"name":"good"`, `"url":"https://good.tailnet.ts.net"`} {
			if !strings.Contains(string(data), want) {
				t.Errorf("list missing %s: %s", want, data)
			}
		}
	})
	t.Run("doctor", func(t *testing.T) {
		result := buildDoctorResult(context.Background(), doctorOptions{})
		found := false
		for _, finding := range result.Findings {
			if finding.Service == "bad" && finding.Code == registry.CodeUnknownConfigKey && finding.Evidence["valid_services"] == "good" {
				found = true
			}
		}
		if result.Counts.Services != 2 || !found || !result.RuntimeSnapshot.Exact {
			t.Fatalf("doctor must retain two services and name bad: counts=%+v findings=%+v", result.Counts, result.Findings)
		}
	})
	t.Run("strict mutation control", func(t *testing.T) {
		_, err := registry.Add(env.regPath, registry.Service{Name: "new", Type: registry.TypeProxy, Target: "http://127.0.0.1:8082"})
		if code, _ := registry.ErrorCode(err); code != registry.CodeUnknownConfigKey {
			t.Fatalf("mutation must still refuse unknown field: %v", err)
		}
		after, err := os.ReadFile(env.regPath)
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatalf("refused mutation changed registry: %v", err)
		}
	})
}
