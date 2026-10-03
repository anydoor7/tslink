package server

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/authmode"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/anydoor7/tslink/internal/testenv"
	"github.com/anydoor7/tslink/internal/testenv/localapitest"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
)

// tierProbe drives daemon processes over one config directory. It counts
// every destructive or remote step an identity transition can take, and
// records the service and auth key each node was constructed with. Every
// node comes up as an enrolled node does: a Tier 1 node through the
// interactive Start path, a Tier 2 node through Up. Its status carries the
// Funnel capability, as it does when the tailnet policy grants funnel to the
// node, so a --no-auto-provision public service serves on either tier.
type tierProbe struct {
	mu          sync.Mutex
	removed     []string
	cleanups    int
	constructed []registry.Service
	authKeys    []string
	now         atomic.Int64
}

type tierStatusClient struct{}

func (tierStatusClient) Status(context.Context) (*ipnstate.Status, error) {
	return tierRunningStatus(), nil
}

func tierRunningStatus() *ipnstate.Status {
	status := funnelEnabledStatus("app.tailnet.ts.net.")
	status.BackendState = ipn.Running.String()
	status.TailscaleIPs = []netip.Addr{netip.MustParseAddr("100.64.0.9")}
	return status
}

var tierBase = time.Date(2030, 8, 31, 12, 0, 0, 0, time.UTC)

func instrumentTiers(t *testing.T) *tierProbe {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	p := &tierProbe{}
	p.now.Store(tierBase.UnixNano())
	oldNow := serverNowFn
	serverNowFn = func() time.Time { return time.Unix(0, p.now.Load()).UTC() }
	oldRemove := removeServiceStateDirFn
	removeServiceStateDirFn = func(cfgDir, name string) error {
		p.mu.Lock()
		p.removed = append(p.removed, name)
		p.mu.Unlock()
		return oldRemove(cfgDir, name)
	}
	oldNew := newTSNetServerFn
	newTSNetServerFn = func(svc registry.Service, _, authKey, _ string) tsnetServer {
		p.mu.Lock()
		p.constructed = append(p.constructed, svc)
		p.authKeys = append(p.authKeys, authKey)
		p.mu.Unlock()
		if authKey == "" {
			return &fakeInteractiveTSNetServer{fakeTSNetServer: fakeTSNetServer{localClient: localapitest.NewClient(nil), certDomains: []string{"app.tailnet.ts.net"}}}
		}
		return &fakeTSNetServer{localClient: localapitest.NewClient(nil), status: tierRunningStatus(), certDomains: []string{"app.tailnet.ts.net"}}
	}
	oldStatus := tsnetStatusClientFn
	tsnetStatusClientFn = func(tsnetServer) (tsnetStatusClient, error) { return tierStatusClient{}, nil }
	oldPoll := interactiveStatusPollInterval
	interactiveStatusPollInterval = time.Millisecond
	oldInterval := lifecycleTickerInterval
	lifecycleTickerInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		serverNowFn = oldNow
		removeServiceStateDirFn = oldRemove
		newTSNetServerFn = oldNew
		tsnetStatusClientFn = oldStatus
		interactiveStatusPollInterval = oldPoll
		lifecycleTickerInterval = oldInterval
	})
	return p
}

// reset forgets what earlier daemon processes did.
func (p *tierProbe) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.removed, p.cleanups, p.constructed, p.authKeys = nil, 0, nil, nil
}

func (p *tierProbe) observed() (removed []string, cleanups int, constructed []registry.Service, authKeys []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.removed...), p.cleanups, append([]registry.Service(nil), p.constructed...), append([]string(nil), p.authKeys...)
}

// newTierServer wires a server the way `tslink serve` does for the tier:
// Tier 1 has no stored credential, so every service resolves an empty auth
// key; Tier 2 resolves a key per service. The lifecycle reconciler persists
// Funnel expiry through the real registry downgrade, as the daemon's does.
func newTierServer(t *testing.T, p *tierProbe, credentialed bool) *Server {
	t.Helper()
	authKey := ""
	if credentialed {
		authKey = "tskey-auth-tier2"
	}
	s, err := New(authKey, "")
	if err != nil {
		t.Fatal(err)
	}
	s.SetCredentialed(credentialed)
	s.SetAuthHandoffFunc(func(context.Context, AuthHandoff) error { return nil })
	s.SetCleanupStaleNodesFn(func(context.Context, []tailapi.CleanupTarget) (tailapi.CleanupResult, error) {
		p.mu.Lock()
		p.cleanups++
		p.mu.Unlock()
		return tailapi.CleanupResult{}, nil
	})
	s.SetEnsureFunnelAttrFn(func(context.Context, tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
		return tailapi.PolicyMutationResult{}, errors.New("a --no-auto-provision service must not reach the Funnel policy")
	})
	regPath := mustRegistryPath(t)
	s.SetLifecycleReconcileFn(func(_ context.Context, now time.Time) (bool, error) {
		expired, err := registry.DowngradeExpiredFunnels(regPath, now, false)
		return len(expired) > 0, err
	})
	return s
}

func mustRegistryPath(t *testing.T) string {
	t.Helper()
	path, err := config.RegistryPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// runDaemonStart runs one daemon process through its real start path (the
// initial lifecycle reconciliation, then the initial sync) and stops it once
// it is ready.
func runDaemonStart(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ready := false
	s.SetReadyFunc(func() error {
		ready = true
		cancel()
		return nil
	})
	if err := s.Run(ctx); err != nil || !ready {
		t.Fatalf("daemon start: err=%v ready=%v", err, ready)
	}
}

// The startup fixture includes registry, identity and OS scheduling work.
// Pin Funnel's inner policy independently of that fixture's hang guard.
func TestFunnelWaitKeepsIndependentTenSecondDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		parent, cancelParent := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelParent()
		wait, cancel, budget := boundedFunnelWait(parent, funnelCapabilityWaitTimeout)
		defer cancel()
		deadline, ok := wait.Deadline()
		if !ok || time.Until(deadline) != 10*time.Second || budget != 10*time.Second {
			t.Fatalf("installed Funnel wait = %v (present=%v, reported=%v), want exactly 10s", time.Until(deadline), ok, budget)
		}
	})
}

// markEnrolled writes the file tsnet keeps its enrollment in, as a completed
// Up or browser authorization leaves it.
func markEnrolled(t *testing.T) string {
	t.Helper()
	marker := filepath.Join(config.NodesDirIn(mustConfigDir(t)), "app", "tailscaled.state")
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("enrolled"), 0o600); err != nil {
		t.Fatal(err)
	}
	return marker
}

func enrollmentKept(marker string) bool {
	data, err := os.ReadFile(marker)
	return err == nil && string(data) == "enrolled"
}

func recordedIdentity(t *testing.T) nodeIdentity {
	t.Helper()
	recorded, found, err := readNodeIdentity(filepath.Join(mustConfigDir(t), "node-identities", "app.json"))
	if err != nil || !found {
		t.Fatalf("identity record: found=%v err=%v", found, err)
	}
	return recorded
}

// A public service with a Funnel deadline, shared with --no-auto-provision so
// the policy preflight is skipped: the one way a Tier 1 daemon serves Funnel.
func expiringPublicService(expires time.Time) registry.Service {
	return registry.Service{
		Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"},
		Funnel: true, PublicAck: true, NoAutoProvision: true, FunnelExpiresAt: &expires,
	}
}

// waitForPrivateNode waits until the lifecycle ticker has rebuilt the running
// node without Funnel.
func waitForPrivateNode(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.RLock()
		node := s.nodes["app"]
		private := node != nil && !node.service.Funnel
		s.mu.RUnlock()
		if private {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the expired Funnel was not rebuilt as a tailnet-only node")
		}
		time.Sleep(time.Millisecond)
	}
}

// expireFunnel enrolls a public service, lets its Funnel deadline pass, and
// applies the expiry the way the daemon does: the real registry downgrade at
// the next start's lifecycle reconciliation, or on the lifecycle ticker of the
// daemon that is serving it.
func expireFunnel(t *testing.T, p *tierProbe, credentialed, hotReload bool) string {
	t.Helper()
	expires := tierBase.Add(time.Hour)
	writeRegistry(t, []registry.Service{expiringPublicService(expires)})
	if hotReload {
		s := newTierServer(t, p, credentialed)
		t.Cleanup(s.closeAllNodes)
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatalf("public start: %v", err)
		}
		marker := markEnrolled(t)
		p.reset()
		p.now.Store(expires.Add(time.Minute).UnixNano())
		ctx, cancel := context.WithCancel(context.Background())
		done := s.startLifecycleTicker(ctx)
		defer func() {
			cancel()
			<-done
			s.closeAllNodes()
		}()
		waitForPrivateNode(t, s)
		return marker
	}
	runDaemonStart(t, newTierServer(t, p, credentialed))
	marker := markEnrolled(t)
	p.reset()
	p.now.Store(expires.Add(time.Minute).UnixNano())
	runDaemonStart(t, newTierServer(t, p, credentialed))
	return marker
}

func assertFunnelDowngradedOnDisk(t *testing.T) {
	t.Helper()
	reg, err := registry.Load(mustRegistryPath(t))
	if err != nil || len(reg.Services) != 1 || reg.Services[0].Funnel {
		t.Fatalf("registry after expiry = %+v err=%v, want the real downgrade to have persisted Funnel off", reg, err)
	}
}

// ORC-T1: a Tier 1 node is built with no advertised tags (newTSNetServer), so
// its tailnet identity is user-owned and untagged. Funnel expiry changes only
// the derived tag:tslink-funnel, which that node never carried, so it must
// keep its enrollment instead of asking for a new browser authorization.
func TestNodeIdentityTier1FunnelExpiryKeepsEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hotReload bool
	}{{"at start", false}, {"on hot reload", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := instrumentTiers(t)
			marker := expireFunnel(t, p, false, tc.hotReload)
			removed, cleanups, constructed, _ := p.observed()
			assertFunnelDowngradedOnDisk(t)
			if len(constructed) != 1 || constructed[0].Funnel {
				t.Fatalf("constructions after expiry = %+v, want one tailnet-only node", constructed)
			}
			if !enrollmentKept(marker) || len(removed) != 0 || cleanups != 0 {
				t.Fatalf("Tier 1 Funnel expiry reset the untagged enrollment: kept=%v removal calls=%v cleanup calls=%d", enrollmentKept(marker), removed, cleanups)
			}
			if recorded := recordedIdentity(t); joinTags(recorded.Tags) != "tag:web" {
				t.Fatalf("recorded tags = %v, want the requested [tag:web] for a later Tier 2 start", recorded.Tags)
			}
		})
	}
}

// Control for the test above, same harness and same expiry: a Tier 2 node
// enrolled with tag:tslink-funnel, so dropping that tag is a new identity and
// still resets.
func TestNodeIdentityTier2FunnelExpiryStillResetsEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hotReload bool
	}{{"at start", false}, {"on hot reload", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := instrumentTiers(t)
			marker := expireFunnel(t, p, true, tc.hotReload)
			removed, cleanups, constructed, _ := p.observed()
			assertFunnelDowngradedOnDisk(t)
			if len(constructed) != 1 || constructed[0].Funnel {
				t.Fatalf("constructions after expiry = %+v, want one tailnet-only node", constructed)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) || strings.Join(removed, ",") != "app" || cleanups != 1 {
				t.Fatalf("Tier 2 Funnel expiry kept the tagged enrollment: stat=%v removal calls=%v cleanup calls=%d", err, removed, cleanups)
			}
			if recorded := recordedIdentity(t); joinTags(recorded.Tags) != "tag:web" {
				t.Fatalf("recorded tags = %v, want [tag:web]", recorded.Tags)
			}
		})
	}
}

// changeRegistered enrolls a Tier 1 private service, then applies mutate the
// way the CLI does (registry.MutateService, which `tslink tags set` uses) and
// lets a fresh start or the running daemon's hot reload pick it up.
func changeRegistered(t *testing.T, p *tierProbe, hotReload bool, mutate func(registry.Service) registry.Service) string {
	t.Helper()
	writeRegistry(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}}})
	s := newTierServer(t, p, false)
	t.Cleanup(s.closeAllNodes)
	if hotReload {
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatalf("first sync: %v", err)
		}
	} else {
		runDaemonStart(t, s)
	}
	marker := markEnrolled(t)
	p.reset()
	if _, err := registry.MutateService(mustRegistryPath(t), "app", func(svc registry.Service) (registry.Service, error) {
		return mutate(svc), nil
	}); err != nil {
		t.Fatal(err)
	}
	if hotReload {
		// The registry watcher runs exactly this sync on a registry write.
		if err := s.syncNodes(context.Background()); err != nil {
			t.Fatalf("hot reload: %v", err)
		}
		s.closeAllNodes()
	} else {
		runDaemonStart(t, newTierServer(t, p, false))
	}
	return marker
}

func TestNodeIdentityTier1TagsSetKeepsEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hotReload bool
	}{{"at start", false}, {"on hot reload", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := instrumentTiers(t)
			marker := changeRegistered(t, p, tc.hotReload, func(svc registry.Service) registry.Service {
				svc.Tags = []string{"tag:new"}
				return svc
			})
			removed, cleanups, constructed, _ := p.observed()
			if len(constructed) != 1 || joinTags(constructed[0].Tags) != "tag:new" {
				t.Fatalf("constructions after tags set = %+v, want one node with the new tags", constructed)
			}
			if !enrollmentKept(marker) || len(removed) != 0 || cleanups != 0 {
				t.Fatalf("Tier 1 tags set reset the untagged enrollment: kept=%v removal calls=%v cleanup calls=%d", enrollmentKept(marker), removed, cleanups)
			}
			if recorded := recordedIdentity(t); joinTags(recorded.Tags) != "tag:new" {
				t.Fatalf("recorded tags = %v, want the requested [tag:new] for a later Tier 2 start", recorded.Tags)
			}
		})
	}
}

// Tier 1 still builds its node with the ephemeral flag and the control URL,
// so a change of either is a new identity and resets as before.
func TestNodeIdentityTier1EphemeralAndControlURLChangesStillReset(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(registry.Service) registry.Service
		check  func(nodeIdentity) bool
	}{
		{"ephemeral", func(svc registry.Service) registry.Service {
			svc.Ephemeral = true
			return svc
		}, func(recorded nodeIdentity) bool { return recorded.Ephemeral }},
		{"control URL", func(svc registry.Service) registry.Service {
			svc.ControlURL = "https://headscale.example.com"
			return svc
		}, func(recorded nodeIdentity) bool { return recorded.ControlURL == "https://headscale.example.com" }},
	} {
		for _, tc := range []struct {
			name      string
			hotReload bool
		}{{"at start", false}, {"on hot reload", true}} {
			t.Run(change.name+" "+tc.name, func(t *testing.T) {
				p := instrumentTiers(t)
				marker := changeRegistered(t, p, tc.hotReload, change.mutate)
				removed, cleanups, constructed, _ := p.observed()
				if len(constructed) != 1 {
					t.Fatalf("constructions after the change = %+v, want one", constructed)
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) || strings.Join(removed, ",") != "app" || cleanups != 1 {
					t.Fatalf("Tier 1 %s change kept the old enrollment: stat=%v removal calls=%v cleanup calls=%d", change.name, err, removed, cleanups)
				}
				if recorded := recordedIdentity(t); !change.check(recorded) || joinTags(recorded.Tags) != "tag:old" {
					t.Fatalf("recorded identity = %+v, want the new %s", recorded, change.name)
				}
			})
		}
	}
}

// Login moves Tier 1 to Tier 2. The credential-upgrade path clears the
// untagged state; the record a Tier 1 Funnel expiry kept must then match the
// Tier 2 request, so the first credentialed start adds no identity reset or
// remote cleanup of its own.
func TestNodeIdentityTier1RecordStaysCorrectForLaterTier2Start(t *testing.T) {
	p := instrumentTiers(t)
	marker := expireFunnel(t, p, false, false)
	if !enrollmentKept(marker) {
		t.Fatal("fixture: Tier 1 expiry did not keep the enrollment")
	}
	if err := authmode.MarkCredentialUpgradePending(); err != nil {
		t.Fatal(err)
	}
	p.reset()
	runDaemonStart(t, newTierServer(t, p, true))
	removed, cleanups, constructed, authKeys := p.observed()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) || strings.Join(removed, ",") != "app" {
		t.Fatalf("login did not clear the Tier 1 state exactly once through the upgrade: stat=%v removal calls=%v", err, removed)
	}
	if cleanups != 0 {
		t.Fatalf("the first Tier 2 start saw an identity change after the upgrade: cleanup calls=%d", cleanups)
	}
	if len(constructed) != 1 || joinTags(constructed[0].Tags) != "tag:web" || authKeys[0] == "" {
		t.Fatalf("Tier 2 construction = %+v keys=%q, want one credentialed node with [tag:web]", constructed, authKeys)
	}
	if pending, err := authmode.CredentialUpgradePending(); err != nil || pending {
		t.Fatalf("credential upgrade still pending=%v err=%v", pending, err)
	}
}

// Logout removes every node's state and keeps the records. A Tier 1 restart
// over a Tier 2 record must not reset, whether or not the tags the record
// names still match (38881a5 already kept the matching case).
func TestNodeIdentityTier1RestartAfterLogoutDoesNotReset(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expire bool
	}{{"unchanged service", false}, {"Funnel expired while logged out", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := instrumentTiers(t)
			expires := tierBase.Add(time.Hour)
			writeRegistry(t, []registry.Service{expiringPublicService(expires)})
			runDaemonStart(t, newTierServer(t, p, true))
			markEnrolled(t)
			if err := os.RemoveAll(config.NodesDirIn(mustConfigDir(t))); err != nil {
				t.Fatal(err)
			}
			p.reset()
			if tc.expire {
				p.now.Store(expires.Add(time.Minute).UnixNano())
			}
			runDaemonStart(t, newTierServer(t, p, false))
			removed, cleanups, constructed, authKeys := p.observed()
			if len(removed) != 0 || cleanups != 0 {
				t.Fatalf("Tier 1 restart after logout reset: removal calls=%v cleanup calls=%d", removed, cleanups)
			}
			if len(constructed) != 1 || authKeys[0] != "" || constructed[0].Funnel == tc.expire {
				t.Fatalf("Tier 1 construction = %+v keys=%q, want one interactive node", constructed, authKeys)
			}
		})
	}
}

// The hot-reload log reports whether a restart changes the node's auth
// identity. A Tier 1 node advertises no tags, so a tag change is not one
// there; the ephemeral flag still is. Tier 2 compares tags as before.
func TestAuthIdentityChangedIgnoresTagsOnTier1(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	base := registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}}
	retagged := base
	retagged.Tags = []string{"tag:new"}
	ephemeral := base
	ephemeral.Ephemeral = true
	for _, credentialed := range []bool{false, true} {
		s, err := New("", "")
		if err != nil {
			t.Fatal(err)
		}
		s.SetCredentialed(credentialed)
		if got := s.authIdentityChanged(base, retagged); got != credentialed {
			t.Fatalf("credentialed=%v: tag change auth_identity_changed=%v, want %v", credentialed, got, credentialed)
		}
		if !s.authIdentityChanged(base, ephemeral) {
			t.Fatalf("credentialed=%v: ephemeral change auth_identity_changed=false, want true", credentialed)
		}
	}
}

// A Tier 1 tag change whose record cannot be updated fails that start, as any
// record write before start does, and still resets nothing.
func TestNodeIdentityTier1RecordWriteFailureKeepsEnrollment(t *testing.T) {
	p := instrumentTiers(t)
	writeRegistry(t, []registry.Service{{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:old"}}})
	runDaemonStart(t, newTierServer(t, p, false))
	marker := markEnrolled(t)
	p.reset()
	oldWrite := writeNodeIdentityFn
	writeNodeIdentityFn = func(string, nodeIdentity) error { return errors.New("synthetic record write failure") }
	t.Cleanup(func() { writeNodeIdentityFn = oldWrite })
	retagged := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:new"}}
	_, err := newTierServer(t, p, false).prepareNodeIdentity(context.Background(), retagged)
	if err == nil || !strings.Contains(err.Error(), "synthetic record write failure") {
		t.Fatalf("prepareNodeIdentity error = %v, want the record write failure", err)
	}
	removed, cleanups, _, _ := p.observed()
	if !enrollmentKept(marker) || len(removed) != 0 || cleanups != 0 {
		t.Fatalf("record write failure reset the enrollment: kept=%v removal calls=%v cleanup calls=%d", enrollmentKept(marker), removed, cleanups)
	}
	if recorded := recordedIdentity(t); joinTags(recorded.Tags) != "tag:old" {
		t.Fatalf("recorded tags = %v, want the old record left unchanged", recorded.Tags)
	}
}

// On Tier 2 a Funnel toggle changes the tags the node is built with (the
// derived tag:tslink-funnel), and the node is reset, so the restart log must
// say so. On Tier 1 it changes nothing the node advertises.
func TestAuthIdentityChangedCountsTheDerivedFunnelTagOnTier2(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	private := registry.Service{Name: "a", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:web"}}
	public := private
	public.Funnel, public.PublicAck = true, true
	for _, credentialed := range []bool{false, true} {
		s, err := New("", "")
		if err != nil {
			t.Fatal(err)
		}
		s.SetCredentialed(credentialed)
		for _, change := range [][2]registry.Service{{private, public}, {public, private}} {
			if got := s.authIdentityChanged(change[0], change[1]); got != credentialed {
				t.Fatalf("credentialed=%v: Funnel %v -> %v auth_identity_changed=%v, want %v", credentialed, change[0].Funnel, change[1].Funnel, got, credentialed)
			}
		}
	}
}

// Turning Funnel on is the other direction of the expiry above: the untagged
// Tier 1 node keeps its enrollment and is rebuilt with its public listener.
func TestNodeIdentityTier1FunnelEnableKeepsEnrollment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hotReload bool
	}{{"at start", false}, {"on hot reload", true}} {
		t.Run(tc.name, func(t *testing.T) {
			p := instrumentTiers(t)
			expires := tierBase.Add(24 * time.Hour)
			marker := changeRegistered(t, p, tc.hotReload, func(svc registry.Service) registry.Service {
				svc.Funnel, svc.PublicAck, svc.NoAutoProvision = true, true, true
				svc.FunnelExpiresAt = &expires
				return svc
			})
			removed, cleanups, constructed, _ := p.observed()
			if len(constructed) != 1 || !constructed[0].Funnel {
				t.Fatalf("constructions after enabling Funnel = %+v, want one public node", constructed)
			}
			if !enrollmentKept(marker) || len(removed) != 0 || cleanups != 0 {
				t.Fatalf("Tier 1 Funnel enable reset the untagged enrollment: kept=%v removal calls=%v cleanup calls=%d", enrollmentKept(marker), removed, cleanups)
			}
			if recorded := recordedIdentity(t); joinTags(recorded.Tags) != "tag:old,tag:tslink-funnel" {
				t.Fatalf("recorded tags = %v, want the requested [tag:old tag:tslink-funnel] for a later Tier 2 start", recorded.Tags)
			}
		})
	}
}
