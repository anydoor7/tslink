package cmd

import (
	"testing"

	"github.com/anydoor7/tslink/internal/errcode"
)

// TestManifestErrorCodesAreTheErrcodeTable: the manifest's error_codes is the
// errcode table, row for row, and so is the compact code->exit map, so the
// manifest cannot omit a code the binary emits (the table test scans for
// those) or state an exit the binary does not use.
func TestManifestErrorCodesAreTheErrcodeTable(t *testing.T) {
	manifest := Manifest()
	compact := CompactManifest()
	rows := errcode.All()
	if len(manifest.ErrorCodes) != len(rows) || len(compact.ErrorCodes) != len(rows) {
		t.Fatalf("manifest has %d codes, compact %d, table %d", len(manifest.ErrorCodes), len(compact.ErrorCodes), len(rows))
	}
	for _, row := range rows {
		info, ok := manifest.ErrorCodes[row.Code]
		if !ok || info.ExitCode != row.Exit || info.Description != row.Description {
			t.Errorf("manifest %s = %+v (present %v), want exit %d and the table's description", row.Code, info, ok, row.Exit)
		}
		if exit, ok := compact.ErrorCodes[row.Code]; !ok || exit != row.Exit {
			t.Errorf("compact %s = %d (present %v), want %d", row.Code, exit, ok, row.Exit)
		}
	}
	// Every code A3 and A4 found emitted but undocumented.
	for _, code := range []string{
		"daemon_not_running", "daemon_setup_failed", "daemon_supervision_unverified",
		"enrollment_required", "invalid_service_config", "link_local_target_refused",
		"registry_reload_invalid", "runtime_snapshot_missing", "runtime_snapshot_stale",
		"runtime_snapshot_unreadable",
	} {
		if _, ok := manifest.ErrorCodes[code]; !ok {
			t.Errorf("manifest still omits %s", code)
		}
	}
	for code, want := range map[string]int{"conflict": 4, "link_local_target_refused": 2, "enrollment_required": 3} {
		if got := manifest.ErrorCodes[code].ExitCode; got != want {
			t.Errorf("manifest %s exit = %d, want %d", code, got, want)
		}
	}
}
