package registry

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestGuestCounterLockFailureWarning(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(old)
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".lock", 0700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		err := FlushGuestCounters(path)
		if err == nil {
			t.Fatal("invalid lock positive control")
		}
		if GuestCounterError(path) != err {
			t.Fatal("lock failure missing from counter warning", err)
		}
		pending := snapshotGuestUsage(path)[view.ID]
		if pending.uses != 1 || pending.sessions != 1 {
			t.Fatal("failed flush lost pending counters", pending)
		}
	}
	if count := strings.Count(logs.String(), "guest counters persistence failed"); count != 1 {
		t.Fatal("lock warning not logged exactly once", count)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("lock failure changed registry bytes", err)
	}
	if err := os.Remove(path + ".lock"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := FlushGuestCounters(path); err != nil {
			t.Fatal(err)
		}
	}
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Guests[0].Uses != 1 || reg.Guests[0].Sessions != 1 || len(snapshotGuestUsage(path)) != 0 {
		t.Fatal("counter retry did not persist exactly once")
	}
	if GuestCounterError(path) != nil {
		t.Fatal("successful write did not clear warning")
	}
	t.Log("lock failure retained 1 use/1 session, logged once; recovery persisted exactly once and cleared warning")
}

func TestGuestCountersBatchAndRetry(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow.Add(time.Duration(i)*time.Second), true, i == 0); reason != "allowed" {
			t.Fatal(reason)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("authorization wrote counters", err)
	}
	memory, err := ShowGuest(path, view.ID, guestTestNow)
	if err != nil || memory.Uses != 50 || memory.Sessions != 1 || memory.LastUsedAt == nil || !memory.LastUsedAt.Equal(guestTestNow.Add(49*time.Second)) {
		t.Fatalf("pending counts: %+v %v", memory, err)
	}
	original := marshalFn
	marshalFn = func(any, string, string) ([]byte, error) { return nil, fmt.Errorf("counter write unavailable") }
	if err := FlushGuestCounters(path); err == nil {
		t.Fatal("failed flush reported success")
	}
	marshalFn = original
	t.Cleanup(func() { marshalFn = original })
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 50 || reg.Guests[0].Sessions != 1 {
		t.Fatalf("persisted counts: %+v %v", reg, err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	revoked, err := RevokeGuest(path, view.ID, guestTestNow)
	if err != nil || !revoked.Revoked || revoked.Uses != 51 {
		t.Fatalf("revoke flush: %+v %v", revoked, err)
	}
	reg, _, err = guestPreflight(path)
	if err != nil || !reg.Guests[0].Revoked || reg.Guests[0].Uses != 51 {
		t.Fatalf("durable revoke counts: %+v %v", reg, err)
	}
}

func TestGuestCounterFlushWriterRecovery(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	// The fixture holds the lock for the whole call, so a flush that waited
	// for the writer would never return.
	flushed := make(chan error, 1)
	go func() { flushed <- FlushGuestCounters(path) }()
	if err = testwait.Recv(t, flushed, "busy flush returned while another writer held the lock"); err == nil {
		t.Fatal("busy flush must remain bounded", err)
	}
	if err = filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("pending batch lost", err)
	}
}

func TestGuestCountersWithOtherRegistryWrites(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	if _, err = Add(path, Service{Name: "other", Type: TypeProxy, Target: "http://127.0.0.1:3001"}); err != nil {
		t.Fatal(err)
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("opportunistic batch missing", err)
	}
}

func TestGuestCounterFlushMissingAndCorruptRegistry(t *testing.T) {
	path := guestRegistry(t)
	view, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); !os.IsNotExist(err) {
		t.Fatal("missing registry flush", err)
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("flush recreated missing registry", err)
	}
	if err = os.WriteFile(path, []byte(`{"guests":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err == nil {
		t.Fatal("corrupt registry rewritten")
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != `{"guests":` {
		t.Fatal("corrupt bytes changed", err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	reg, _, err := Preflight(path)
	if err != nil || reg.Guests[0].Uses != 1 {
		t.Fatal("failed batch lost on recovery", err)
	}
}

// inFlushWindow runs window once inside FlushGuestCounters, after its unlocked
// pending check and before it tries the writer lock: where the gate monitor's
// flush meets the commit that woke it.
func inFlushWindow(t *testing.T, window func()) *bool {
	t.Helper()
	old := tryLockFn
	t.Cleanup(func() { tryLockFn = old })
	ran := false
	tryLockFn = func(f *os.File) (bool, error) {
		if !ran {
			ran = true
			window()
		}
		return old(f)
	}
	return &ran
}

// countRegistryPublications counts replacements in path's directory through
// the post-rename directory sync. fail is consulted on each count.
func countRegistryPublications(t *testing.T, path string, fail func() error) *int {
	t.Helper()
	count := 0
	dir := filepath.Clean(filepath.Dir(path))
	t.Cleanup(atomicfile.SetDirectorySyncForTest(func(d string) error {
		if filepath.Clean(d) != dir {
			return nil
		}
		count++
		if fail != nil {
			return fail()
		}
		return nil
	}))
	return &count
}

// registryIdentity reads the identity through a handle: on Windows a path Stat
// loads the file ID lazily, at comparison time, after the rewrite under test.
func registryIdentity(t *testing.T, path string) os.FileInfo {
	t.Helper()
	f, err := openRegistryFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func twoGuests(t *testing.T, path string) (GuestView, GuestView) {
	t.Helper()
	first, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := CreateGuest(path, guestOptions())
	if err != nil {
		t.Fatal(err)
	}
	return first, second
}

func TestGuestCounterFlushSkipsCountersACommitFolded(t *testing.T) {
	path := guestRegistry(t)
	view, other := twoGuests(t, path)
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	publications := countRegistryPublications(t, path, nil)
	var folded os.FileInfo
	var before int
	ran := inFlushWindow(t, func() {
		if _, err := RevokeGuest(path, other.ID, guestTestNow); err != nil {
			t.Fatal(err)
		}
		folded = registryIdentity(t, path)
		before = *publications
	})
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if !*ran {
		t.Fatal("flush returned before its window: the unlocked check saw no counters")
	}
	if before == 0 {
		t.Fatal("publication probe missed the window commit")
	}
	if len(snapshotGuestUsage(path)) != 0 || GuestCounterError(path) != nil {
		t.Fatal("window commit did not fold and confirm the counters")
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 || reg.Guests[0].Sessions != 1 || !reg.Guests[1].Revoked {
		t.Fatal("window commit state", err)
	}
	if got := *publications - before; got != 0 {
		t.Fatalf("flush rewrote the registry %d time(s) with nothing pending", got)
	}
	if !os.SameFile(folded, registryIdentity(t, path)) {
		t.Fatal("flush replaced the registry file with nothing pending")
	}

	// Control: the same probes see a flush that has counters to publish.
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow.Add(time.Minute), true, false); reason != "allowed" {
		t.Fatal(reason)
	}
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if got := *publications - before; got != 1 {
		t.Fatalf("publication probe saw %d rewrites for one flush with counters", got)
	}
	if os.SameFile(folded, registryIdentity(t, path)) {
		t.Fatal("identity probe missed a rewrite")
	}
	reg, _, err = guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 2 || reg.Guests[0].Sessions != 1 {
		t.Fatal("control flush did not publish", err)
	}
	t.Logf("window commit published %d time(s); flush after it: 0 rewrites, same file; control flush: 1 rewrite, new file", before)
}

func TestGuestCounterFlushPublishesCountersRecordedAfterItsCheck(t *testing.T) {
	path := guestRegistry(t)
	view, other := twoGuests(t, path)
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	publications := countRegistryPublications(t, path, nil)
	var before int
	ran := inFlushWindow(t, func() {
		if _, err := RevokeGuest(path, other.ID, guestTestNow); err != nil {
			t.Fatal(err)
		}
		if len(snapshotGuestUsage(path)) != 0 {
			t.Fatal("window commit left the first batch pending")
		}
		before = *publications
		if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow.Add(time.Minute), true, false); reason != "allowed" {
			t.Fatal(reason)
		}
	})
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if !*ran {
		t.Fatal("flush returned before its window: the unlocked check saw no counters")
	}
	if got := *publications - before; got != 1 {
		t.Fatalf("flush published %d time(s) for counters recorded in its window", got)
	}
	reg, _, err := guestPreflight(path)
	last := reg.Guests[0].LastUsedAt
	if err != nil || reg.Guests[0].Uses != 2 || reg.Guests[0].Sessions != 1 || last == nil || !last.Equal(guestTestNow.Add(time.Minute)) {
		t.Fatal("counters recorded after the unlocked check were not persisted", err)
	}
	if len(snapshotGuestUsage(path)) != 0 || GuestCounterError(path) != nil {
		t.Fatal("persisted batch not acknowledged as durable")
	}
	t.Log("commit folded 1 use/1 session in the window; 1 use recorded after it was flushed: disk 2 uses/1 session, nothing pending")
}

func TestGuestCounterFlushConfirmsAPublicationTheCommitLeftUnconfirmed(t *testing.T) {
	path := guestRegistry(t)
	view, other := twoGuests(t, path)
	if _, reason := CheckGuest(path, "photos", view.ID, guestTestNow, true, true); reason != "allowed" {
		t.Fatal(reason)
	}
	syncFailure := errors.New("injected directory fsync failure")
	failing := true
	publications := countRegistryPublications(t, path, func() error {
		if failing {
			return syncFailure
		}
		return nil
	})
	var before int
	ran := inFlushWindow(t, func() {
		if _, err := RevokeGuest(path, other.ID, guestTestNow); !atomicfile.IsPublished(err) || !errors.Is(err, syncFailure) {
			t.Fatal("window commit was not published with unconfirmed durability", err)
		}
		// Acknowledged out of pending; only the retained failure remains.
		if len(snapshotGuestUsage(path)) != 0 || !atomicfile.IsPublished(GuestCounterError(path)) {
			t.Fatal("window state is not a published, unconfirmed batch")
		}
		failing = false
		before = *publications
	})
	if err := FlushGuestCounters(path); err != nil {
		t.Fatal(err)
	}
	if !*ran {
		t.Fatal("flush returned before its window: the unlocked check saw no counters")
	}
	if got := *publications - before; got != 1 {
		t.Fatalf("flush published %d time(s) to confirm an unconfirmed batch", got)
	}
	if GuestCounterError(path) != nil {
		t.Fatal("successful flush left the durability failure retained")
	}
	reg, _, err := guestPreflight(path)
	if err != nil || reg.Guests[0].Uses != 1 || reg.Guests[0].Sessions != 1 || !reg.Guests[1].Revoked {
		t.Fatal("confirmed batch changed or reapplied", err)
	}
	t.Log("commit published 1 use/1 session with a failed directory sync; flush rewrote once and cleared the failure; disk 1 use/1 session")
}
