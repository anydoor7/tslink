package cmd

import (
	"io"
	"path/filepath"
	"slices"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
)

// cliShareRegister runs the registration half of `tslink share <target>`: the
// CLI has no --tags flag, so its request never carries tags.
func cliShareRegister(t *testing.T, regPath, target string) (registry.Service, bool, error) {
	t.Helper()
	spec, err := inferShareTarget(target, true)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = applyShareExposure(spec, shareRequest{Target: target, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	return registerShare(regPath, spec, "")
}

// TestCLIShareRetryReusesAServiceWhoseTagsChanged pins README's promise that
// retrying the same target reuses its service. A CLI caller cannot ask for
// tags, so a later change to the default tag or to the share's own tags must
// not turn the retry into a conflict.
func TestCLIShareRetryReusesAServiceWhoseTagsChanged(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(t *testing.T, regPath, service string)
		wantTags []string
	}{
		// R3's TestR3ProbeCLIShareRetryAfterSetDefault.
		{"tags set-default", func(t *testing.T, _, _ string) {
			if err := tagsSetDefaultRun(io.Discard, "tag:myteam", false); err != nil {
				t.Fatal(err)
			}
		}, []string{"tag:tsmain"}},
		// R3's TestR3ProbeCLIShareRetryAfterTagsSet.
		{"tags set", func(t *testing.T, regPath, service string) {
			if _, err := tagsSetForPath(regPath, service, "tag:web"); err != nil {
				t.Fatal(err)
			}
		}, []string{"tag:web"}},
		{"tags add", func(t *testing.T, _, service string) {
			if err := tagsAddRun(io.Discard, service, "tag:extra", false); err != nil {
				t.Fatal(err)
			}
		}, []string{"tag:tsmain", "tag:extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfgDir := t.TempDir()
			t.Setenv(config.ConfigDirEnv, cfgDir)
			regPath := filepath.Join(cfgDir, "registry.json")
			first, created, err := cliShareRegister(t, regPath, "3000")
			if err != nil || !created || !slices.Equal(first.Tags, []string{"tag:tsmain"}) {
				t.Fatalf("first share = %+v created=%v err=%v", first, created, err)
			}
			tc.change(t, regPath, first.Name)
			again, created, err := cliShareRegister(t, regPath, "3000")
			if err != nil || created || again.Name != first.Name {
				t.Fatalf("retry after %s: err=%v created=%v name=%q; want %q reused", tc.name, err, created, again.Name, first.Name)
			}
			reg, err := registry.Load(regPath)
			if err != nil || len(reg.Services) != 1 || !slices.Equal(reg.Services[0].Tags, tc.wantTags) {
				t.Fatalf("registry after retry = %+v err=%v; want one service keeping tags %v", reg, err, tc.wantTags)
			}
		})
	}

	// The default tag still applies to a service the share creates.
	cfgDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, cfgDir)
	if err := tagsSetDefaultRun(io.Discard, "tag:myteam", false); err != nil {
		t.Fatal(err)
	}
	created, ok, err := cliShareRegister(t, filepath.Join(cfgDir, "registry.json"), "4000")
	if err != nil || !ok || !slices.Equal(created.Tags, []string{"tag:myteam"}) {
		t.Fatalf("new share after set-default = %+v created=%v err=%v; want the new default tag", created, ok, err)
	}
}

// TestShareReuseStillComparesExplicitlyRequestedTags is the other half: a
// caller that did ask for tags (MCP share with tags) still gets a conflict
// rather than a service carrying tags it did not ask for.
func TestShareReuseStillComparesExplicitlyRequestedTags(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, cfgDir)
	regPath := filepath.Join(cfgDir, "registry.json")
	if _, created, err := cliShareRegister(t, regPath, "3000"); err != nil || !created {
		t.Fatalf("first share: created=%v err=%v", created, err)
	}
	spec := shareIntentForRegression(t, shareRequest{Tags: []string{"tag:tsmain"}})
	spec.Service.Target = "http://localhost:3000"
	if reused, created, err := registerShare(regPath, spec, ""); err != nil || created {
		t.Fatalf("explicit default tag = %+v created=%v err=%v; want reuse", reused, created, err)
	}
	spec = shareIntentForRegression(t, shareRequest{Tags: []string{"tag:other"}})
	spec.Service.Target = "http://localhost:3000"
	if _, created, err := registerShare(regPath, spec, ""); err == nil || created {
		t.Fatalf("explicit different tag: created=%v err=%v; want a conflict", created, err)
	}
}
