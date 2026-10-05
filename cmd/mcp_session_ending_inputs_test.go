package cmd

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

// TestMCPHelpDocumentsSessionEndingInputs keeps `tslink mcp --help` honest
// about the inputs that end a session. Each documented claim is checked
// against the server, so the help cannot drift into describing behavior the
// server does not have.
func TestMCPHelpDocumentsSessionEndingInputs(t *testing.T) {
	mcpCmd, _, err := rootCmd.Find([]string{"mcp"})
	if err != nil {
		t.Fatal(err)
	}
	help := strings.Join(strings.Fields(mcpCmd.Long), " ")
	for _, want := range []string{
		"malformed JSON",
		"a JSON value that is not a JSON-RPC message",
		"a JSON-RPC batch",
		"a record longer than 1048576 bytes",
		"end the session and drop the answers of calls still in flight",
		"reuses the id of a call still in flight gets no answer",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("tslink mcp --help does not say %q:\n%s", want, mcpCmd.Long)
		}
	}

	slow := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`
	after := `{"jsonrpc":"2.0","id":10,"method":"ping"}`
	for name, bad := range map[string]string{
		"malformed JSON":  "this is not json",
		"not JSON-RPC":    `{"hello":1}`,
		"batch":           `[{"jsonrpc":"2.0","id":3,"method":"ping"}]`,
		"oversize record": `{"jsonrpc":"2.0","id":3,"method":"ping","params":{"note":"` + strings.Repeat("a", mcpMaxRecordBytes) + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			// The deadline is only the session's hang guard; a session that
			// ended by it still fails the ctx.Err() check below.
			ctx, cancel := context.WithTimeout(context.Background(), testwait.Budget(t))
			defer cancel()
			started, stopped := make(chan struct{}), make(chan struct{})
			watching, release := make(chan struct{}), make(chan struct{})
			actions := fakeMCPActions()
			actions.status = func(ctx context.Context) (any, error) {
				close(started)
				<-release
				close(stopped)
				return mcpStatusSummary{}, nil
			}
			// Observe SDK cancellation through a context-aware call, then let
			// status return its normal answer. Status itself ignores cancellation,
			// so the test still checks that its answer is dropped by the transport.
			actions.url = func(ctx context.Context, _ string, _ time.Duration) (any, error) {
				close(watching)
				<-ctx.Done()
				close(release)
				return nil, ctx.Err()
			}
			in, input := io.Pipe()
			defer in.Close()
			defer input.Close()
			var stdout strings.Builder
			done := make(chan error, 1)
			go func() {
				err := runMCPStdio(ctx, in, &stdout, actions)
				_ = in.CloseWithError(err)
				done <- err
			}()
			observer := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"url","arguments":{"name":"web"}}}`
			if _, err := io.WriteString(input, initializedMCPInput(slow+"\n"+observer)); err != nil {
				t.Fatalf("write active call: %v", err)
			}
			// Establish an actual in-flight call before sending the bad input.
			// It stays in flight until session cancellation, however long the
			// race detector takes to scan an oversize record under load.
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("call did not start before the session-ending input")
			}
			select {
			case <-watching:
			case <-ctx.Done():
				t.Fatal("cancellation observer did not start")
			}
			// Refusing the input can close the pipe before this write finishes.
			_, _ = io.WriteString(input, bad+"\n"+after+"\n")
			err := <-done
			if err == nil || ctx.Err() != nil {
				t.Fatalf("session error = %v, want the input to end the session", err)
			}
			select {
			case <-stopped:
			default:
				t.Fatal("session-ending input did not release the active call")
			}
			for _, frame := range decodeMCPResponses(t, stdout.String()) {
				if frame["id"] == float64(2) || frame["id"] == float64(9) || frame["id"] == float64(10) {
					t.Fatalf("answer survived a session-ending input: %+v", frame)
				}
			}
		})
	}

	t.Run("duplicate in-flight id", func(t *testing.T) {
		release := make(chan struct{})
		var calls atomic.Int32
		actions := fakeMCPActions()
		actions.status = func(ctx context.Context) (any, error) {
			calls.Add(1)
			<-release
			return mcpStatusSummary{}, nil
		}
		// The SDK accepts messages in order. A later url call releases the
		// first call only after the duplicate was checked while still in flight.
		actions.url = func(context.Context, string, time.Duration) (any, error) {
			close(release)
			return URLResult{}, nil
		}
		barrier := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"url","arguments":{"name":"web"}}}`
		stdout := runMCPSession(t, initializedMCPInput(slow+"\n"+slow+"\n"+barrier), actions)
		if got := calls.Load(); got != 1 {
			t.Fatalf("duplicate in-flight id dispatched %d calls, want 1", got)
		}
		answers := 0
		for _, frame := range decodeMCPResponses(t, stdout) {
			if frame["id"] == float64(2) {
				answers++
			}
		}
		if answers != 1 {
			t.Fatalf("id 2 sent twice while in flight got %d answers, want 1: %s", answers, stdout)
		}
	})
}
