package server

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
)

func TestWave1Round2WatcherPreservesExtendedService(t *testing.T) {
	for _, field := range []string{"health", "host", "limits", "people"} {
		t.Run(field, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			synctest.Test(t, func(t *testing.T) {
				installChannelRegistryWatcher(t)
				s := newLossRecoveryServer(t)
				newTSNetServerFn = func(registry.Service, string, string, string) tsnetServer {
					return &fakeTSNetServer{localClient: localapitest.NewClient(nil)}
				}
				svc := registry.Service{Name: "photos", Type: registry.TypeProxy, Target: "http://localhost:2283", PreserveHost: true, Health: &registry.HealthConfig{Path: "/ready"}, RequestLimits: registry.RecommendedUploadLimits()}
				path := writeRegistry(t, []registry.Service{svc})
				if err := s.syncNodes(context.Background()); err != nil {
					t.Fatal(err)
				}
				startRegistryWatcherTest(t, s)
				generation := s.syncGeneration.Load()
				switch field {
				case "health":
					svc.Health = &registry.HealthConfig{Path: "/other-ready"}
				case "host":
					svc.PreserveHost = false
				case "limits":
					svc.RequestLimits = &registry.RequestLimits{MaxBody: "64MiB"}
				case "people":
					svc.PeopleScoped = true
				}
				writeRegistry(t, []registry.Service{svc})
				// No event is delivered: exercise the lost-event recovery path
				// against a real registry file containing all wave-one fields.
				advanceWatcherTime(registryStateCheckInterval)
				if got := s.syncGeneration.Load(); got != generation+1 {
					t.Fatalf("%s edit was not reconciled: generation=%d want=%d", field, got, generation+1)
				}
				loaded, err := registry.Load(path)
				if err != nil || len(loaded.Services) != 1 {
					t.Fatalf("registry: %+v %v", loaded, err)
				}
				s.mu.RLock()
				node := s.nodes[svc.Name]
				var observed registry.Service
				if node != nil {
					observed = node.service
				}
				s.mu.RUnlock()
				if !reflect.DeepEqual(observed, loaded.Services[0]) {
					t.Fatalf("watcher lost extended service fields: got=%+v want=%+v", observed, loaded.Services[0])
				}
				advanceWatcherTime(2 * registryStateCheckInterval)
				if got := s.syncGeneration.Load(); got != generation+1 {
					t.Fatalf("unchanged rich service repeated synchronization: %d", got)
				}
				// The same recovery mechanism must still revoke the service.
				writeRegistry(t, nil)
				advanceWatcherTime(registryStateCheckInterval)
				if s.nodeRunning(svc.Name) || s.syncGeneration.Load() != generation+2 {
					t.Fatal("lost-event removal retained the extended service")
				}
				advanceWatcherTime(time.Second)
			})
		})
	}
}
