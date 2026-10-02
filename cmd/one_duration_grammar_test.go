package cmd

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
)

func TestParseDurationIsGoSyntaxPlusDays(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"7d":       7 * durationDay,
		"168h":     7 * durationDay,
		"1d12h":    36 * time.Hour,
		"1.5d":     36 * time.Hour,
		"90d":      90 * durationDay,
		"15m":      15 * time.Minute,
		"300ms":    300 * time.Millisecond,
		" 20s ":    20 * time.Second,
		"-1d":      -durationDay,
		"0":        0,
		"2h45m30s": 2*time.Hour + 45*time.Minute + 30*time.Second,
	} {
		got, err := parseDuration(input)
		if err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "d", "7", "7x", "7dd", "1.2.3d", "never", "d7", "7d?"} {
		if got, err := parseDuration(input); err == nil {
			t.Errorf("parseDuration(%q) = %v, want an error", input, got)
		}
	}
	// TSLink prints durations (funnel_remaining, log windows) with
	// time.Duration.String, so every such value parses back.
	for _, d := range []time.Duration{0, time.Second, 167*time.Hour + 59*time.Minute + 59*time.Second, 7 * durationDay, 1500 * time.Millisecond} {
		if got, err := parseDuration(d.String()); err != nil || got != d {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", d.String(), got, err, d)
		}
	}
}

// Duration inputs read Go syntax plus days; Funnel TTL is a fixed choice.
func TestDurationGrammarAndFunnelChoices(t *testing.T) {
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

	// Funnel accepts flexible relative lifetimes under the shared policy.
	for spelling, want := range map[string]time.Duration{"7d": 7 * durationDay, "24h": 24 * time.Hour, "72h": 72 * time.Hour, "1h": time.Hour, "2h": 2 * time.Hour, "6d": 6 * durationDay, "167h59m59s": 7*durationDay - time.Second, "168h": 7 * durationDay, "1d": durationDay, "60m": time.Hour} {
		got, never, err := registry.ParseFunnelTTL(spelling)
		if err != nil || never || got != want {
			t.Errorf("funnel_ttl %q = %v never=%v err=%v; want %v", spelling, got, never, err, want)
		}
	}
	for _, refused := range []string{"soon", "7d1s", "59m", "never"} {
		if _, _, err := registry.ParseFunnelTTL(refused); err == nil || !strings.Contains(err.Error(), "valid examples:") {
			t.Errorf("funnel_ttl %q: %v, want the list of choices", refused, err)
		}
	}
	if _, never, err := registry.ParseFunnelTTL("never"); err == nil || never {
		t.Errorf("funnel_ttl never = %v, %v", never, err)
	}
	// Over the protocol: add with funnel_ttl 7d records a seven-day Funnel.
	restoreShareSeams(t)
	paths := mcpSharePaths(t)
	oldEnsure := ensureDaemonFn
	t.Cleanup(func() { ensureDaemonFn = oldEnsure })
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	result, err := callMCPTool(context.Background(), defaultMCPActions(paths, io.Discard), "add",
		json.RawMessage(`{"name":"pub","type":"proxy","target":"localhost:3000","funnel":true,"public_ack":true,"funnel_ttl":"7d"}`))
	if err != nil || result.IsError {
		t.Fatalf("add funnel_ttl 7d: %+v, %v", result, err)
	}
	if svc := mcpLoadService(t, paths.Registry, "pub"); svc.FunnelExpiresAt == nil || time.Until(*svc.FunnelExpiresAt) < 7*durationDay-time.Minute {
		t.Fatalf("funnel_expires_at = %v, want seven days from now", svc.FunnelExpiresAt)
	}

	// login --expires-in.
	for spelling, want := range map[string]time.Duration{"90d": 90 * durationDay, "2160h": 90 * durationDay, "30d12h": 30*durationDay + 12*time.Hour} {
		if got, err := parseLoginExpiresIn(spelling); err != nil || got != want {
			t.Errorf("--expires-in %q = %v, %v; want %v", spelling, got, err, want)
		}
	}

	// The event stream keepalive, within 5s..5m.
	if keepalive, err := parseMCPEventsKeepalive("0.001d"); err != nil || keepalive != 86400*time.Millisecond {
		t.Errorf("events_keepalive 0.001d = %v, %v", keepalive, err)
	}

	// Printed durations round-trip through the grammar, but are not TTL choices.
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(7 * durationDay)
	remaining := registry.FunnelRemainingAt(registry.Service{Name: "pub", Type: registry.TypeProxy, Funnel: true, PublicAck: true, FunnelExpiresAt: &deadline}, now)
	if remaining == nil || *remaining != "168h0m0s" {
		t.Fatalf("funnel_remaining = %v, want 168h0m0s", remaining)
	}
	if got, err := parseDuration(*remaining); err != nil || got != 7*durationDay {
		t.Errorf("printed duration %q = %v, %v; want 7d", *remaining, got, err)
	}
	if got, _, err := registry.ParseFunnelTTL(*remaining); err != nil || got != 7*durationDay {
		t.Errorf("funnel_ttl %q: %v, want the list of choices", *remaining, err)
	}
	if _, err := resolveMCPLogsQuery(mcpLogsArguments{Since: *remaining}); err != nil {
		t.Errorf("logs since %q (a funnel_remaining value): %v", *remaining, err)
	}
}
