package inspect

import (
	"strings"
	"testing"
)

// TestRuntimeSnapshotCodeDescriptionsMatchWhatTheCodesMean: B1-7 made a
// malformed or incompatible runtime.json runtime_snapshot_unreadable, and
// kept runtime_snapshot_stale for a readable snapshot of another daemon or
// registry. The descriptions agents read in the manifest and in doctor say
// the same.
func TestRuntimeSnapshotCodeDescriptionsMatchWhatTheCodesMean(t *testing.T) {
	stale := strings.ToLower(WarningCodeRegistry[WarningCodeRuntimeSnapshotStale].Description)
	unreadable := strings.ToLower(WarningCodeRegistry[WarningCodeRuntimeSnapshotUnreadable].Description)
	for _, word := range []string{"malformed", "schema_version"} {
		if !strings.Contains(unreadable, word) {
			t.Errorf("runtime_snapshot_unreadable description %q does not cover %s", unreadable, word)
		}
		if strings.Contains(stale, word) {
			t.Errorf("runtime_snapshot_stale description %q still claims %s", stale, word)
		}
	}
	if !strings.Contains(stale, "does not match") {
		t.Errorf("runtime_snapshot_stale description %q no longer says what stale means", stale)
	}
}
