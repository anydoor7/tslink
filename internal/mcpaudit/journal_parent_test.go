package mcpaudit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalParentKinds(t *testing.T) {
	for _, name := range []string{"missing-file", "missing-parent", "regular-parent", "regular-ancestor"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "audit.json")
			wantError := false
			switch name {
			case "missing-parent":
				path = filepath.Join(root, "missing", "audit.json")
			case "regular-parent", "regular-ancestor":
				blocked := filepath.Join(root, "file")
				if err := os.WriteFile(blocked, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(blocked, "audit.json")
				if name == "regular-ancestor" {
					path = filepath.Join(blocked, "nested", "audit.json")
				}
				wantError = true
			}
			entries, err := (Journal{Path: path}).Read()
			t.Logf("entries=%d read_error=%v", len(entries), err)
			if (err != nil) != wantError {
				t.Errorf("Read error=%v; want error=%v", err, wantError)
			}
			if wantError {
				if err := (Journal{Path: path}).Record(context.Background(), Entry{ID: "control"}); err == nil {
					t.Fatal("write should reject regular ancestor")
				}
				got, err := os.ReadFile(filepath.Join(root, "file"))
				if err != nil || string(got) != "retained" {
					t.Fatalf("ancestor changed: %q %v", got, err)
				}
			} else if err != nil || len(entries) != 0 {
				t.Fatalf("absent journal=%v %v", entries, err)
			}
		})
	}
}
