package main

import (
	"os"

	"github.com/monody0007/tslink/cmd"
	"github.com/monody0007/tslink/internal/output"
)

func main() {
	err := cmd.Execute()
	if err == nil {
		return
	}

	code := output.ExitCode(err)

	if cmd.WasJSONRequested() {
		output.Failure("", code, err.Error())
	} else {
		// Print error in human-readable format (Cobra's SilenceErrors is on)
		os.Stderr.WriteString("Error: " + err.Error() + "\n")
	}

	os.Exit(code)
}
