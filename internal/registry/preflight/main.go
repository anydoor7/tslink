// Command preflight performs a read-only strict compatibility check of a
// registry.json copy. It never rewrites the inspected file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/monody0007/tslink/internal/registry"
)

type issueJSON struct {
	Index   int    `json:"index"`
	Name    string `json:"name"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type reportJSON struct {
	OK            bool        `json:"ok"`
	SchemaVersion int         `json:"schema_version,omitempty"`
	ValidServices int         `json:"valid_services"`
	TotalServices int         `json:"total_services"`
	Issues        []issueJSON `json:"issues"`
	GlobalError   string      `json:"global_error,omitempty"`
}

func main() {
	path := flag.String("registry", "", "path to a registry.json copy")
	flag.Parse()
	os.Exit(run(*path, os.Stdout, os.Stderr))
}

func run(path string, stdout, stderr io.Writer) int {
	report := reportJSON{Issues: []issueJSON{}}
	exitCode := 0
	if path == "" {
		report.GlobalError = "-registry is required"
		exitCode = 2
	} else {
		reg, issues, err := registry.Preflight(path)
		if err != nil {
			report.GlobalError = err.Error()
			exitCode = 1
		} else {
			report.SchemaVersion = reg.SchemaVersion
			report.ValidServices = len(reg.Services)
			report.TotalServices = len(reg.Services) + len(issues)
			for _, issue := range issues {
				code, _ := registry.ErrorCode(issue.Err)
				report.Issues = append(report.Issues, issueJSON{Index: issue.Index, Name: issue.Name, Code: code, Message: issue.Err.Error()})
			}
			if len(issues) > 0 {
				exitCode = 1
			}
		}
	}
	report.OK = exitCode == 0
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return exitCode
}
