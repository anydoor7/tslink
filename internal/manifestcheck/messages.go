// Package manifestcheck defines the stable user-facing contract for the
// generated CLI manifest check.
package manifestcheck

const (
	OutputFile = "docs/cli-manifest.json"

	// StrictUpToDateMessage is asserted verbatim by the macOS release gate.
	// Keep the generator and the workflow validator tied to this one value.
	StrictUpToDateMessage = "gen-manifest: " + OutputFile + " is up to date"
)
