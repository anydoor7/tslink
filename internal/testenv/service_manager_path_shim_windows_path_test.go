package testenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPlantServiceManagerShimsRefusesUnsafePathsOnlyWhereAScriptIsPlanted is
// W-1. The embed check exists because a unix fake is a shell script with the
// log path baked into it. It ran before the Windows branch, and every Windows
// path contains `\`, so planting always failed there: the cmd package's
// TestMain refused to start and no cmd test had ever run on Windows.
//
// Each platform gets the half it can execute. On Windows a native temp path,
// which must contain `\` for the case to mean anything, has to plant cleanly:
// no script, and the empty log the teardown report reads. On unix the same
// character must still be refused, before anything is created, so the fix
// cannot have widened into "skip the check".
func TestPlantServiceManagerShimsRefusesUnsafePathsOnlyWhereAScriptIsPlanted(t *testing.T) {
	if runtime.GOOS == "windows" {
		dir := t.TempDir()
		if !strings.Contains(dir, `\`) {
			t.Fatalf("control: temp dir %q has no backslash, so this run cannot reproduce the refusal", dir)
		}
		logPath, planted, err := PlantServiceManagerShims(dir)
		if err != nil {
			t.Fatalf("PlantServiceManagerShims(%q) error = %v, want a native Windows path accepted", dir, err)
		}
		if len(planted) != 0 {
			t.Fatalf("planted = %v on Windows, want none: no fake can run there", planted)
		}
		calls, err := ReadServiceManagerShimCalls(logPath)
		if err != nil || len(calls) != 0 {
			t.Fatalf("shim log %q: calls=%v err=%v, want an existing empty log", logPath, calls, err)
		}
		return
	}

	dir := filepath.Join(t.TempDir(), `back\slash`)
	_, planted, err := PlantServiceManagerShims(dir)
	if err == nil || !strings.Contains(err.Error(), "cannot be embedded in the shim script") {
		t.Fatalf("PlantServiceManagerShims(%q) error = %v, want the embed refusal", dir, err)
	}
	if len(planted) != 0 {
		t.Fatalf("planted = %v after a refusal", planted)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("the refused directory was created anyway (stat err = %v)", statErr)
	}
}
