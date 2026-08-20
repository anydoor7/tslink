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
	"strings"

	"github.com/monody0007/tslink/cmd"
	"github.com/monody0007/tslink/internal/manifestcheck"
)

const outputFile = manifestcheck.OutputFile

type manifestCheckResult struct {
	ManifestPlatform cmd.PlatformInfo
	RunningPlatform  cmd.PlatformInfo
	SamePlatform     bool
	Equal            bool
}

type manifestCheckOutput struct {
	Stdout []string
	Stderr string
	OK     bool
}

func main() {
	check := flag.Bool("check", false, "verify the committed fixture is up to date instead of writing it")
	flag.Parse()

	manifest := cmd.Manifest()
	goVersion, err := minimumGoVersionFromGoMod("go.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	manifest.Toolchain.MinimumGoVersion = goVersion

	data, err := json.MarshalIndent(manifest, "", "  ")
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
		result, err := compareManifest(existing, data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-manifest:", err)
			os.Exit(1)
		}
		output := outputForManifestCheck(result)
		for _, line := range output.Stdout {
			fmt.Println(line)
		}
		if !output.OK {
			fmt.Fprintln(os.Stderr, output.Stderr)
			os.Exit(1)
		}
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

func compareManifest(existing, generated []byte) (manifestCheckResult, error) {
	existingPlatform, existingObject, err := decodeManifest(existing)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("decode committed manifest: %w", err)
	}
	generatedPlatform, generatedObject, err := decodeManifest(generated)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("decode generated manifest: %w", err)
	}

	result := manifestCheckResult{
		ManifestPlatform: existingPlatform,
		RunningPlatform:  generatedPlatform,
		SamePlatform:     existingPlatform.GOOS == generatedPlatform.GOOS,
	}
	if result.SamePlatform {
		generatedForComparison, err := normalizeGeneratedArchitecture(
			generated,
			generatedObject["platform"],
			generatedPlatform.GOARCH,
			existingPlatform.GOARCH,
		)
		if err != nil {
			return manifestCheckResult{}, err
		}
		result.Equal = bytes.Equal(bytes.TrimRight(existing, "\n"), bytes.TrimRight(generatedForComparison, "\n"))
		return result, nil
	}

	// Exclude only the fields proven platform-dependent. Comparing the remaining
	// JSON object means future top-level fields are checked by default rather than
	// silently omitted from the cross-platform gate.
	delete(existingObject, "platform")
	delete(existingObject, "commands")
	delete(generatedObject, "platform")
	delete(generatedObject, "commands")
	existingIndependent, err := json.Marshal(existingObject)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("normalize committed manifest: %w", err)
	}
	generatedIndependent, err := json.Marshal(generatedObject)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("normalize generated manifest: %w", err)
	}
	result.Equal = bytes.Equal(existingIndependent, generatedIndependent)
	return result, nil
}

func normalizeGeneratedArchitecture(data, platformJSON []byte, generatedGOARCH, manifestGOARCH string) ([]byte, error) {
	if generatedGOARCH == manifestGOARCH {
		return data, nil
	}
	generatedJSON, err := json.Marshal(generatedGOARCH)
	if err != nil {
		return nil, fmt.Errorf("encode running goarch: %w", err)
	}
	manifestJSON, err := json.Marshal(manifestGOARCH)
	if err != nil {
		return nil, fmt.Errorf("encode manifest goarch: %w", err)
	}
	oldField := append([]byte(`"goarch": `), generatedJSON...)
	newField := append([]byte(`"goarch": `), manifestJSON...)
	if bytes.Count(platformJSON, oldField) != 1 {
		return nil, fmt.Errorf("generated platform does not contain exactly one goarch field")
	}
	normalizedPlatform := bytes.Replace(platformJSON, oldField, newField, 1)
	if bytes.Count(data, platformJSON) != 1 {
		return nil, fmt.Errorf("generated manifest does not contain exactly one platform object")
	}
	return bytes.Replace(data, platformJSON, normalizedPlatform, 1), nil
}

func outputForManifestCheck(result manifestCheckResult) manifestCheckOutput {
	if !result.Equal {
		if result.SamePlatform {
			return manifestCheckOutput{
				Stderr: fmt.Sprintf("gen-manifest: %s is stale; run `go run ./tools/gen-manifest`", outputFile),
			}
		}
		return manifestCheckOutput{
			Stderr: fmt.Sprintf(
				"gen-manifest: %s is stale; the committed manifest is authoritative for GOOS=%s and must be regenerated on that GOOS",
				outputFile,
				result.ManifestPlatform.GOOS,
			),
		}
	}
	if result.SamePlatform {
		return manifestCheckOutput{
			Stdout: []string{manifestcheck.StrictUpToDateMessage},
			OK:     true,
		}
	}
	compared, skipped := crossPlatformCoverageMessages(result)
	return manifestCheckOutput{
		Stdout: []string{
			compared,
			skipped,
			fmt.Sprintf("gen-manifest: %s platform-independent fields are up to date", outputFile),
		},
		OK: true,
	}
}

func decodeManifest(data []byte) (cmd.PlatformInfo, map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return cmd.PlatformInfo{}, nil, err
	}
	platformJSON, ok := object["platform"]
	if !ok {
		return cmd.PlatformInfo{}, nil, fmt.Errorf("required top-level field platform is missing")
	}
	var platform cmd.PlatformInfo
	if err := json.Unmarshal(platformJSON, &platform); err != nil {
		return cmd.PlatformInfo{}, nil, fmt.Errorf("decode platform: %w", err)
	}
	if platform.GOOS == "" || platform.GOARCH == "" {
		return cmd.PlatformInfo{}, nil, fmt.Errorf("platform requires non-empty goos and goarch")
	}
	return platform, object, nil
}

func formatPlatform(platform cmd.PlatformInfo) string {
	return platform.GOOS + "/" + platform.GOARCH
}

func crossPlatformCoverageMessages(result manifestCheckResult) (string, string) {
	manifestPlatform := formatPlatform(result.ManifestPlatform)
	runningPlatform := formatPlatform(result.RunningPlatform)
	return fmt.Sprintf(
			"gen-manifest: compared platform-independent top-level fields (all except platform and commands) for manifest %s and running %s",
			manifestPlatform, runningPlatform,
		), fmt.Sprintf(
			"gen-manifest: skipped platform-specific commands and flags for manifest %s on running %s",
			manifestPlatform, runningPlatform,
		)
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
