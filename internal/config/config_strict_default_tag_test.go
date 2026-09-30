package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/testenv"
)

func writeConfigFixture(t *testing.T, raw string) string {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if raw != "" {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestDefaultTagFailsClosedOnAConfigItCannotRead is A1's run1: a trailing
// comma or a key typo used to yield tag:tsmain, which add, share and login
// then persisted.
func TestDefaultTagFailsClosedOnAConfigItCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     string
		problem string
	}{
		"trailing comma":  {raw: `{"default_tag":"tag:web",}`, problem: "invalid character"},
		"key typo":        {raw: `{"default_tags":"tag:web"}`, problem: `unknown key "default_tags"`},
		"nested key typo": {raw: `{"default_tag":"tag:web","mcp":{"enabeld":true}}`, problem: `unknown key "enabeld"`},
		"trailing data":   {raw: `{"default_tag":"tag:web"} {}`, problem: "unexpected data after the JSON object"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeConfigFixture(t, tc.raw)
			tag, err := DefaultTag()
			var loadErr *ConfigLoadError
			if !errors.As(err, &loadErr) || loadErr.StableCode() != CodeConfigLoadFailed {
				t.Fatalf("DefaultTag() = %q, %v; want %s", tag, err, CodeConfigLoadFailed)
			}
			if tag != "" {
				t.Fatalf("DefaultTag() returned %q alongside its error", tag)
			}
			if !strings.Contains(err.Error(), strconv.Quote(path)) || !strings.Contains(err.Error(), tc.problem) {
				t.Fatalf("error %q does not name %q and %q", err, path, tc.problem)
			}
			if next := strings.Join(loadErr.NextCommands(), "\n"); !strings.Contains(next, path) {
				t.Fatalf("next %q does not name the file", next)
			}
			if _, err := LoadGlobalConfig(); !errors.As(err, &loadErr) {
				t.Fatalf("LoadGlobalConfig() error = %v, want the same strict refusal", err)
			}
		})
	}
}

func TestDefaultTagReadsAValidOrAbsentConfig(t *testing.T) {
	writeConfigFixture(t, "")
	if tag, err := DefaultTag(); err != nil || tag != "tag:tsmain" {
		t.Fatalf("DefaultTag() without config.json = %q, %v; want tag:tsmain", tag, err)
	}
	writeConfigFixture(t, `{"control_url":"https://headscale.example.com"}`)
	if tag, err := DefaultTag(); err != nil || tag != "tag:tsmain" {
		t.Fatalf("DefaultTag() without default_tag = %q, %v; want tag:tsmain", tag, err)
	}
	writeConfigFixture(t, `{"default_tag":"tag:web","mcp":{"enabled":true,"allow":["alice@example.com"]}}`)
	if tag, err := DefaultTag(); err != nil || tag != "tag:web" {
		t.Fatalf("DefaultTag() = %q, %v; want tag:web", tag, err)
	}
}

// TestGetDefaultTagKeepsItsLenientReadForDisplayCallers pins that the
// non-mutating GetDefaultTag is unchanged: it still reads a configured tag past
// an unknown key and still falls back on an unreadable file.
func TestGetDefaultTagKeepsItsLenientReadForDisplayCallers(t *testing.T) {
	writeConfigFixture(t, `{"default_tag":"tag:web","future_key":{"x":1}}`)
	if tag := GetDefaultTag(); tag != "tag:web" {
		t.Fatalf("GetDefaultTag() = %q, want tag:web", tag)
	}
	writeConfigFixture(t, `{"default_tag":"tag:web",}`)
	if tag := GetDefaultTag(); tag != "tag:tsmain" {
		t.Fatalf("GetDefaultTag() on a malformed file = %q, want the tag:tsmain fallback", tag)
	}
}

// TestUpdateGlobalConfigRefusesToDropUnknownKeys: a rewrite from the typed
// struct used to drop every key it did not know.
func TestUpdateGlobalConfigRefusesToDropUnknownKeys(t *testing.T) {
	raw := `{"default_tag":"tag:web","future_key":{"x":1}}`
	path := writeConfigFixture(t, raw)
	err := UpdateGlobalConfig(func(cfg *GlobalConfig) error {
		cfg.ControlURL = "https://headscale.example.com"
		return nil
	})
	var loadErr *ConfigLoadError
	if !errors.As(err, &loadErr) || !strings.Contains(err.Error(), `unknown key "future_key"`) {
		t.Fatalf("UpdateGlobalConfig() error = %v, want the unknown key refused", err)
	}
	if data, _ := os.ReadFile(path); string(data) != raw {
		t.Fatalf("config.json = %s, want it untouched", data)
	}
}

// TestUpdateGlobalConfigKeepsBothConcurrentWriters interleaves a second
// writer between the first writer's read and write. Under the lock the second
// writer waits and then reads the first one's result, so both values stay.
func TestUpdateGlobalConfigKeepsBothConcurrentWriters(t *testing.T) {
	path := writeConfigFixture(t, `{}`)
	var once sync.Once
	secondDone := make(chan error, 1)
	globalConfigAfterLoadHook = func() {
		once.Do(func() {
			go func() {
				secondDone <- UpdateGlobalConfig(func(cfg *GlobalConfig) error {
					cfg.DefaultTag = "tag:web"
					return nil
				})
			}()
			// Give an unlocked second writer ample time to finish its whole
			// read-modify-write inside this one; a locked one cannot start.
			select {
			case err := <-secondDone:
				secondDone <- err
			case <-time.After(300 * time.Millisecond):
			}
		})
	}
	t.Cleanup(func() { globalConfigAfterLoadHook = nil })

	if err := UpdateGlobalConfig(func(cfg *GlobalConfig) error {
		cfg.ControlURL = "https://headscale.example.com"
		return nil
	}); err != nil {
		t.Fatalf("first writer: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second writer: %v", err)
	}
	cfg, err := LoadGlobalConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlURL != "https://headscale.example.com" || cfg.DefaultTag != "tag:web" {
		data, _ := os.ReadFile(path)
		t.Fatalf("config.json = %s, want both writers' values", data)
	}
}
