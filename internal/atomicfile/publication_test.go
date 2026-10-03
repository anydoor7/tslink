package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFilePublicationBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := WriteFile(path, []byte("old")); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("directory sync unavailable")
	restore := SetDirectorySyncForTest(func(string) error { return cause })
	t.Cleanup(restore)
	err := WriteFile(path, []byte("new"))
	if !IsPublished(err) || !IsPublished(fmt.Errorf("caller: %w", err)) || !errors.Is(err, cause) || !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("publication error lost: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "new" {
		t.Fatal("published bytes missing", readErr)
	}
	if IsPublished(nil) || IsPublished(cause) {
		t.Fatal("ordinary error marked published")
	}
	restore()
	if err := WriteFile(path, []byte("durable")); err != nil {
		t.Fatal(err)
	}
}
