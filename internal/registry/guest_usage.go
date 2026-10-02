package registry

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/filelock"
)

type guestUsage struct {
	uses, sessions uint64
	last           *time.Time
}

// Pending usage is process-local. Authorization never depends on its persistence.
// The server flushes every 30s and on shutdown; save folds it into any other write.
var guestUsageState = struct {
	sync.Mutex
	pending map[string]map[string]guestUsage
	errors  map[string]error
}{pending: make(map[string]map[string]guestUsage), errors: make(map[string]error)}

// GuestCounterError retains a persistence failure until a later registry write
// succeeds. A no-op flush cannot confirm durability of an earlier publication.
func GuestCounterError(path string) error {
	guestUsageState.Lock()
	defer guestUsageState.Unlock()
	return guestUsageState.errors[path]
}

func recordGuestCounterError(path string, err error) {
	guestUsageState.Lock()
	prior := guestUsageState.errors[path]
	if err == nil {
		delete(guestUsageState.errors, path)
	} else {
		guestUsageState.errors[path] = err
	}
	changed := err != nil && (prior == nil || prior.Error() != err.Error())
	guestUsageState.Unlock()
	if changed {
		slog.Warn("guest counters persistence failed", "error", err)
	}
}

func addGuestUsage(path, id string, at time.Time, use, session bool) {
	if !use && !session {
		return
	}
	guestUsageState.Lock()
	defer guestUsageState.Unlock()
	m := guestUsageState.pending[path]
	if m == nil {
		m = make(map[string]guestUsage)
		guestUsageState.pending[path] = m
	}
	u := m[id]
	if use {
		u.uses++
		t := at.UTC()
		if u.last == nil || t.After(*u.last) {
			u.last = &t
		}
	}
	if session {
		u.sessions++
	}
	m[id] = u
}

func applyGuestUsage(g *GuestGrant, u guestUsage) {
	g.Uses += u.uses
	g.Sessions += u.sessions
	if u.last != nil && (g.LastUsedAt == nil || u.last.After(*g.LastUsedAt)) {
		g.LastUsedAt = u.last
	}
}

func pendingGuestViews(path string, reg *Registry) {
	guestUsageState.Lock()
	defer guestUsageState.Unlock()
	for i := range reg.Guests {
		applyGuestUsage(&reg.Guests[i], guestUsageState.pending[path][reg.Guests[i].ID])
	}
}

func snapshotGuestUsage(path string) map[string]guestUsage {
	guestUsageState.Lock()
	defer guestUsageState.Unlock()
	out := make(map[string]guestUsage, len(guestUsageState.pending[path]))
	for id, usage := range guestUsageState.pending[path] {
		out[id] = usage
	}
	return out
}

func acknowledgeGuestUsage(path string, batch map[string]guestUsage) {
	guestUsageState.Lock()
	defer guestUsageState.Unlock()
	pending := guestUsageState.pending[path]
	for id, saved := range batch {
		current := pending[id]
		current.uses -= saved.uses
		current.sessions -= saved.sessions
		if current.uses == 0 {
			current.last = nil
		}
		if current.uses == 0 && current.sessions == 0 {
			delete(pending, id)
		} else {
			pending[id] = current
		}
	}
	if len(pending) == 0 {
		delete(guestUsageState.pending, path)
	}
}

// FlushGuestCounters refuses a busy writer rather than delaying shutdown or a
// periodic worker indefinitely. Unpublished batches remain pending for the next
// write; published batches are acknowledged even if durability is unconfirmed.
func FlushGuestCounters(path string) error {
	guestUsageState.Lock()
	pending := len(guestUsageState.pending[path]) != 0
	guestUsageState.Unlock()
	if !pending {
		return nil
	}
	acquired, err := tryWithLock(path, func() error {
		reg, issues, err := guestPreflight(path)
		if err != nil {
			recordGuestCounterError(path, err)
			return err
		}
		if len(issues) > 0 {
			recordGuestCounterError(path, issues[0])
			return issues[0]
		}
		return save(path, reg)
	})
	if err != nil {
		recordGuestCounterError(path, err)
		return err
	}
	if !acquired {
		err := fmt.Errorf("guest counters: registry writer busy")
		recordGuestCounterError(path, err)
		return err
	}
	return nil
}

// Shared readers see the file published by the most recent committed writer.
// No grant snapshot survives a request, so a completed revoke cannot be stale.
func readGuestState(path string) (*Registry, []ServiceIssue, error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("unsafe guest registry lock")
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		acquired, err := filelock.TryReadLock(f)
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			break
		}
		if !time.Now().Before(deadline) {
			return nil, nil, fmt.Errorf("guest registry busy")
		}
		time.Sleep(2 * time.Millisecond)
	}
	defer filelock.Unlock(f)
	return guestPreflight(path)
}

var guestCommits = struct {
	sync.Mutex
	next      uint64
	listeners map[string]map[uint64]func([]GuestGrant)
}{listeners: make(map[string]map[uint64]func([]GuestGrant))}

// WatchGuestCommits observes successful in-process commits synchronously. File
// notifications in the gate also observe commits made by other processes.
func WatchGuestCommits(path string, fn func([]GuestGrant)) func() {
	guestCommits.Lock()
	defer guestCommits.Unlock()
	guestCommits.next++
	id := guestCommits.next
	if guestCommits.listeners[path] == nil {
		guestCommits.listeners[path] = make(map[uint64]func([]GuestGrant))
	}
	guestCommits.listeners[path][id] = fn
	return func() {
		guestCommits.Lock()
		defer guestCommits.Unlock()
		delete(guestCommits.listeners[path], id)
		if len(guestCommits.listeners[path]) == 0 {
			delete(guestCommits.listeners, path)
		}
	}
}

func notifyGuestCommit(path string, grants []GuestGrant) {
	guestCommits.Lock()
	callbacks := make([]func([]GuestGrant), 0, len(guestCommits.listeners[path]))
	for _, fn := range guestCommits.listeners[path] {
		callbacks = append(callbacks, fn)
	}
	guestCommits.Unlock()
	for _, fn := range callbacks {
		fn(grants)
	}
}
