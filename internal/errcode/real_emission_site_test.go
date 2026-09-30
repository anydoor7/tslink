package errcode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodeDeclarationsNeedARealEmissionSite(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module example.com/control\n\ngo 1.26\n",
		"control.go": `package control
   const CodeUnused = "unused_code"
   const CodeReturned = "returned_code"
   const CodeCarried = "carried_code"
   type CodedError struct { Code string }
   var carried = CodedError{Code:CodeCarried}
   func returned() string { return CodeReturned }
  `,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scan := scanEmittedCodes(t, root)
	if len(scan.problems) != 0 {
		t.Fatal(scan.problems)
	}
	sites := scan.sites()
	for _, code := range []string{"returned_code", "carried_code"} {
		if len(sites[code]) == 0 {
			t.Errorf("missed real emission of %s", code)
		}
	}
	if len(sites["unused_code"]) != 0 {
		t.Errorf("unused declaration counted as emission: %v", sites["unused_code"])
	}
}
