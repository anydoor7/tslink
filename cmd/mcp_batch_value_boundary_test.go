package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

// mcpDecoderSeparators is every byte encoding/json's Decoder accepts between
// two top-level values. The SDK decodes the stream with that Decoder rather
// than framing on newlines, so any of them can start a second value, and a
// batch, on the same line.
var mcpDecoderSeparators = []struct {
	name string
	sep  string
}{
	{"space", " "},
	{"tab", "\t"},
	{"CR", "\r"},
	{"LF", "\n"},
}

// readMCPGuarded drains a record limit reader the way the decoder does and
// returns what it handed over together with the first error.
func readMCPGuarded(input string) (string, error) {
	reader := newMCPRecordLimitReader(strings.NewReader(input), mcpMaxRecordBytes)
	var handed strings.Builder
	buffer := make([]byte, 7)
	for {
		n, err := reader.Read(buffer)
		handed.Write(buffer[:n])
		if err != nil {
			return handed.String(), err
		}
	}
}

func TestMCPRecordGuardRefusesABatchAfterAnyValueSeparator(t *testing.T) {
	const first = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	const batch = `[{"jsonrpc":"2.0","id":2,"method":"ping"}]`
	for _, tc := range mcpDecoderSeparators {
		t.Run(tc.name, func(t *testing.T) {
			handed, err := readMCPGuarded(first + tc.sep + batch + "\n")
			if !errors.Is(err, errMCPBatchUnsupported) {
				t.Fatalf("read error = %v, want the batch refusal", err)
			}
			if handed != first+tc.sep {
				t.Fatalf("guard handed over %q, want only the bytes before the batch", handed)
			}
		})
		// Controls: a '[' that does not begin a top-level value is not a batch,
		// wherever the separator puts it.
		for _, control := range []string{
			`{"params":` + tc.sep + `[1,2]}`,
			`{"note":"a [b ]["}` + tc.sep + `{"note":"\"[\\"}`,
			first + tc.sep + `{"jsonrpc":"2.0","id":2,"method":"ping","params":{"a":[{"b":"]["}]}}`,
		} {
			t.Run(tc.name+" control", func(t *testing.T) {
				input := control + "\n"
				handed, err := readMCPGuarded(input)
				if err != io.EOF || handed != input {
					t.Fatalf("guard refused %q: handed %q, err %v", input, handed, err)
				}
			})
		}
	}
}

// TestMCPSessionRefusesABatchAfterAnyValueSeparator drives the whole stdio
// server at every revision that removed batching. The pinned SDK's own batch
// check never runs here (mcpDrainConnection hides IOTransport's version hook),
// so the record guard is the only thing standing between these inputs and a
// batch answer. A space or tab right after a value may instead trip the SDK's
// trailing-data check first; either way the batch must not run. Whether the
// request before the batch is answered is not asserted: a refused stream ends
// the session, which drops in-flight answers.
func TestMCPSessionRefusesABatchAfterAnyValueSeparator(t *testing.T) {
	const batch = `[{"jsonrpc":"2.0","id":2,"method":"ping"},{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list","arguments":{}}}]`
	handshakes := map[string]string{
		"2025-06-18":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		"2025-11-25":       `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		mcpProtocolVersion: `{"jsonrpc":"2.0","id":1,"method":"ping","params":{` + mcpCurrentRevisionMeta + `}}`,
	}
	for version, handshake := range handshakes {
		for _, tc := range mcpDecoderSeparators {
			t.Run(version+"/"+tc.name, func(t *testing.T) {
				var listed atomic.Bool
				actions := fakeMCPActions()
				actions.list = func(ctx context.Context) (any, error) {
					listed.Store(true)
					return map[string]any{"services": []mcpServiceSummary{}}, nil
				}
				stdout, err := tryMCPSession(t, handshake+tc.sep+batch+"\n", actions)
				frames := decodeMCPResponses(t, stdout)
				for _, frame := range frames {
					if frame["id"] == float64(2) || frame["id"] == float64(3) {
						t.Fatalf("batch member answered: %+v", frame)
					}
				}
				if listed.Load() {
					t.Fatal("a tool inside a refused batch ran")
				}
				switch {
				case errors.Is(err, errMCPBatchUnsupported):
				case (tc.sep == " " || tc.sep == "\t") && err != nil && strings.Contains(err.Error(), "invalid trailing data"):
				default:
					t.Fatalf("session error = %v, want the batch refusal", err)
				}
			})
		}
	}

	// Control: the same line shape carrying a single request instead of a
	// batch is served, so the refusals above come from the batch, not from the
	// separator.
	for _, sep := range []string{"\r", "\n"} {
		stdout := runMCPSession(t, handshakes["2025-06-18"]+sep+`{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n", fakeMCPActions())
		if frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2)); mcpFrameError(frame) != nil {
			t.Fatalf("single request after %q = %+v", sep, frame)
		}
	}
}
