package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestManifestDescribesTheBinaryNotTheWorld is A3-3: the manifest stated
// facts about GitHub and Homebrew (no public release, no prebuilt, no tap,
// build from source "before the first public release", the goreleaser
// version and the Homebrew artifact kind), and a test pinned them false. A
// released binary would repeat them forever. What is left describes the
// binary and the source it was built from.
func TestManifestDescribesTheBinaryNotTheWorld(t *testing.T) {
	data, err := json.Marshal(Manifest())
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["release"]; ok {
		t.Fatal("manifest still has a release block")
	}
	var toolchain map[string]json.RawMessage
	if err := json.Unmarshal(top["toolchain"], &toolchain); err != nil {
		t.Fatalf("toolchain: %v", err)
	}
	for _, key := range []string{"goreleaser_version", "homebrew_artifact"} {
		if _, ok := toolchain[key]; ok {
			t.Fatalf("manifest toolchain still has %s", key)
		}
	}
	for _, phrase := range []string{"public_release_available", "prebuilt_available", "homebrew_tap_available", "before the first public release", "external_gates"} {
		if strings.Contains(string(data), phrase) {
			t.Fatalf("manifest still says %q", phrase)
		}
	}
	// Control: the binary's own facts are still there.
	if _, ok := toolchain["minimum_go_version"]; !ok {
		t.Fatalf("toolchain lost minimum_go_version: %s", top["toolchain"])
	}
	if _, ok := top["error_codes"]; !ok {
		t.Fatal("manifest lost error_codes; the probe is blind")
	}
}
