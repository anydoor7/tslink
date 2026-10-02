// Command gen-manifest exports the CLI manifest (commands, flags, exit codes,
// registry schema version, and security capabilities) to docs/cli-manifest.json.
// Downstream documentation and clients consume this fixture, so
// command/schema/capability facts have a single source of truth in the
// product repository.
//
//	go run ./tools/gen-manifest                # (re)write docs/cli-manifest.json
//	go run ./tools/gen-manifest -check         # exit nonzero if the committed fixture is stale
//	go run ./tools/gen-manifest -output <path> # write this binary's native manifest
//
// The fixture is the same file on every supported platform, so a contributor
// on Linux or Windows regenerates exactly what is committed and -check fails
// on every platform when it is stale. Two things would otherwise follow the
// machine that runs the generator:
//
//   - A flag registered on some platforms only (install and uninstall --force
//     on darwin) carries platforms. The running platform describes the flags it
//     registers; an entry it cannot produce, one whose platforms leave it out,
//     is kept from the committed fixture. The platform where that flag exists
//     regenerates and checks it. JSON result fields are written from static
//     tables on every platform and need nothing kept.
//   - platform always names fixturePlatform, not the running machine.
//
// -output writes the running binary's native manifest instead: its own
// platform and only the flags it registers. That is the evidence the
// release workflow collects on each platform for check-manifest-platforms,
// which verifies every platforms mark against the real divergence.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/anydoor7/tslink/cmd"
	"github.com/anydoor7/tslink/internal/manifestcheck"
)

const outputFile = manifestcheck.OutputFile

// fixturePlatform is the platform the committed fixture names: the one it has
// always been generated on, kept so the file does not change for consumers.
var fixturePlatform = cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"}

func main() {
	check := flag.Bool("check", false, "verify the committed fixture is up to date instead of writing it")
	outputPath := flag.String("output", outputFile, "write this binary's native manifest to this path instead of the fixture")
	flag.Parse()
	if *check && *outputPath != outputFile {
		fmt.Fprintln(os.Stderr, "gen-manifest: -check cannot be combined with a non-default -output path")
		os.Exit(1)
	}

	native := cmd.Manifest()
	goVersion, err := minimumGoVersionFromGoMod("go.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	native.Toolchain.MinimumGoVersion = goVersion

	if *outputPath != outputFile {
		data, err := encodeManifest(native)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-manifest:", err)
			os.Exit(1)
		}
		writeOrExit(*outputPath, data)
		return
	}

	committed, err := os.ReadFile(outputFile)
	if err != nil && (*check || !os.IsNotExist(err)) {
		fmt.Fprintln(os.Stderr, "gen-manifest: cannot read", outputFile, "-", err)
		os.Exit(1)
	}
	data, err := fixtureBytes(native, committed, runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}

	if *check {
		output := outputForManifestCheck(fixtureUpToDate(committed, data))
		for _, line := range output.Stdout {
			fmt.Println(line)
		}
		if !output.OK {
			fmt.Fprintln(os.Stderr, output.Stderr)
			os.Exit(1)
		}
		return
	}
	writeOrExit(*outputPath, data)
}

func writeOrExit(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest: write failed:", err)
		os.Exit(1)
	}
	fmt.Printf("gen-manifest: wrote %s\n", path)
}

func encodeManifest(manifest cmd.CLIManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// fixtureBytes turns the native manifest of a binary running on goos into the
// platform-independent fixture, keeping from committed (nil when there is no
// committed fixture yet) the flag entries only another platform can produce.
func fixtureBytes(native cmd.CLIManifest, committed []byte, goos string) ([]byte, error) {
	fixture := native
	fixture.Platform = fixturePlatform
	fixture.Commands = slices.Clone(native.Commands)
	if committed != nil {
		var previous cmd.CLIManifest
		if err := json.Unmarshal(committed, &previous); err != nil {
			return nil, fmt.Errorf("read the platform-marked flags of the committed %s: %w; restore it before regenerating", outputFile, err)
		}
		keepOtherPlatformFlags(&fixture, previous, goos)
	}
	return encodeManifest(fixture)
}

// keepOtherPlatformFlags copies into fixture every committed flag entry whose
// platforms leave goos out and which the running binary therefore cannot
// describe. Entries are keyed by command path and flag name; one the running
// binary registers itself is never replaced.
func keepOtherPlatformFlags(fixture *cmd.CLIManifest, committed cmd.CLIManifest, goos string) {
	commands := make(map[string]int, len(fixture.Commands))
	for i, command := range fixture.Commands {
		commands[command.Path] = i
	}
	for _, previous := range committed.Commands {
		index, ok := commands[previous.Path]
		if !ok {
			continue
		}
		command := &fixture.Commands[index]
		for _, entry := range previous.Flags {
			if len(entry.Platforms) == 0 || slices.Contains(entry.Platforms, goos) {
				continue
			}
			if slices.ContainsFunc(command.Flags, func(f cmd.FlagInfo) bool { return f.Name == entry.Name }) {
				continue
			}
			command.Flags = insertFlag(command.Flags, entry)
		}
	}
}

// insertFlag uses the native manifest's canonical name ordering across scopes.
func insertFlag(flags []cmd.FlagInfo, entry cmd.FlagInfo) []cmd.FlagInfo {
	at := len(flags)
	for i, f := range flags {
		if f.Name > entry.Name {
			at = i
			break
		}
	}
	return slices.Insert(slices.Clone(flags), at, entry)
}

// fixtureUpToDate compares the committed fixture with the one this platform
// generates, byte for byte apart from trailing newlines.
func fixtureUpToDate(committed, generated []byte) bool {
	return bytes.Equal(bytes.TrimRight(committed, "\n"), bytes.TrimRight(generated, "\n"))
}

type manifestCheckOutput struct {
	Stdout []string
	Stderr string
	OK     bool
}

func outputForManifestCheck(upToDate bool) manifestCheckOutput {
	if !upToDate {
		return manifestCheckOutput{
			Stderr: fmt.Sprintf("gen-manifest: %s is stale; run `go run ./tools/gen-manifest`", outputFile),
		}
	}
	return manifestCheckOutput{
		Stdout: []string{manifestcheck.StrictUpToDateMessage},
		OK:     true,
	}
}

func minimumGoVersionFromGoMod(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("%s does not declare a go version", path)
}
