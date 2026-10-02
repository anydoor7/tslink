package errcode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScannerTracksPeopleFallbackEmission(t *testing.T) {
	root := t.TempDir()
	if e := os.Mkdir(filepath.Join(root, "cmd"), 0700); e != nil {
		t.Fatal(e)
	}
	for file, body := range map[string]string{
		"go.mod": "module github.com/anydoor7/tslink\n\ngo 1.26\n",
		"cmd/control.go": `package cmd
type PeopleInviteView struct { Code string }
func peopleInviteCode(err error, fallback string) string { return fallback }
func peopleInviteFailure(op string, err error, fallback string) PeopleInviteView { return PeopleInviteView{Code:peopleInviteCode(err,fallback)} }
func direct() PeopleInviteView { return PeopleInviteView{Code:peopleInviteCode(nil,"direct_fallback")} }
func failure() PeopleInviteView { return peopleInviteFailure("app",nil,"returned_fallback") }
`,
	} {
		if e := os.WriteFile(filepath.Join(root, file), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	scan := scanEmittedCodes(t, root)
	if len(scan.problems) != 0 {
		t.Fatal(scan.problems)
	}
	for _, code := range []string{"direct_fallback", "returned_fallback"} {
		if len(scan.sites()[code]) == 0 {
			t.Fatalf("fallback emission missed: %s", code)
		}
	}
}
