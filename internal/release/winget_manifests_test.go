package release_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWingetManifestsExecutes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for owner-run winget generation")
	}
	for _, script := range []string{"winget-manifests-test.py", "winget-submit-test.py"} {
		cmd := exec.Command(python, "-B", filepath.Join(repoRoot(t), "scripts", script))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("offline winget controls failed: %v\n%s", err, out)
		}
	}
}
