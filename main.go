package main

import (
	"os"

	"github.com/monody0007/tslink/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
