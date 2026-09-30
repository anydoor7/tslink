package cmd

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"
	"time"
)

func TestMCPAddFunnelRenewalAgreesWithPublishedHint(t *testing.T) {
	paths := mcpSharePaths(t)
	oldEnsure := ensureDaemonFn
	t.Cleanup(func() { ensureDaemonFn = oldEnsure })
	ensureDaemonFn = func(context.Context, io.Writer, bool) error { return nil }
	actions := defaultMCPActions(paths, io.Discard)
	add := actions.add
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	actions.add = func(ctx context.Context, params AddParams, preserve bool) (any, error) {
		params.Now = now
		now = now.Add(time.Minute)
		return add(ctx, params, preserve)
	}
	args := json.RawMessage(`{"name":"pub","type":"proxy","target":"localhost:3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	first, err := callMCPTool(context.Background(), actions, "add", args)
	if err != nil || first.IsError {
		t.Fatalf("first call: %+v, %v", first, err)
	}
	expiry := *mcpLoadService(t, paths.Registry, "pub").FunnelExpiresAt
	second, err := callMCPTool(context.Background(), actions, "add", args)
	if err != nil || second.IsError {
		t.Fatalf("second call: %+v, %v", second, err)
	}
	renewed := *mcpLoadService(t, paths.Registry, "pub").FunnelExpiresAt
	if renewed.Sub(expiry) != time.Minute {
		t.Fatalf("identical call did not renew expiry: %s -> %s", expiry, renewed)
	}
	fields := mcpResultStructured(t, second)["replaced_fields"].([]any)
	if !slices.Contains(fields, any("funnel_expires_at")) {
		t.Fatalf("renewal not reported: %v", fields)
	}
	stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`), fakeMCPActions())
	frame := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))
	tools := frame["result"].(map[string]any)["tools"].([]any)
	found := 0
	for _, raw := range tools {
		tool := raw.(map[string]any)
		if tool["name"] != "add" && tool["name"] != "share" {
			continue
		}
		found++
		if tool["annotations"].(map[string]any)["idempotentHint"] != false {
			t.Errorf("%s publishes idempotentHint=true despite Funnel renewal", tool["name"])
		}
	}
	if found != 2 {
		t.Fatalf("found %d hints, want 2", found)
	}
}
