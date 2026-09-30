package errcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScannerRejectsUnregisteredCodeCarrier(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod": "module example.com/control\n\ngo 1.26\n",
		"control.go": `package control
   type NewlyAddedCarrier struct { Message string; Code string }
   type CodedError struct { Code string }
   var known = CodedError{Code:"known_code"}
   var unknown = NewlyAddedCarrier{Code:"hidden_code"}
  `,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scan := scanEmittedCodes(t, root)
	if len(scan.sites()["known_code"]) == 0 {
		t.Fatal("positive control: scanner missed known carrier")
	}
	if len(scan.problems) != 1 || !strings.Contains(scan.problems[0], "NewlyAddedCarrier") {
		t.Fatalf("unregistered Code string carrier must be named; problems=%v", scan.problems)
	}
}
