package cmd

import "testing"

func TestManifestCarriesMachineConsumerFacts(t *testing.T) {
	m := Manifest()

	if m.Toolchain.GoReleaserVersion != "v2.17.0" {
		t.Fatalf("GoReleaserVersion = %q, want v2.17.0", m.Toolchain.GoReleaserVersion)
	}
	if m.Toolchain.HomebrewArtifact != "cask" {
		t.Fatalf("HomebrewArtifact = %q, want cask", m.Toolchain.HomebrewArtifact)
	}
	if len(m.RegistrySchema.ServiceTypes) == 0 || len(m.RegistrySchema.UnavailableFeatures) == 0 {
		t.Fatalf("registry schema is incomplete: %#v", m.RegistrySchema)
	}
	for _, feature := range m.RegistrySchema.UnavailableFeatures {
		if !contains(feature, "feature_unavailable") {
			t.Fatalf("unavailable feature %q does not name feature_unavailable", feature)
		}
	}
	if !containsString(m.APIActions, apiActionDoctor) ||
		!containsString(m.APIActions, apiActionAccessExplain) ||
		!containsString(m.APIActions, apiActionTemplateApply) {
		t.Fatalf("api actions missing shipped actions: %v", m.APIActions)
	}
	if m.Release.PublicReleaseAvailable || m.Release.PrebuiltAvailable || m.Release.HomebrewTapAvailable {
		t.Fatalf("release availability must stay false before first public readback: %#v", m.Release)
	}
}

func TestManifestHighRiskFlagsAndCredentialBoundaries(t *testing.T) {
	m := Manifest()

	assertHighRisk := func(command string, want ...string) {
		t.Helper()
		for _, op := range m.HighRiskOperations {
			if op.Command != command {
				continue
			}
			for _, flag := range want {
				if !containsString(op.RequiredFlags, flag) {
					t.Fatalf("%s required flags = %v, want %s", command, op.RequiredFlags, flag)
				}
			}
			return
		}
		t.Fatalf("high-risk operation for %s not found", command)
	}
	assertHighRisk("tslink login", "--manage-acl")
	assertHighRisk("tslink serve", "--manage-acl")
	assertHighRisk("tslink tags delete-remote", "--force", "--manage-acl")

	var sawStdin, sawArgvAvoid bool
	for _, src := range m.CredentialSources.Automation {
		if containsString(src.Flags, "--api-key-stdin") || containsString(src.Flags, "--client-secret-stdin") {
			sawStdin = true
		}
	}
	for _, src := range m.CredentialSources.Avoid {
		if containsString(src.Flags, "--api-key") || containsString(src.Flags, "--client-secret") {
			sawArgvAvoid = true
		}
	}
	if !sawStdin || !sawArgvAvoid {
		t.Fatalf("credential boundary must prefer stdin and warn on argv: %#v", m.CredentialSources)
	}
}

func TestManifestCommandsCarryInheritedFlags(t *testing.T) {
	m := Manifest()
	commands := map[string]CommandInfo{}
	for _, c := range m.Commands {
		commands[c.Path] = c
	}

	for _, path := range []string{
		"tslink doctor",
		"tslink access explain",
		"tslink template apply",
		"tslink tags delete-remote",
	} {
		if _, ok := commands[path]; !ok {
			t.Fatalf("manifest missing command %s", path)
		}
	}
	assertFlag := func(command, flag string) {
		t.Helper()
		c, ok := commands[command]
		if !ok {
			t.Fatalf("manifest missing command %s", command)
		}
		for _, f := range c.Flags {
			if f.Name == flag {
				return
			}
		}
		t.Fatalf("%s flags = %#v, want %s", command, c.Flags, flag)
	}
	assertFlag("tslink login", "api-key-stdin")
	assertFlag("tslink login", "client-secret-stdin")
	assertFlag("tslink login", "manage-acl")
	assertFlag("tslink serve", "manage-acl")
	assertFlag("tslink tags delete-remote", "force")
	assertFlag("tslink tags delete-remote", "manage-acl")
	assertFlag("tslink status", "json")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func contains(value, want string) bool {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
