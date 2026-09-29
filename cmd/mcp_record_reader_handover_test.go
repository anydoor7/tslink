package cmd

import (
	"strings"
	"testing"
)

// The two tests below are the orchestrator's F-1 probe, kept verbatim as the
// regression test. encoding/json's Decoder scans every byte a Read returned
// before it looks at the error returned with them, so a guard that hands over
// the refused bytes together with its error still lets the decoder dispatch
// the batch it refused, or the record after an oversize one.

func TestOrcProbeBatchGuardHandsOverBytes(t *testing.T) {
	in := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n[{\"jsonrpc\":\"2.0\",\"id\":2}]\n{\"tail\":1}\n"
	r := newMCPRecordLimitReader(strings.NewReader(in), 1<<20)
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	idx := strings.Index(in, "[")
	t.Logf("n=%d len(in)=%d batchStart=%d err=%v handedOver=%q", n, len(in), idx, err, string(buf[:n]))
	if n > idx {
		t.Errorf("guard returned %d bytes, %d of them at or after the batch start", n, n-idx)
	}
}

func TestOrcProbeLimitGuardHandsOverBytes(t *testing.T) {
	in := strings.Repeat("x", 40) + "\n{\"next\":1}\n"
	r := newMCPRecordLimitReader(strings.NewReader(in), 16)
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	t.Logf("n=%d len(in)=%d err=%v", n, len(in), err)
	if n > 17 {
		t.Errorf("limit guard returned %d bytes, past the limit position 17 (includes following record)", n)
	}
}
