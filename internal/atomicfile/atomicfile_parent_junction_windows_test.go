//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// makeJunction creates a real directory junction with mklink /J, which needs no
// privilege (unlike a symlink). The test skips only if cmd.exe cannot create it.
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a directory junction on this runner: %v: %s", err, out)
	}
}

// TestWriteFileInExistingDirWritesThroughARealJunction is R4-8 on a real
// junction, standing in for a redirected Startup folder. The control asserts
// the premise the fix rests on: Lstat reports the junction as ModeIrregular
// without ModeDir. If a Go release changes that, this test says so rather than
// passing for an unrelated reason.
func TestWriteFileInExistingDirWritesThroughARealJunction(t *testing.T) {
	restoreAtomicFileHooks(t)
	root := t.TempDir()
	realStartup := filepath.Join(root, "real-startup")
	if err := os.Mkdir(realStartup, 0o700); err != nil {
		t.Fatal(err)
	}
	startup := filepath.Join(root, "Startup")
	makeJunction(t, startup, realStartup)

	info, err := os.Lstat(startup)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeIrregular == 0 || info.IsDir() {
		t.Fatalf("control: Lstat(junction) mode = %v, want ModeIrregular without ModeDir", info.Mode())
	}

	if err := WriteFileInExistingDir(filepath.Join(startup, "tslink.vbs"), []byte("script\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFileInExistingDir() through a junction error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(realStartup, "tslink.vbs"))
	if err != nil || string(data) != "script\r\n" {
		t.Fatalf("junction target holds %q, %v; want the written script", data, err)
	}
}

// TestWriteFileInExistingDirRefusesAJunctionThatIsNotADirectory is the negative
// half: a junction whose target is a file resolves to no directory, so it must
// still be refused and nothing written.
func TestWriteFileInExistingDirRefusesAJunctionThatIsNotADirectory(t *testing.T) {
	restoreAtomicFileHooks(t)
	root := t.TempDir()
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startup := filepath.Join(root, "Startup")
	makeJunction(t, startup, file)

	err := WriteFileInExistingDir(filepath.Join(startup, "tslink.vbs"), []byte("script\r\n"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "validate parent for") {
		t.Fatalf("WriteFileInExistingDir() through a junction to a file error = %v, want a parent refusal", err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "not a directory\n" {
		t.Fatalf("the junction's file target changed: %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "tslink.vbs")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a file was written beside the refused junction (lstat err = %v)", err)
	}
}
