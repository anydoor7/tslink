//go:build windows

package logrotate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTruncateLogRefusesAPathThatNoLongerNamesTheWritersFile pins the identity
// check in the Windows truncate. The append-only handle cannot truncate, so the
// truncate goes through a second handle opened by path, and the path is only a
// name: if it was replaced after RotateStderrLog compared it with the handle,
// the second handle is on someone else's file. That file must be left alone.
//
// The control is the same call with the writer's own identity, which must
// truncate, so a run where both refuse cannot be read as the check working.
func TestTruncateLogRefusesAPathThatNoLongerNamesTheWritersFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	other := filepath.Join(dir, "somebody-elses.log")
	handle := openSupervisedLog(t, target, bytes.Repeat([]byte("ours\n"), 100))
	if err := os.WriteFile(other, []byte("not ours\n"), 0o600); err != nil {
		t.Fatalf("seed %s: %v", other, err)
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		t.Fatalf("stat %s: %v", other, err)
	}

	before := digestOf(t, target)
	err = truncateLog(handle, target, otherInfo)
	if err == nil || !strings.Contains(err.Error(), "was replaced during rotation") {
		t.Fatalf("truncateLog with a foreign identity error = %v, want the replaced-path refusal", err)
	}
	if digestOf(t, target) != before {
		t.Fatal("the refused truncate still modified the file at the path")
	}

	ownInfo, err := handle.Stat()
	if err != nil {
		t.Fatalf("stat the handle: %v", err)
	}
	if err := truncateLog(handle, target, ownInfo); err != nil {
		t.Fatalf("control: truncateLog with the writer's own identity: %v", err)
	}
	if got := sizeOf(t, target); got != 0 {
		t.Fatalf("control: size after truncate = %d, want 0", got)
	}
}
