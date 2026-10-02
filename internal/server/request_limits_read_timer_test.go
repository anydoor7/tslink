package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// HTTP/2 applies future read deadlines and their cancellation on its server
// loop. Hold cancellation behind backend work to model a delayed server loop.
type queuedReadDeadlineWriter struct {
	*httptest.ResponseRecorder
	armed, expired bool
}

func (w *queuedReadDeadlineWriter) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		w.armed = true
	}
	return nil
}

type queuedReadDeadlineBody struct {
	w     *queuedReadDeadlineWriter
	reads int
}

func (b *queuedReadDeadlineBody) Read(p []byte) (int, error) {
	if b.w.expired {
		return 0, context.DeadlineExceeded
	}
	b.reads++
	if b.reads == 1 {
		p[0] = 'x'
		return 1, nil
	}
	return 0, io.EOF
}

func (*queuedReadDeadlineBody) Close() error { return nil }

func TestHTTP2UploadDeadlineEndsWithRead(t *testing.T) {
	w := &queuedReadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	body := &progressBody{
		ReadCloser: &queuedReadDeadlineBody{w: w}, ctl: http.NewResponseController(w),
		idle: time.Hour, state: &requestBudgetState{}, http2: true,
		reject: func(*requestLimitError) {},
	}
	if n, err := body.Read(make([]byte, 1)); n != 1 || err != nil {
		t.Fatalf("first read = %d, %v", n, err)
	}
	// While the backend handles the byte, apply any uncancelled server-loop
	// deadline. There is no upload Read in progress during this interval.
	w.expired = w.armed
	if n, err := body.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("completed upload after backend work = %d, %v", n, err)
	}
	if body.state.failure != nil {
		t.Fatalf("backend work classified as an upload failure: %v", body.state.failure)
	}
}

func TestHTTP2UploadTimerDoesNotInterruptAnotherReadOrDisposal(t *testing.T) {
	for _, closing := range []bool{false, true} {
		t.Run(map[bool]string{false: "another_read", true: "disposal"}[closing], func(t *testing.T) {
			w := &uploadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			prior, current := make(chan struct{}), make(chan struct{})
			state := &requestBudgetState{readDone: current, closing: closing}
			body := &progressBody{ctl: http.NewResponseController(w), state: state}
			if closing {
				prior = current
			}
			body.expireRead(prior)
			if got := w.snapshot(); len(got) != 0 {
				t.Fatalf("completed upload timer interrupted a later phase: %v", got)
			}
		})
	}
}

func TestHTTP2UploadTimerInterruptsActiveRead(t *testing.T) {
	w := &uploadDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	body := &progressBody{ctl: http.NewResponseController(w), state: &requestBudgetState{readDone: done}}
	before := time.Now()
	body.expireRead(done)
	if got := w.snapshot(); len(got) != 1 || got[0].IsZero() || got[0].After(time.Now()) || got[0].Before(before.Add(-time.Second)) {
		t.Fatalf("active upload must receive an immediate read deadline: %v", got)
	}
}
