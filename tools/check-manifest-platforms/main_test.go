package main

import (
	"strings"
	"testing"

	"github.com/monody0007/tslink/cmd"
)

type syntheticFlag struct {
	path      string
	name      string
	presentOn []string
	mark      []string
}

func syntheticManifests(flags ...syntheticFlag) map[string]cmd.CLIManifest {
	manifests := map[string]cmd.CLIManifest{}
	for _, goos := range comparedGOOS {
		manifest := cmd.CLIManifest{
			SchemaVersion:      2,
			SupportedPlatforms: append([]string(nil), comparedGOOS...),
			Platform:           cmd.PlatformInfo{GOOS: goos, GOARCH: "arm64"},
			Commands:           []cmd.CommandInfo{{Path: "tslink"}, {Path: "tslink doctor"}, {Path: "tslink tags delete-remote"}},
		}
		for _, spec := range flags {
			if !contains(spec.presentOn, goos) {
				continue
			}
			for i := range manifest.Commands {
				if manifest.Commands[i].Path == spec.path {
					manifest.Commands[i].Flags = append(manifest.Commands[i].Flags, cmd.FlagInfo{
						Name: spec.name, Type: "bool", Usage: "synthetic", Platforms: append([]string(nil), spec.mark...),
					})
				}
			}
		}
		manifests[goos] = manifest
	}
	return manifests
}

func TestVerifyPlatformFlagMarksAcceptsDarwinLinuxAndWindowsOnlyShapes(t *testing.T) {
	manifests := syntheticManifests(
		syntheticFlag{path: "tslink doctor", name: "unix-probe", presentOn: []string{"darwin", "linux"}, mark: []string{"darwin", "linux"}},
		syntheticFlag{path: "tslink doctor", name: "windows-probe", presentOn: []string{"windows"}, mark: []string{"windows"}},
		syntheticFlag{path: "tslink doctor", name: "darwin-probe", presentOn: []string{"darwin"}, mark: []string{"darwin"}},
	)
	count, err := verifyPlatformFlagMarks(manifests)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("divergent entry count = %d, want 3", count)
	}
}

func TestVerifyPlatformFlagMarksRejectsUnmarkedPlatformSpecificEntry(t *testing.T) {
	manifests := syntheticManifests(syntheticFlag{
		path: "tslink doctor", name: "unix-probe", presentOn: []string{"darwin", "linux"}, mark: nil,
	})
	if _, err := verifyPlatformFlagMarks(manifests); err == nil || !strings.Contains(err.Error(), "unmarked platform-specific entry tslink doctor --unix-probe") {
		t.Fatalf("unmarked divergence error = %v", err)
	}
}

func TestVerifyPlatformFlagMarksRejectsStaleMark(t *testing.T) {
	manifests := syntheticManifests(syntheticFlag{
		path: "tslink doctor", name: "stale", presentOn: []string{"darwin", "linux", "windows"}, mark: []string{"darwin"},
	})
	if _, err := verifyPlatformFlagMarks(manifests); err == nil || !strings.Contains(err.Error(), "stale mark on tslink doctor --stale") {
		t.Fatalf("stale mark error = %v", err)
	}
}

func TestVerifyPlatformFlagMarksKeysByCommandPathAndFlagName(t *testing.T) {
	manifests := syntheticManifests(
		syntheticFlag{path: "tslink doctor", name: "force", presentOn: []string{"darwin"}, mark: []string{"darwin"}},
		syntheticFlag{path: "tslink tags delete-remote", name: "force", presentOn: []string{"darwin", "linux", "windows"}, mark: nil},
	)
	if count, err := verifyPlatformFlagMarks(manifests); err != nil || count != 1 {
		t.Fatalf("path-qualified collision check = count %d, err %v", count, err)
	}
}

func TestVerifyPlatformFlagMarksRejectsCommandIdentityDivergence(t *testing.T) {
	manifests := syntheticManifests()
	linux := manifests["linux"]
	linux.Commands[1].Path = "tslink nonexistent"
	manifests["linux"] = linux
	if _, err := verifyPlatformFlagMarks(manifests); err == nil || !strings.Contains(err.Error(), "command identities differ on linux") {
		t.Fatalf("command divergence error = %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestVerifyPlatformFlagMarksRejectsDisagreementAboutSupportedPlatforms(t *testing.T) {
	// This loop quantifies over supported_platforms. A manifest that disagrees
	// about the set silently changes what "exists on all platforms" means, so
	// every stale-mark verdict below it would be computed against a different
	// universe than the other two artifacts.
	manifests := syntheticManifests(syntheticFlag{
		path: "tslink doctor", name: "darwin-probe", presentOn: []string{"darwin"}, mark: []string{"darwin"},
	})
	linux := manifests["linux"]
	linux.SupportedPlatforms = []string{"linux"}
	manifests["linux"] = linux

	_, err := verifyPlatformFlagMarks(manifests)
	if err == nil {
		t.Fatal("accepted manifests that disagree about supported_platforms")
	}
	if !strings.Contains(err.Error(), "supported_platforms") {
		t.Fatalf("error = %v, want a supported_platforms diagnosis", err)
	}
}
