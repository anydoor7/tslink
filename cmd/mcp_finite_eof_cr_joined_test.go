package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestMCPFiniteEOFCRJoinedRecordsAnswerEveryCall pins the answer-loss half of
// the finite-stdin drain. Two messages share one newline-delimited record
// (joined by CR, which the decoder accepts between values), so counting
// records undercounts messages: a drain that waits for "one answer per
// record" can close the session while the second message's tool call has
// not started yet, and that call's answer is lost. The loss is a start-order
// race, so the case loops; the control sends the same calls on separate
// lines.
func TestMCPFiniteEOFCRJoinedRecordsAnswerEveryCall(t *testing.T) {
	head := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	const ping = `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	const slow = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{}}}`
	var joined, separate []string
	want := []float64{1}
	for id := 2; id <= 9; id++ {
		call := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, id)
		if id%3 == 0 {
			call = fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"status","arguments":{}}}`, id)
		}
		joined = append(joined, call)
		separate = append(separate, call)
		want = append(want, float64(id))
	}
	for _, tc := range []struct {
		name  string
		input string
		want  []float64
	}{
		// R3's probe shape.
		{"ping CR slow call", head + ping + "\r" + slow + "\n", []float64{1, 2, 3}},
		// More messages per record widen the window a record-counting drain
		// can close in, which makes the loss show up in far fewer sessions.
		{"eight calls CR-joined", head + strings.Join(joined, "\r") + "\n", want},
		{"control: separate lines", head + ping + "\n" + slow + "\n", []float64{1, 2, 3}},
		{"control: eight calls on separate lines", head + strings.Join(separate, "\n") + "\n", want},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions := fakeMCPActions()
			actions.status = func(ctx context.Context) (any, error) {
				time.Sleep(5 * time.Millisecond)
				return mcpStatusSummary{DaemonRunning: true, ServiceCount: 7}, nil
			}
			lost := 0
			var sessionErrors []error
			const attempts = 60
			for attempt := 0; attempt < attempts; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				var stdout bytes.Buffer
				err := runMCPStdio(ctx, strings.NewReader(tc.input), &stdout, actions)
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("attempt %d: finite input did not settle", attempt)
				}
				if err != nil {
					sessionErrors = append(sessionErrors, err)
				}
				frames := decodeMCPResponses(t, stdout.String())
				for _, id := range tc.want {
					answered := false
					for _, frame := range frames {
						if frame["id"] == id && frame["result"] != nil {
							answered = true
						}
					}
					if !answered {
						lost++
					}
				}
			}
			// A lost answer is the claim under test; the session error a closed
			// session reports for the refused write is its side effect, so it is
			// checked second.
			if lost > 0 {
				t.Fatalf("lost %d of %d answers over %d sessions (session errors: %v)", lost, len(tc.want)*attempts, attempts, sessionErrors)
			}
			if len(sessionErrors) > 0 {
				t.Fatalf("finite sessions ended with errors: %v", sessionErrors)
			}
		})
	}
}
