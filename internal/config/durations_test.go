package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurationConfigStrictValidation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(ConfigDirEnv, dir)
	p, err := LoadLifetimePolicy()
	if err != nil || p.PublicMax != 0 {
		t.Fatalf("default: %+v %v", p, err)
	}
	path := filepath.Join(dir, "config.json")
	for _, tc := range []struct {
		body   string
		want   time.Duration
		reason string
	}{
		{`{"durations":{"public_max":"36h"}}`, 36 * time.Hour, ""},
		{`{"durations":{"public_max":"1h"}}`, time.Hour, ""},
		{`{"durations":{"public_max":"1w2d"}}`, 9 * 24 * time.Hour, ""},
		{`{"durations":{"public_max":"59m"}}`, 0, "at least 1h"},
		{`{"durations":{"public_max":"never"}}`, 0, "valid examples"},
		{`{"durations":{"public_max":"until 2030-01-01"}}`, 0, "valid examples"},
		{`{"durations":{"public_max":""}}`, 0, "positive"},
		{`{"durations":{"public_max":null}}`, 0, "positive"},
		{`{"durations":{}}`, 0, "positive"},
		{`{"durations":{"public_max":123}}`, 0, "cannot unmarshal"},
		{`{"durations":{"public_mx":"1h"}}`, 0, "unknown key"},
		{`{"durations":{"public_max":"-1h"}}`, 0, "valid examples"},
		{`{"durations":{"public_max":"999999999999999999999d"}}`, 0, "representable"},
		{`{"durations":{"public_max":"1h"}} {}`, 0, "unexpected data"},
		{`{"durations":`, 0, "EOF"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := LoadLifetimePolicy()
			if tc.reason == "" {
				if err != nil || p.PublicMax != tc.want {
					t.Fatalf("%+v %v", p, err)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.reason) {
					t.Fatalf("%v want %s", err, tc.reason)
				}
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	cfg := GlobalConfig{ControlURL: "https://control.example", Durations: &DurationPolicyConfig{PublicMax: "8d"}}
	if err := SaveGlobalConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if err := UpdateGlobalConfig(func(c *GlobalConfig) error { c.DefaultTag = "tag:test"; return nil }); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadGlobalConfig()
	if err != nil || stored.Durations.PublicMax != "8d" || stored.ControlURL != cfg.ControlURL || stored.DefaultTag != "tag:test" {
		t.Fatalf("%+v %v", stored, err)
	}
	before, _ := os.ReadFile(path)
	cfg.Durations.PublicMax = "never"
	if err := SaveGlobalConfig(cfg); err == nil {
		t.Fatal("invalid config saved")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("refused save changed config")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLifetimePolicy(); err == nil {
		t.Fatal("directory config admitted")
	}
}
