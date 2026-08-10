package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/registry"
)

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

func TestManifestDocumentsDarwinUninstallJSONContract(t *testing.T) {
	m := Manifest()
	var uninstall CommandInfo
	for _, command := range m.Commands {
		if command.Path == "tslink uninstall" {
			uninstall = command
			break
		}
	}
	if uninstall.Path == "" {
		t.Fatal("manifest missing tslink uninstall")
	}

	outcome, ok := uninstall.JSONResultFields["launchctl_outcome"]
	if !ok {
		t.Fatal("uninstall manifest missing launchctl_outcome")
	}
	for _, want := range []string{"not_installed", "unloaded", "already_absent", "unconfirmed"} {
		if !containsString(outcome.Values, want) {
			t.Fatalf("launchctl_outcome values = %v, want %q", outcome.Values, want)
		}
	}
	target := uninstall.JSONResultFields["launchctl_target"].Description
	if !strings.Contains(target, "empty for not_installed and already_absent") {
		t.Fatalf("launchctl_target contract = %q", target)
	}
	rawOutput := uninstall.JSONResultFields["launchctl_output"].Description
	if !strings.Contains(rawOutput, "Verbatim trimmed launchctl output") || !strings.Contains(rawOutput, "omitted") {
		t.Fatalf("launchctl_output contract = %q", rawOutput)
	}
	detail := uninstall.JSONResultFields["detail"].Description
	if !strings.Contains(detail, "TSLink-authored") || !strings.Contains(detail, "separate") {
		t.Fatalf("detail contract = %q", detail)
	}
}

func TestManifestFlagsAreSelfDescribingAndRelationshipsAreExplicit(t *testing.T) {
	m := Manifest()
	commands := map[string]CommandInfo{}
	for _, command := range m.Commands {
		commands[command.Path] = command
		for _, flag := range command.Flags {
			if strings.TrimSpace(flag.Usage) == "" {
				t.Fatalf("%s --%s has empty usage", command.Path, flag.Name)
			}
		}
	}

	flag := func(command, name string) FlagInfo {
		t.Helper()
		for _, candidate := range commands[command].Flags {
			if candidate.Name == name {
				return candidate
			}
		}
		t.Fatalf("%s missing --%s", command, name)
		return FlagInfo{}
	}
	proxy := flag("tslink add", "proxy")
	if !containsString(proxy.OneOf, "--dir") || !containsString(proxy.OneOf, "--tcp") {
		t.Fatalf("add --proxy one_of = %v", proxy.OneOf)
	}
	funnel := flag("tslink add", "funnel")
	if !containsString(funnel.Requires, "--public") || !containsString(funnel.Conflicts, "--allow") {
		t.Fatalf("add --funnel relationships = %+v", funnel)
	}
	for _, name := range []string{"domain", "acme-email"} {
		reserved := flag("tslink add", name)
		if !strings.HasPrefix(reserved.Usage, "[UNAVAILABLE]") {
			t.Fatalf("add --%s usage = %q, want [UNAVAILABLE] prefix", name, reserved.Usage)
		}
	}
}

func TestCompactManifestStaysBelowAgentTokenBudget(t *testing.T) {
	data, err := json.Marshal(CompactManifest())
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(data) >= 2000 {
		t.Fatalf("compact manifest = %d bytes, want < 2000", len(data))
	}
	compact := CompactManifest()
	if compact.ErrorCodes[registry.CodeURLNotReady] != 5 {
		t.Fatalf("url_not_ready exit = %d, want 5", compact.ErrorCodes[registry.CodeURLNotReady])
	}
	if _, ok := compact.Commands["url"]; !ok {
		t.Fatal("compact manifest missing url command")
	}
}

func TestManifestPlatformDescriptionsAreStable(t *testing.T) {
	if got := platformNeutralCommandShort("tslink install", "Install as macOS LaunchAgent"); strings.Contains(got, "macOS") {
		t.Fatalf("install short = %q, want platform-neutral description", got)
	}
	if got := platformNeutralCommandShort("tslink uninstall", "Remove systemd unit"); strings.Contains(got, "systemd") {
		t.Fatalf("uninstall short = %q, want platform-neutral description", got)
	}
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
