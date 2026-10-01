package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoginHelpConditionalCredentialFallback(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"login"})
	if err != nil || command == nil {
		t.Fatalf("registered login command missing: %v", err)
	}
	var out bytes.Buffer
	previous := command.OutOrStdout()
	command.SetOut(&out)
	t.Cleanup(func() { command.SetOut(previous) })
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	if help == "" {
		t.Fatal("rendered login help is empty")
	}
	start := strings.Index(help, "Credentials are stored")
	if start < 0 {
		t.Fatal("positive control: credential storage paragraph absent from rendered help")
	}
	end := strings.Index(help[start:], "Metadata never")
	if end < 0 {
		t.Fatal("credential storage paragraph terminator absent from rendered help")
	}
	paragraph := strings.Join(strings.Fields(help[start:start+end]), " ")
	for _, want := range []string{
		"macOS and Linux", "restricted-permission file fallback (0600)",
		"stale keychain credential is proven absent or removed",
		"completely unreachable or its state is uncertain", "login fails explicitly",
		"restore keychain access and retry", "Windows has no credential file fallback",
	} {
		if !strings.Contains(paragraph, want) {
			t.Errorf("rendered login help storage paragraph lacks %q:\n%s", want, paragraph)
		}
	}
	if strings.Contains(paragraph, "On systems without keychain support") {
		t.Errorf("rendered login help still promises unconditional fallback:\n%s", paragraph)
	}
}

func TestLoginHelpStorageManifestParity(t *testing.T) {
	raw, err := os.ReadFile("../docs/cli-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var published CLIManifest
	if err := json.Unmarshal(raw, &published); err != nil {
		t.Fatal(err)
	}
	live := Manifest().CredentialSources.Storage
	if !reflect.DeepEqual(live, published.CredentialSources.Storage) {
		t.Fatalf("live credential storage manifest differs from published manifest: live=%+v published=%+v", live, published.CredentialSources.Storage)
	}
	for _, source := range live {
		if source.ID != "restricted_file_fallback" {
			continue
		}
		for _, want := range []string{"macOS/Linux", "stale keychain credentials are proven absent or removed", "unreachable or uncertain keychain causes explicit failure", "restore keychain access and retry", "Windows has no file fallback"} {
			if !strings.Contains(source.Boundary, want) {
				t.Errorf("manifest fallback boundary lacks %q: %s", want, source.Boundary)
			}
		}
		return
	}
	t.Fatal("restricted_file_fallback missing from live credential storage manifest")
}
