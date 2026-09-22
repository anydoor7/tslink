// Command gen-manifest exports the CLI manifest (commands, flags, exit codes,
// registry schema version, and security capabilities) to docs/cli-manifest.json.
// Downstream documentation and clients consume this fixture, so
// command/schema/capability facts have a single source of truth in the
// product repository.
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
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/cmd"
	"github.com/monody0007/tslink/internal/manifestcheck"
)

const outputFile = manifestcheck.OutputFile

var knownGOARCH = map[string]struct{}{
	"386": {}, "amd64": {}, "arm": {}, "arm64": {}, "loong64": {},
	"mips": {}, "mips64": {}, "mips64le": {}, "mipsle": {},
	"ppc64": {}, "ppc64le": {}, "riscv64": {}, "s390x": {}, "wasm": {},
}

type manifestCheckResult struct {
	ManifestPlatform cmd.PlatformInfo
	RunningPlatform  cmd.PlatformInfo
	SamePlatform     bool
	Equal            bool
	ManifestCoverage manifestCoverage
	RunningCoverage  manifestCoverage
}

type manifestCoverage struct {
	Commands               int
	Flags                  int
	MarkedFlags            int
	JSONResultFields       int
	MarkedJSONResultFields int
}

type manifestCheckOutput struct {
	Stdout []string
	Stderr string
	OK     bool
}

func main() {
	check := flag.Bool("check", false, "verify the committed fixture is up to date instead of writing it")
	outputPath := flag.String("output", outputFile, "write the generated manifest to this path")
	flag.Parse()
	if *check && *outputPath != outputFile {
		fmt.Fprintln(os.Stderr, "gen-manifest: -check cannot be combined with a non-default -output path")
		os.Exit(1)
	}

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

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outputPath, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-manifest: write failed:", err)
		os.Exit(1)
	}
	fmt.Printf("gen-manifest: wrote %s\n", *outputPath)
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

	// Platform provenance itself is expected to differ. Inside commands, remove
	// only entries carrying an explicit platforms qualifier. Command identities,
	// unmarked flags/result fields, and unknown future command fields remain in
	// the comparison by default.
	delete(existingObject, "platform")
	delete(generatedObject, "platform")
	result.ManifestCoverage, err = excludeMarkedCommandEntries(existingObject)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("normalize committed commands: %w", err)
	}
	result.RunningCoverage, err = excludeMarkedCommandEntries(generatedObject)
	if err != nil {
		return manifestCheckResult{}, fmt.Errorf("normalize generated commands: %w", err)
	}
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

func excludeMarkedCommandEntries(object map[string]json.RawMessage) (manifestCoverage, error) {
	var coverage manifestCoverage
	commandsJSON, ok := object["commands"]
	if !ok {
		return coverage, fmt.Errorf("required top-level field commands is missing")
	}
	var commands []map[string]json.RawMessage
	if err := json.Unmarshal(commandsJSON, &commands); err != nil {
		return coverage, fmt.Errorf("decode commands: %w", err)
	}
	coverage.Commands = len(commands)
	for _, command := range commands {
		if flagsJSON, ok := command["flags"]; ok {
			var flags []map[string]json.RawMessage
			if err := json.Unmarshal(flagsJSON, &flags); err != nil {
				return coverage, fmt.Errorf("decode command flags: %w", err)
			}
			coverage.Flags += len(flags)
			unmarked := flags[:0]
			for _, flagInfo := range flags {
				marked, err := hasPlatformMark(flagInfo)
				if err != nil {
					return coverage, fmt.Errorf("decode flag platforms: %w", err)
				}
				if marked {
					coverage.MarkedFlags++
					continue
				}
				unmarked = append(unmarked, flagInfo)
			}
			if len(unmarked) == 0 {
				delete(command, "flags")
			} else {
				encoded, err := json.Marshal(unmarked)
				if err != nil {
					return coverage, fmt.Errorf("encode unmarked flags: %w", err)
				}
				command["flags"] = encoded
			}
		}

		if fieldsJSON, ok := command["json_result_fields"]; ok {
			var fields map[string]map[string]json.RawMessage
			if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
				return coverage, fmt.Errorf("decode JSON result fields: %w", err)
			}
			coverage.JSONResultFields += len(fields)
			for name, fieldInfo := range fields {
				marked, err := hasPlatformMark(fieldInfo)
				if err != nil {
					return coverage, fmt.Errorf("decode JSON result field %q platforms: %w", name, err)
				}
				if marked {
					coverage.MarkedJSONResultFields++
					delete(fields, name)
				}
			}
			if len(fields) == 0 {
				delete(command, "json_result_fields")
			} else {
				encoded, err := json.Marshal(fields)
				if err != nil {
					return coverage, fmt.Errorf("encode unmarked JSON result fields: %w", err)
				}
				command["json_result_fields"] = encoded
			}
		}
	}
	encoded, err := json.Marshal(commands)
	if err != nil {
		return coverage, fmt.Errorf("encode normalized commands: %w", err)
	}
	object["commands"] = encoded
	return coverage, nil
}

func hasPlatformMark(entry map[string]json.RawMessage) (bool, error) {
	platformsJSON, ok := entry["platforms"]
	if !ok {
		return false, nil
	}
	var platforms []string
	if err := json.Unmarshal(platformsJSON, &platforms); err != nil {
		return false, err
	}
	if len(platforms) == 0 {
		return false, fmt.Errorf("platforms must be a non-empty GOOS set when present")
	}
	known := make(map[string]struct{}, len(cmd.SupportedManifestPlatforms()))
	for _, platform := range cmd.SupportedManifestPlatforms() {
		known[platform] = struct{}{}
	}
	seen := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		if _, ok := known[platform]; !ok {
			return false, fmt.Errorf("unsupported GOOS %q", platform)
		}
		if _, ok := seen[platform]; ok {
			return false, fmt.Errorf("duplicate GOOS %q", platform)
		}
		seen[platform] = struct{}{}
	}
	if len(platforms) == len(known) {
		return false, fmt.Errorf("platforms names every supported GOOS; omit it instead")
	}
	return true, nil
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
			if result.ManifestPlatform.GOARCH != result.RunningPlatform.GOARCH {
				return manifestCheckOutput{
					Stderr: fmt.Sprintf(
						"gen-manifest: %s differs across architectures on GOOS=%s (manifest GOARCH=%s, running GOARCH=%s); manifest generation must remain architecture-independent",
						outputFile,
						result.ManifestPlatform.GOOS,
						result.ManifestPlatform.GOARCH,
						result.RunningPlatform.GOARCH,
					),
				}
			}
			return manifestCheckOutput{
				Stderr: fmt.Sprintf("gen-manifest: %s is stale; run `go run ./tools/gen-manifest`", outputFile),
			}
		}
		return manifestCheckOutput{
			Stderr: fmt.Sprintf(
				"gen-manifest: %s is stale; the committed manifest is authoritative for GOOS=%s and must be regenerated on that GOOS, or mark a proven platform-scoped command entry with platforms",
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
			fmt.Sprintf("gen-manifest: %s unmarked fields are up to date", outputFile),
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
	if _, ok := knownGOARCH[platform.GOARCH]; !ok {
		return cmd.PlatformInfo{}, nil, fmt.Errorf("platform goarch %q is not a recognized Go architecture", platform.GOARCH)
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
			"gen-manifest: compared all top-level fields except platform for manifest %s and running %s",
			manifestPlatform, runningPlatform,
		), fmt.Sprintf(
			"gen-manifest: compared %d/%d command identities and %d/%d committed flag entries; excluded %d platform-marked flags and %d platform-marked JSON result fields",
			result.ManifestCoverage.Commands,
			result.ManifestCoverage.Commands,
			result.ManifestCoverage.Flags-result.ManifestCoverage.MarkedFlags,
			result.ManifestCoverage.Flags,
			result.ManifestCoverage.MarkedFlags,
			result.ManifestCoverage.MarkedJSONResultFields,
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
