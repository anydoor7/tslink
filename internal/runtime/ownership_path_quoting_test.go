package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/registry"
)

// TestOwnershipLoadErrorShowsThePathVerbatim is W-4. The ledger error quoted
// its path with %q, which doubles every backslash: on Windows that is every
// separator, so the message and both recovery steps named a path that does
// not exist ("C:\\Users\\..."). It is reproduced on every OS by putting a
// backslash in the path itself, which unix allows in a file name and Windows
// reads as a separator.
func TestOwnershipLoadErrorShowsThePathVerbatim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), `back\slash`)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node-ownership.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"nodes":[`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Control: the escaped and the verbatim spelling must differ, or the
	// negative assertion below cannot tell them apart.
	escaped := strconv.Quote(path)
	if escaped == `"`+path+`"` {
		t.Fatalf("control: %s has nothing %%q would escape", path)
	}

	_, err := LoadOwnership(path)
	var coded registry.CodedError
	if !errors.As(err, &coded) || len(coded.Next) < 2 {
		t.Fatalf("LoadOwnership() error = %v, want a coded error with recovery steps", err)
	}
	for _, text := range append([]string{coded.Message}, coded.Next[:2]...) {
		if !strings.Contains(text, `"`+path+`"`) {
			t.Errorf("%q does not name the ledger path verbatim (%s)", text, path)
		}
		if strings.Contains(text, escaped) {
			t.Errorf("%q names the ledger path with its backslashes doubled", text)
		}
	}
}
