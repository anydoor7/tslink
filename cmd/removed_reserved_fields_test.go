package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

// TestReservedFeatureSurfaceIsGone replaces the tests that pinned
// feature_unavailable for custom domains, ACME and middleware: the flags,
// registry keys, manifest entries and error code no longer exist.
func TestReservedFeatureSurfaceIsGone(t *testing.T) {
	add, _, err := rootCmd.Find([]string{"add"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"domain", "acme-email"} {
		if add.Flags().Lookup(name) != nil {
			t.Fatalf("add still has --%s", name)
		}
		// A copy keeps the shared command's parsed state untouched.
		probe := *add
		probe.ResetFlags()
		probe.Flags().AddFlagSet(add.LocalFlags())
		parseErr := probe.ParseFlags([]string{"--" + name, "x"})
		if parseErr == nil || output.ExitCode(parseErr) != output.ExitUsage {
			t.Fatalf("add --%s x: error = %v (exit %d), want an unknown-flag usage error", name, parseErr, output.ExitCode(parseErr))
		}
	}

	manifest := Manifest()
	if _, ok := manifest.ErrorCodes["feature_unavailable"]; ok {
		t.Fatal("manifest still declares feature_unavailable")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"unavailable_features", `"acme-email"`, `"name":"domain"`, "feature_unavailable"} {
		if strings.Contains(string(encoded), removed) {
			t.Fatalf("manifest still contains %s", removed)
		}
	}
	// Control: the same probe finds a flag and a code that do exist.
	for _, present := range []string{`"name":"funnel-ttl"`, registry.CodeFunnelPublicAckRequired} {
		if !strings.Contains(string(encoded), present) {
			t.Fatalf("manifest probe is blind: %s not found", present)
		}
	}
}

// TestRegistryCheckRefusesRemovedReservedKeys: a registry that still carries
// one of the removed keys is refused through strict decoding, naming the key.
func TestRegistryCheckRefusesRemovedReservedKeys(t *testing.T) {
	for key, value := range map[string]string{
		"domain":     `"app.example.com"`,
		"acme_email": `"admin@example.com"`,
		"middleware": `{"basic_auth":"user:pass"}`,
	} {
		path := filepath.Join(t.TempDir(), "registry.json")
		raw := fmt.Sprintf(`{"schema_version":1,"services":[{"name":"web","type":"proxy","target":"http://localhost:3000","%s":%s}]}`, key, value)
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := registryCheck(path)
		failure := output.NewFailureForError("registry check", err)
		if failure.Error == nil || failure.Error.Code != registry.CodeUnknownConfigKey || failure.Code != output.ExitUsage {
			t.Fatalf("%s: failure = %+v, want %s with exit %d", key, failure, registry.CodeUnknownConfigKey, output.ExitUsage)
		}
		if !strings.Contains(failure.Error.Message, fmt.Sprintf("%q", key)) || strings.Contains(failure.Error.Message, "user:pass") {
			t.Fatalf("%s: message = %q, want it to name the key and nothing of its value", key, failure.Error.Message)
		}
	}
}
