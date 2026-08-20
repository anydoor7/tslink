package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monody0007/tslink/cmd"
)

func testManifestBytes(t *testing.T, platform cmd.PlatformInfo, mutate func(*cmd.CLIManifest)) []byte {
	t.Helper()
	manifest := cmd.Manifest()
	manifest.Platform = platform
	if mutate != nil {
		mutate(&manifest)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func otherPlatform() cmd.PlatformInfo {
	goos := "linux"
	if runtime.GOOS == goos {
		goos = "darwin"
	}
	return cmd.PlatformInfo{GOOS: goos, GOARCH: runtime.GOARCH}
}

func TestCheckManifestStrictSamePlatformRejectsGenuinelyStaleManifest(t *testing.T) {
	platform := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, platform, nil)
	existing := testManifestBytes(t, platform, func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Short = "deliberately stale same-platform command text"
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !result.SamePlatform || result.Equal {
		t.Fatalf("same-platform stale check = %+v, want strict mismatch", result)
	}
}

func TestCheckManifestCrossPlatformAcceptsIndependentMatchAndSkipsCommands(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.Commands[0].Short = "platform-specific command difference"
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || !result.Equal {
		t.Fatalf("cross-platform command-only check = %+v, want independent match", result)
	}
}

func TestCheckManifestCrossPlatformRejectsPlatformIndependentChange(t *testing.T) {
	running := cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	generated := testManifestBytes(t, running, nil)
	existing := testManifestBytes(t, otherPlatform(), func(manifest *cmd.CLIManifest) {
		manifest.RegistrySchemaVersion++
	})

	result, err := compareManifest(existing, generated)
	if err != nil {
		t.Fatal(err)
	}
	if result.SamePlatform || result.Equal {
		t.Fatalf("cross-platform independent-field check = %+v, want mismatch", result)
	}
}

func TestCheckManifestRequiresDeclaredPlatform(t *testing.T) {
	data := testManifestBytes(t, cmd.PlatformInfo{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}, nil)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	delete(object, "platform")
	withoutPlatform, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}

	_, err = compareManifest(withoutPlatform, data)
	if err == nil || !strings.Contains(err.Error(), "required top-level field platform is missing") {
		t.Fatalf("missing platform error = %v", err)
	}
}

func TestCheckManifestCrossPlatformReportsComparedAndSkippedCoverage(t *testing.T) {
	result := manifestCheckResult{
		ManifestPlatform: cmd.PlatformInfo{GOOS: "darwin", GOARCH: "arm64"},
		RunningPlatform:  cmd.PlatformInfo{GOOS: "linux", GOARCH: "arm64"},
	}
	compared, skipped := crossPlatformCoverageMessages(result)
	if want := "gen-manifest: compared platform-independent top-level fields (all except platform and commands) for manifest darwin/arm64 and running linux/arm64"; compared != want {
		t.Fatalf("compared message = %q, want %q", compared, want)
	}
	if want := "gen-manifest: skipped platform-specific commands and flags for manifest darwin/arm64 on running linux/arm64"; skipped != want {
		t.Fatalf("skipped message = %q, want %q", skipped, want)
	}
}

func TestMinimumGoVersionFromGoMod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n\ngo 1.26.5\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := minimumGoVersionFromGoMod(path)
	if err != nil {
		t.Fatalf("minimumGoVersionFromGoMod() error = %v", err)
	}
	if got != "1.26.5" {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want 1.26.5", got)
	}
}

func TestMinimumGoVersionFromGoModFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(path, []byte("module example.test/tslink\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if got, err := minimumGoVersionFromGoMod(path); err == nil {
		t.Fatalf("minimumGoVersionFromGoMod() = %q, want error for missing go directive", got)
	}
}
