package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

// mcpEventsTestState is the stand-in for the payload package cmd builds. This
// package must not know that shape, so the tests do not assert it either; they
// assert that whatever the builder returned arrived intact.
type mcpEventsTestState struct {
	Revision int `json:"revision"`
}

func mcpEventsAllowedClient(t *testing.T) *LocalClient {
	t.Helper()
	return fakeWhoIsClient(t, &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
		Node:        &tailcfg.Node{},
	}, nil)
}

// mcpEventsFrame is one parsed SSE frame.
type mcpEventsFrame struct {
	Event string
	ID    string
	Data  string
}

// readMCPEventFrame reads frames until one arrives or the stream ends. It
// parses the wire bytes rather than any in-process structure, so a frame that
// is malformed as SSE fails here instead of passing on its Go value.
func readMCPEventFrame(t *testing.T, reader *bufio.Reader) mcpEventsFrame {
	t.Helper()
	var frame mcpEventsFrame
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read event frame: %v (partial %+v)", err, frame)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if frame.Event != "" || frame.Data != "" {
				return frame
			}
		case strings.HasPrefix(line, "event: "):
			frame.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			frame.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			frame.Data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func decodeMCPEventPayload(t *testing.T, frame mcpEventsFrame) MCPEventPayload {
	t.Helper()
	var payload MCPEventPayload
	if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
		t.Fatalf("decode %q: %v", frame.Data, err)
	}
	return payload
}

// mcpEventsTestServer starts the shipped control-plane handler over loopback
// with the event stream mounted and one authorized caller.
func mcpEventsTestServer(t *testing.T, hub *eventHub, snapshot func(context.Context) (any, error)) *httptest.Server {
	t.Helper()
	cp := &MCPControlPlane{
		AllowedUsers:   []string{"alice@example.com"},
		Handler:        &mcpProbeHandler{},
		EventsSnapshot: snapshot,
	}
	srv := httptest.NewServer(newMCPControlPlaneHandler(cp, mcpEventsAllowedClient(t), hub))
	t.Cleanup(srv.Close)
	return srv
}

func mcpEventsOpenStream(t *testing.T, srv *httptest.Server) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+MCPEventsPath, nil)
	if err != nil {
		cancel()
		t.Fatalf("build events request: %v", err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open event stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		t.Fatalf("event stream status = %d, want 200 (body=%q)", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		resp.Body.Close()
		cancel()
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	return bufio.NewReader(resp.Body), func() {
		cancel()
		resp.Body.Close()
	}
}

// TestMCPEventStreamSendsSnapshotThenUpdate is the core contract: the first
// frame is a full state labelled snapshot, and a later runtime change produces
// an update frame carrying the new state. Both are read off the wire.
func TestMCPEventStreamSendsSnapshotThenUpdate(t *testing.T) {
	var revision atomic.Int64
	revision.Store(1)
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return mcpEventsTestState{Revision: int(revision.Load())}, nil
	})

	reader, closeStream := mcpEventsOpenStream(t, srv)
	defer closeStream()

	first := readMCPEventFrame(t, reader)
	if first.Event != MCPEventSnapshot {
		t.Fatalf("first frame = %+v, want a snapshot", first)
	}
	firstPayload := decodeMCPEventPayload(t, first)
	if firstPayload.Type != MCPEventSnapshot || firstPayload.Sequence != 1 {
		t.Fatalf("first payload = %+v, want type snapshot sequence 1", firstPayload)
	}
	if first.ID != strconv.FormatUint(firstPayload.EventID, 10) {
		t.Fatalf("SSE id %q does not match payload event_id %d", first.ID, firstPayload.EventID)
	}
	if firstPayload.Keepalive == "" || firstPayload.EmittedAt == "" {
		t.Fatalf("first payload omits keepalive or emitted_at: %+v", firstPayload)
	}
	if !strings.Contains(first.Data, `"revision":1`) {
		t.Fatalf("snapshot data = %q, want the builder's state", first.Data)
	}

	// Wait for the handler to be parked on its select before publishing, so
	// the notification exercises the running loop rather than the buffered
	// channel the subscription starts with.
	waitForSubscribers(t, hub, 1)
	revision.Store(2)
	hub.publish()

	second := readMCPEventFrame(t, reader)
	if second.Event != MCPEventUpdate {
		t.Fatalf("second frame = %+v, want an update", second)
	}
	secondPayload := decodeMCPEventPayload(t, second)
	if secondPayload.Type != MCPEventUpdate || secondPayload.Sequence != 2 {
		t.Fatalf("second payload = %+v, want type update sequence 2", secondPayload)
	}
	if secondPayload.EventID <= firstPayload.EventID {
		t.Fatalf("event_id went from %d to %d, want it to advance", firstPayload.EventID, secondPayload.EventID)
	}
	if !strings.Contains(second.Data, `"revision":2`) {
		t.Fatalf("update data = %q, want the rebuilt state", second.Data)
	}
}

func waitForSubscribers(t *testing.T, hub *eventHub, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hub.subscriberCount() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("subscriber count = %d, want %d", hub.subscriberCount(), want)
}

// TestMCPEventStreamRejectsUnauthorizedCallerWithoutBuildingState covers the
// authorization requirement in the direction that matters: not only is the
// answer 403, the snapshot builder — which reads local daemon state — never
// runs for a caller outside the principal list.
func TestMCPEventStreamRejectsUnauthorizedCallerWithoutBuildingState(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		login string
	}{
		{"caller outside the allow list", []string{"alice@example.com"}, "eve@example.com"},
		{"no principal configured", nil, "alice@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var builds atomic.Int64
			cp := &MCPControlPlane{
				AllowedUsers: tc.allow,
				Handler:      &mcpProbeHandler{},
				EventsSnapshot: func(context.Context) (any, error) {
					builds.Add(1)
					return mcpEventsTestState{Revision: 1}, nil
				},
			}
			lc := fakeWhoIsClient(t, &apitype.WhoIsResponse{
				UserProfile: &tailcfg.UserProfile{LoginName: tc.login},
				Node:        &tailcfg.Node{},
			}, nil)
			handler := newMCPControlPlaneHandler(cp, lc, newEventHub())

			req := httptest.NewRequest(http.MethodGet, "https://tslink-mcp.example.ts.net"+MCPEventsPath, nil)
			req.Host = "tslink-mcp.example.ts.net"
			req.RemoteAddr = "100.64.0.9:1234"
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (body=%q)", rr.Code, rr.Body.String())
			}
			if got := builds.Load(); got != 0 {
				t.Fatalf("snapshot builds = %d, want 0 behind a rejected caller", got)
			}
			if strings.Contains(rr.Body.String(), "revision") {
				t.Fatalf("rejection body carried state: %q", rr.Body.String())
			}
		})
	}
}

// TestMCPEventStreamRejectsBadOriginBeforeBuildingState pins that the event
// path inherits the same Origin rule the tool path has, decided before any
// state is read.
func TestMCPEventStreamRejectsBadOriginBeforeBuildingState(t *testing.T) {
	var builds atomic.Int64
	cp := &MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
		EventsSnapshot: func(context.Context) (any, error) {
			builds.Add(1)
			return mcpEventsTestState{Revision: 1}, nil
		},
	}
	handler := newMCPControlPlaneHandler(cp, mcpEventsAllowedClient(t), newEventHub())

	for _, origin := range []string{"https://evil.example.com", "http://tslink-mcp.example.ts.net", "null"} {
		t.Run(origin, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://tslink-mcp.example.ts.net"+MCPEventsPath, nil)
			req.Host = "tslink-mcp.example.ts.net"
			req.RemoteAddr = "100.64.0.9:1234"
			req.Header.Set("Origin", origin)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
		})
	}
	if got := builds.Load(); got != 0 {
		t.Fatalf("snapshot builds = %d, want 0 behind a rejected Origin", got)
	}
}

// TestMCPEventStreamIsNotMountedWithoutASnapshotBuilder is the default-off
// proof. A control plane built without EventsSnapshot answers the path from the
// mux's own 404, so the stream cannot be reached by a deployment that did not
// ask for it.
//
// The authorized 200 in the second half is the control: it shows the 404 comes
// from the missing mount and not from the request shape being wrong.
func TestMCPEventStreamIsNotMountedWithoutASnapshotBuilder(t *testing.T) {
	newRequest := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "https://tslink-mcp.example.ts.net"+MCPEventsPath, nil)
		req.Host = "tslink-mcp.example.ts.net"
		req.RemoteAddr = "100.64.0.9:1234"
		return req
	}

	without := newMCPControlPlaneHandler(&MCPControlPlane{
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	}, mcpEventsAllowedClient(t), newEventHub())
	rr := httptest.NewRecorder()
	without.ServeHTTP(rr, newRequest())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status without a snapshot builder = %d, want 404", rr.Code)
	}

	hub := newEventHub()
	with := newMCPControlPlaneHandler(&MCPControlPlane{
		AllowedUsers:   []string{"alice@example.com"},
		Handler:        &mcpProbeHandler{},
		EventsSnapshot: func(context.Context) (any, error) { return mcpEventsTestState{Revision: 1}, nil },
	}, mcpEventsAllowedClient(t), hub)

	ctx, cancel := context.WithCancel(context.Background())
	streamed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rr := httptest.NewRecorder()
		with.ServeHTTP(rr, newRequest().WithContext(ctx))
		streamed <- rr
	}()
	waitForSubscribers(t, hub, 1)
	cancel()
	mounted := <-streamed
	if mounted.Code != http.StatusOK {
		t.Fatalf("status with a snapshot builder = %d, want 200", mounted.Code)
	}
	if !strings.Contains(mounted.Body.String(), "event: "+MCPEventSnapshot) {
		t.Fatalf("mounted stream body = %q, want a snapshot frame", mounted.Body.String())
	}
}

// TestMCPEventStreamRejectsNonGET keeps the stream from answering a method it
// does not implement.
func TestMCPEventStreamRejectsNonGET(t *testing.T) {
	handler := newMCPControlPlaneHandler(&MCPControlPlane{
		AllowedUsers:   []string{"alice@example.com"},
		Handler:        &mcpProbeHandler{},
		EventsSnapshot: func(context.Context) (any, error) { return mcpEventsTestState{Revision: 1}, nil },
	}, mcpEventsAllowedClient(t), newEventHub())

	req := httptest.NewRequest(http.MethodPost, "https://tslink-mcp.example.ts.net"+MCPEventsPath, strings.NewReader("{}"))
	req.Host = "tslink-mcp.example.ts.net"
	req.RemoteAddr = "100.64.0.9:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", got)
	}
}

// TestMCPEventStreamEmitsKeepaliveFrames drives serveMCPEventStream directly so
// the heartbeat can be observed in milliseconds instead of at the clamped
// minimum production period. The clamp itself is asserted separately in
// TestMCPEventsKeepaliveClamp; together they cover "the period is bounded" and
// "the heartbeat actually fires".
func TestMCPEventStreamEmitsKeepaliveFrames(t *testing.T) {
	hub := newEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, MCPEventsPath, nil).WithContext(ctx)
	// The handler writes from its own goroutine while this test polls for the
	// heartbeat, so the recorder has to be safe for concurrent use;
	// httptest.ResponseRecorder is not.
	rr := newSyncResponseRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveMCPEventStream(rr, req, newEventStateCache(func(context.Context) (any, error) {
			return mcpEventsTestState{Revision: 1}, nil
		}), hub, 5*time.Millisecond)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(rr.String(), "event: "+MCPEventKeepalive) >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-done

	body := rr.String()
	if strings.Count(body, "event: "+MCPEventKeepalive) < 2 {
		t.Fatalf("keepalive frames = %d, want at least 2 (body=%q)", strings.Count(body, "event: "+MCPEventKeepalive), body)
	}
	if !strings.Contains(body, `"type":"`+MCPEventKeepalive+`"`) {
		t.Fatalf("keepalive frame does not name its own type: %q", body)
	}
	// A heartbeat must not smuggle state; that is what makes it cheap enough to
	// send on a fixed period.
	keepaliveFrame := body[strings.Index(body, "event: "+MCPEventKeepalive):]
	if strings.Contains(keepaliveFrame[:strings.Index(keepaliveFrame, "\n\n")], `"state"`) {
		t.Fatalf("keepalive frame carries state: %q", keepaliveFrame)
	}
}

func TestMCPEventsKeepaliveClamp(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, DefaultMCPEventsKeepalive},
		{-time.Second, DefaultMCPEventsKeepalive},
		{time.Millisecond, MinMCPEventsKeepalive},
		{20 * time.Second, 20 * time.Second},
		{time.Hour, MaxMCPEventsKeepalive},
	}
	for _, tc := range cases {
		if got := mcpEventsKeepalive(tc.in); got != tc.want {
			t.Fatalf("mcpEventsKeepalive(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestMCPEventStreamReleasesItsSubscriberOnDisconnect is the goroutine-leak
// guard: when the client goes away the handler returns and the hub forgets it,
// so a reconnect loop cannot accumulate subscribers.
func TestMCPEventStreamReleasesItsSubscriberOnDisconnect(t *testing.T) {
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return mcpEventsTestState{Revision: 1}, nil
	})

	for i := 0; i < 3; i++ {
		reader, closeStream := mcpEventsOpenStream(t, srv)
		readMCPEventFrame(t, reader)
		waitForSubscribers(t, hub, 1)
		closeStream()
		waitForSubscribers(t, hub, 0)
	}
}

// TestMCPEventPublishDoesNotBlockOnAStalledSubscriber is the slow-client
// requirement at the point it matters: publish runs on the daemon's sync path,
// under Server.mu, so a client that stopped reading must not be able to reach
// it. A stalled subscriber's notifications coalesce instead of queueing.
func TestMCPEventPublishDoesNotBlockOnAStalledSubscriber(t *testing.T) {
	hub := newEventHub()
	stalled, releaseStalled := hub.subscribe()
	defer releaseStalled()
	healthy, releaseHealthy := hub.subscribe()
	defer releaseHealthy()

	start := time.Now()
	for i := 0; i < 1000; i++ {
		hub.publish()
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("1000 publishes with a stalled subscriber took %s, want a non-blocking fan-out", elapsed)
	}

	// The stalled subscriber holds exactly one pending notification, not 1000.
	if len(stalled) != 1 {
		t.Fatalf("stalled subscriber queue = %d, want 1 coalesced notification", len(stalled))
	}
	select {
	case <-healthy:
	default:
		t.Fatal("healthy subscriber received nothing")
	}
}

// TestMCPEventStreamRefusesBeyondTheConcurrentLimit proves the bound exists and
// that it is the bound, by opening exactly the limit and then one more.
func TestMCPEventStreamRefusesBeyondTheConcurrentLimit(t *testing.T) {
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return mcpEventsTestState{Revision: 1}, nil
	})

	closers := make([]func(), 0, mcpEventsMaxStreams)
	defer func() {
		for _, closeStream := range closers {
			closeStream()
		}
	}()
	for i := 0; i < mcpEventsMaxStreams; i++ {
		reader, closeStream := mcpEventsOpenStream(t, srv)
		closers = append(closers, closeStream)
		readMCPEventFrame(t, reader)
	}
	waitForSubscribers(t, hub, mcpEventsMaxStreams)

	resp, err := srv.Client().Get(srv.URL + MCPEventsPath)
	if err != nil {
		t.Fatalf("open the stream past the limit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status past the limit = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("503 past the limit carries no Retry-After")
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "too many concurrent event streams") {
		t.Fatalf("503 body = %q, want a reason the caller can act on", body)
	}
}

// TestMCPEventStreamReportsASnapshotFailure keeps a broken state read from
// looking like a quiet daemon.
func TestMCPEventStreamReportsASnapshotFailure(t *testing.T) {
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return nil, fmt.Errorf("registry unreadable")
	})
	reader, closeStream := mcpEventsOpenStream(t, srv)
	defer closeStream()

	payload := decodeMCPEventPayload(t, readMCPEventFrame(t, reader))
	if payload.StateError != "registry unreadable" {
		t.Fatalf("payload = %+v, want state_error naming the failure", payload)
	}
	if payload.State != nil {
		t.Fatalf("payload carries state alongside a failure: %+v", payload)
	}
}

// TestRuntimeSnapshotWriteNotifiesEventSubscribers is the event-source wiring.
//
// The stream is fed by the one function every registry change, lifecycle tick
// and service failure already ends in, which is why no goroutine polls a file.
// Withdrawing the snapshot notifies too: a failed sync leaves services a client
// was told were running with no runtime backing, and that is a state change.
func TestRuntimeSnapshotWriteNotifiesEventSubscribers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")

	oldPathFn := runtimeSnapshotPathFn
	runtimeSnapshotPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { runtimeSnapshotPathFn = oldPathFn })

	srv := &Server{
		nodes:           map[string]*ServiceNode{},
		serviceFailures: map[string]runtimesnapshot.ServiceState{},
		daemonPID:       4242,
		daemonStartedAt: time.Now().UTC(),
		events:          newEventHub(),
	}
	changed, release := srv.events.subscribe()
	defer release()

	srv.mu.Lock()
	srv.writeRuntimeSnapshotLocked("fingerprint", true)
	srv.mu.Unlock()
	select {
	case <-changed:
	default:
		t.Fatal("writing the runtime snapshot did not notify subscribers")
	}

	srv.removeRuntimeSnapshot()
	select {
	case <-changed:
	default:
		t.Fatal("removing the runtime snapshot did not notify subscribers")
	}
}

// TestEventHubPublishIsSafeOnAZeroValueServer covers the Server literals other
// tests in this package construct, where the hub was never built.
func TestEventHubPublishIsSafeOnAZeroValueServer(t *testing.T) {
	var hub *eventHub
	hub.publish()
}

// TestAccessLogResponseWriterCarriesWriteDeadlineThrough is the mutation-proof
// for the Unwrap added to responseWriter.
//
// Every control-plane response passes through AccessLogMiddleware. Without
// Unwrap, http.ResponseController stops at that wrapper and answers
// SetWriteDeadline with ErrNotSupported, so the event stream would silently run
// with no write deadline in production while still having one in any test that
// bypassed the middleware. The second half is the control: the same probe
// against a wrapper that does not unwrap does report ErrNotSupported, which is
// what makes the first half a real assertion rather than a tautology.
func TestAccessLogResponseWriterCarriesWriteDeadlineThrough(t *testing.T) {
	base := &deadlineRecordingResponseWriter{ResponseWriter: httptest.NewRecorder()}
	wrapped := &responseWriter{ResponseWriter: base}
	deadline := time.Now().Add(time.Minute)
	if err := http.NewResponseController(wrapped).SetWriteDeadline(deadline); err != nil {
		t.Fatalf("SetWriteDeadline through responseWriter = %v, want it to reach the connection", err)
	}
	if !base.writeDeadline.Equal(deadline) {
		t.Fatalf("recorded deadline = %v, want %v", base.writeDeadline, deadline)
	}

	opaque := &opaqueResponseWriter{ResponseWriter: base}
	if err := http.NewResponseController(opaque).SetWriteDeadline(deadline); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("control probe error = %v, want ErrNotSupported; the positive case above would then be vacuous", err)
	}
}

// deadlineRecordingResponseWriter stands in for the real connection-backed
// writer, which is the only thing that implements SetWriteDeadline.
type deadlineRecordingResponseWriter struct {
	http.ResponseWriter
	writeDeadline time.Time
}

func (w *deadlineRecordingResponseWriter) SetWriteDeadline(t time.Time) error {
	w.writeDeadline = t
	return nil
}

// opaqueResponseWriter is the control: a wrapper with no Unwrap, which is what
// responseWriter was before this change.
type opaqueResponseWriter struct {
	http.ResponseWriter
}

// TestMCPEventStreamSetsAWriteDeadlinePerFrame proves the bound is applied on
// the path that writes frames, not merely available.
func TestMCPEventStreamSetsAWriteDeadlinePerFrame(t *testing.T) {
	base := &deadlineRecordingResponseWriter{ResponseWriter: httptest.NewRecorder()}
	writer := &responseWriter{ResponseWriter: base}
	hub := newEventHub()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, MCPEventsPath, nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveMCPEventStream(writer, req, newEventStateCache(func(context.Context) (any, error) {
			return mcpEventsTestState{Revision: 1}, nil
		}), hub, time.Hour)
	}()
	waitForSubscribers(t, hub, 1)
	cancel()
	<-done

	if base.writeDeadline.IsZero() {
		t.Fatal("no write deadline was set while writing the snapshot frame")
	}
	if got := time.Until(base.writeDeadline); got > mcpEventsWriteTimeout+time.Minute {
		t.Fatalf("write deadline is %s out, want about %s", got, mcpEventsWriteTimeout)
	}
}

// syncResponseRecorder is a ResponseWriter a test can read while the handler
// under test is still writing.
type syncResponseRecorder struct {
	mu     sync.Mutex
	body   strings.Builder
	header http.Header
	status int
}

func newSyncResponseRecorder() *syncResponseRecorder {
	return &syncResponseRecorder{header: make(http.Header)}
}

func (r *syncResponseRecorder) Header() http.Header { return r.header }

func (r *syncResponseRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}

func (r *syncResponseRecorder) WriteHeader(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *syncResponseRecorder) Flush() {}

func (r *syncResponseRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

// setEventStateMaxAge pins the cache's staleness bound for one test. The
// default is short enough that an unlucky scheduler could expire an entry in
// the middle of a test that is about to assert it was reused.
func setEventStateMaxAge(t *testing.T, d time.Duration) {
	t.Helper()
	old := mcpEventStateMaxAge
	mcpEventStateMaxAge = d
	t.Cleanup(func() { mcpEventStateMaxAge = old })
}

// TestMCPEventStateIsBuiltOncePerGeneration is the single-flight proof.
//
// Concurrency here is the point, not incidental: the leader is held inside the
// builder until every follower has arrived, so a cache that merely deduplicated
// completed builds would still fail this. Each phase also asserts a build does
// happen for a new generation, which is the control — a cache that answered
// everything from one build forever would satisfy "built once" trivially.
func TestMCPEventStateIsBuiltOncePerGeneration(t *testing.T) {
	const followers = 7

	var builds atomic.Int64
	entered := make(chan struct{}, followers+1)
	release := make(chan struct{})
	cache := &eventStateCache{
		maxAge: time.Minute,
		build: func(context.Context) (any, error) {
			builds.Add(1)
			entered <- struct{}{}
			<-release
			return mcpEventsTestState{Revision: int(builds.Load())}, nil
		},
	}

	// Phase 1: one leader for generation 4, then everyone else joins it.
	results := make(chan any, followers+1)
	go func() {
		state, err := cache.get(context.Background(), 4)
		if err != nil {
			t.Error(err)
		}
		results <- state
	}()
	<-entered // the leader is inside the builder, so c.entry is published

	for i := 0; i < followers; i++ {
		go func() {
			state, err := cache.get(context.Background(), 4)
			if err != nil {
				t.Error(err)
			}
			results <- state
		}()
	}
	close(release)

	first := <-results
	for i := 0; i < followers; i++ {
		if got := <-results; got != first {
			t.Fatalf("follower received %+v, want the leader's %+v", got, first)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("builds for one generation = %d, want 1", got)
	}

	// Phase 2 (control): a later generation must actually rebuild, or the
	// assertion above would be satisfied by a cache that never expires.
	release = make(chan struct{})
	close(release)
	if _, err := cache.get(context.Background(), 5); err != nil {
		t.Fatalf("get(generation 5) error = %v", err)
	}
	<-entered
	if got := builds.Load(); got != 2 {
		t.Fatalf("builds after a new generation = %d, want 2", got)
	}
}

// TestMCPEventStateCacheSharesBuildFailures keeps a failing build from
// degenerating into the storm it was added to prevent. A daemon that cannot
// read its own state is the worst moment to multiply the reads.
func TestMCPEventStateCacheSharesBuildFailures(t *testing.T) {
	var builds atomic.Int64
	wantErr := errors.New("registry unreadable")
	cache := &eventStateCache{
		maxAge: time.Minute,
		build: func(context.Context) (any, error) {
			builds.Add(1)
			return nil, wantErr
		},
	}
	for i := 0; i < 5; i++ {
		state, err := cache.get(context.Background(), 1)
		if !errors.Is(err, wantErr) {
			t.Fatalf("get() error = %v, want %v", err, wantErr)
		}
		if state != nil {
			t.Fatalf("failed build returned state %+v", state)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("builds for one failing generation = %d, want 1", got)
	}
}

// TestMCPEventStateCacheRebuildsAfterMaxAge is the staleness bound. The cache
// keys on the publish sites being complete; this is what keeps a gap in them
// from freezing a stream's state indefinitely.
func TestMCPEventStateCacheRebuildsAfterMaxAge(t *testing.T) {
	now := time.Now()
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return now }
	t.Cleanup(func() { serverNowFn = oldNow })

	var builds atomic.Int64
	cache := &eventStateCache{
		maxAge: time.Second,
		build: func(context.Context) (any, error) {
			builds.Add(1)
			return mcpEventsTestState{Revision: int(builds.Load())}, nil
		},
	}
	if _, err := cache.get(context.Background(), 3); err != nil {
		t.Fatalf("first get error = %v", err)
	}
	if _, err := cache.get(context.Background(), 3); err != nil {
		t.Fatalf("second get error = %v", err)
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("builds inside the freshness window = %d, want 1", got)
	}

	now = now.Add(2 * time.Second)
	if _, err := cache.get(context.Background(), 3); err != nil {
		t.Fatalf("third get error = %v", err)
	}
	if got := builds.Load(); got != 2 {
		t.Fatalf("builds after the freshness window = %d, want 2", got)
	}
}

// TestMCPEventStreamsShareOneBuildPerChange is the same property at the level
// the cost is actually paid: real streams over a real handler, all woken by one
// publish, must produce one pass over the daemon's files rather than one each.
func TestMCPEventStreamsShareOneBuildPerChange(t *testing.T) {
	const streams = 3
	setEventStateMaxAge(t, time.Minute)

	var builds atomic.Int64
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return mcpEventsTestState{Revision: int(builds.Add(1))}, nil
	})

	readers := make([]*bufio.Reader, 0, streams)
	for i := 0; i < streams; i++ {
		reader, closeStream := mcpEventsOpenStream(t, srv)
		defer closeStream()
		if frame := readMCPEventFrame(t, reader); frame.Event != MCPEventSnapshot {
			t.Fatalf("stream %d opened with %q, want a snapshot", i, frame.Event)
		}
		readers = append(readers, reader)
	}
	waitForSubscribers(t, hub, streams)
	afterOpen := builds.Load()
	if afterOpen != 1 {
		t.Fatalf("builds for %d opening streams = %d, want 1", streams, afterOpen)
	}

	hub.publish()
	for i, reader := range readers {
		frame := readMCPEventFrame(t, reader)
		if frame.Event != MCPEventUpdate {
			t.Fatalf("stream %d received %q, want an update", i, frame.Event)
		}
	}
	if got := builds.Load(); got != afterOpen+1 {
		t.Fatalf("builds after one change across %d streams = %d, want %d", streams, got, afterOpen+1)
	}
}

// TestMCPEventIDIsInstanceWideAndInstanceIsStable pins the id contract that
// replaced the per-connection counter.
//
// Two sequential connections: the per-connection Sequence restarts at 1 —
// that is what it is for — while the SSE id and its payload mirror keep
// advancing, and the instance identifier does not move. A client can therefore
// read "id went backwards" as loss and "instance changed" as a restart, which
// is exactly what a per-connection id made impossible.
func TestMCPEventIDIsInstanceWideAndInstanceIsStable(t *testing.T) {
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		return mcpEventsTestState{Revision: 1}, nil
	})

	open := func() MCPEventPayload {
		t.Helper()
		reader, closeStream := mcpEventsOpenStream(t, srv)
		defer closeStream()
		frame := readMCPEventFrame(t, reader)
		payload := decodeMCPEventPayload(t, frame)
		if payload.Sequence != 1 {
			t.Fatalf("first frame sequence = %d, want 1 on every connection", payload.Sequence)
		}
		if payload.Instance == "" {
			t.Fatal("payload carries no instance identifier")
		}
		if frame.ID != strconv.FormatUint(payload.EventID, 10) {
			t.Fatalf("SSE id %q does not match payload event_id %d", frame.ID, payload.EventID)
		}
		return payload
	}

	firstConn := open()
	secondConn := open()

	if secondConn.EventID <= firstConn.EventID {
		t.Fatalf("event_id across connections went %d -> %d, want it to keep advancing", firstConn.EventID, secondConn.EventID)
	}
	if secondConn.Instance != firstConn.Instance {
		t.Fatalf("instance changed within one process: %q -> %q", firstConn.Instance, secondConn.Instance)
	}
	if firstConn.Instance != mcpEventInstanceID {
		t.Fatalf("payload instance %q is not the process identifier %q", firstConn.Instance, mcpEventInstanceID)
	}
}

// TestMCPEventStateCacheReleasesWaitersWhenABuildPanics covers the failure mode
// that sharing a build introduced.
//
// Before the cache, a panicking builder ended exactly one stream: every stream
// built its own state. Now the waiters are parked on one channel and a newly
// arriving stream joins them instead of starting its own build, so a panic that
// did not release them would leave the whole path hung — and hung streams show
// nothing at all, which is indistinguishable from a quiet daemon.
//
// The control is the last phase: once the entry ages out, a build that no
// longer panics must succeed, so this is not asserting a permanently poisoned
// cache.
func TestMCPEventStateCacheReleasesWaitersWhenABuildPanics(t *testing.T) {
	now := time.Now()
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return now }
	t.Cleanup(func() { serverNowFn = oldNow })

	var explode atomic.Bool
	explode.Store(true)
	cache := &eventStateCache{
		maxAge: time.Second,
		build: func(context.Context) (any, error) {
			if explode.Load() {
				panic("builder exploded")
			}
			return mcpEventsTestState{Revision: 9}, nil
		},
	}

	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = cache.get(context.Background(), 1)
	}()
	if r := <-panicked; r == nil {
		t.Fatal("the leader did not panic, so nothing below is about the panic path")
	}

	// A follower arriving afterwards must be answered, not parked forever.
	done := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), 1)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a waiter behind a panicking build got no error")
		}
		if !strings.Contains(err.Error(), "panicked") {
			t.Fatalf("waiter error = %v, want it to name the panic", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a waiter behind a panicking build never returned")
	}

	// Control: the cache recovers once the entry ages out.
	explode.Store(false)
	now = now.Add(2 * time.Second)
	state, err := cache.get(context.Background(), 1)
	if err != nil {
		t.Fatalf("get() after the panicking entry aged out = %v, want a fresh build", err)
	}
	if state != (mcpEventsTestState{Revision: 9}) {
		t.Fatalf("recovered state = %+v, want the new build's", state)
	}
}

// TestMCPEventStreamSurvivesAPanickingBuilderEndToEnd re-checks, under the
// shared-build architecture, the property an earlier review established under
// the per-stream one: a snapshot builder that panics must not leak the stream
// slot or the hub subscription, and a later stream must still be served.
func TestMCPEventStreamSurvivesAPanickingBuilderEndToEnd(t *testing.T) {
	setEventStateMaxAge(t, time.Millisecond)

	var explode atomic.Bool
	explode.Store(true)
	hub := newEventHub()
	srv := mcpEventsTestServer(t, hub, func(context.Context) (any, error) {
		if explode.Load() {
			panic("builder exploded")
		}
		return mcpEventsTestState{Revision: 7}, nil
	})

	resp, err := srv.Client().Get(srv.URL + MCPEventsPath)
	if err == nil {
		resp.Body.Close()
	}
	waitForSubscribers(t, hub, 0)

	// The slot and the subscription are both back, so a healthy stream opens.
	explode.Store(false)
	time.Sleep(5 * time.Millisecond) // let the poisoned entry age out
	reader, closeStream := mcpEventsOpenStream(t, srv)
	defer closeStream()
	frame := readMCPEventFrame(t, reader)
	if frame.Event != MCPEventSnapshot || !strings.Contains(frame.Data, `"revision":7`) {
		t.Fatalf("stream after a panicking build = %+v, want a fresh snapshot", frame)
	}
}
