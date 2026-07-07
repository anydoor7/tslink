package main

import (
	"os"

	"github.com/monody0007/tslink/cmd"
	"github.com/monody0007/tslink/internal/output"
)

// version is set via ldflags at build time (e.g., -X main.version=v1.2.3).
var version = "dev"

func main() {
	cmd.Version = version
	err := cmd.Execute()
	if err == nil {
		return
	}

	code := output.ExitCode(err)

	if output.IsSilent(err) {
		os.Exit(code)
	}

	if cmd.WasJSONRequested() {
		output.FailureForError("", err)
	} else {
		// Print error in human-readable format (Cobra's SilenceErrors is on)
		os.Stderr.WriteString("Error: " + err.Error() + "\n")
	}

	os.Exit(code)
}
