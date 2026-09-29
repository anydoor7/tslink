package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/server"
)

// The production server must satisfy the interface serve type-asserts, or
// the unverified flag would silently never reach it.
var _ controlURLTrustSetter = (*server.Server)(nil)

// mockServerControlURLTrust records whether serve told the server that its
// control URL is an unverified fallback.
type mockServerControlURLTrust struct {
	mockServer
	unverified []bool
}

func (m *mockServerControlURLTrust) SetControlURLUnverified(unverified bool) {
	m.unverified = append(m.unverified, unverified)
}

// When config.json cannot be loaded, serve still starts on the default
// control URL (it does not refuse), but it must say so, naming the file, and
// must mark that URL as unverified so the server never resets a node identity
// on a control URL difference alone. A flag or a loaded config is verified.
func TestServeConfigLoadFailureMarksControlURLUnverified(t *testing.T) {
	for _, tc := range []struct {
		name           string
		configErr      error
		flagURL        string
		wantURL        string
		wantUnverified bool
	}{
		{name: "config.json unreadable", configErr: errors.New("parse config.json: invalid character"), wantURL: "", wantUnverified: true},
		{name: "config.json unreadable but flag given", configErr: errors.New("parse config.json: invalid character"), flagURL: "https://flag.example.com", wantURL: "https://flag.example.com"},
		{name: "config.json loaded", wantURL: "https://headscale.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockServeDefaults(t, dir)
			serveLoadGlobalFn = func() (config.GlobalConfig, error) {
				if tc.configErr != nil {
					return config.GlobalConfig{}, tc.configErr
				}
				return config.GlobalConfig{ControlURL: "https://headscale.example.com"}, nil
			}
			srv := &mockServerControlURLTrust{}
			var gotURL string
			serveNewServerFn = func(authKey, controlURL string) (serverRunner, error) {
				gotURL = controlURL
				return srv, nil
			}
			cmd := findServeCmd(t)
			if tc.flagURL != "" {
				if err := cmd.Flags().Set("control-url", tc.flagURL); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cmd.Flags().Set("control-url", "") })
			}
			logs := captureSlog(t)

			if err := cmd.RunE(cmd, nil); err != nil {
				t.Fatalf("serve refused to start: %v", err)
			}
			if !srv.runCalled || gotURL != tc.wantURL {
				t.Fatalf("server run=%v control URL=%q, want run with %q", srv.runCalled, gotURL, tc.wantURL)
			}
			if got := len(srv.unverified) == 1 && srv.unverified[0]; got != tc.wantUnverified {
				t.Fatalf("SetControlURLUnverified calls = %v, want unverified=%v", srv.unverified, tc.wantUnverified)
			}
			configPath, err := config.ConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			warned := false
			for _, line := range strings.Split(logs(), "\n") {
				if strings.Contains(line, "level=WARN") && strings.Contains(line, "config.json could not be loaded") && strings.Contains(line, configPath) {
					warned = true
				}
			}
			if warned != tc.wantUnverified {
				t.Fatalf("warning naming %s logged=%v, want %v; log:\n%s", configPath, warned, tc.wantUnverified, logs())
			}
		})
	}
}
