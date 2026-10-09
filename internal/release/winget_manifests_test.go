package release_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWingetManifestsExecutes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for owner-run winget generation")
	}
	scripts := []string{"winget-manifests-test.py"}
	// winget-submit.sh is owner-run from macOS; its fixtures fake gh/git with
	// shebang executables on PATH, which Windows bash cannot execute.
	if runtime.GOOS != "windows" {
		scripts = append(scripts, "winget-submit-test.py")
	}
	for _, script := range scripts {
		cmd := exec.Command(python, "-B", filepath.Join(repoRoot(t), "scripts", script))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("offline winget controls failed: %v\n%s", err, out)
		}
	}
}
