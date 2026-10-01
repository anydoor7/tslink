package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

// mintedTestKey stands for an auth key the credentialed provider mints for the
// owner's tailnet through api.tailscale.com. tsnet sends whatever key it gets in
// the registration request to the node's control URL.
const mintedTestKey = "tskey-auth-MINTED-FOR-OWNER-TAILNET"

// codeCredentialControlURLMismatch is the wire value of the per-service issue;
// it is spelled out here because the string is the contract.
const codeCredentialControlURLMismatch = "credential_control_url_mismatch"

type mintedKeyConstruction struct {
	authKey    string
	controlURL string
}

// mintedKeyProbe records every call to the credentialed auth-key provider and
// every tsnet node the daemon constructs, per service.
type mintedKeyProbe struct {
	mu            sync.Mutex
	providerCalls map[string]int
	constructed   map[string][]mintedKeyConstruction
}

func (p *mintedKeyProbe) calls(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.providerCalls[name]
}

func (p *mintedKeyProbe) nodes(name string) []mintedKeyConstruction {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]mintedKeyConstruction(nil), p.constructed[name]...)
}

func mintedKeyTCPService(name string, port int, controlURL string) registry.Service {
	return registry.Service{Name: name, Type: registry.TypeTCP, Target: "127.0.0.1:5432", Port: port, Tags: []string{"tag:tsmain"}, ControlURL: controlURL}
}

// newCredentialedDaemonForMintedKeys builds a daemon wired the way cmd/serve.go
// wires a credentialed (Tier 2) daemon: New with no static key, the stored
// credential tier, and a provider that mints a key per service.
func newCredentialedDaemonForMintedKeys(t *testing.T, globalControlURL string, services []registry.Service) (*Server, *mintedKeyProbe) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, services)
	probe := &mintedKeyProbe{providerCalls: map[string]int{}, constructed: map[string][]mintedKeyConstruction{}}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _ string, authKey, controlURL string) tsnetServer {
		probe.mu.Lock()
		probe.constructed[svc.Name] = append(probe.constructed[svc.Name], mintedKeyConstruction{authKey: authKey, controlURL: controlURL})
		probe.mu.Unlock()
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("", globalControlURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	s.SetCredentialed(true)
	s.SetAuthKeyProvider(func(_ context.Context, svc registry.Service) (string, error) {
		probe.mu.Lock()
		probe.providerCalls[svc.Name]++
		probe.mu.Unlock()
		return mintedTestKey, nil
	})
	return s, probe
}

func assertMintedKeyRefused(t *testing.T, s *Server, probe *mintedKeyProbe, name, controlURL string) {
	t.Helper()
	if calls := probe.calls(name); calls != 0 {
		t.Fatalf("auth-key provider called %d time(s) for %q, whose control server is %s; a key minted for the owner's tailnet must never be requested for it", calls, name, controlURL)
	}
	for _, node := range probe.nodes(name) {
		if node.authKey == mintedTestKey {
			t.Fatalf("tsnet node for %q constructed with the minted key and control URL %q", name, node.controlURL)
		}
	}
	if s.nodeRunning(name) {
		t.Fatalf("service %q is running although its control server is %s", name, controlURL)
	}
	s.mu.RLock()
	failure, failed := s.serviceFailures[name]
	s.mu.RUnlock()
	if !failed || failure.Error == nil || failure.Error.Code != codeCredentialControlURLMismatch {
		t.Fatalf("service %q failure = %+v (recorded %v), want per-service issue %s", name, failure.Error, failed, codeCredentialControlURLMismatch)
	}
	if !strings.Contains(failure.Error.Message, controlURL) || len(failure.Error.Next) == 0 {
		t.Fatalf("issue for %q = %+v, want the control URL named and next steps", name, failure.Error)
	}
}

func assertMintedKeyUsed(t *testing.T, s *Server, probe *mintedKeyProbe, name, wantControlURL string) {
	t.Helper()
	if calls := probe.calls(name); calls != 1 {
		t.Fatalf("auth-key provider called %d time(s) for %q, want 1", calls, name)
	}
	nodes := probe.nodes(name)
	if len(nodes) != 1 || nodes[0].authKey != mintedTestKey || nodes[0].controlURL != wantControlURL {
		t.Fatalf("tsnet nodes for %q = %+v, want one with the minted key and control URL %q", name, nodes, wantControlURL)
	}
	if !s.nodeRunning(name) {
		t.Fatalf("service %q is not running", name)
	}
}

// A per-service control_url is an ordinary registry field that the MCP add
// tool can set. In a credentialed daemon it must not make the daemon hand a
// tailnet-joining key to that host, while a service on Tailscale's control
// server in the same daemon still starts with its minted key.
func TestCredentialedDaemonNeverSendsMintedKeyToServiceControlURL(t *testing.T) {
	const foreign = "https://control.example.invalid"
	s, probe := newCredentialedDaemonForMintedKeys(t, "", []registry.Service{
		mintedKeyTCPService("exfil", 5001, foreign),
		mintedKeyTCPService("plain", 5002, ""),
		mintedKeyTCPService("synonym", 5003, "https://controlplane.tailscale.com"),
		mintedKeyTCPService("login", 5004, "https://login.tailscale.com"),
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	assertMintedKeyRefused(t, s, probe, "exfil", foreign)
	assertMintedKeyUsed(t, s, probe, "plain", "")
	assertMintedKeyUsed(t, s, probe, "synonym", "https://controlplane.tailscale.com")
	assertMintedKeyUsed(t, s, probe, "login", "https://login.tailscale.com")
}

// The global control URL (serve --control-url or config.json) is the fallback
// for every service without its own, so it gets the same refusal.
func TestCredentialedDaemonNeverSendsMintedKeyToGlobalControlURL(t *testing.T) {
	const foreign = "https://control.example.invalid"
	s, probe := newCredentialedDaemonForMintedKeys(t, foreign, []registry.Service{
		mintedKeyTCPService("app", 5001, ""),
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	assertMintedKeyRefused(t, s, probe, "app", foreign)
}

// tsnet resolves an empty control URL from TS_CONTROL_URL before its built-in
// default, so an empty control URL is Tailscale's only when that is unset.
func TestCredentialedDaemonNeverSendsMintedKeyToTSControlURLEnvironment(t *testing.T) {
	const foreign = "https://control.example.invalid"
	t.Setenv("TS_CONTROL_URL", foreign)
	s, probe := newCredentialedDaemonForMintedKeys(t, "", []registry.Service{
		mintedKeyTCPService("app", 5001, ""),
	})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	assertMintedKeyRefused(t, s, probe, "app", foreign)
}

// Direct callers of startNodeLocked pass the same gate: the provider is never
// reached for a non-Tailscale control server.
func TestStartNodeLockedNeverRequestsMintedKeyForForeignControlURL(t *testing.T) {
	const foreign = "https://control.example.invalid"
	s, probe := newCredentialedDaemonForMintedKeys(t, "", nil)
	err := s.startNodeLocked(context.Background(), mintedKeyTCPService("direct", 5001, foreign))
	if code, ok := registry.ErrorCode(err); !ok || code != codeCredentialControlURLMismatch {
		t.Fatalf("startNodeLocked() error = %v (code %q), want %s", err, code, codeCredentialControlURLMismatch)
	}
	if calls := probe.calls("direct"); calls != 0 || len(probe.nodes("direct")) != 0 {
		t.Fatalf("provider calls = %d, tsnet nodes = %+v, want neither", calls, probe.nodes("direct"))
	}
}

// A key the user supplied is not minted by TSLink; the static key given to New
// keeps reaching the control server the user configured, as before.
func TestUserSuppliedStaticKeyStillReachesItsControlServer(t *testing.T) {
	const headscale = "https://headscale.example.com"
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{mintedKeyTCPService("app", 5001, "")})
	var got []mintedKeyConstruction
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(_ registry.Service, _ string, authKey, controlURL string) tsnetServer {
		got = append(got, mintedKeyConstruction{authKey: authKey, controlURL: controlURL})
		return &fakeTSNetServer{}
	}
	t.Cleanup(func() { newTSNetServerFn = oldNew })
	s, err := New("tskey-auth-ISSUED-BY-HEADSCALE", headscale)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeAllNodes)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if len(got) != 1 || got[0].authKey != "tskey-auth-ISSUED-BY-HEADSCALE" || got[0].controlURL != headscale || !s.nodeRunning("app") {
		t.Fatalf("tsnet nodes = %+v running=%v, want the user's key sent to %s", got, s.nodeRunning("app"), headscale)
	}
}

// serve declares the legacy authkey file, a key the user supplied, and the
// daemon keeps sending that key to the control server the user configured.
func TestDeclaredUserSuppliedProviderKeyStillReachesItsControlServer(t *testing.T) {
	const headscale = "https://headscale.example.com"
	s, probe := newCredentialedDaemonForMintedKeys(t, headscale, []registry.Service{mintedKeyTCPService("app", 5001, "")})
	s.SetUserSuppliedAuthKey(true)
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	nodes := probe.nodes("app")
	if probe.calls("app") != 1 || len(nodes) != 1 || nodes[0].controlURL != headscale || !s.nodeRunning("app") {
		t.Fatalf("provider calls = %d, nodes = %+v, running = %v; want the user's key sent to %s", probe.calls("app"), nodes, s.nodeRunning("app"), headscale)
	}
}

// Pointing a running service at another control server stops it before any
// identity reset: its node state stays, so pointing it back resumes the same
// node instead of enrolling a new one.
func TestForeignControlURLStopsRunningNodeWithoutIdentityReset(t *testing.T) {
	const foreign = "https://control.example.invalid"
	s, probe := newCredentialedDaemonForMintedKeys(t, "", []registry.Service{mintedKeyTCPService("app", 5001, "")})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("initial syncNodes() error = %v", err)
	}
	if !s.nodeRunning("app") {
		t.Fatal("control: app did not start on Tailscale's control server")
	}
	marker := filepath.Join(config.NodesDirIn(mustConfigDir(t)), "app", "tailscaled.state")
	if err := os.WriteFile(marker, []byte("enrolled"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{mintedKeyTCPService("app", 5001, foreign)})
	if err := s.syncNodes(context.Background()); err != nil {
		t.Fatalf("syncNodes() error = %v", err)
	}
	if calls, nodes := probe.calls("app"), probe.nodes("app"); calls != 1 || len(nodes) != 1 {
		t.Fatalf("provider calls = %d, tsnet nodes = %+v; want only the first start's", calls, nodes)
	}
	if s.nodeRunning("app") {
		t.Fatalf("app still runs after its control URL became %s", foreign)
	}
	s.mu.RLock()
	failure := s.serviceFailures["app"]
	s.mu.RUnlock()
	if failure.Error == nil || failure.Error.Code != codeCredentialControlURLMismatch {
		t.Fatalf("app failure = %+v, want %s", failure.Error, codeCredentialControlURLMismatch)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("node state of a refused service was reset: %v", err)
	}
}

// The MCP control-plane node uses the global control URL and the same
// provider, so it is refused the same way; the daemon then refuses to start.
func TestMCPControlPlaneNeverRequestsMintedKeyForForeignControlURL(t *testing.T) {
	const foreign = "https://control.example.invalid"
	s, probe := newCredentialedDaemonForMintedKeys(t, foreign, nil)
	s.SetMCPControlPlane(&MCPControlPlane{
		Tags:         []string{"tag:ops"},
		AllowedUsers: []string{"alice@example.com"},
		Handler:      &mcpProbeHandler{},
	})
	err := s.startMCPControlPlane(context.Background())
	t.Cleanup(s.closeMCPControlPlane)
	if code, ok := registry.ErrorCode(err); !ok || code != codeCredentialControlURLMismatch {
		t.Fatalf("startMCPControlPlane() error = %v (code %q), want %s", err, code, codeCredentialControlURLMismatch)
	}
	if calls := probe.calls(DefaultMCPNodeName); calls != 0 || len(probe.nodes(DefaultMCPNodeName)) != 0 {
		t.Fatalf("provider calls = %d, tsnet nodes = %+v for the control plane, want neither", calls, probe.nodes(DefaultMCPNodeName))
	}
}
