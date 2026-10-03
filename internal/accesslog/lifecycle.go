package accesslog

import (
	"sync"
	"time"
)

// Lifecycle is the daemon-owned writer handle retained by existing listeners.
// Initialization and retry I/O run outside its recording lock. Failed starts
// count drops and retain the exact missing-history window in runtime state,
// even when the access-log directory cannot be written.
type Lifecycle struct {
	mu      sync.RWMutex
	retryMu sync.Mutex
	dir     string
	opts    Options
	now     func() time.Time
	store   *Store
	health  Health
	closed  bool
	done    chan struct{}
}

func NewLifecycle(dir string, opts Options, now func() time.Time) *Lifecycle {
	l := &Lifecycle{dir: dir, opts: opts, now: now, done: make(chan struct{}), health: Health{Enabled: opts.IsEnabled(), Current: true}}
	l.Retry(now())
	return l
}

// Retry is called on the daemon lifecycle tick, never on a request goroutine.
func (l *Lifecycle) Retry(at time.Time) {
	l.retryMu.Lock()
	defer l.retryMu.Unlock()
	l.mu.RLock()
	skip := l.closed || l.store != nil || !l.opts.IsEnabled()
	l.mu.RUnlock()
	if skip {
		return
	}
	store, err := New(l.dir, l.opts, l.now)
	completedAt := l.now().UTC()
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.health.Error = "access_log_init_failed"
		if len(l.health.MissingHistory) == 0 {
			l.health.MissingHistory = []HistoryWindow{{Start: at.UTC(), Reason: "access_log_init_failed"}}
		}
	} else {
		l.store = store
		l.health.Error = ""
		if len(l.health.MissingHistory) > 0 {
			end := completedAt
			if end.Before(l.health.MissingHistory[0].Start) {
				end = l.health.MissingHistory[0].Start
			}
			l.health.MissingHistory[0].End = &end
		}
	}
	l.health.UpdatedAt = completedAt
}
func (l *Lifecycle) Record(e Event) bool { return l.RecordResolved(e, nil) }
func (l *Lifecycle) RecordResolved(e Event, resolve func() Identity) bool {
	l.mu.Lock()
	if !l.opts.IsEnabled() {
		l.mu.Unlock()
		return true
	}
	store := l.store
	if store == nil || l.closed {
		l.health.Drops++
		l.mu.Unlock()
		return false
	}
	l.mu.Unlock()
	return store.RecordResolved(e, resolve)
}
func (l *Lifecycle) Health() Health {
	l.mu.RLock()
	h := l.health
	store := l.store
	h.MissingHistory = append([]HistoryWindow(nil), h.MissingHistory...)
	l.mu.RUnlock()
	if store != nil {
		written := store.Health()
		h.LastWrite = written.LastWrite
		h.Size = written.Size
		h.Drops += written.Drops - store.initialDrops
		if written.UpdatedAt.After(h.UpdatedAt) {
			h.UpdatedAt = written.UpdatedAt
		}
		h.Error = written.Error
	}
	return h
}
func (l *Lifecycle) Close() {
	l.retryMu.Lock()
	defer l.retryMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if l.store != nil {
		l.store.Close()
	} else {
		close(l.done)
	}
}
func (l *Lifecycle) Done() <-chan struct{} {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.store != nil {
		return l.store.Done()
	}
	return l.done
}
