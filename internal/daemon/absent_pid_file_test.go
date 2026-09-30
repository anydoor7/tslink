package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMissingPIDFileIsReportedAsMissing is A3-5 (noticed by A3 for A1): a
// missing PID file counted as unknown, so a never-started install reported
// daemon_state "unknown". IsPIDFileMissing tells status that no daemon runs
// for this config directory; a PID file that exists, even one that cannot be
// read, is not missing.
func TestMissingPIDFileIsReportedAsMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "tslink.pid")
	if !IsPIDFileMissing(missing) {
		t.Fatal("missing PID file not reported as missing")
	}
	// It is not proof of absence for the artifact cleanup, which keeps its
	// own conservative rule.
	if IsProcessAbsentFromPIDFile(missing) {
		t.Fatal("IsProcessAbsentFromPIDFile changed meaning for a missing file")
	}
	// Controls: an unreadable or live PID file is not missing.
	garbage := filepath.Join(dir, "garbage.pid")
	if err := os.WriteFile(garbage, []byte("not a pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(dir, "self.pid")
	if err := WritePID(self); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{garbage, self} {
		if IsPIDFileMissing(path) {
			t.Fatalf("%s reported as missing", filepath.Base(path))
		}
	}
}
