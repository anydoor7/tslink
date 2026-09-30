//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2ETempPathGuardAcceptsGOTMPDIR: T.TempDir creates its directories in
// GOTMPDIR when that is set, so with GOTMPDIR outside TMPDIR the e2e guard
// refused the suite's own binary directory. It now accepts GOTMPDIR as a
// second temp root and still refuses every other path.
func TestE2ETempPathGuardAcceptsGOTMPDIR(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(base, "tmp")
	gotmp := filepath.Join(base, "gotmp")
	other := filepath.Join(base, "other")
	for _, dir := range []string{tmp, gotmp, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", tmp)
	t.Setenv("GOTMPDIR", gotmp)

	for _, accepted := range []string{filepath.Join(tmp, "tslink"), filepath.Join(gotmp, "tslink")} {
		got, err := e2eResolveTempPath(accepted)
		if err != nil || got != accepted {
			t.Fatalf("e2eResolveTempPath(%q) = %q, %v; want it accepted", accepted, got, err)
		}
	}
	refused := filepath.Join(other, "tslink")
	if _, err := e2eResolveTempPath(refused); err == nil || !strings.Contains(err.Error(), "refusing to operate") {
		t.Fatalf("e2eResolveTempPath(%q) error = %v, want a refusal outside both roots", refused, err)
	}

	// Without GOTMPDIR only the OS temp root is a temp root.
	t.Setenv("GOTMPDIR", "")
	if _, err := e2eResolveTempPath(filepath.Join(gotmp, "tslink")); err == nil || !strings.Contains(err.Error(), "refusing to operate") {
		t.Fatalf("GOTMPDIR unset: path under the former GOTMPDIR error = %v, want a refusal", err)
	}
	if _, err := e2eResolveTempPath(filepath.Join(tmp, "tslink")); err != nil {
		t.Fatalf("GOTMPDIR unset: path under TMPDIR refused: %v", err)
	}
}
