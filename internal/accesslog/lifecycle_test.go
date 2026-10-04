package accesslog

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func closeLifecycle(t *testing.T, l *Lifecycle) {
	t.Helper()
	l.Close()
	// Like closeStore, join fixture-owned I/O before inspecting or removing it.
	<-l.Done()
}
func TestLifecycleMissingWindow(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "access-log")
	if err := os.WriteFile(blocked, []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	now := testTime
	clock := func() time.Time { return now }
	l := NewLifecycle(dir, Options{}, clock)
	defer closeLifecycle(t, l)
	if l.Record(event("photos", "alice", "/album/private")) {
		t.Fatal("missing writer accepted event")
	}
	h := l.Health()
	if !h.Current || h.Error != "access_log_init_failed" || h.Drops != 1 || len(h.MissingHistory) != 1 || !h.MissingHistory[0].Start.Equal(testTime) || h.MissingHistory[0].End != nil {
		t.Fatal(h)
	}
	now = now.Add(time.Hour)
	l.Retry(now)
	if len(l.Health().MissingHistory) != 1 || !l.Health().MissingHistory[0].Start.Equal(testTime) {
		t.Fatal("retry reset history window")
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	// The tick timestamp may precede slow initialization: close at completion.
	l.Retry(testTime)
	h = l.Health()
	if h.Error != "" || h.MissingHistory[0].End == nil || !h.MissingHistory[0].End.Equal(now) || h.Drops != 1 {
		t.Fatal(h)
	}
	if !l.RecordResolved(event("photos", "", "/album/private"), func() Identity { return Identity{Login: "alice"} }) {
		t.Fatal("recovered writer rejected")
	}
	l.Retry(now)
	closeLifecycle(t, l)
	l.Close()
	l.Retry(now)
	if l.Record(event("photos", "alice", "/")) {
		t.Fatal("closed writer accepted")
	}
	if l.Health().Drops != 2 {
		t.Fatal(l.Health())
	}
	result, err := Query(dir, Filter{})
	if err != nil || len(result.Events) != 1 || result.Events[0].Identity.Login != "alice" || result.Events[0].Path != "/album" {
		t.Fatal(result, err)
	}
}
func TestLifecycleDisabledAndFailedClose(t *testing.T) {
	off := false
	l := NewLifecycle(t.TempDir(), Options{Enabled: &off}, func() time.Time { return testTime })
	if !l.Record(Event{}) || l.Health().Error != "" || l.Health().Enabled {
		t.Fatal(l.Health())
	}
	closeLifecycle(t, l)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "access-log"), []byte("blocked"), 0600)
	failed := NewLifecycle(dir, Options{}, func() time.Time { return testTime })
	closeLifecycle(t, failed)
	closeLifecycle(t, failed)
}
func TestLifecycleCompetingWriter(t *testing.T) {
	dir := t.TempDir()
	owner := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	l := NewLifecycle(dir, Options{}, func() time.Time { return testTime })
	defer closeLifecycle(t, l)
	if l.Health().Error != "access_log_init_failed" || l.Record(Event{}) {
		t.Fatal("second writer acquired directory")
	}
	closeStore(t, owner)
	l.Retry(testTime.Add(time.Second))
	if l.Health().Error != "" || !l.Record(event("photos", "alice", "/album")) {
		t.Fatal("lock release did not recover")
	}
	closeLifecycle(t, l)
}

func TestLifecycleHealthPublication(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "access-log"), 0700)
	if err := os.WriteFile(filepath.Join(dir, "access-log", "health.json"), []byte(`{"enabled":true,"drops":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(testTime.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	l := NewLifecycle(dir, Options{}, now)
	defer closeLifecycle(t, l)
	if l.Health().Drops != 0 {
		t.Fatal("old drops attributed to current instance")
	}
	later := testTime.Add(time.Minute)
	clock.Store(later.UnixNano())
	if !l.Record(event("photos", "alice", "/album/item")) {
		t.Fatal("record")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		h := l.Health()
		if h.UpdatedAt.Equal(later) && h.LastWrite != nil && h.LastWrite.Equal(later) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	h := l.Health()
	if !h.UpdatedAt.Equal(later) || h.LastWrite == nil || !h.LastWrite.Equal(later) {
		t.Fatal("writer publication timestamp lost", h)
	}
	closeLifecycle(t, l)
	if ReadHealth(dir).Drops != 99 {
		t.Fatal("historical drop total lost")
	}
}

func TestLifecycleWindowClockRollback(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "access-log")
	os.WriteFile(blocked, []byte("blocked"), 0600)
	var clock atomic.Int64
	clock.Store(testTime.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	l := NewLifecycle(dir, Options{}, now)
	defer closeLifecycle(t, l)
	os.Remove(blocked)
	earlier := testTime.Add(-time.Hour)
	clock.Store(earlier.UnixNano())
	l.Retry(earlier)
	h := l.Health()
	if len(h.MissingHistory) != 1 || h.MissingHistory[0].End == nil || !h.MissingHistory[0].End.Equal(testTime) {
		t.Fatal("rollback reversed history window", h)
	}
}
