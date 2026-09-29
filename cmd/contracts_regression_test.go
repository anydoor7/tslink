package cmd

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func shareIntentForRegression(t *testing.T, req shareRequest) shareTargetSpec {
	t.Helper()
	spec := shareTargetSpec{Service: registry.Service{Type: registry.TypeProxy, Target: "http://localhost:3000", Ephemeral: true}, NameBase: "example"}
	out, err := applyShareExposure(spec, req)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestShareReuseRejectsDifferentRequestedTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	first := shareIntentForRegression(t, shareRequest{Tags: []string{"tag:private"}})
	if _, created, err := registerShare(path, first, ""); err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	changed := shareIntentForRegression(t, shareRequest{Tags: []string{"tag:public"}})
	if _, created, err := registerShare(path, changed, ""); err == nil || created || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "tag:private") || !strings.Contains(err.Error(), "tag:public") {
		t.Fatalf("changed tags must report both values as conflict; created=%v err=%v", created, err)
	}
	reg, err := registry.Load(path)
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Tags[0] != "tag:private" {
		t.Fatalf("registry changed: %+v err=%v", reg, err)
	}
}

func TestShareReuseKeepsDefaultAndTreatsTagsAsSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	first := shareIntentForRegression(t, shareRequest{})
	initial, created, err := registerShare(path, first, "")
	if err != nil || !created {
		t.Fatalf("initial: %+v created=%v err=%v", initial, created, err)
	}
	repeated, created, err := registerShare(path, first, "")
	if err != nil || created || repeated.Name != initial.Name {
		t.Fatalf("default repeat: %+v created=%v err=%v", repeated, created, err)
	}

	setPath := filepath.Join(t.TempDir(), "registry.json")
	tagsA := shareIntentForRegression(t, shareRequest{Tags: []string{"tag:alpha", "tag:beta"}})
	firstSet, created, err := registerShare(setPath, tagsA, "")
	if err != nil || !created {
		t.Fatalf("set initial: %+v created=%v err=%v", firstSet, created, err)
	}
	tagsB := shareIntentForRegression(t, shareRequest{Tags: []string{"tag:beta", "tag:alpha"}})
	reused, created, err := registerShare(setPath, tagsB, "")
	if err != nil || created || reused.Name != firstSet.Name {
		t.Fatalf("set repeat: %+v created=%v err=%v", reused, created, err)
	}
}

func TestShareReuseHonorsExplicitFunnelDeadline(t *testing.T) {
	cases := []struct {
		name           string
		firstTTL       string
		secondTTL      string
		secondExplicit bool
		wantConflict   bool
	}{
		{"never-to-one-hour", "never", "1h", true, true},
		{"long-to-one-hour", "72h", "1h", true, true},
		{"bounded-to-never", "1h", "never", true, true},
		{"shorter-to-longer", "1h", "8h", true, false},
		{"same-duration-repeat", "1h", "1h", true, false},
		{"default-retains-bounded", "1h", "", false, false},
		{"default-refuses-never", "never", "", false, true},
		{"default-refuses-longer", "72h", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.json")
			first := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: tc.firstTTL, FunnelTTLSet: true})
			original, created, err := registerShare(path, first, "")
			if err != nil || !created {
				t.Fatalf("initial: %+v created=%v err=%v", original, created, err)
			}
			// Ensure repeated finite TTLs do not match only because their absolute deadlines happen to be identical.
			time.Sleep(time.Millisecond)
			second := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: tc.secondTTL, FunnelTTLSet: tc.secondExplicit})
			reused, created, err := registerShare(path, second, "")
			if tc.wantConflict {
				if err == nil || created || output.ExitCode(err) != output.ExitConflict || !strings.Contains(err.Error(), "funnel deadline") {
					t.Fatalf("want Funnel deadline conflict, got %+v created=%v err=%v", reused, created, err)
				}
			} else if err != nil || created || reused.Name != original.Name {
				t.Fatalf("want unchanged reuse, got %+v created=%v err=%v", reused, created, err)
			}
			stored, loadErr := registry.Load(path)
			if loadErr != nil || len(stored.Services) != 1 || !sameDeadline(stored.Services[0].FunnelExpiresAt, original.FunnelExpiresAt) {
				t.Fatalf("deadline changed: %+v err=%v", stored, loadErr)
			}
		})
	}
}

func sameDeadline(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
