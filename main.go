package main

import (
	"os"

	"github.com/monody0007/tslink/cmd"
	"github.com/monody0007/tslink/internal/logging"
	"github.com/monody0007/tslink/internal/output"
)

// version and commit are set via ldflags at build time
// (e.g., -X main.version=v1.2.3 -X main.commit=<sha>). commit is empty for
// plain `go build`; release builds embed the exact VCS SHA.
var (
	version = "dev"
	commit  = ""
)

func main() {
	logging.Init(false)
	cmd.Version = version
	cmd.Commit = commit
	err := cmd.Execute()
	if err == nil {
		return
	}

	code := output.ExitCode(err)

	if output.IsSilent(err) {
		os.Exit(code)
	}

	if cmd.WasJSONRequested() {
		output.FailureForError(cmd.LastCommandName(), err)
	} else {
		// Print error in human-readable format (Cobra's SilenceErrors is on)
		os.Stderr.WriteString("Error: " + err.Error() + "\n")
	}

	os.Exit(code)
}
