package errcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScannerRejectsSameNameCarrierInDifferentPackage(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                    "module example.com/control\n\ngo 1.26\n",
		"known.go":                  "package control\ntype CodedError struct{Code string}\nvar known = CodedError{Code: \"known_code\"}\n",
		"internal/runtime/error.go": "package runtime\ntype CodedError struct{Code string}\nvar unknown = CodedError{Code: \"hidden_code\"}\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	scan := scanEmittedCodes(t, root)
	if len(scan.sites()["known_code"]) == 0 {
		t.Fatal("positive control missed registered carrier")
	}
	if len(scan.problems) != 1 || !strings.Contains(scan.problems[0], "unregistered Code string carrier internal/runtime.CodedError") {
		t.Fatalf("must name same-name unregistered carrier internal/runtime.CodedError; problems=%v", scan.problems)
	}
}
