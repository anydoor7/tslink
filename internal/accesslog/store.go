package accesslog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/filelock"
)

const maxRecordBytes = 32768

var segmentName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-\d{6}\.jsonl$`)

type HistoryWindow struct {
	Start  time.Time  `json:"start"`
	End    *time.Time `json:"end,omitempty"` // absent while history is unavailable
	Reason string     `json:"reason"`
}
type Health struct {
	Current        bool            `json:"current"`
	MissingHistory []HistoryWindow `json:"missing_history,omitempty"`
	Enabled        bool            `json:"enabled"`
	LastWrite      *time.Time      `json:"last_write"`
	Drops          uint64          `json:"drops"`
	Size           int64           `json:"size_bytes"`
	Error          string          `json:"error,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// Store owns one bounded queue and one worker. File operations and fsync never
// execute on serving goroutines. A process lock refuses a second writer.
type queuedEvent struct {
	event   Event
	resolve func() Identity
}
type Store struct {
	dir          string
	opts         Options
	now          func() time.Time
	queue        chan queuedEvent
	active       atomic.Int64
	stop         chan struct{}
	done         chan struct{}
	closing      atomic.Bool
	once         sync.Once
	drops        atomic.Uint64
	initialDrops uint64
	mu           sync.RWMutex
	health       Health
	lock         *os.File
	// beforeAppend is injected before worker startup in tests to stall disk I/O.
	beforeAppend func()
}

func New(configDir string, opts Options, now func() time.Time) (*Store, error) {
	return newStore(configDir, opts, now, nil)
}
func newStore(configDir string, opts Options, now func() time.Time, before func()) (*Store, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	opts = opts.Defaults()
	s := &Store{dir: filepath.Join(configDir, "access-log"), opts: opts, now: now, queue: make(chan queuedEvent, opts.QueueSize), stop: make(chan struct{}), done: make(chan struct{}), beforeAppend: before, health: Health{Enabled: opts.IsEnabled()}}
	if err := atomicfile.EnsurePrivateDir(s.dir); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, "writer.lock")
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok, err := filelock.TryLock(f)
	if err != nil || !ok {
		f.Close()
		return nil, fmt.Errorf("access log writer already active or unavailable")
	}
	s.lock = f
	previous := ReadHealth(configDir)
	s.initialDrops = previous.Drops
	s.drops.Store(previous.Drops)
	s.health.LastWrite = previous.LastWrite
	go s.run()
	return s, nil
}
func (s *Store) Record(e Event) bool { return s.RecordResolved(e, nil) }

// RecordResolved defers optional WhoIs enrichment to the bounded worker. The
// callback must be bounded and may capture only address/identity metadata.
func (s *Store) RecordResolved(e Event, resolve func() Identity) bool {
	if s == nil || !s.opts.IsEnabled() {
		return true
	}
	s.active.Add(1)
	defer s.active.Add(-1)
	if s.closing.Load() {
		s.drops.Add(1)
		return false
	}
	if e.Time.IsZero() {
		e.Time = s.now().UTC()
	}
	e.Path = PathForMode(e.Path, s.opts.ModeFor(e.PathMode, nil))
	e = sanitize(e)
	select {
	case s.queue <- queuedEvent{e, resolve}:
		return true
	default:
		s.drops.Add(1)
		return false
	}
}
func (s *Store) Health() Health {
	s.mu.RLock()
	h := s.health
	s.mu.RUnlock()
	h.Drops = s.drops.Load()
	return h
}

// Close begins a drain without waiting for disk. Wait is available to a bounded
// shutdown caller; the serving path never joins the writer.
func (s *Store) Close() {
	if s != nil {
		s.once.Do(func() { s.closing.Store(true); close(s.stop) })
	}
}
func (s *Store) Done() <-chan struct{} { return s.done }
func (s *Store) setError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.health.Error = "access_log_io_failed"
	} else {
		s.health.Error = ""
	}
}
func (s *Store) publish() {
	s.mu.Lock()
	s.health.UpdatedAt = s.now().UTC()
	s.mu.Unlock()
	h := s.Health()
	data, err := json.Marshal(h)
	if err == nil {
		err = atomicfile.WriteFile(filepath.Join(s.dir, "health.json"), append(data, '\n'))
	}
	if err != nil {
		s.setError(err)
	}
}
func (s *Store) run() {
	defer close(s.done)
	defer s.lock.Close()
	defer filelock.Unlock(s.lock)
	err := s.recover()
	s.setError(err)
	healthy := err == nil
	if healthy {
		err := s.evict(0)
		healthy = err == nil
		s.setError(err)
	}
	// Publish drop counters independently of a stalled append/enrichment worker.
	stopHealth, healthDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(healthDone)
		s.publish()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.publish()
			case <-stopHealth:
				return
			}
		}
	}()
	defer func() { close(stopHealth); <-healthDone; s.publish() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case e := <-s.queue:
			if healthy {
				healthy = s.write(e)
			} else {
				s.drops.Add(1)
			}
		case <-ticker.C:
			if !healthy {
				err := s.recover()
				healthy = err == nil
				s.setError(err)
			}
			if healthy {
				if err := s.evict(0); err != nil {
					healthy = false
					s.setError(err)
				}
			}
		case <-s.stop:
			for s.active.Load() != 0 {
				runtime.Gosched()
			}
			for {
				select {
				case e := <-s.queue:
					if healthy {
						healthy = s.write(e)
					} else {
						s.drops.Add(1)
					}
				default:
					return
				}
			}
		}
	}
}
func (s *Store) write(q queuedEvent) bool {
	e := q.event
	if q.resolve != nil {
		e.Identity = q.resolve()
		e = sanitize(e)
	}
	if s.beforeAppend != nil {
		s.beforeAppend()
	}
	err := s.append(e)
	if err != nil {
		s.drops.Add(1)
	}
	s.setError(err)
	return err == nil
}

type segment struct {
	name string
	size int64
}

func segments(dir string) ([]segment, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []segment{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []segment{}
	for _, entry := range entries {
		if !segmentName.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("access log segment is not a regular file")
		}
		out = append(out, segment{entry.Name(), info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}
func (s *Store) evict(incoming int64) error {
	files, err := segments(s.dir)
	if err != nil {
		return err
	}
	var size int64
	for _, f := range files {
		size += f.size
	}
	cutoff := s.now().UTC().AddDate(0, 0, 1-s.opts.RetentionDays).Format("2006-01-02")
	for _, f := range files {
		if f.name[:10] < cutoff || size+incoming > s.opts.MaxBytes {
			if err := os.Remove(filepath.Join(s.dir, f.name)); err != nil {
				return err
			}
			size -= f.size
		}
	}
	s.mu.Lock()
	s.health.Size = size
	s.mu.Unlock()
	return nil
}
func (s *Store) recover() error {
	files, err := segments(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range files {
		path := filepath.Join(s.dir, entry.name)
		if err := atomicfile.ConvergePrivateFile(path); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		reader := bufio.NewReaderSize(f, maxRecordBytes)
		var good int64
		for {
			line, readErr := reader.ReadSlice('\n')
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil || !json.Valid(bytes.TrimSpace(line)) {
				f.Close()
				return fmt.Errorf("invalid access log segment")
			}
			good += int64(len(line))
		}
		err = f.Truncate(good)
		if err == nil {
			err = f.Sync()
		}
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) append(e Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxRecordBytes {
		return fmt.Errorf("access event too large")
	}
	// Never create a historical segment after retention has already elapsed.
	cutoff := s.now().UTC().AddDate(0, 0, 1-s.opts.RetentionDays).Format("2006-01-02")
	day := e.Time.UTC().Format("2006-01-02")
	if day < cutoff {
		return fmt.Errorf("access event older than retention")
	}
	if err := s.evict(int64(len(data))); err != nil {
		return err
	}
	files, err := segments(s.dir)
	if err != nil {
		return err
	}
	name := day + "-000000.jsonl"
	segmentCap := min(int64(1<<20), s.opts.MaxBytes/4)
	for _, f := range files {
		if f.name[:10] == day {
			name = f.name
			if f.size+int64(len(data)) > segmentCap {
				var seq int
				fmt.Sscanf(f.name[11:], "%d.jsonl", &seq)
				name = fmt.Sprintf("%s-%06d.jsonl", day, seq+1)
			}
		}
	}
	path := filepath.Join(s.dir, name)
	if err := atomicfile.ConvergePrivateFile(path); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		_ = f.Truncate(info.Size())
		_ = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if info.Size() == 0 {
		if err := syncDirectory(s.dir); err != nil {
			return err
		}
	}
	s.mu.Lock()
	t := s.now().UTC()
	s.health.LastWrite = &t
	s.health.Size += int64(n)
	s.mu.Unlock()
	return nil
}

// ReadHealth is strictly read-only. Missing state is reported explicitly; no
// directory, lock or permission change is made by CLI/MCP queries.
func ReadHealth(configDir string) Health {
	h := Health{Enabled: true}
	path := filepath.Join(configDir, "access-log", "health.json")
	var data []byte
	err := atomicfile.ReadSettled(path, func() error {
		return atomicfile.RetryFileOperation(func() error {
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() > 4096 {
				return errors.New("unsafe access log health file")
			}
			f, err := atomicfile.OpenSharedRead(path)
			if err != nil {
				return err
			}
			defer f.Close()
			data, err = io.ReadAll(io.LimitReader(f, 4097))
			if err == nil && len(data) > 4096 {
				return errors.New("oversized access log health file")
			}
			return err
		})
	})
	if os.IsNotExist(err) {
		h.Error = "access_log_not_started"
		return h
	}
	if err != nil {
		h.Error = "access_log_health_unavailable"
		return h
	}
	if json.Unmarshal(data, &h) != nil {
		h.Error = "access_log_health_unavailable"
	}
	return h
}
