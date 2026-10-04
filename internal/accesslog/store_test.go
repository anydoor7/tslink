package accesslog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

var testTime = time.Date(2030, 7, 10, 12, 0, 0, 0, time.UTC)

func closeStore(t *testing.T, s *Store) {
	t.Helper()
	s.Close()
	// Fixture ownership ends only after queued writes, final health publication
	// and lock release. Disk throughput is not a test deadline; the go test
	// process timeout still diagnoses a stuck worker with goroutine stacks.
	<-s.Done()
}
func newTestStore(t *testing.T, dir string, o Options, now func() time.Time) *Store {
	t.Helper()
	s, e := New(dir, o, now)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { closeStore(t, s) })
	return s
}
func event(app, who, path string) Event {
	return Event{App: app, Kind: "http", Identity: Identity{Login: who}, Path: path, Method: "GET", Status: 200, Decision: "allowed"}
}
func TestCloseHelpersJoinDelayedWriter(t *testing.T) {
	for _, kind := range []string{"store", "lifecycle"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				s, err := newStore(dir, Options{}, func() time.Time { return testTime }, func() {
					// The worker and its delay belong to this bubble. File writes
					// remain real; no wall-clock I/O speed is asserted.
					time.Sleep(11 * time.Second)
				})
				if err != nil {
					t.Fatal(err)
				}
				defer func() { s.Close(); <-s.Done() }()
				if !s.Record(event("delayed", "alice", "/ok")) {
					t.Fatal("event not accepted")
				}
				start := time.Now()
				if kind == "store" {
					closeStore(t, s)
				} else {
					closeLifecycle(t, &Lifecycle{store: s})
				}
				if time.Since(start) < 11*time.Second {
					t.Fatal("close returned before the delayed write")
				}
				r, err := Query(dir, Filter{})
				if err != nil || len(r.Events) != 1 || r.Events[0].App != "delayed" {
					t.Fatalf("delayed write missing: %+v, %v", r, err)
				}
			})
		})
	}
}

func TestCloseDrainsAcceptedQueueAndReleasesWriter(t *testing.T) {
	dir := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, err := newStore(dir, Options{QueueSize: 128}, func() time.Time { return testTime }, func() {
		once.Do(func() { close(entered); <-release })
	})
	if err != nil {
		t.Fatal(err)
	}
	var unblock sync.Once
	defer func() { unblock.Do(func() { close(release) }); closeStore(t, s) }()
	for i := 0; i < 96; i++ {
		e := event("queued", "alice", "/ok")
		e.Time = testTime.Add(time.Duration(i) * time.Second)
		if !s.Record(e) {
			t.Fatalf("event %d not accepted", i)
		}
	}
	<-entered
	var closers sync.WaitGroup
	for i := 0; i < 8; i++ {
		closers.Go(s.Close)
	}
	closers.Wait()
	select {
	case <-s.Done():
		t.Fatal("Done closed while an accepted write was blocked")
	default:
	}
	if _, err := New(dir, Options{}, nil); err == nil {
		t.Fatal("writer lock released before drain")
	}
	unblock.Do(func() { close(release) })
	closeStore(t, s)
	r, err := Query(dir, Filter{Limit: 10000})
	if err != nil || len(r.Events) != 96 || r.Summary.Count != 96 {
		t.Fatalf("accepted queue lost writes: count=%d, %v", len(r.Events), err)
	}
	for i, e := range r.Events {
		if !e.Time.Equal(testTime.Add(time.Duration(95-i) * time.Second)) {
			t.Fatalf("queued event %d missing or duplicated", 95-i)
		}
	}
	files, err := segments(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	for _, f := range files {
		size += f.size
	}
	h := ReadHealth(dir)
	if h.Drops != 0 || h.Error != "" || h.Size != size || size == 0 || h.LastWrite == nil || !h.LastWrite.Equal(testTime) || !h.UpdatedAt.Equal(testTime) {
		t.Fatalf("final health does not match durable files: %+v, size=%d", h, size)
	}
	// Done must also release the process lock. Reopening runs real recovery and
	// proves the persisted records survive a new writer, rather than a cache.
	reopened := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	closeStore(t, reopened)
	r, err = Query(dir, Filter{Limit: 10000})
	if err != nil || r.Summary.Count != 96 {
		t.Fatalf("reopen lost durable records: %+v, %v", r.Summary, err)
	}
}

func TestStoreQueryPrivacy(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	for _, e := range []Event{event("photos", "alice", "/album?token=query-secret"), event("photos", "bob", "/guest/path-bearer-secret"), {App: "docs", Kind: "guest", Decision: "denied", Reason: "expired", Identity: Identity{Remote: "192.168.4.19:23"}, Path: "https://host/full-url-secret"}} {
		if !s.Record(e) {
			t.Fatal("record dropped")
		}
	}
	closeStore(t, s)
	r, e := Query(dir, Filter{})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Events) != 3 || r.Summary.Count != 3 || r.Summary.Denied != 1 || r.Summary.Allowed != 2 {
		t.Fatalf("query %+v", r)
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"query-secret", "path-bearer-secret", "full-url-secret", "192.168.4.19", "token="} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("privacy leaked %q: %s", secret, b)
		}
	}
	files, _ := segments(filepath.Join(dir, "access-log"))
	for _, file := range files {
		raw, e := os.ReadFile(filepath.Join(dir, "access-log", file.name))
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"query-secret", "path-bearer-secret", "full-url-secret"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("disk leaked %s", secret)
			}
		}
	}
	found := false
	for _, ev := range r.Events {
		if ev.App == "docs" {
			found = ev.Identity.Remote == "192.168.4.0/24"
		}
	}
	if !found {
		t.Fatal("coarse address absent")
	}
	h := ReadHealth(dir)
	if h.Drops != 0 || h.Size <= 0 || h.LastWrite == nil || !h.LastWrite.Equal(testTime) || h.Error != "" {
		t.Fatalf("health %+v", h)
	}
}
func TestQueryFiltersAndSummaries(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	for i := 0; i < 8; i++ {
		e := event("photos", "alice", "/ok")
		e.Time = testTime.Add(time.Duration(i) * time.Minute)
		if i == 2 {
			e.App = "docs"
		}
		if i == 3 {
			e.Identity.Login = "bob"
		}
		if i%2 == 0 {
			e.Decision = "denied"
			e.Reason = "people"
		}
		s.Record(e)
	}
	closeStore(t, s)
	since := testTime.Add(time.Minute)
	until := testTime.Add(6 * time.Minute)
	r, e := Query(dir, Filter{App: "photos", Who: "ALICE", Since: &since, Until: &until, Decision: "denied", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Events) != 1 || r.Summary.Count != 2 || !r.Truncated || !r.Events[0].Time.Equal(until) || r.Summary.Apps[0].LastSeen != nil {
		t.Fatalf("filters %+v", r)
	}
	r, e = Query(dir, Filter{Until: &until, Limit: 100})
	if e != nil || r.Summary.Count != 7 || !r.Events[0].Time.Equal(until) {
		t.Fatalf("inclusive upper bound %+v %v", r, e)
	}
	r, e = Query(dir, Filter{Limit: 2})
	if e != nil || len(r.Events) != 2 || r.Summary.Count != 8 || !r.Summary.Apps[1].LastSeen.Equal(testTime.Add(7*time.Minute)) {
		t.Fatalf("summary %+v %v", r, e)
	}
	for _, f := range []Filter{{Limit: -1}, {Limit: 10001}, {Decision: "unknown"}, {Since: &until, Until: &since}} {
		if _, e = Query(dir, f); e == nil {
			t.Fatalf("invalid filter accepted %+v", f)
		}
	}
}
func TestRetentionAndSizeEviction(t *testing.T) {
	var clock atomic.Int64
	clock.Store(testTime.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{RetentionDays: 2, MaxBytes: 65536, QueueSize: 1024}, now)
	s.Record(event("old", "alice", "/old"))
	closeStore(t, s)
	clock.Store(testTime.Add(48 * time.Hour).UnixNano())
	s = newTestStore(t, dir, Options{RetentionDays: 2, MaxBytes: 65536}, now)
	s.Record(event("new", "alice", "/new"))
	closeStore(t, s)
	r, e := Query(dir, Filter{})
	if e != nil || len(r.Events) != 1 || r.Events[0].App != "new" {
		t.Fatalf("retention %+v %v", r, e)
	}
	// Exercise size eviction with deliberately large, explicitly opted-in full paths.
	s = newTestStore(t, dir, Options{PathMode: "full", MaxBytes: 65536, QueueSize: 1024}, now)
	for i := 0; i < 96; i++ {
		ev := event("new", "alice", "/"+strings.Repeat(strings.Repeat("a", 30)+"/", 50))
		ev.Time = now().Add(time.Duration(i) * time.Second)
		if !s.Record(ev) {
			t.Fatal("queue unexpectedly full")
		}
	}
	closeStore(t, s)
	r, e = Query(dir, Filter{Limit: 10000})
	if e != nil || r.Summary.Count >= 97 || r.Summary.Count < 1 {
		t.Fatalf("size eviction %+v %v", r.Summary, e)
	}
	if h := s.Health(); h.Size > 65536 || h.Size == 0 {
		t.Fatalf("size cap %+v", h)
	}
	if !r.Events[0].Time.Equal(now().Add(95 * time.Second)) {
		t.Fatal("oldest-first eviction lost newest event")
	}
	for i, e := range r.Events {
		if !e.Time.Equal(now().Add(time.Duration(95-i) * time.Second)) {
			t.Fatalf("oldest-first eviction did not retain the newest suffix at %d", i)
		}
	}
}
func TestCrashTailRecovery(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	s.Record(event("before", "alice", "/ok"))
	closeStore(t, s)
	files, _ := segments(filepath.Join(dir, "access-log"))
	path := filepath.Join(dir, "access-log", files[0].name)
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString(`{"schema_version":1,"path":"partial-secret`)
	f.Sync()
	f.Close()
	r, e := Query(dir, Filter{})
	if e != nil || r.Summary.Count != 1 {
		t.Fatalf("partial live query %+v %v", r, e)
	}
	s = newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	s.Record(event("after", "bob", "/ok"))
	closeStore(t, s)
	r, e = Query(dir, Filter{})
	if e != nil || r.Summary.Count != 2 {
		t.Fatalf("recovery %+v %v", r, e)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "partial-secret") {
		t.Fatal("partial tail not truncated")
	}
}
func TestQueueSaturationAndConcurrentClose(t *testing.T) {
	dir := t.TempDir()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s, e := newStore(dir, Options{QueueSize: 1}, func() time.Time { return testTime }, func() { once.Do(func() { close(entered); <-release }) })
	if e != nil {
		t.Fatal(e)
	}
	s.Record(event("first", "alice", "/ok"))
	<-entered
	if !s.Record(event("queued", "bob", "/ok")) {
		t.Fatal("queue should have room")
	}
	start := time.Now()
	for i := 0; i < 100; i++ {
		if s.Record(event("drop", "bob", "/ok")) {
			t.Fatal("saturated queue accepted")
		}
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("queue blocked serving")
	}
	if s.Health().Drops != 100 {
		t.Fatalf("drops %d", s.Health().Drops)
	}
	deadline := time.Now().Add(3 * time.Second)
	for ReadHealth(dir).Drops != 100 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if ReadHealth(dir).Drops != 100 {
		t.Fatal("saturated writer did not surface drops while append was stalled")
	}

	s.Close()
	if s.Record(event("closed", "bob", "/ok")) {
		t.Fatal("closed writer accepted")
	}
	close(release)
	closeStore(t, s)
	if h := ReadHealth(dir); h.Drops != 101 {
		t.Fatalf("persisted drops %+v", h)
	}
	r, e := Query(dir, Filter{})
	if e != nil || r.Summary.Count != 2 {
		t.Fatalf("drain %+v %v", r, e)
	}
	// Raced admission is either accepted and drained, or explicitly counted.
	dir = t.TempDir()
	s = newTestStore(t, dir, Options{QueueSize: 1024}, func() time.Time { return testTime })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 50; n++ {
				s.Record(event("race", "alice", "/ok"))
			}
		}()
	}
	s.Close()
	wg.Wait()
	closeStore(t, s)
	r, e = Query(dir, Filter{Limit: 10000})
	if e != nil || uint64(r.Summary.Count)+s.Health().Drops != 400 {
		t.Fatalf("admission lost event %+v %v", s.Health(), e)
	}
}
func TestWriterFailureIsolation(t *testing.T) {
	t.Run("second writer", func(t *testing.T) {
		dir := t.TempDir()
		s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
		if _, e := New(dir, Options{}, nil); e == nil {
			t.Fatal("second writer admitted")
		}
		closeStore(t, s)
	})
	t.Run("corrupt complete line", func(t *testing.T) {
		dir := t.TempDir()
		os.Mkdir(filepath.Join(dir, "access-log"), 0700)
		os.WriteFile(filepath.Join(dir, "access-log", "2030-07-10-000000.jsonl"), []byte("broken\n"), 0600)
		s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
		s.Record(event("app", "alice", "/ok"))
		closeStore(t, s)
		if s.Health().Drops != 1 || s.Health().Error != "access_log_io_failed" {
			t.Fatalf("corruption not surfaced %+v", s.Health())
		}
		if _, e := Query(dir, Filter{}); e == nil {
			t.Fatal("corruption silently ignored")
		}
	})
	t.Run("directory segment", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "access-log", "2030-07-10-000000.jsonl"), 0700)
		s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
		s.Record(event("app", "alice", "/ok"))
		closeStore(t, s)
		if s.Health().Drops != 1 {
			t.Fatal("special file not refused")
		}
		if _, e := Query(dir, Filter{}); e == nil {
			t.Fatal("directory accepted")
		}
	})
	t.Run("read-only missing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		r, e := Query(dir, Filter{})
		if e != nil || r.Summary.Count != 0 {
			t.Fatal(e)
		}
		if _, e = os.Stat(dir); !os.IsNotExist(e) {
			t.Fatal("query created directory")
		}
		if ReadHealth(dir).Error != "access_log_not_started" {
			t.Fatal("missing health hidden")
		}
	})
	t.Run("disabled", func(t *testing.T) {
		dir := t.TempDir()
		v := false
		s := newTestStore(t, dir, Options{Enabled: &v}, func() time.Time { return testTime })
		if !s.Record(event("app", "alice", "/secret")) {
			t.Fatal("disabled rejected")
		}
		closeStore(t, s)
		r, e := Query(dir, Filter{})
		if e != nil || r.Summary.Count != 0 || ReadHealth(dir).Enabled {
			t.Fatalf("disabled recorded %+v", r)
		}
	})
}
func TestOptionsAndSanitizer(t *testing.T) {
	for _, o := range []Options{{RetentionDays: -1}, {RetentionDays: 3651}, {MaxBytes: 1}, {MaxBytes: 1 << 31}, {QueueSize: -1}, {QueueSize: 65537}} {
		if _, e := New(t.TempDir(), o, nil); e == nil {
			t.Fatalf("bad options admitted %+v", o)
		}
	}
	v := false
	if (Options{}).PathsEnabled(&v) || (Options{RecordPath: &v}).PathsEnabled(nil) {
		t.Fatal("path opt-out ignored")
	}
	if CoarseRemote("[2001:db8:abcd:23::1]:43") != "2001:db8:abcd::/48" || CoarseRemote("invalid") != "" {
		t.Fatal("coarse IPv6 wrong")
	}
	if SafePath("/"+strings.Repeat("x/", 5000)) != "/[redacted]" {
		t.Fatal("oversize path was not bounded")
	}
	for _, path := range []string{"/token/short-secret", "/a/tskey-api-secret", "/a/abcdefghijklmnopqrstuvwxyz0123456789"} {
		if strings.Contains(SafePath(path), "secret") || strings.Contains(SafePath(path), "abcdefghijklmnopqrstuvwxyz") {
			t.Fatalf("token path not redacted %s", SafePath(path))
		}
	}
	e := sanitize(Event{Kind: "bogus", App: "https://bad", Identity: Identity{Login: "alice\n", Node: "node", Remote: "1.2.3.4:80", Tags: []string{"tag:a"}}, Grant: &Grant{Kind: "person", Entry: "tskey-bad"}, Decision: "denied", Reason: "attacker-supplied", Connection: strings.Repeat("x", 65)})
	if e.App != "[redacted]" || e.Identity.Login != "alice" || e.Identity.Remote != "" || e.Reason != "unavailable" || e.Kind != "unknown" || e.Grant.Entry != "[redacted]" {
		t.Fatalf("sanitize %+v", e)
	}
}
