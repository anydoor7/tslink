package cmd

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// operatorDisabledFunnelShare shares port 3000 through Funnel and then turns
// Funnel off the way `tslink add port-3000 --proxy <target>` without --funnel
// does: buildService without funnel, written by AddWithOutcome with the
// deadline preserved. The record is tailnet-only with public_ack cleared and
// keeps the old deadline, which is then moved into the past.
func operatorDisabledFunnelShare(t *testing.T, actions mcpActions, regPath string) registry.Service {
	t.Helper()
	callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	reg, err := registry.Load(regPath)
	if err != nil || len(reg.Services) != 1 {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
	shared := reg.Services[0]
	svc, err := buildService(AddParams{Name: shared.Name, Proxy: shared.Target, Ephemeral: shared.Ephemeral})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.AddWithOutcome(regPath, svc, registry.AddOptions{PreserveFunnelExpiry: true}); err != nil {
		t.Fatal(err)
	}
	if reg, err = registry.Load(regPath); err != nil || len(reg.Services) != 1 || !sameDeadline(reg.Services[0].FunnelExpiresAt, shared.FunnelExpiresAt) {
		t.Fatalf("add without --funnel did not preserve the share's deadline: %+v err=%v", reg, err)
	}
	expireShareFunnel(t, regPath, shared.Name, false)
	if reg, err = registry.Load(regPath); err != nil || len(reg.Services) != 1 {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
	disabled := reg.Services[0]
	if disabled.Funnel || disabled.PublicAck || disabled.FunnelExpiresAt == nil || disabled.FunnelExpiresAt.After(time.Now()) || !sameShareBackend(disabled, shared) {
		t.Fatalf("operator-disabled record = %+v, want the share's backend, tailnet-only, public_ack cleared and an expired deadline", disabled)
	}
	return disabled
}

// TestTailnetShareReusesAnOperatorDisabledFunnelRecord pins README's promise
// that retrying a share reuses its service, for a target whose Funnel the
// operator switched off. The record is exactly the tailnet-only posture a
// plain share asks for; an expired deadline it still carries must not make it
// read as a Funnel share.
func TestTailnetShareReusesAnOperatorDisabledFunnelRecord(t *testing.T) {
	t.Run("mcp share", func(t *testing.T) {
		actions, regPath := shareMCPWireActions(t)
		operatorDisabledFunnelShare(t, actions, regPath)
		before, _ := readRegistryBytes(t, filepath.Dir(regPath))
		result := callMCPShare(t, actions, `{"target":"3000"}`)
		if result["name"] != "port-3000" || result["status"] != "ready" {
			t.Fatalf("tailnet-only share = %v, want the operator-disabled port-3000 reused and ready", result)
		}
		for _, field := range []string{"funnel_rearmed", "funnel_expires_at"} {
			if _, ok := result[field]; ok {
				t.Fatalf("tailnet-only reuse reported %s: %v", field, result)
			}
		}
		if after, _ := readRegistryBytes(t, filepath.Dir(regPath)); !bytes.Equal(after, before) {
			t.Fatalf("a reuse changed the registry:\nbefore %s\nafter  %s", before, after)
		}
	})
	t.Run("cli share", func(t *testing.T) {
		actions, regPath := shareMCPWireActions(t)
		operatorDisabledFunnelShare(t, actions, regPath)
		before, _ := readRegistryBytes(t, filepath.Dir(regPath))
		// The request `tslink share 3000` builds: it has no Funnel flags.
		result, err := executeShare(context.Background(), sharePaths{Registry: regPath}, shareRequest{Target: "3000", Ephemeral: true}, time.Second, io.Discard)
		if err != nil || result.Name != "port-3000" || result.Status != "ready" || result.FunnelRearmed {
			t.Fatalf("tslink share 3000 = %+v err=%v, want the operator-disabled port-3000 reused and ready", result, err)
		}
		if after, _ := readRegistryBytes(t, filepath.Dir(regPath)); !bytes.Equal(after, before) {
			t.Fatalf("a reuse changed the registry:\nbefore %s\nafter  %s", before, after)
		}
	})
}

// TestFunnelShareDoesNotRearmAnOperatorDisabledRecord is the security
// boundary next to the reuse above: a Funnel share of a target whose Funnel
// the operator switched off conflicts, turns nothing back on and writes
// nothing, with or without public_ack. Only the daemon's own downgrade, which
// keeps public_ack, is re-armed by an identical retry.
func TestFunnelShareDoesNotRearmAnOperatorDisabledRecord(t *testing.T) {
	const postureConflict = `cannot reuse service \"port-3000\" for this share target: its exposure posture is funnel=false`
	for _, tc := range []struct{ name, arguments string }{
		{"identical funnel share", `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`},
		{"funnel share without public_ack", `{"target":"3000","funnel":true,"funnel_ttl":"1h"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actions, regPath := shareMCPWireActions(t)
			operatorDisabledFunnelShare(t, actions, regPath)
			before, _ := readRegistryBytes(t, filepath.Dir(regPath))
			stdout := runMCPSession(t, initializedMCPInput(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"share","arguments":`+tc.arguments+`}}`), actions)
			result, _ := mcpFrameByID(t, decodeMCPResponses(t, stdout), float64(2))["result"].(map[string]any)
			if result == nil || result["isError"] != true || !strings.Contains(stdout, postureConflict) || strings.Contains(stdout, "funnel_rearmed") {
				t.Fatalf("funnel share of an operator-disabled record = %s, want a posture conflict", stdout)
			}
			if after, _ := readRegistryBytes(t, filepath.Dir(regPath)); !bytes.Equal(after, before) {
				t.Fatalf("a refused funnel share changed the registry:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	t.Run("daemon downgrade is still re-armed", func(t *testing.T) {
		actions, regPath := shareMCPWireActions(t)
		const arguments = `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`
		callMCPShare(t, actions, arguments)
		expireShareFunnel(t, regPath, "port-3000", true)
		reg, err := registry.Load(regPath)
		if err != nil || len(reg.Services) != 1 || reg.Services[0].Funnel || !reg.Services[0].PublicAck {
			t.Fatalf("downgraded record = %+v err=%v, want tailnet-only with public_ack kept", reg, err)
		}
		again := callMCPShare(t, actions, arguments)
		if again["name"] != "port-3000" || again["funnel_rearmed"] != true {
			t.Fatalf("identical funnel share after the daemon downgrade = %v, want port-3000 re-armed", again)
		}
		reg, err = registry.Load(regPath)
		if err != nil || len(reg.Services) != 1 || !reg.Services[0].Funnel || reg.Services[0].FunnelExpiresAt == nil || time.Until(*reg.Services[0].FunnelExpiresAt) < 59*time.Minute {
			t.Fatalf("re-armed record = %+v err=%v, want Funnel on with a fresh 1h deadline", reg, err)
		}
	})
}

// TestShareRequestedExposureReadsOnlyADaemonDowngradeForAFunnelRequest pins
// the reuse helper's contract on both records above. Only a Funnel request
// reads a downgraded record as the Funnel share it was, and only when the
// record kept public_ack, as the daemon's downgrade does. Every other pairing
// compares the record as stored.
func TestShareRequestedExposureReadsOnlyADaemonDowngradeForAFunnelRequest(t *testing.T) {
	funnel := shareIntentForRegression(t, shareRequest{Funnel: true, PublicAck: true, FunnelTTL: "1h", FunnelTTLSet: true})
	tailnet := shareIntentForRegression(t, shareRequest{})

	actions, regPath := shareMCPWireActions(t)
	callMCPShare(t, actions, `{"target":"3000","funnel":true,"public_ack":true,"funnel_ttl":"1h"}`)
	expireShareFunnel(t, regPath, "port-3000", true)
	reg, err := registry.Load(regPath)
	if err != nil || len(reg.Services) != 1 {
		t.Fatalf("registry = %+v err=%v", reg, err)
	}
	downgraded := reg.Services[0]
	actions, regPath = shareMCPWireActions(t)
	disabled := operatorDisabledFunnelShare(t, actions, regPath)

	now := time.Now()
	for _, tc := range []struct {
		name       string
		existing   registry.Service
		spec       shareTargetSpec
		wantFunnel bool
	}{
		{"daemon downgrade, funnel request", downgraded, funnel, true},
		{"daemon downgrade, tailnet-only request", downgraded, tailnet, false},
		{"operator-disabled, funnel request", disabled, funnel, false},
		{"operator-disabled, tailnet-only request", disabled, tailnet, false},
	} {
		want := tc.existing
		want.Funnel = tc.wantFunnel
		if got := shareRequestedExposure(tc.existing, tc.spec, now); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: read as %+v, want %+v", tc.name, got, want)
		}
	}
}
