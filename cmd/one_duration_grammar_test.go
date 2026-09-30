package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/duration"
	"github.com/monody0007/tslink/internal/registry"
)

// TestEveryDurationInputSpeaksOneGrammar is A3-5's probe: MCP logs since
// "7d" failed with time: unknown unit "d" while funnel_ttl accepted 7d and
// funnel_remaining printed 168h0m0s, which funnel_ttl refused. Every duration
// input now reads Go syntax plus d for days, each within its own bounds, and
// reads back what funnel_remaining prints.
func TestEveryDurationInputSpeaksOneGrammar(t *testing.T) {
	// MCP logs since: the default ceiling is 168h, spelled either way.
	for _, since := range []string{"7d", "168h", "1d12h"} {
		if _, err := resolveMCPLogsQuery(mcpLogsArguments{Since: since}); err != nil {
			t.Errorf("logs since %q: %v", since, err)
		}
	}
	if _, err := resolveMCPLogsQuery(mcpLogsArguments{Since: "8d"}); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("logs since 8d: %v, want the ceiling named", err)
	}

	// MCP url wait: the cap applies whatever the spelling.
	if wait, err := parseMCPWait("0.001d"); err != nil || wait != 86400*time.Millisecond {
		t.Errorf("wait 0.001d = %v, %v", wait, err)
	}
	if _, err := parseMCPWait("1d"); err == nil || !strings.Contains(err.Error(), "exceeds the maximum") {
		t.Errorf("wait 1d: %v, want the cap named rather than a parse error", err)
	}

	// The CLI --wait flags the url tool mirrors read the same grammar, and
	// still report themselves as duration flags.
	for _, name := range []string{"add", "share", "url"} {
		command, _, err := rootCmd.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		flag := command.Flags().Lookup("wait")
		if flag.Value.Type() != "duration" {
			t.Errorf("%s --wait type = %s", name, flag.Value.Type())
		}
		if err := command.Flags().Set("wait", "0.001d"); err != nil {
			t.Errorf("%s --wait 0.001d: %v", name, err)
		}
		if wait, err := command.Flags().GetDuration("wait"); err != nil || wait != 86400*time.Millisecond {
			t.Errorf("%s --wait 0.001d = %v, %v", name, wait, err)
		}
		if err := command.Flags().Set("wait", flag.DefValue); err != nil {
			t.Fatal(err)
		}
		flag.Changed = false
	}

	// funnel_ttl: the same five choices, in either spelling.
	for spelling, want := range map[string]time.Duration{"7d": 7 * duration.Day, "168h": 7 * duration.Day, "1d": 24 * time.Hour, "3d": 72 * time.Hour, "60m": time.Hour} {
		got, never, err := registry.ParseFunnelTTL(spelling)
		if err != nil || never || got != want {
			t.Errorf("funnel_ttl %q = %v never=%v err=%v; want %v", spelling, got, never, err, want)
		}
	}
	if _, _, err := registry.ParseFunnelTTL("2h"); err == nil {
		t.Error("funnel_ttl 2h accepted; the choices are 1h, 8h, 24h, 72h, 7d and never")
	}
	if _, never, err := registry.ParseFunnelTTL("never"); err != nil || !never {
		t.Errorf("funnel_ttl never = %v, %v", never, err)
	}

	// login --expires-in.
	for spelling, want := range map[string]time.Duration{"90d": 90 * duration.Day, "2160h": 90 * duration.Day, "30d12h": 30*duration.Day + 12*time.Hour} {
		if got, err := parseLoginExpiresIn(spelling); err != nil || got != want {
			t.Errorf("--expires-in %q = %v, %v; want %v", spelling, got, err, want)
		}
	}

	// The event stream keepalive, within 5s..5m.
	if keepalive, err := parseMCPEventsKeepalive("0.001d"); err != nil || keepalive != 86400*time.Millisecond {
		t.Errorf("events_keepalive 0.001d = %v, %v", keepalive, err)
	}

	// funnel_remaining is printed in Go's form, which every input reads.
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(7 * duration.Day)
	remaining := registry.FunnelRemainingAt(registry.Service{Name: "pub", Type: registry.TypeProxy, Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}, now)
	if remaining == nil {
		t.Fatal("funnel_remaining = nil")
	}
	if got, _, err := registry.ParseFunnelTTL(*remaining); err != nil || got != 7*duration.Day {
		t.Errorf("funnel_ttl %q (a funnel_remaining value) = %v, %v; want 7d", *remaining, got, err)
	}
	if _, err := resolveMCPLogsQuery(mcpLogsArguments{Since: *remaining}); err != nil {
		t.Errorf("logs since %q (a funnel_remaining value): %v", *remaining, err)
	}
}
