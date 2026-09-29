package server

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

type startObservation struct {
	ready    bool
	running  []string
	failures map[string]tsruntime.ServiceState
	snapshot *tsruntime.Snapshot
}

// runUntilReady runs the daemon's real start path and records what was up,
// what failed, and what runtime.json said at the moment it became ready.
func runUntilReady(t *testing.T, s *Server) (error, startObservation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var obs startObservation
	s.SetReadyFunc(func() error {
		obs.ready = true
		s.mu.RLock()
		for name := range s.nodes {
			obs.running = append(obs.running, name)
		}
		obs.failures = make(map[string]tsruntime.ServiceState, len(s.serviceFailures))
		for name, failure := range s.serviceFailures {
			obs.failures[name] = failure
		}
		s.mu.RUnlock()
		sort.Strings(obs.running)
		if path, err := config.RuntimeSnapshotPath(); err == nil {
			obs.snapshot, _ = tsruntime.Load(path)
		}
		cancel()
		return nil
	})
	return s.Run(ctx), obs
}

func snapshotService(snapshot *tsruntime.Snapshot, name string) (tsruntime.ServiceSnapshot, bool) {
	if snapshot == nil {
		return tsruntime.ServiceSnapshot{}, false
	}
	for _, svc := range snapshot.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return tsruntime.ServiceSnapshot{}, false
}

// One damaged, foreign, or newer identity record must never take the whole
// daemon down, and must never delete or reset node state. A record that
// cannot be read is a coded failure of its own service only, naming the file
// and a remedy. A newer or extended record is an unknown identity: the node
// keeps its state and the record is not rewritten. A directory entry whose
// stem is not a service name is not a record at all.
func TestNodeIdentityBadRecordAtStartFailsOnlyThatService(t *testing.T) {
	type outcome int
	const (
		allUp           outcome = iota // every service running, nothing failed
		apiFailed                      // api failed with a coded record error; others running
		apiUnknownKeeps                // api running over its existing state; record untouched
	)
	for _, tc := range []struct {
		name, file, content string
		unreadable          bool
		want                outcome
	}{
		{name: "control-no-extra-file", want: allUp},
		{name: "corrupt-registered", file: "api.json", content: "{bad", want: apiFailed},
		{name: "empty-registered", file: "api.json", content: "", want: apiFailed},
		{name: "truncated-registered", file: "api.json", content: `{"version":1,"service":"api","tags":["tag:api"`, want: apiFailed},
		{name: "invalid-origin-registered", file: "api.json", content: `{"version":1,"service":"api","tags":["tag:api","tag:web"],"ephemeral":false,"control_url":"","origin":"guessed"}`, want: apiFailed},
		{name: "future-version-registered", file: "api.json", content: `{"version":2,"service":"api","tags":["tag:other"],"ephemeral":true,"control_url":"https://elsewhere.example","origin":"enrolled"}`, want: apiUnknownKeeps},
		{name: "future-field-registered", file: "api.json", content: `{"version":1,"service":"api","tags":["tag:other"],"ephemeral":false,"control_url":"","origin":"requested_before_up","enrolled_node_id":"n1"}`, want: apiUnknownKeeps},
		{name: "unreadable-registered", unreadable: true, want: allUp},
		{name: "corrupt-orphan", file: "gone.json", content: "{bad", want: allUp},
		{name: "stray-appledouble", file: "._api.json", content: "\x00\x05\x16\x07", want: allUp},
		{name: "stray-copy", file: "api copy.json", content: `{}`, want: allUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.unreadable && runtime.GOOS == "windows" {
				t.Skip("mode 000 is not an unreadable file on Windows")
			}
			services := legacyLayoutServices(t)
			markers := writeLegacyLayout(t, services)
			p := instrumentIdentity(t)
			startLegacyLayoutOnce(t, p)
			identitiesDir := filepath.Join(mustConfigDir(t), "node-identities")
			recordPath := filepath.Join(identitiesDir, "api.json")
			var planted string
			if tc.file != "" {
				planted = filepath.Join(identitiesDir, tc.file)
				if err := os.WriteFile(planted, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.unreadable {
				if err := os.Chmod(recordPath, 0o000); err != nil {
					t.Fatal(err)
				}
			}
			var plantedBefore []byte
			if planted != "" {
				plantedBefore, _ = os.ReadFile(planted)
			}

			s := newIdentityProbeServer(t, "", p)
			err, obs := runUntilReady(t, s)
			if err != nil || !obs.ready {
				t.Fatalf("one identity record took the daemon down: Run err=%v ready=%v", err, obs.ready)
			}
			if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
				t.Fatalf("bad record reset node state: removed=%v cleanups=%d", removed, p.cleanupCount())
			}
			assertStateIntact(t, markers, "files", "api", "eph")
			if planted != "" {
				if after, err := os.ReadFile(planted); err != nil || !bytes.Equal(after, plantedBefore) {
					t.Fatalf("planted file %s was changed or removed: err=%v", tc.file, err)
				}
			}
			if obs.snapshot == nil {
				t.Fatal("runtime.json missing at ready")
			}
			switch tc.want {
			case allUp, apiUnknownKeeps:
				if strings.Join(obs.running, ",") != "api,eph,files" || len(obs.failures) != 0 {
					t.Fatalf("running=%v failures=%v, want every service up", obs.running, obs.failures)
				}
			case apiFailed:
				if strings.Join(obs.running, ",") != "eph,files" {
					t.Fatalf("running=%v, want eph,files (api must not start from an unreadable record)", obs.running)
				}
				failure, ok := obs.failures["api"]
				if !ok || failure.Error == nil || failure.RuntimeState != tsruntime.ServiceRuntimeFailed {
					t.Fatalf("api failure = %+v, want a coded per-service failure", failure)
				}
				if failure.Error.Code != "internal_error" || !strings.Contains(failure.Error.Message, recordPath) {
					t.Fatalf("api failure code=%q message=%q, want internal_error naming %s", failure.Error.Code, failure.Error.Message, recordPath)
				}
				if !strings.Contains(strings.Join(failure.Error.Next, "\n"), "aside") {
					t.Fatalf("api failure next=%q, want a move-aside remedy", failure.Error.Next)
				}
				snap, ok := snapshotService(obs.snapshot, "api")
				if !ok || snap.Error == nil || snap.Error.Code != "internal_error" {
					t.Fatalf("runtime.json api entry = %+v, want the coded failure", snap)
				}
			}
			if tc.want == apiUnknownKeeps {
				after, err := os.ReadFile(recordPath)
				if err != nil || string(after) != tc.content {
					t.Fatalf("unknown identity record was rewritten: %q err=%v", after, err)
				}
			}
			if tc.unreadable {
				info, err := os.Stat(recordPath)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("unreadable record not converged to 0600: %v err=%v", info.Mode().Perm(), err)
				}
			}
		})
	}
}

// During hot reload a damaged record for a removed service used to fail every
// sync and withdraw runtime.json for every running service.
func TestNodeIdentityBadRecordDuringHotReloadKeepsRuntimeSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, file  string
		wantFailure string
	}{
		{name: "removed service", file: "gone.json"},
		{name: "running service", file: "api.json", wantFailure: "api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			services := legacyLayoutServices(t)
			markers := writeLegacyLayout(t, services)
			p := instrumentIdentity(t)
			s := newIdentityProbeServer(t, "", p)
			t.Cleanup(s.closeAllNodes)
			if err := s.syncNodesAuthoritative(context.Background()); err != nil {
				t.Fatal(err)
			}
			snapshotPath, err := config.RuntimeSnapshotPath()
			if err != nil {
				t.Fatal(err)
			}
			planted := filepath.Join(mustConfigDir(t), "node-identities", tc.file)
			if err := os.WriteFile(planted, []byte("{bad"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := s.syncNodes(context.Background()); err != nil {
				t.Fatalf("hot-reload sync failed on one bad record: %v", err)
			}
			if _, err := os.Stat(snapshotPath); err != nil {
				t.Fatalf("runtime.json withdrawn for every service: %v", err)
			}
			for _, name := range []string{"files", "api", "eph"} {
				if !s.nodeRunning(name) {
					t.Fatalf("%s stopped by an unrelated or unchanged bad record", name)
				}
			}
			s.mu.RLock()
			failure, failed := s.serviceFailures[tc.wantFailure]
			failures := len(s.serviceFailures)
			s.mu.RUnlock()
			if tc.wantFailure == "" && failures != 0 {
				t.Fatalf("bad record of a removed service recorded failures: %d", failures)
			}
			if tc.wantFailure != "" && (!failed || failure.Error == nil || !strings.Contains(failure.Error.Message, planted)) {
				t.Fatalf("running service with a bad record: failure=%+v, want a coded failure naming %s", failure, planted)
			}
			assertStateIntact(t, markers, "files", "api", "eph")
			if data, err := os.ReadFile(planted); err != nil || string(data) != "{bad" {
				t.Fatalf("bad record changed: %q err=%v", data, err)
			}
		})
	}
}

// A file whose stem is not a service name is not a record this daemon wrote.
// It is skipped with a warning naming the file, before any registry read or
// record parsing, and left in place.
func TestNodeIdentitySweepSkipsStrayFileWithWarning(t *testing.T) {
	services := legacyLayoutServices(t)
	writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	startLegacyLayoutOnce(t, p)
	stray := filepath.Join(mustConfigDir(t), "node-identities", "api copy.json")
	if err := os.WriteFile(stray, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldLogger := slog.Default()
	logs := installCaptureLogger()
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	s := newIdentityProbeServer(t, "", p)
	desired := make(map[string]registry.Service, len(services))
	for _, svc := range services {
		desired[svc.Name] = svc
	}
	if err := s.removeAbsentNodeIdentities(desired); err != nil {
		t.Fatalf("stray file failed the sweep: %v", err)
	}
	var warned bool
	logs.mu.Lock()
	for _, record := range logs.records {
		if record.Level != slog.LevelWarn || record.Message != "ignoring a file in node-identities that is not a service identity record" {
			continue
		}
		record.Attrs(func(a slog.Attr) bool {
			if a.Key == "path" && a.Value.String() == stray {
				warned = true
			}
			return true
		})
	}
	logs.mu.Unlock()
	if !warned {
		t.Fatalf("no warning naming %s", stray)
	}
	if data, err := os.ReadFile(stray); err != nil || string(data) != `{}` {
		t.Fatalf("stray file changed: %q err=%v", data, err)
	}
}

// The documented remedy for an unreadable record is to move it aside. The
// failed service must then recover on the lifecycle ticker, without a registry
// change or a restart, by adopting its existing state with no reset.
func TestNodeIdentityRecordMovedAsideRecoversOnTicker(t *testing.T) {
	services := legacyLayoutServices(t)
	markers := writeLegacyLayout(t, services)
	p := instrumentIdentity(t)
	startLegacyLayoutOnce(t, p)
	recordPath := filepath.Join(mustConfigDir(t), "node-identities", "api.json")
	if err := os.WriteFile(recordPath, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newIdentityProbeServer(t, "", p)
	t.Cleanup(s.closeAllNodes)
	s.SetLifecycleReconcileFn(func(context.Context, time.Time) (bool, error) { return false, nil })
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("sync with one unreadable record: %v", err)
	}
	if s.nodeRunning("api") {
		t.Fatal("api started from an unreadable record")
	}
	if err := os.Rename(recordPath, recordPath+".bak"); err != nil {
		t.Fatal(err)
	}

	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 10 * time.Millisecond
	t.Cleanup(func() { lifecycleTickerInterval = oldInterval })
	ctx, cancel := context.WithCancel(context.Background())
	done := s.startLifecycleTicker(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !s.nodeRunning("api") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if !s.nodeRunning("api") {
		t.Fatal("api did not recover on the ticker after its record was moved aside")
	}
	s.mu.RLock()
	_, stillFailed := s.serviceFailures["api"]
	s.mu.RUnlock()
	if stillFailed {
		t.Fatal("api recovered but its record failure is still reported")
	}
	if removed := p.removedNames(); len(removed) != 0 || p.cleanupCount() != 0 {
		t.Fatalf("recovery reset state: removed=%v cleanups=%d", removed, p.cleanupCount())
	}
	assertStateIntact(t, markers, "api")
	recorded, found, err := readNodeIdentity(recordPath)
	if err != nil || !found || recorded.Origin != identityLegacyAdopted {
		t.Fatalf("recovered record = %+v found=%v err=%v, want legacy adoption", recorded, found, err)
	}
}
