package accesslog

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreFailureRecoveryAndHealth(t *testing.T) {
	dir := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s, err := newStore(dir, Options{}, func() time.Time { return testTime }, func() { once.Do(func() { close(entered); <-release }) })
	if err != nil {
		t.Fatal(err)
	}
	s.Record(event("first", "alice", "/ok"))
	<-entered
	bad := filepath.Join(dir, "access-log", "2030-07-10-000000.jsonl")
	if err = os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for s.Health().Drops == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Drops != 1 || s.Health().Error != "access_log_io_failed" {
		t.Fatalf("disk error %+v", s.Health())
	}
	// A repair is read by the worker's periodic recovery without delaying serving.
	os.Remove(bad)
	deadline = time.Now().Add(3 * time.Second)
	for s.Health().Error != "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Error != "" {
		t.Fatal("disk recovery did not resume")
	}
	s.RecordResolved(event("recovered", "alice", "/ok"), func() Identity { return Identity{Node: "tag-node", Tags: []string{"tag:reader"}} })
	closeStore(t, s)
	r, e := Query(dir, Filter{Who: "tag:reader"})
	if e != nil || r.Summary.Count != 1 || r.Events[0].Identity.Node != "tag-node" {
		t.Fatalf("tag query %+v %v", r, e)
	}
	s = newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	closeStore(t, s)
	if ReadHealth(dir).Drops != 1 {
		t.Fatal("restart lost drops")
	}
	os.WriteFile(filepath.Join(dir, "access-log", "health.json"), []byte("bad"), 0600)
	if ReadHealth(dir).Error != "access_log_health_unavailable" {
		t.Fatal("corrupt health hidden")
	}
	os.Remove(filepath.Join(dir, "access-log", "health.json"))
	os.Mkdir(filepath.Join(dir, "access-log", "health.json"), 0700)
	if ReadHealth(dir).Error != "access_log_health_unavailable" {
		t.Fatal("special health file admitted")
	}
	s = newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	deadline = time.Now().Add(3 * time.Second)
	for s.Health().Error == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Error != "access_log_io_failed" {
		t.Fatal("health snapshot failure was not reported")
	}
	os.Remove(filepath.Join(dir, "access-log", "health.json"))
	s.Record(event("health-repaired", "alice", "/ok"))
	deadline = time.Now().Add(3 * time.Second)
	for (s.Health().Error != "" || s.Health().LastWrite == nil) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Error != "" || s.Health().LastWrite == nil {
		t.Fatal("health failure did not recover after filesystem repair")
	}
	// With no queued event, the idle eviction timer must surface file failures.
	idleBad := filepath.Join(dir, "access-log", "2030-07-10-000001.jsonl")
	if err := os.Mkdir(idleBad, 0700); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for s.Health().Error == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Error != "access_log_io_failed" {
		t.Fatal("idle eviction failure was not reported")
	}
	os.Remove(idleBad)
	closeStore(t, s)
}
func TestIdleRetentionClockAndGlobalPath(t *testing.T) {
	dir := t.TempDir()
	var clock atomic.Int64
	clock.Store(testTime.UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	off := false
	s := newTestStore(t, dir, Options{RetentionDays: 1, RecordPath: &off}, now)
	s.Record(event("app", "alice", "/sensitive-name"))
	deadline := time.Now().Add(3 * time.Second)
	for s.Health().LastWrite == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	r, e := Query(dir, Filter{})
	if e != nil || r.Summary.Count != 1 || r.Events[0].Path != "" {
		t.Fatalf("global path %+v %v", r, e)
	}
	clock.Store(testTime.Add(24 * time.Hour).UnixNano())
	deadline = time.Now().Add(3 * time.Second)
	for s.Health().Size > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.Health().Size != 0 {
		t.Fatal("idle retention did not evict with injected clock")
	}
	closeStore(t, s)
}
func TestStoreAppendFailures(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	closeStore(t, s)
	e := sanitize(event("app", "alice", "/ok"))
	e.Time = testTime
	e.DurationMS = math.NaN()
	if err := s.append(e); err == nil {
		t.Fatal("invalid numeric event admitted")
	}
	e.DurationMS = 0
	e.Identity.Tags = []string{strings.Repeat("<", 40000)}
	if err := s.append(e); err == nil {
		t.Fatal("oversize event admitted")
	}
	e.Identity.Tags = nil
	e.Time = testTime.Add(-31 * 24 * time.Hour)
	if err := s.append(e); err == nil {
		t.Fatal("old event admitted")
	}
	e.Time = testTime
	os.RemoveAll(s.dir)
	os.WriteFile(s.dir, []byte("not-directory"), 0600)
	if err := s.append(e); err == nil {
		t.Fatal("missing store failure hidden")
	}
	if _, err := Query(dir, Filter{}); err == nil {
		t.Fatal("unsafe dir query admitted")
	}
	if _, err := New(dir, Options{}, nil); err == nil {
		t.Fatal("unsafe dir writer admitted")
	}
	if err := syncDirectory(filepath.Join(dir, "missing")); runtime.GOOS != "windows" && err == nil {
		t.Fatal("directory sync failure hidden")
	}
}
func TestQueryMalformedAndIdentityFilters(t *testing.T) {
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	s.Record(event("app", "K@example.com", "/ok"))
	closeStore(t, s)
	r, e := Query(dir, Filter{Who: "K@example.com"})
	if e != nil || r.Summary.Count != 0 {
		t.Fatal("query Unicode-folded identities")
	}
	r, e = Query(dir, Filter{Who: "K@example.com"})
	if e != nil || r.Summary.Count != 1 {
		t.Fatal("exact identity query failed")
	}
	files, _ := segments(filepath.Join(dir, "access-log"))
	path := filepath.Join(dir, "access-log", files[0].name)
	for _, raw := range []string{"invalid\n", `{"schema_version":99}` + "\n", strings.Repeat("x", maxRecordBytes+1) + "\n"} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, e := Query(dir, Filter{}); e == nil {
			t.Fatal("malformed query succeeded")
		}
	}
	e2 := sanitize(Event{Kind: "tcp_open", Identity: Identity{Node: "node"}})
	e2.Time = testTime
	raw, _ := jsonMarshalEvent(e2)
	os.WriteFile(path, raw, 0600)
	r, e = Query(dir, Filter{Who: "node"})
	if e != nil || r.Summary.Count != 1 || r.Summary.People[0].Key != "node" {
		t.Fatalf("node match %+v %v", r, e)
	}
	e2.Identity.Node = ""
	raw, _ = jsonMarshalEvent(e2)
	os.WriteFile(path, raw, 0600)
	r, e = Query(dir, Filter{})
	if e != nil || r.Summary.People[0].Key != "unknown" {
		t.Fatalf("unknown summary %+v %v", r, e)
	}
}
func jsonMarshalEvent(e Event) ([]byte, error) {
	raw, err := json.Marshal(e)
	return append(raw, '\n'), err
}
func TestCrashProcessAppendRecovery(t *testing.T) {
	if path := os.Getenv("TSLINK_ACCESS_CRASH_FILE"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			os.Exit(98)
		}
		_, err = f.WriteString(`{"schema_version":1,"app":"partial`)
		if err != nil {
			os.Exit(99)
		}
		f.Sync()
		os.Exit(73)
	}
	dir := t.TempDir()
	s := newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	s.Record(event("before", "alice", "/ok"))
	closeStore(t, s)
	files, _ := segments(filepath.Join(dir, "access-log"))
	path := filepath.Join(dir, "access-log", files[0].name)
	child := exec.Command(os.Args[0], "-test.run=^TestCrashProcessAppendRecovery$")
	child.Env = append(os.Environ(), "TSLINK_ACCESS_CRASH_FILE="+path)
	err := child.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 73 {
		t.Fatalf("crash child exit %v", err)
	}
	s = newTestStore(t, dir, Options{}, func() time.Time { return testTime })
	s.Record(event("after", "alice", "/ok"))
	closeStore(t, s)
	r, e := Query(dir, Filter{})
	if e != nil || r.Summary.Count != 2 {
		t.Fatalf("process crash recovery %+v %v", r, e)
	}
}
func TestCoarsePrefixAndBoundedTags(t *testing.T) {
	if CoarseRemote("192.168.4.0/24") != "192.168.4.0/24" || CoarseRemote("::ffff:192.168.4.9") != "192.168.4.0/24" {
		t.Fatal("coarse prefix/idempotence broken")
	}
	e := sanitize(Event{Identity: Identity{Tags: make([]string, 40)}})
	if len(e.Identity.Tags) != 32 {
		t.Fatal("tags not bounded")
	}
	var missing *Store
	if !missing.Record(Event{}) {
		t.Fatal("nil writer rejected")
	}
	missing.Close()
}
