package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/cmd"
)

// committedFixture is docs/cli-manifest.json as committed. On a platform that
// does not register a platform-only flag, it is the only description of it.
func committedFixture(t *testing.T) cmd.CLIManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), outputFile))
	if err != nil {
		t.Fatal(err)
	}
	var manifest cmd.CLIManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

// nativeFor is the manifest a binary built for goos/goarch writes with
// -output: its own platform, and only the flags it registers. It is built
// from this binary's manifest: flags marked for platforms that leave goos out
// are dropped, and flags marked for goos that this binary lacks are added from
// the committed fixture.
func nativeFor(t *testing.T, goos, goarch string) cmd.CLIManifest {
	t.Helper()
	manifest := cmd.Manifest()
	manifest.Platform = cmd.PlatformInfo{GOOS: goos, GOARCH: goarch}
	manifest.Commands = slices.Clone(manifest.Commands)
	committed := map[string]cmd.CommandInfo{}
	for _, command := range committedFixture(t).Commands {
		committed[command.Path] = command
	}
	for i := range manifest.Commands {
		command := &manifest.Commands[i]
		command.Flags = slices.DeleteFunc(slices.Clone(command.Flags), func(f cmd.FlagInfo) bool {
			return len(f.Platforms) > 0 && !slices.Contains(f.Platforms, goos)
		})
		for _, entry := range committed[command.Path].Flags {
			if len(entry.Platforms) > 0 && slices.Contains(entry.Platforms, goos) &&
				!slices.ContainsFunc(command.Flags, func(f cmd.FlagInfo) bool { return f.Name == entry.Name }) {
				command.Flags = insertFlag(command.Flags, entry)
			}
		}
	}
	return manifest
}

func hasFlag(manifest cmd.CLIManifest, path, name string) bool {
	for _, command := range manifest.Commands {
		if command.Path == path {
			return slices.ContainsFunc(command.Flags, func(f cmd.FlagInfo) bool { return f.Name == name })
		}
	}
	return false
}

var fixturePlatforms = []cmd.PlatformInfo{
	{GOOS: "darwin", GOARCH: "arm64"}, {GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"}, {GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "amd64"}, {GOOS: "windows", GOARCH: "arm64"},
}

// TestFixtureIsTheSameOnEveryPlatform is A4-4: gen-manifest wrote the
// manifest of the platform it ran on, so on Linux it rewrote platform.goos and
// dropped the two darwin-only --force flags, while -check on Linux passed; a
// contributor saw green locally and red only on the macOS runner. Every
// platform now generates the same fixture from its own native manifest and
// the committed one.
func TestFixtureIsTheSameOnEveryPlatform(t *testing.T) {
	darwin := nativeFor(t, "darwin", "arm64")
	if !hasFlag(darwin, "tslink install", "force") || !hasFlag(darwin, "tslink uninstall", "force") {
		t.Fatal("fixture: the darwin manifest lacks install/uninstall --force")
	}
	previous, err := json.Marshal(committedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := fixtureBytes(darwin, previous, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range fixturePlatforms {
		native := nativeFor(t, platform.GOOS, platform.GOARCH)
		if platform.GOOS != "darwin" && hasFlag(native, "tslink install", "force") {
			t.Fatalf("fixture: the %s native manifest has the darwin-only --force", platform.GOOS)
		}
		got, err := fixtureBytes(native, reference, platform.GOOS)
		if err != nil {
			t.Fatal(err)
		}
		if !fixtureUpToDate(reference, got) {
			t.Errorf("%s/%s generates a different fixture:\n%s", platform.GOOS, platform.GOARCH, firstDifference(reference, got))
		}
	}
}

// TestCheckFailsOnEveryPlatformWhenStale: -check compares the whole fixture
// byte for byte on every platform; nothing but another platform's own flags
// is taken from the committed file.
func TestCheckFailsOnEveryPlatformWhenStale(t *testing.T) {
	reference, err := fixtureBytes(nativeFor(t, "darwin", "arm64"), nil, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	stale := map[string]string{
		"a command's text": strings.Replace(string(reference), `"short": "Install TSLink as the current platform's user startup service"`, `"short": "Install TSLink"`, 1),
		"the platform":     strings.Replace(string(reference), `"goos": "darwin"`, `"goos": "linux"`, 1),
		"an error code":    strings.Replace(string(reference), `"enrollment_required": {`+"\n"+`      "exit_code": 3`, `"enrollment_required": {`+"\n"+`      "exit_code": 1`, 1),
		"a renamed flag":   strings.Replace(string(reference), `"name": "json",`, `"name": "jsonx",`, 1),
	}
	for what, committed := range stale {
		if committed == string(reference) {
			t.Fatalf("fixture: the %s mutation changed nothing", what)
		}
		for _, platform := range fixturePlatforms {
			got, err := fixtureBytes(nativeFor(t, platform.GOOS, platform.GOARCH), []byte(committed), platform.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			if fixtureUpToDate([]byte(committed), got) {
				t.Errorf("-check on %s/%s passes a fixture stale in %s", platform.GOOS, platform.GOARCH, what)
			}
		}
	}
}

// TestKeptFlagsAreKeyedByCommandAndName: a committed flag entry is kept only
// for the platform-marked (command, flag) it names, and never replaces a flag
// the running binary registers itself.
func TestKeptFlagsAreKeyedByCommandAndName(t *testing.T) {
	linux := nativeFor(t, "linux", "amd64")
	committed := nativeFor(t, "darwin", "arm64")
	for i := range committed.Commands {
		for j := range committed.Commands[i].Flags {
			flag := &committed.Commands[i].Flags[j]
			if flag.Name == "force" {
				// install/uninstall --force are darwin-only; tags delete-remote
				// --force exists everywhere and Linux registers it itself, so
				// even a committed entry wrongly marked darwin-only must not
				// replace or duplicate it.
				flag.Usage = "committed " + committed.Commands[i].Path + " --force"
				if committed.Commands[i].Path == "tslink tags delete-remote" {
					flag.Platforms = []string{"darwin"}
				}
			}
		}
	}
	data, err := json.Marshal(committed)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fixtureBytes(linux, data, "linux")
	if err != nil {
		t.Fatal(err)
	}
	var fixture cmd.CLIManifest
	if err := json.Unmarshal(got, &fixture); err != nil {
		t.Fatal(err)
	}
	usages := map[string]string{}
	for _, command := range fixture.Commands {
		for _, flag := range command.Flags {
			if flag.Name == "force" {
				if _, twice := usages[command.Path]; twice {
					t.Errorf("%s lists --force twice", command.Path)
				}
				usages[command.Path] = flag.Usage
			}
		}
	}
	if usages["tslink install"] != "committed tslink install --force" || usages["tslink uninstall"] != "committed tslink uninstall --force" {
		t.Errorf("kept darwin-only flags = %v, want the committed install and uninstall --force", usages)
	}
	if strings.HasPrefix(usages["tslink tags delete-remote"], "committed") {
		t.Errorf("tags delete-remote --force was taken from the committed file: %q", usages["tslink tags delete-remote"])
	}
}

// TestNativeManifestKeepsItsPlatform: -output writes the running binary's own
// manifest, which check-manifest-platforms compares across platforms to prove
// every platforms mark; the fixture's fixed platform and kept flags must not
// leak into it.
func TestNativeManifestKeepsItsPlatform(t *testing.T) {
	native := cmd.Manifest()
	if native.Platform.GOOS != runtime.GOOS || native.Platform.GOARCH != runtime.GOARCH {
		t.Fatalf("native platform = %+v, want %s/%s", native.Platform, runtime.GOOS, runtime.GOARCH)
	}
	if got := hasFlag(native, "tslink install", "force"); got != (runtime.GOOS == "darwin") {
		t.Fatalf("native manifest on %s has install --force = %v", runtime.GOOS, got)
	}
}

func TestFixtureNeedsAReadableCommittedFile(t *testing.T) {
	if _, err := fixtureBytes(nativeFor(t, "linux", "amd64"), []byte("<<<<<<< HEAD\n"), "linux"); err == nil {
		t.Fatal("a committed fixture that is not JSON was ignored; its darwin-only flags would be dropped")
	}
}

func firstDifference(want, got []byte) string {
	wantLines, gotLines := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			return fmt.Sprintf("line %d: want %s\n got %s", i+1, wantLines[i], gotLines[i])
		}
	}
	return "lengths differ"
}
