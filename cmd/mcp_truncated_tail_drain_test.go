package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestMCPTruncatedFinalRecordStillAnswersEarlierRequests covers a client that
// dies mid-write. The decoder reports the cut-off last record as
// io.ErrUnexpectedEOF rather than io.EOF; it is still the end of the input, so
// every request accepted before it keeps its answer, initialize included, and
// only then does the session end with the error.
func TestMCPTruncatedFinalRecordStillAnswersEarlierRequests(t *testing.T) {
	const truncated = `{"jsonrpc":"2.0","id":9,"method":"ping"`
	for _, tc := range []struct {
		name     string
		calls    string
		slow     time.Duration
		attempts int
		want     []float64
	}{
		// R3's probe shape: a slow call still running when the input ends.
		{"slow call in flight", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`, 300 * time.Millisecond, 1, []float64{1, 2}},
		{"fast calls", `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, 0, 30, []float64{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := fakeMCPActions()
			actions.status = func(ctx context.Context) (any, error) {
				time.Sleep(tc.slow)
				return mcpStatusSummary{DaemonRunning: true, ServiceCount: 7}, nil
			}
			input := initializedMCPInput(tc.calls) + truncated
			for attempt := 0; attempt < tc.attempts; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				var stdout bytes.Buffer
				err := runMCPStdio(ctx, strings.NewReader(input), &stdout, actions)
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("attempt %d: truncated input did not settle", attempt)
				}
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("attempt %d: session error = %v, want io.ErrUnexpectedEOF", attempt, err)
				}
				frames := decodeMCPResponses(t, stdout.String())
				for _, id := range tc.want {
					var answered map[string]any
					for _, frame := range frames {
						if frame["id"] == id {
							answered = frame
						}
					}
					if answered == nil || answered["result"] == nil {
						t.Fatalf("attempt %d: request %v accepted before the truncated record was not answered: %s", attempt, id, stdout.String())
					}
				}
				for _, frame := range frames {
					if frame["id"] == float64(9) {
						t.Fatalf("attempt %d: the truncated record was answered: %+v", attempt, frame)
					}
				}
			}
		})
	}
}
