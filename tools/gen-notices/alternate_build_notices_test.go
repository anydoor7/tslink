package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestLinkedSourceHeadersIncludeAlternateBuildFiles(t *testing.T) {
	dir := t.TempDir()
	for _, active := range []string{"old.go", "new.go"} {
		ignored := "old.go"
		if active == "old.go" {
			ignored = "new.go"
		}
		data := fmt.Sprintf(`{"Dir":%q,"GoFiles":[%q],"IgnoredGoFiles":[%q,"ignored_test.go"],"Module":{"Path":"example.com/conditional","Version":"v1.0.0","Dir":%q}}`, filepath.ToSlash(dir), active, ignored, filepath.ToSlash(dir))
		mods, err := parseLinkedPackages([]byte(data))
		if err != nil || len(mods) != 1 {
			t.Fatalf("parse conditional package: modules=%v err=%v", mods, err)
		}
		want := []string{filepath.Join(dir, "new.go"), filepath.Join(dir, "old.go")}
		if !slices.Equal(mods[0].sourceFiles, want) {
			t.Errorf("active %s: notice inventory must retain both build variants, got %v want %v", active, mods[0].sourceFiles, want)
		}
	}
}
