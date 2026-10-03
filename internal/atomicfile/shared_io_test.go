package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateFileReadRoundTripAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := WriteFile(path, []byte("snapshot")); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(path); err != nil || string(data) != "snapshot" {
		t.Fatalf("read snapshot = %q, %v", data, err)
	}
	if _, err := ReadFile(path + ".missing"); !os.IsNotExist(err) {
		t.Fatalf("genuine absence = %v", err)
	}
	if _, err := ReadFile(filepath.Dir(path)); err == nil {
		t.Fatal("directory read accepted")
	}
	if _, err := ReadFile("invalid\x00path"); err == nil {
		t.Fatal("invalid path accepted")
	}
}
