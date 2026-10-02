package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
)

func TestPollableStatusHealthUsesNamedRegistryService(t *testing.T) {
	for _, reader := range []struct {
		name string
		read statusRead
	}{{"command", commandStatus}, {"read_only", readOnlyStatus}} {
		t.Run(reader.name, func(t *testing.T) {
			for _, tc := range []struct {
				name   string
				second []string
			}{
				{"removed_first", []string{"slow"}},
				{"removed_last", []string{"fast"}},
				{"removed_all", []string{}},
				{"reordered", []string{"slow", "fast"}},
				{"replaced", []string{"other", "slow"}},
				{"unchanged", []string{"fast", "slow"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					defer func() {
						if caught := recover(); caught != nil {
							t.Errorf("getPollableStatus panicked after %s registry read: %v", tc.name, caught)
						}
					}()
					dir := t.TempDir()
					t.Setenv(config.ConfigDirEnv, dir)
					regPath := filepath.Join(dir, "registry.json")
					pidPath := filepath.Join(dir, "tslink.pid")
					snapshotPath := filepath.Join(dir, "runtime.json")
					now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
					checked := now.Add(-3 * time.Minute)
					observed := health.State{State: health.Healthy, Kind: registry.TypeProxy, LastChecked: &checked}
					fast := addStatusTestService(t, regPath, registry.Service{
						Name: "fast", Type: registry.TypeProxy, Target: "http://localhost:1234",
						Health: &registry.HealthConfig{Interval: "10s", Timeout: "1s"},
					})
					slow := addStatusTestService(t, regPath, registry.Service{
						Name: "slow", Type: registry.TypeProxy, Target: "http://localhost:5678",
						Health: &registry.HealthConfig{Interval: "5m", Timeout: "1s"},
					})
					snapshot := tsruntime.NewSnapshot(4242, now.Add(-time.Hour), statusRegistryFingerprint(t, regPath), now, []tsruntime.ServiceState{
						{Service: fast, RuntimeHost: "fast.example.ts.net", Health: observed},
						{Service: slow, RuntimeHost: "slow.example.ts.net", Health: observed},
					})
					if err := tsruntime.Save(snapshotPath, snapshot); err != nil {
						t.Fatal(err)
					}
					withStatusURLSeams(t, true, 4242, now.Add(-time.Hour))
					oldNow, oldLoad := statusNowFn, pollableStatusLoadRegistryFn
					t.Cleanup(func() {
						statusNowFn, pollableStatusLoadRegistryFn = oldNow, oldLoad
					})
					statusNowFn = func() time.Time { return now }
					second := registry.Registry{SchemaVersion: registry.CurrentRegistrySchemaVersion, Services: []registry.Service{}}
					present := make(map[string]bool)
					for _, name := range tc.second {
						svc := fast
						if name == "slow" {
							svc = slow
						} else if name == "other" {
							svc.Name = name
							svc.Health = slow.Health
						}
						second.Services = append(second.Services, svc)
						present[name] = true
					}
					reads := 0
					pollableStatusLoadRegistryFn = func(path string) (*registry.Registry, []registry.ServiceIssue, error) {
						reads++
						if path != regPath {
							t.Fatalf("second registry path = %q, want isolated %q", path, regPath)
						}
						// Replace the real file at the boundary between reads; the
						// production diagnostic decoder still consumes both versions.
						data, err := json.Marshal(second)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, data, 0600); err != nil {
							t.Fatal(err)
						}
						return registry.LoadForDiagnostics(path)
					}
					got, err := reader.read.getPollableStatus(pidPath, regPath, snapshotPath, filepath.Join(dir, "auth-handoff.json"))
					if err != nil {
						t.Fatalf("getPollableStatus: %v", err)
					}
					if reads != 1 {
						t.Fatalf("second registry reads = %d, want 1", reads)
					}
					if got.ServiceCount != 2 || len(got.Services) != 2 {
						t.Fatalf("first-read service list changed: count=%d services=%+v", got.ServiceCount, got.Services)
					}
					for i, name := range []string{"fast", "slow"} {
						if got.Services[i].Name != name {
							t.Errorf("service[%d] name = %q, want first-read %q", i, got.Services[i].Name, name)
						}
						want := health.Unchecked(registry.TypeProxy)
						if present[name] {
							want = observed
							if name == "fast" {
								want.State = health.Unknown
								want.LastError = "health_observation_stale"
							}
						}
						if !reflect.DeepEqual(got.Services[i].Health, want) {
							t.Errorf("%s health = %+v, want %+v (own config when present; unchecked when absent)", name, got.Services[i].Health, want)
						}
					}
				})
			}
		})
	}
}
