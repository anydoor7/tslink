package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tailscale.com/tsnet"
)

// installFakeLsof puts an lsof on PATH that only records that it ran, and
// returns a func reporting how many times it did.
func installFakeLsof(t *testing.T) func() int {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho x >> " + calls + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
	return func() int {
		data, err := os.ReadFile(calls)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "x\n")
	}
}

// TestNodeLocalClientSkipsHostDaemonAuthDiscovery pins W0130: every LocalAPI
// call on a node (the per-request WhoIs, the per-handshake GetCertificate)
// must stay in process. A plain local.Client on macOS first runs
// safesocket.LocalTCPPortAndToken, which forks lsof under a process-wide lock
// to find the Tailscale app; under launchd /usr/sbin is on PATH, so that cost
// 15-20 ms on every request through every node.
func TestNodeLocalClientSkipsHostDaemonAuthDiscovery(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("host LocalAPI auth discovery runs lsof only on macOS")
	}
	lsofCalls := installFakeLsof(t)
	raw := &tsnet.Server{Dir: t.TempDir(), Hostname: "w0130-localclient", Ephemeral: true, Logf: func(string, ...any) {}}
	srv := &helloTrackingServer{Server: raw}
	defer CloseTSNetServer("w0130-localclient", srv)

	ctx := context.Background()
	lc, err := srv.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	if !lc.OmitAuth || lc.Dial == nil {
		t.Fatalf("node LocalAPI client: OmitAuth=%v, in-memory Dial set=%v; want true, true", lc.OmitAuth, lc.Dial != nil)
	}
	if again, _ := srv.LocalClient(); again != lc {
		t.Fatal("a node must reuse one LocalAPI client so its connections are pooled")
	}
	if _, err := lc.StatusWithoutPeers(ctx); err != nil {
		t.Fatalf("node LocalAPI unreachable through the wrapped client: %v", err)
	}
	if n := lsofCalls(); n != 0 {
		t.Fatalf("node LocalAPI call ran lsof %d times; want 0", n)
	}

	// Control: tsnet's own client on the same node does run the discovery,
	// so a zero above means the wrapper removed it, not that the probe is
	// blind.
	base, err := raw.LocalClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.StatusWithoutPeers(ctx); err != nil {
		t.Fatal(err)
	}
	if n := lsofCalls(); n == 0 {
		t.Fatal("control: tsnet's default client did not run lsof; the probe cannot tell the fix apart")
	}
}
