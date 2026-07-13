// Command gen-manifest exports the CLI manifest (commands, flags, exit codes,
// registry schema version, and security capabilities) to docs/cli-manifest.json.
// The documentation site consumes this fixture, so command/schema/capability
// facts have a single source of truth in the product repository.
//
//	go run ./tools/gen-manifest          # (re)write docs/cli-manifest.json
//	go run ./tools/gen-manifest -check   # exit nonzero if the committed fixture is stale
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/monody0007/tslink/cmd"
)

const outputFile = "docs/cli-manifest.json"

func main() {
	check := flag.Bool("check", false, "verify the committed fixture is up to date instead of writing it")
	flag.Parse()

	data, err := json.MarshalIndent(cmd.Manifest(), "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if *check {
		existing, err := os.ReadFile(outputFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-manifest: cannot read", outputFile, "-", err)
			os.Exit(1)
		}
		if !bytes.Equal(bytes.TrimRight(existing, "\n"), bytes.TrimRight(data, "\n")) {
			fmt.Fprintf(os.Stderr, "gen-manifest: %s is stale; run `go run ./tools/gen-manifest`\n", outputFile)
			os.Exit(1)
		}
		fmt.Printf("gen-manifest: %s is up to date\n", outputFile)
		return
	}

	if err := os.MkdirAll("docs", 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outputFile, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest: write failed:", err)
		os.Exit(1)
	}
	fmt.Printf("gen-manifest: wrote %s\n", outputFile)
}
