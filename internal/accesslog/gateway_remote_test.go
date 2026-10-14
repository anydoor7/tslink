package accesslog

import "testing"

func TestGatewayRemoteDenyReasonsRemainTyped(t *testing.T) {
	for _, reason := range []string{"remote_upstream_requires_tag", "remote_upstream_outside_tailnet", "remote_upstream_unavailable", "remote_upstream_not_peer"} {
		e := Event{Kind: "http", Via: "portal", App: "media", Decision: "denied", Reason: reason}
		got := sanitize(e)
		if got.Reason != reason || got.App != "media" || got.Via != "portal" {
			t.Fatal(got)
		}
	}
}
