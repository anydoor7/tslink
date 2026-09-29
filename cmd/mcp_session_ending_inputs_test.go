package cmd

import (
	"context"
	"strings"
	"testing"
	"time"
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
	actions := fakeMCPActions()
	actions.status = func() (any, error) {
		time.Sleep(300 * time.Millisecond)
		return mcpStatusSummary{}, nil
	}
	for name, bad := range map[string]string{
		"malformed JSON":  "this is not json",
		"not JSON-RPC":    `{"hello":1}`,
		"batch":           `[{"jsonrpc":"2.0","id":3,"method":"ping"}]`,
		"oversize record": `{"jsonrpc":"2.0","id":3,"method":"ping","params":{"note":"` + strings.Repeat("a", mcpMaxRecordBytes) + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var stdout strings.Builder
			err := runMCPStdio(ctx, strings.NewReader(initializedMCPInput(slow)+bad+"\n"+after+"\n"), &stdout, actions)
			if err == nil || ctx.Err() != nil {
				t.Fatalf("session error = %v, want the input to end the session", err)
			}
			for _, frame := range decodeMCPResponses(t, stdout.String()) {
				if frame["id"] == float64(2) || frame["id"] == float64(10) {
					t.Fatalf("answer survived a session-ending input: %+v", frame)
				}
			}
		})
	}

	t.Run("duplicate in-flight id", func(t *testing.T) {
		stdout := runMCPSession(t, initializedMCPInput(slow+"\n"+slow), actions)
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
