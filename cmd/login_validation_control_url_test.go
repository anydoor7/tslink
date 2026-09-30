package cmd

import (
	"testing"

	"tailscale.com/ipn"
)

// TestClientSecretValidationNodeUsesTailscaleControl is B1V-1: login mints a
// real, preauthorized key with the candidate OAuth client secret and brings
// up a validation node with it. That node left ControlURL empty, so tsnet
// took TS_CONTROL_URL from the environment and sent a key minted for the
// owner's tailnet to whatever server it named. The credential is a Tailscale
// API credential, so the node always registers with Tailscale's control
// server.
func TestClientSecretValidationNodeUsesTailscaleControl(t *testing.T) {
	t.Setenv("TS_CONTROL_URL", "https://evil.example")
	srv := newClientSecretValidationServer(t.TempDir(), "tskey-auth-synthetic", []string{"tag:tsmain"})
	if srv.ControlURL != ipn.DefaultControlURL {
		t.Fatalf("validation node ControlURL = %q, want %q whatever TS_CONTROL_URL says", srv.ControlURL, ipn.DefaultControlURL)
	}
	// Control: the node is the one login builds, with the key it was given.
	if srv.AuthKey != "tskey-auth-synthetic" || srv.Hostname != clientSecretValidationHostname {
		t.Fatalf("validation node = %q/%q, want the login validation node", srv.Hostname, srv.AuthKey)
	}
}
