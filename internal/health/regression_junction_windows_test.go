package health

import (
	"context"
	"github.com/anydoor7/tslink/internal/registry"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Prepared for native Windows; compilation is not runtime acceptance.
func TestReReviewExistingWindowsJunctionIsAValidDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	junction := filepath.Join(dir, "junction")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Type: registry.TypeFile, Path: target}
	if got := Probe(context.Background(), svc); got != "" {
		t.Fatal("ordinary directory positive control", got)
	}
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture setup failed: %v %s", err, out)
	}
	info, err := os.Stat(junction)
	if err != nil || !info.IsDir() {
		t.Fatal("junction does not resolve to directory", err)
	}
	lstat, err := os.Lstat(junction)
	if err != nil {
		t.Fatal(err)
	}
	svc.Path = junction
	got := Probe(context.Background(), svc)
	t.Logf("junction Lstat mode=%s Stat.IsDir=%v Probe=%q", lstat.Mode(), info.IsDir(), got)
	if got != "" {
		t.Errorf("registered readable directory junction rejected: %s", got)
	}
}
