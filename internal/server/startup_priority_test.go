package server

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// syncStartOrder runs one real reconciliation over file services named in
// registry order and returns the order their nodes were constructed in.
func syncStartOrder(t *testing.T, names []string, hint func(regPath string)) []string {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	services := make([]registry.Service, len(names))
	for i, name := range names {
		services[i] = registry.Service{Name: name, Type: registry.TypeFile, Path: t.TempDir()}
	}
	regPath := writeRegistry(t, services)
	hint(regPath)

	var mu sync.Mutex
	var started []string
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
		mu.Lock()
		started = append(started, svc.Name)
		mu.Unlock()
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })

	s, err := New("key", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(started)
}

func TestSyncNodesStartsHigherStartupPriorityFirst(t *testing.T) {
	got := syncStartOrder(t, []string{"notes", "photos", "dashboard", "media"}, func(regPath string) {
		for name, priority := range map[string]int{"dashboard": 10, "photos": 5, "notes": -1} {
			if err := registry.SetStartupPriority(context.Background(), regPath, name, priority); err != nil {
				t.Fatal(err)
			}
		}
	})
	if want := []string{"dashboard", "photos", "media", "notes"}; !slices.Equal(got, want) {
		t.Fatalf("start order = %v, want %v", got, want)
	}
}

func TestSyncNodesKeepsRegistryOrderWithoutStartupPriorities(t *testing.T) {
	got := syncStartOrder(t, []string{"notes", "photos", "dashboard"}, func(string) {})
	if want := []string{"notes", "photos", "dashboard"}; !slices.Equal(got, want) {
		t.Fatalf("start order = %v, want registry order %v", got, want)
	}
}

// A hint file that cannot be read costs only the ordering: every service still
// starts, in registry order, and the problem is logged.
func TestSyncNodesStartsEveryServiceWhenStartupPrioritiesAreUnreadable(t *testing.T) {
	old := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(old) })
	got := syncStartOrder(t, []string{"notes", "photos", "dashboard"}, func(regPath string) {
		if err := os.WriteFile(registry.StartupPriorityPath(regPath), []byte(`{"schema_version":1,"services":{"dashboard":10},"typo":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	if want := []string{"notes", "photos", "dashboard"}; !slices.Equal(got, want) {
		t.Fatalf("start order = %v, want registry order %v", got, want)
	}
	if n := len(logs.byMessage("startup priorities unreadable; starting nodes in registry order")); n != 1 {
		t.Fatalf("unreadable hint file logged %d warnings, want 1", n)
	}
}
