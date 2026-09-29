package cmd

import (
	"errors"
	"io"
	"sync/atomic"
	"testing"
)

// mcpEscapedStringNotifications are valid notifications whose string holds an
// escape. A backslash escapes exactly one byte, so the string still ends at
// its real closing quote. A guard that never cleared its escape state would
// read everything after it as string content, including a top-level batch on
// a later line.
var mcpEscapedStringNotifications = []struct{ name, notification string }{
	{"escaped backslash", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99,"reason":"x\\y"}}`},
	{"escaped quote", `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99,"reason":"x\"y"}}`},
}

func TestMCPRecordGuardRefusesABatchAfterAnEscapedString(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":9,"method":"ping"}]`
	for _, tc := range mcpEscapedStringNotifications {
		t.Run(tc.name, func(t *testing.T) {
			handed, err := readMCPGuarded(tc.notification + "\n" + batch + "\n")
			if !errors.Is(err, errMCPBatchUnsupported) {
				t.Fatalf("read error = %v, want the batch refusal", err)
			}
			if handed != tc.notification+"\n" {
				t.Fatalf("guard handed over %q, want only the notification", handed)
			}
		})
		// Control: the same notification followed by a single request is
		// handed over whole, so the refusal comes from the batch.
		t.Run(tc.name+" control", func(t *testing.T) {
			input := tc.notification + "\n" + `{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n"
			handed, err := readMCPGuarded(input)
			if err != io.EOF || handed != input {
				t.Fatalf("guard refused %q: handed %q, err %v", input, handed, err)
			}
		})
	}
}

// TestMCPSessionRefusesABatchAfterAnEscapedString is the same input through
// the whole stdio server, where the record guard is the only batch refusal
// (see TestMCPSessionRefusesABatchAfterAnyValueSeparator): no batch member
// may be answered or run.
func TestMCPSessionRefusesABatchAfterAnEscapedString(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":9,"method":"ping"},{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"list","arguments":{}}}]`
	handshakes := map[string]string{
		"2025-06-18":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		"2025-11-25":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		mcpProtocolVersion: `{"jsonrpc":"2.0","id":1,"method":"ping","params":{` + mcpCurrentRevisionMeta + `}}`,
	}
	for version, handshake := range handshakes {
		for _, tc := range mcpEscapedStringNotifications {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				var listed atomic.Bool
				actions := fakeMCPActions()
				actions.list = func() (any, error) {
					listed.Store(true)
					return map[string]any{"services": []mcpServiceSummary{}}, nil
				}
				stdout, err := tryMCPSession(t, handshake+"\n"+tc.notification+"\n"+batch+"\n", actions)
				for _, frame := range decodeMCPResponses(t, stdout) {
					if frame["id"] == float64(9) || frame["id"] == float64(10) {
						t.Fatalf("batch member answered: %+v", frame)
					}
				}
				if listed.Load() {
					t.Fatal("a tool inside a refused batch ran")
				}
				if !errors.Is(err, errMCPBatchUnsupported) {
					t.Fatalf("session error = %v, want the batch refusal", err)
				}
			})
			// Control: a single request in the batch's place is answered, so
			// the notification is accepted and does not end the session.
			t.Run(version+"/"+tc.name+" control", func(t *testing.T) {
				stdout := runMCPSession(t, handshake+"\n"+tc.notification+"\n"+`{"jsonrpc":"2.0","id":9,"method":"ping"}`+"\n", fakeMCPActions())
				if frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(9)); mcpFrameError(frame) != nil {
					t.Fatalf("single request after the notification = %+v", frame)
				}
			})
		}
	}
}
