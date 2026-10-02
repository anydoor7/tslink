package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// MCPEventsPath is the control plane's server-sent event stream.
//
// It is a separate path from MCPControlPlanePath on purpose. The Streamable
// HTTP transport mounted there runs stateless, which makes the SDK answer GET
// with 405; sharing one path would mean deciding between the two by method,
// and the MCP transport — not this file — owns what GET means on /mcp.
const MCPEventsPath = "/events"

// Event stream frame types. Every frame says what it is in two places: the SSE
// `event:` field and the payload's own "type". A client is never asked to
// infer whether it is holding a full state or a delta.
const (
	MCPEventSnapshot  = "snapshot"
	MCPEventUpdate    = "update"
	MCPEventKeepalive = "keepalive"
)

const (
	// DefaultMCPEventsKeepalive is the heartbeat period when configuration
	// does not set one.
	//
	// The heartbeat is not optional. A tailnet long connection can go silent
	// without the socket reporting an error, and in that state "no events" and
	// "the connection is dead" produce byte-identical observations at the
	// client: nothing arrives either way. The heartbeat is what makes those two
	// distinguishable, so a client can reconnect on silence instead of showing
	// stale state forever.
	DefaultMCPEventsKeepalive = 20 * time.Second
	// MinMCPEventsKeepalive and MaxMCPEventsKeepalive bound the configured
	// value. Below the minimum the heartbeat becomes traffic rather than
	// liveness; above the maximum a dead connection outlives any UI that cares.
	MinMCPEventsKeepalive = 5 * time.Second
	MaxMCPEventsKeepalive = 5 * time.Minute

	// mcpEventsMaxStreams bounds concurrent event streams.
	//
	// The control-plane listener is already wrapped in newLimitedListener with
	// httpMaxActiveConns, the same bound every service listener uses. That bound
	// is sized for request/response traffic, where a connection is returned in
	// milliseconds. An event stream holds its connection for as long as the
	// client stays open, so without a second, smaller bound a handful of
	// reconnect-looping clients could park the whole listener budget and lock
	// every MCP tool call out of the daemon. This bound is therefore about
	// what a connection costs over time, not about how many exist at an
	// instant, which is why it is separate from and far below the listener's.
	mcpEventsMaxStreams = 32

	// mcpEventsWriteTimeout bounds one frame write. A phone that stops reading
	// must not be able to wedge its own handler goroutine forever; the
	// publisher is already insulated from it (see eventHub.publish), so this
	// bound only decides how long a stuck stream keeps its own slot.
	mcpEventsWriteTimeout = 10 * time.Second
)

// mcpEventStateMaxAge bounds how long one built state may be reused.
//
// The generation key is what collapses a change storm into a single build. This
// bound answers a different question: what a stream that connects long after
// the last change should be handed. Its first frame is that client's whole
// picture of the daemon, so it reads current state rather than inheriting an
// entry built at some earlier, arbitrary time. Streams inside one burst still
// share a single build, which is the point of the cache.
//
// This is NOT a safety net for missing publish sites. An already-connected
// stream never wakes on its own, so an incomplete publish set still means that
// stream sees nothing — the bound would only make the staleness differ between
// a fresh connection and an old one, which is worse to diagnose than a uniform
// silence. The fix for a missing publish site is to add the publish site.
var mcpEventStateMaxAge = time.Second

// mcpEventInstanceID identifies this daemon process for the lifetime of the
// process. See the id/sequence/instance contract above.
var mcpEventInstanceID = newMCPEventInstanceID()

// mcpEventID numbers frames across every stream this process serves. It is the
// value written to the SSE id: field.
var mcpEventID atomic.Uint64

func newMCPEventInstanceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		// The field must be populated: a client that cannot see the instance
		// cannot tell a restart from a gap. A clock-derived value is unique
		// enough for that comparison even though it is not random.
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}

// MCPEventPayload is the envelope every frame carries. State is the
// transport-independent body built by MCPControlPlane.EventsSnapshot; this
// package never inspects it.
//
// Three numbering fields, three different scopes. They are not redundant, and
// reading one as the other is the mistake this comment exists to prevent:
//
//   - Instance names the daemon process. It is generated once at start and is
//     the same on every frame of every stream until the daemon restarts. It is
//     the only thing that lets a client tell "the server restarted, so EventID
//     legitimately starts low again" from "I lost frames".
//   - EventID is monotonic over the whole instance and is what the SSE id:
//     field carries. It is shared by every stream, so one stream sees gaps
//     wherever another stream consumed a value: it orders frames and detects
//     staleness across a reconnect, and it must not be used for per-stream gap
//     detection.
//   - Sequence is this connection's own frame counter, starting at 1 for the
//     snapshot. It is contiguous within one connection and resets on the next,
//     which is what makes per-stream gap detection possible at all.
//
// There is no Last-Event-ID replay. A reconnect is answered with a full
// snapshot, so a client that missed frames is made current by the first frame
// it receives rather than by asking for the ones it lost.
type MCPEventPayload struct {
	Type string `json:"type"`
	// Instance is this daemon process's identity; see the contract above.
	Instance string `json:"instance"`
	// EventID mirrors the SSE id: field: monotonic across this instance,
	// shared by every stream.
	EventID uint64 `json:"event_id"`
	// Sequence is the per-connection frame ordinal, starting at 1.
	Sequence   uint64 `json:"sequence"`
	EmittedAt  string `json:"emitted_at"`
	Keepalive  string `json:"keepalive_interval,omitempty"`
	State      any    `json:"state,omitempty"`
	StateError string `json:"state_error,omitempty"`
}

// eventHub fans one "runtime state changed" notification out to every open
// stream.
//
// Each subscriber gets a capacity-1 channel and publish never blocks on it.
// That combination is what keeps one stalled client from reaching the
// publisher: a send that would block is dropped, and dropping is correct here
// because the pending notification already in the buffer means the same thing
// the dropped one would have — "rebuild and send current state". Notifications
// coalesce instead of queueing, so a subscriber that wakes up late sends one
// current frame rather than replaying a backlog of stale ones.
type eventHub struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
	// generation counts published changes. It is the cache key streams use to
	// share one built state per change instead of each rebuilding it; see
	// eventStateCache.
	generation atomic.Uint64
}

func newEventHub() *eventHub {
	return &eventHub{subs: make(map[chan struct{}]struct{})}
}

// subscribe registers a listener and returns it with its own release func.
// release is idempotent, so a handler can defer it unconditionally.
func (h *eventHub) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
		})
	}
}

// publish wakes every subscriber. It is safe to call while holding Server.mu:
// it takes only its own lock and performs no blocking send.
//
// The generation is advanced before any subscriber is woken, so a stream that
// wakes and immediately reads the generation cannot read the value this change
// superseded.
func (h *eventHub) publish() {
	if h == nil {
		return
	}
	h.generation.Add(1)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// currentGeneration is the change counter a stream keys its state build on.
func (h *eventHub) currentGeneration() uint64 {
	if h == nil {
		return 0
	}
	return h.generation.Load()
}

func (h *eventHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// eventStateCache builds one state per change and hands the same result to
// every stream that asks for it.
//
// Without it, the cost of a change is O(open streams): every stream rebuilds
// the payload independently, and building it means re-reading registry.json,
// runtime.json, the PID file and the auth handoff. One registry write with the
// stream limit saturated is then 32 identical passes over the same files,
// arriving within milliseconds of each other because they were all woken by the
// same notification. That is the cost profile polling was supposed to be
// replaced to avoid.
//
// The key is the hub's generation. The first stream to ask for a generation
// builds it; every other stream asking for the same generation waits on that
// one build and receives its result — including its error, because a failing
// build that is not shared degenerates straight back into the storm at exactly
// the moment the daemon is least healthy.
//
// Only the newest entry is kept. An older generation's answer has been
// superseded by definition, so there is nothing to look it up for.
type eventStateCache struct {
	build  func(context.Context) (any, error)
	maxAge time.Duration
	// nowFn is the clock this cache reads, captured once by whoever built it.
	// It is a field rather than a direct read of the package-level seam for the
	// same reason the accept loop in startNodeLocked and the lifecycle ticker
	// hoist theirs: this cache is reached from net/http's per-request
	// goroutines, which outlive the test that installed a stub and restored it
	// from t.Cleanup. Reading the seam from those goroutines is a data race
	// whether or not any current test happens to trigger it.
	nowFn func() time.Time

	mu    sync.Mutex
	entry *eventStateEntry
}

type eventStateEntry struct {
	generation uint64
	builtAt    time.Time
	done       chan struct{}
	state      any
	err        error
}

func newEventStateCache(build func(context.Context) (any, error), nowFn func() time.Time) *eventStateCache {
	return &eventStateCache{build: build, maxAge: mcpEventStateMaxAge, nowFn: nowFn}
}

// fresh reports whether an entry may still answer for the requested
// generation. An in-flight entry is always fresh: it was started for this
// generation or a later one, so waiting for it is what sharing means.
func (e *eventStateEntry) fresh(generation uint64, now time.Time, maxAge time.Duration) bool {
	if e == nil || e.generation < generation {
		return false
	}
	select {
	case <-e.done:
	default:
		return true
	}
	return now.Sub(e.builtAt) < maxAge
}

// get returns the state for generation, building it at most once.
func (c *eventStateCache) get(ctx context.Context, generation uint64) (any, error) {
	if c == nil || c.build == nil {
		return nil, nil
	}
	c.mu.Lock()
	if entry := c.entry; entry.fresh(generation, c.nowFn(), c.maxAge) {
		c.mu.Unlock()
		select {
		case <-entry.done:
			return entry.state, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entry := &eventStateEntry{generation: generation, done: make(chan struct{})}
	c.entry = entry
	c.mu.Unlock()
	c.run(ctx, entry)
	return entry.state, entry.err
}

// run performs the one build this caller became responsible for.
//
// The deferred recover is load-bearing and is the price of sharing: a builder
// that panics used to take down only the stream that called it, because each
// stream built its own state. Now every stream waiting on this generation is
// parked on entry.done, and a later stream would join them rather than start
// its own build — so a single panic with no release here would wedge the whole
// path until the next publish, with the streams showing nothing at all. The
// panic is re-raised after the waiters are freed, so net/http still logs it and
// still ends this stream.
func (c *eventStateCache) run(ctx context.Context, entry *eventStateEntry) {
	defer func() {
		if r := recover(); r != nil {
			entry.err = fmt.Errorf("event state build panicked: %v", r)
			entry.builtAt = c.nowFn()
			close(entry.done)
			panic(r)
		}
	}()
	// The build is detached from this caller's cancellation on purpose: its
	// result belongs to every stream waiting on this generation, so the one
	// that happened to arrive first must not decide what the others get by
	// disconnecting mid-build.
	entry.state, entry.err = c.build(context.WithoutCancel(ctx))
	entry.builtAt = c.nowFn()
	close(entry.done)
}

// mcpEventsKeepalive resolves the configured heartbeat period.
func mcpEventsKeepalive(configured time.Duration) time.Duration {
	if configured <= 0 {
		return DefaultMCPEventsKeepalive
	}
	if configured < MinMCPEventsKeepalive {
		return MinMCPEventsKeepalive
	}
	if configured > MaxMCPEventsKeepalive {
		return MaxMCPEventsKeepalive
	}
	return configured
}

// newMCPEventsHandler serves the control plane's event stream.
//
// It is mounted behind the same Origin and authorization middleware the MCP
// transport is mounted behind; nothing about identity is decided here.
func newMCPEventsHandler(cp *MCPControlPlane, hub *eventHub) http.Handler {
	keepalive := mcpEventsKeepalive(cp.EventsKeepalive)
	slots := make(chan struct{}, mcpEventsMaxStreams)
	// Read the clock seam here, on the goroutine that assembles the mux, and
	// hand the function value down. Every site below runs on a net/http
	// request goroutine that outlives the caller, so none of them may touch the
	// package-level variable directly. Production assigns it once, at init.
	nowFn := serverNowFn
	// One cache for the whole handler, not one per stream: sharing is the
	// entire point.
	cache := newEventStateCache(cp.EventsSnapshot, nowFn)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The cache is global and includes owner inventory. Reduced clients use
		// scoped list/status polling instead of receiving this owner's stream.
		if session, ok := mcpscope.FromContext(r.Context()); ok && session.Scope.Role != "owner" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			mcpEventsError(w, http.StatusMethodNotAllowed, "event stream accepts GET only")
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			slog.Warn("mcp event stream refused: concurrent stream limit reached",
				"limit", mcpEventsMaxStreams, "remote_addr", r.RemoteAddr)
			w.Header().Set("Retry-After", "5")
			mcpEventsError(w, http.StatusServiceUnavailable, "too many concurrent event streams")
			return
		}
		serveMCPEventStream(w, r, cache, hub, keepalive, nowFn)
	})
}

// mcpEventsError writes a refusal in the same JSON-RPC error shape the rest of
// the control plane uses, so a client parses one error format on this node.
func mcpEventsError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"error":   map[string]any{"code": -32600, "message": message},
	})
}

// serveMCPEventStream runs one stream until the client disconnects, a write
// fails, or the daemon shuts the request context down.
//
// Ordering is load-bearing: the hub subscription is taken *before* the first
// snapshot is built. Subscribing afterwards would leave a window in which a
// change that landed between building and subscribing is never delivered and
// never superseded, and the client would sit on state it believes is current.
func serveMCPEventStream(
	w http.ResponseWriter,
	r *http.Request,
	cache *eventStateCache,
	hub *eventHub,
	keepalive time.Duration,
	nowFn func() time.Time,
) {
	changed, release := hub.subscribe()
	defer release()

	// The generation is read before the build, never after: a change that lands
	// during a build then advances past the value this frame was keyed on, and
	// the notification it also delivered makes the next iteration rebuild.
	// Reading it afterwards would let a frame claim a generation it did not
	// observe, and the cache would answer the next stream with it.
	snapshot := func(ctx context.Context) (any, error) {
		return cache.get(ctx, hub.currentGeneration())
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	// Defensive against an intermediary that buffers responses. tsnet serves
	// this directly, so it is a statement of intent rather than a fix.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	controller := http.NewResponseController(w)
	stream := &mcpEventStream{w: w, controller: controller, nowFn: nowFn}

	ctx := r.Context()
	var sequence uint64

	sequence++
	if err := stream.write(mcpEventFrame(ctx, MCPEventSnapshot, sequence, keepalive, snapshot, nowFn)); err != nil {
		slog.Warn("mcp event stream ended writing the initial snapshot", "remote_addr", r.RemoteAddr, "error", err)
		return
	}

	ticker := time.NewTicker(keepalive)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-changed:
			sequence++
			if err := stream.write(mcpEventFrame(ctx, MCPEventUpdate, sequence, keepalive, snapshot, nowFn)); err != nil {
				slog.Warn("mcp event stream ended writing an update", "remote_addr", r.RemoteAddr, "error", err)
				return
			}
		case <-ticker.C:
			sequence++
			if err := stream.write(mcpEventFrame(ctx, MCPEventKeepalive, sequence, keepalive, nil, nowFn)); err != nil {
				slog.Warn("mcp event stream ended writing a keepalive", "remote_addr", r.RemoteAddr, "error", err)
				return
			}
		}
	}
}

// mcpEventFrame renders one frame. A snapshot builder that fails produces a
// frame carrying state_error rather than silence, so a client can tell "the
// daemon could not read its own state" from "nothing has changed".
func mcpEventFrame(
	ctx context.Context,
	kind string,
	sequence uint64,
	keepalive time.Duration,
	snapshot func(context.Context) (any, error),
	nowFn func() time.Time,
) []byte {
	payload := MCPEventPayload{
		Type:      kind,
		Instance:  mcpEventInstanceID,
		EventID:   mcpEventID.Add(1),
		Sequence:  sequence,
		EmittedAt: nowFn().UTC().Format(time.RFC3339Nano),
		Keepalive: keepalive.String(),
	}
	if snapshot != nil {
		state, err := snapshot(ctx)
		if err != nil {
			payload.StateError = err.Error()
		} else {
			payload.State = state
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshal can only fail on the injected state, so fall back to an
		// envelope that still names the frame and reports the failure.
		body, _ = json.Marshal(MCPEventPayload{
			Type:       kind,
			Instance:   payload.Instance,
			EventID:    payload.EventID,
			Sequence:   sequence,
			EmittedAt:  payload.EmittedAt,
			Keepalive:  payload.Keepalive,
			StateError: "state is not serializable",
		})
	}
	// id: carries the instance-wide EventID, not the per-connection Sequence.
	// A client that detects gaps on a per-connection counter would report loss
	// on every reconnect, because that counter restarts at 1 by design.
	return []byte(fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", kind, payload.EventID, body))
}

// mcpEventStream writes frames under a deadline.
type mcpEventStream struct {
	w          http.ResponseWriter
	controller *http.ResponseController
	// nowFn is the clock seam, captured by the handler. See eventStateCache.
	nowFn func() time.Time
	// deadlineUnsupported records that this ResponseWriter chain cannot carry
	// a write deadline, so the warning is logged once per stream rather than
	// once per frame.
	deadlineUnsupported bool
}

func (s *mcpEventStream) write(frame []byte) error {
	deadline := s.nowFn().Add(mcpEventsWriteTimeout)
	if err := s.controller.SetWriteDeadline(deadline); err != nil {
		if !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		if !s.deadlineUnsupported {
			s.deadlineUnsupported = true
			slog.Warn("mcp event stream has no write deadline support; a stalled client holds its stream slot until it disconnects")
		}
	}
	if _, err := s.w.Write(frame); err != nil {
		return err
	}
	return s.controller.Flush()
}
