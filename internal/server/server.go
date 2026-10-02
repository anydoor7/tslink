package server

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/authmode"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/errcode"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/logging"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/security"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/fsnotify/fsnotify"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tsnet"
)

type tsnetServer interface {
	Up(context.Context) (*ipnstate.Status, error)
	Listen(network, addr string) (net.Listener, error)
	ListenTLS(network, addr string) (net.Listener, error)
	ListenFunnel(network, addr string, opts ...tsnet.FunnelOption) (net.Listener, error)
	LocalClient() (*LocalClient, error)
	CertDomains() []string
	Close() error
}

func newTSNetServer(svc registry.Service, stateDir, authKey, controlURL string) tsnetServer {
	svc = serviceForNodeConstruction(svc)
	advertiseTags := append([]string(nil), svc.Tags...)
	if authKey == "" {
		// Interactive enrollment creates a user-owned node. Advertising tags on
		// that path would require pre-existing ACL tag ownership and turns a fresh
		// tailnet into a self-imposed first-run failure.
		advertiseTags = nil
	}
	return &tsnet.Server{
		Hostname:      svc.Name,
		Dir:           stateDir,
		AuthKey:       authKey,
		Ephemeral:     svc.Ephemeral,
		ControlURL:    controlURL,
		AdvertiseTags: advertiseTags,
		UserLogf:      logging.TSNetUserLogf,
	}
}

var newTSNetServerFn = newTSNetServer

// CloseTSNetServer releases a tsnet node whose start may not have completed;
// every Close that can follow an incomplete Start or Up goes through it
// (service nodes, the MCP control-plane node, the CLI's client-secret
// validation node), while stopNodeLocked closes only nodes whose start
// completed. tsnet's Close
// dereferences state its start builds part-way through, so closing a node
// whose start failed early panics upstream (tailscale.com v1.102.4,
// tsnet.Server.close; reached for example when os.Executable fails because
// /proc/self/exe is unreadable). That panic must not take down the daemon,
// whose supervisor would restart it into the same failure, nor turn a CLI
// command's start error into a panic trace: it is logged, and the start error
// stays the reported outcome. The log line holds name and the panic value only,
// so name must never be a credential.
func CloseTSNetServer(name string, srv io.Closer) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("tsnet close after a failed start panicked; the node is abandoned", "name", name, "panic", fmt.Sprint(r))
		}
	}()
	_ = srv.Close()
}

var (
	credentialUpgradePendingFn = authmode.CredentialUpgradePending
	clearCredentialUpgradeFn   = authmode.ClearCredentialUpgradePending
)

type tsnetStarter interface {
	Start() error
}

type tsnetStatusClient interface {
	Status(context.Context) (*ipnstate.Status, error)
}

var (
	tsnetStatusClientFn = func(srv tsnetServer) (tsnetStatusClient, error) {
		return srv.LocalClient()
	}
	interactiveStatusPollInterval = 100 * time.Millisecond
	nodeStartupTimeout            = 30 * time.Second
	funnelCapabilityWaitTimeout   = 10 * time.Second
	funnelCapabilityPollInterval  = 250 * time.Millisecond
)

var (
	configDirFn             = config.Dir
	registryPathFn          = config.RegistryPath
	runtimeSnapshotPathFn   = config.RuntimeSnapshotPath
	nodeOwnershipPathFn     = config.NodeOwnershipPath
	runtimeSaveSnapshotFn   = runtimesnapshot.Save
	runtimeRemoveSnapshotFn = runtimesnapshot.Remove
	recordOwnedNodeFn       = runtimesnapshot.RecordOwnedNode
	ownershipRetryWaitFn    = waitForOwnershipRetry
	serverNowFn             = time.Now
	beforeInitialSyncFn     = func(context.Context) error { return nil }
	registryLoadRuntimeFn   = registry.LoadForRuntime
	registryWatchLoadFn     = registry.LoadForRuntime
	afterDesiredLoadedFn    = func(context.Context, uint64) error { return nil }
	observeNodeContextFn    = func(string, context.Context) {}
	serveTCPFn              = serveTCP
	registrySettleDelay     = 50 * time.Millisecond
	lifecycleTickerInterval = 30 * time.Second
	// policyRetryMaxWait caps the spacing of the lifecycle ticker's retries of
	// a failed Funnel policy preflight. Each retry costs one Tailscale policy
	// API request (plus the tag ensure with --manage-acl), and a service can
	// stay blocked for good (tailnet HTTPS disabled, Funnel capability
	// missing): about 2880 requests a day at one per 30 s tick, 96 at the cap.
	policyRetryMaxWait = 15 * time.Minute
)

var ownershipRetryDelays = []time.Duration{time.Second, 5 * time.Second}

func waitForOwnershipRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

const (
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 30 * time.Second
	httpIdleTimeout       = 60 * time.Second
	httpShutdownTimeout   = 5 * time.Second
	httpMaxHeaderBytes    = 64 << 10
	httpMaxRequestBytes   = 32 << 20
	httpMaxActiveConns    = 256
)

var errServerShuttingDown = errors.New("server shutting down")

var newHTTPServerFn = func(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       0,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

var shutdownHTTPServerFn = func(ctx context.Context, srv *http.Server) error {
	return srv.Shutdown(ctx)
}

var closeHTTPServerFn = func(srv *http.Server) error {
	return srv.Close()
}

type limitedListener struct {
	net.Listener
	sem     chan struct{}
	kind    string
	service string
}

func newLimitedListener(ln net.Listener, limit int, kind, service string) net.Listener {
	if limit <= 0 {
		return ln
	}
	return &limitedListener{
		Listener: ln,
		sem:      make(chan struct{}, limit),
		kind:     kind,
		service:  service,
	}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.sem <- struct{}{}:
			limited := &limitedConn{Conn: conn, release: func() { <-l.sem }}
			if tlsConn, ok := conn.(*tls.Conn); ok {
				return &limitedTLSConn{limitedConn: limited, tlsConn: tlsConn}, nil
			}
			return limited, nil
		default:
			slog.Warn("connection limit exceeded; closing accepted connection", "kind", l.kind, "name", l.service)
			_ = conn.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(c.release)
	return err
}

// net/http needs the TLS connection state even when the connection is capped.
// Go 1.26 reads ConnectionState before reading the request, so perform the
// handshake here with a bound; Go 1.27 calls HandshakeContext first.
type limitedTLSConn struct {
	*limitedConn
	tlsConn *tls.Conn
}

func (c *limitedTLSConn) HandshakeContext(ctx context.Context) error {
	return c.tlsConn.HandshakeContext(ctx)
}

func (c *limitedTLSConn) ConnectionState() tls.ConnectionState {
	ctx, cancel := context.WithTimeout(context.Background(), httpReadHeaderTimeout)
	defer cancel()
	_ = c.tlsConn.HandshakeContext(ctx)
	return c.tlsConn.ConnectionState()
}

func ResourceBudgetMiddleware(next http.Handler) http.Handler {
	return RequestLimitsMiddleware(registry.Service{Type: registry.TypeProxy}, nil, next)
}

// instrumentServiceHandler applies the per-request wrappers every HTTP node
// serves through, the access log outermost. The access log is the one status
// recorder on this chain.
func instrumentServiceHandler(serviceName string, identity *IdentityResolver, handler http.Handler) http.Handler {
	handler = ResourceBudgetMiddleware(handler)
	return AccessLogMiddleware(serviceName, identity, handler)
}

type registryWatcher interface {
	Add(string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type fsNotifyRegistryWatcher struct {
	*fsnotify.Watcher
}

func (w *fsNotifyRegistryWatcher) Events() <-chan fsnotify.Event {
	return w.Watcher.Events
}

func (w *fsNotifyRegistryWatcher) Errors() <-chan error {
	return w.Watcher.Errors
}

var newRegistryWatcherFn = func() (registryWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &fsNotifyRegistryWatcher{Watcher: watcher}, nil
}

// ServiceNode represents a single tsnet node serving one service.
type ServiceNode struct {
	tcpDone              <-chan struct{}
	limitWarnings        *serviceLimitWarnings
	tsnetSrv             tsnetServer
	service              registry.Service
	nodeID               string
	runtimeHost          string
	funnelListenerActive bool
	listener             net.Listener
	httpSrv              *http.Server
	handlerCloser        io.Closer
	cancel               context.CancelFunc
	closed               atomic.Bool
}

// EnsureTagsFunc is the signature for ensuring ACL tags exist.
type EnsureTagsFunc func(ctx context.Context, tags []string) error

// EnsureFunnelAttrFunc is the signature for the fused, lossless Funnel policy
// transaction. The request includes ordinary tags only when --manage-acl is
// enabled; the shared Funnel owner and nodeAttrs edits remain default-on.
type EnsureFunnelAttrFunc func(ctx context.Context, request tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error)

// AuthKeyProvider resolves auth material for a service immediately before its tsnet node starts.
type AuthKeyProvider func(ctx context.Context, svc registry.Service) (string, error)

// AuthHandoff is emitted when a credential-free tsnet node needs the user to
// authorize it. AuthURL comes from local.Client.Status, the documented stable
// status API.
type AuthHandoff struct {
	Service string
	AuthURL string
}

// AuthHandoffFunc receives credential-free interactive enrollment events.
type AuthHandoffFunc func(context.Context, AuthHandoff) error

// CleanupStaleNodesFunc removes stale tailnet nodes for service targets before forced reauth.
type CleanupStaleNodesFunc func(ctx context.Context, targets []tailapi.CleanupTarget) (tailapi.CleanupResult, error)

// LifecycleReconcileFunc persists wall-clock expiration and reconciles remote
// resources. The bool reports whether registry.json changed.
type LifecycleReconcileFunc func(context.Context, time.Time) (bool, error)

// Server manages multiple tsnet nodes, one per registered service.
type Server struct {
	accessWriter             accesslog.Writer
	accessOptions            accesslog.Options
	lastAccessHealth         accesslog.Health
	lastGuestCounterWarnings map[string]inspect.WarningView
	nodes                    map[string]*ServiceNode
	// stateReservations counts, per service, the startups in progress that
	// may write into its tsnet state directory; see reserveNodeState. It is
	// guarded by mu, like nodes.
	stateReservations       map[string]int
	serviceFailures         map[string]runtimesnapshot.ServiceState
	globalFailure           *runtimesnapshot.ServiceError
	authKey                 string
	authKeyProvider         AuthKeyProvider
	authKeyUserSupplied     bool
	credentialed            bool
	controlURL              string
	controlURLUnverified    bool
	mu                      sync.RWMutex
	cfgDir                  string
	ensureTagsFn            EnsureTagsFunc
	ensureFunnelAttrFn      EnsureFunnelAttrFunc
	autoProvisionFunnel     bool
	authHandoffFn           AuthHandoffFunc
	cleanupNodesFn          CleanupStaleNodesFunc
	lifecycleReconcileFn    LifecycleReconcileFunc
	lastSyncFailed          atomic.Bool
	identityRetryPending    atomic.Bool
	policyRetry             policyRetryBackoff
	shuttingDown            atomic.Bool
	syncGeneration          atomic.Uint64
	reconcileGate           chan struct{}
	startupCancelMu         sync.Mutex
	startupCancel           context.CancelFunc
	startupGeneration       uint64
	lastRegistryFingerprint string
	lastSnapshotComplete    bool
	// Watcher callbacks compare against the exact state of a successful sync,
	// including isolated decode errors. A partial or failed sync is not applied.
	appliedWatchFingerprint string
	appliedWatchFile        os.FileInfo
	inFlightWatchTarget     *watchedRegistryTarget
	registryWatchGate       atomic.Uint32
	syncResultMu            sync.Mutex
	latestSyncResult        syncResult
	syncResultChanged       chan struct{}
	daemonPID               int
	daemonStartedAt         time.Time
	readyFn                 func() error
	mcpControlPlane         *MCPControlPlane
	mcpNode                 *mcpControlPlaneNode
	// events fans runtime-state changes out to open control-plane event
	// streams. It is always present so publishing is unconditional and cannot
	// be skipped by a code path that forgot to check whether anyone is
	// listening; with no subscribers, publish is a locked map walk over zero
	// entries.
	events *eventHub
	// credentialStateDigest is the last observed digest of the credential
	// files in the config directory. It exists so a filesystem event that did
	// not change the credential state does not become a frame on every open
	// stream.
	credentialStateMu     sync.Mutex
	credentialStateKnown  bool
	credentialStateDigest string
	healthStates          map[string]serviceHealth
	healthProbePool       *healthReadPool
	healthNodePool        *healthReadPool
	alerts                health.AlertsView
	runtimeSnapshotDirty  bool
}

// New creates a new multi-node server.
func New(authKey, controlURL string) (*Server, error) {
	cfgDir, err := configDirFn()
	if err != nil {
		return nil, err
	}
	return &Server{
		nodes:               make(map[string]*ServiceNode),
		serviceFailures:     make(map[string]runtimesnapshot.ServiceState),
		reconcileGate:       make(chan struct{}, 1),
		syncResultChanged:   make(chan struct{}),
		authKey:             authKey,
		authKeyProvider:     staticAuthKeyProvider(authKey),
		authKeyUserSupplied: authKey != "",
		credentialed:        authKey != "",
		controlURL:          controlURL,
		cfgDir:              cfgDir,
		cleanupNodesFn:      tailapi.CleanupStaleNodesResult,
		ensureFunnelAttrFn:  tailapi.EnsureFunnelAttr,
		autoProvisionFunnel: true,
		daemonPID:           os.Getpid(),
		daemonStartedAt:     time.Now().UTC(),
		events:              newEventHub(),
	}, nil
}

// AccessLogWriter returns the owning daemon's typed nonblocking writer. It is
// nil before Run has initialized storage or when local storage is unavailable.
func (s *Server) AccessLogWriter() accesslog.Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.accessWriter
}

// SetEnsureTagsFn sets the function called to ensure ACL tags before starting nodes.
func (s *Server) SetEnsureTagsFn(fn EnsureTagsFunc) {
	s.ensureTagsFn = fn
}

// SetEnsureFunnelAttrFn sets the function used for automatic Funnel policy
// provisioning.
func (s *Server) SetEnsureFunnelAttrFn(fn EnsureFunnelAttrFunc) {
	if fn == nil {
		s.ensureFunnelAttrFn = tailapi.EnsureFunnelAttr
		return
	}
	s.ensureFunnelAttrFn = fn
}

// SetAutoProvisionFunnel applies the daemon-wide kill switch. False wins over
// every service's default-on setting without mutating registry.json.
func (s *Server) SetAutoProvisionFunnel(enabled bool) {
	s.autoProvisionFunnel = enabled
}

// SetAuthKeyProvider sets the function used to resolve auth material per service.
// A provider is taken to mint its keys through the Tailscale API until
// SetUserSuppliedAuthKey says otherwise, so call that after this.
func (s *Server) SetAuthKeyProvider(fn AuthKeyProvider) {
	if fn == nil {
		s.authKeyProvider = staticAuthKeyProvider(s.authKey)
		s.authKeyUserSupplied = s.authKey != ""
		return
	}
	s.authKeyProvider = fn
	s.authKeyUserSupplied = false
}

// SetUserSuppliedAuthKey records whether the auth-key provider returns a key
// the user supplied (the legacy authkey file) rather than one minted through
// the Tailscale API with a stored credential. Only a minted key is tied to
// Tailscale's control server; see mintedKeyControlURLError.
func (s *Server) SetUserSuppliedAuthKey(userSupplied bool) {
	s.authKeyUserSupplied = userSupplied
}

// SetControlURLUnverified marks the server's control URL as a default the
// caller fell back to (config.json failed to load) rather than configuration.
// A service without its own control URL then has an unknown control server for
// identity comparison, so a control URL difference alone never resets its node.
func (s *Server) SetControlURLUnverified(unverified bool) {
	s.controlURLUnverified = unverified
}

// SetCredentialed records whether this process is running the stored-
// credential tier. The command layer resolves this independently from the
// per-service auth keys supplied by AuthKeyProvider.
func (s *Server) SetCredentialed(credentialed bool) {
	s.credentialed = credentialed
}

// SetAuthHandoffFunc sets the callback used to publish interactive login URLs.
func (s *Server) SetAuthHandoffFunc(fn AuthHandoffFunc) {
	s.authHandoffFn = fn
}

// SetCleanupStaleNodesFn sets the function used to remove stale tailnet nodes before forced reauth.
func (s *Server) SetCleanupStaleNodesFn(fn CleanupStaleNodesFunc) {
	s.cleanupNodesFn = fn
}

// SetLifecycleReconcileFn configures the shared cleanup/expiration reconciler.
func (s *Server) SetLifecycleReconcileFn(fn LifecycleReconcileFunc) {
	s.lifecycleReconcileFn = fn
}

// SetReadyFunc sets a callback invoked after watcher setup and initial sync succeed.
func (s *Server) SetReadyFunc(fn func() error) {
	s.readyFn = fn
}

func staticAuthKeyProvider(authKey string) AuthKeyProvider {
	return func(context.Context, registry.Service) (string, error) {
		return authKey, nil
	}
}

// Run starts all registered service nodes and watches for registry changes.
func (s *Server) Run(ctx context.Context) error {
	s.shuttingDown.Store(false)
	cfg, cfgErr := config.LoadGlobalConfig()
	if cfgErr != nil {
		return cfgErr
	}
	if cfg.AccessLog != nil {
		s.accessOptions = *cfg.AccessLog
	}

	if path, err := registryPathFn(); err == nil {
		if _, err := registry.ExpirePeople(path, serverNowFn()); err != nil {
			slog.Warn("initial people expiry reconciliation failed", "error", err)
		}
	}
	if s.lifecycleReconcileFn != nil {
		if _, err := s.lifecycleReconcileFn(ctx, serverNowFn()); err != nil {
			s.beginShutdown()
			s.closeAllNodes()
			return fmt.Errorf("initial lifecycle reconciliation: %w", err)
		}
	}
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	watchDone, err := s.startRegistryWatcher(watchCtx)
	if err != nil {
		s.beginShutdown()
		s.closeAllNodes()
		return fmt.Errorf("registry watcher setup failed: %w", err)
	}

	writer := accesslog.NewLifecycle(s.cfgDir, s.accessOptions, serverNowFn)
	s.mu.Lock()
	s.accessWriter = writer
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, false)
	s.mu.Unlock()
	defer func() {
		writer.Close()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-writer.Done():
		case <-timer.C:
			slog.Warn("access log drain timed out")
		}
	}()

	if err := beforeInitialSyncFn(ctx); err != nil {
		s.beginShutdown()
		cancelWatch()
		<-watchDone
		s.closeAllNodes()
		return fmt.Errorf("before initial sync: %w", err)
	}

	// The control plane is started before the registry is applied so that an
	// enabled-but-unauthorized configuration refuses the whole daemon rather
	// than coming up half-exposed. When no control plane is configured this
	// returns without constructing anything.
	if err := s.startMCPControlPlane(ctx); err != nil {
		s.beginShutdown()
		cancelWatch()
		<-watchDone
		s.closeAllNodes()
		return fmt.Errorf("mcp control plane: %w", err)
	}

	if err := s.syncNodesAuthoritative(ctx); err != nil {
		s.beginShutdown()
		cancelWatch()
		<-watchDone
		s.closeAllNodes()
		return fmt.Errorf("initial sync failed: %w", err)
	}
	if s.readyFn != nil {
		if err := s.readyFn(); err != nil {
			s.beginShutdown()
			cancelWatch()
			<-watchDone
			s.closeAllNodes()
			return fmt.Errorf("mark ready: %w", err)
		}
	}
	lifecycleDone := s.startLifecycleTicker(watchCtx)
	healthDone := s.startHealthMonitor(watchCtx)

	<-ctx.Done()
	s.beginShutdown()
	cancelWatch()
	<-lifecycleDone
	<-healthDone
	<-watchDone
	s.closeAllNodes()
	return nil
}

func (s *Server) startLifecycleTicker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	// Read the clock seam on this goroutine, for the same reason the accept loop
	// in startNodeLocked does: the ticker outlives this call, and every test that
	// stubs serverNowFn restores it from t.Cleanup, so a read from inside the
	// goroutine races that restore. TestStartLifecycleTicker_ReadsClockSeamBeforeSpawning
	// pins it from the other side: it starts the ticker, swaps the seam, and
	// requires the tick to carry the value captured here. Capturing the function
	// value costs nothing: production assigns this variable once, at init.
	nowFn := serverNowFn
	peoplePath, _ := registryPathFn()
	go func() {
		defer close(done)
		ticker := time.NewTicker(lifecycleTickerInterval)
		defer ticker.Stop()
		maxPolicyRetryWaitTicks := policyRetryWaitCapTicks(lifecycleTickerInterval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := nowFn()
				if writer, ok := s.AccessLogWriter().(*accesslog.Lifecycle); ok {
					writer.Retry(now)
				}
				if peoplePath != "" {
					if _, err := registry.ExpirePeople(peoplePath, now); err != nil {
						slog.Warn("people expiry reconciliation failed", "error", err)
					}
				}
				shouldSync := s.lifecycleReconcileFn == nil
				if s.lifecycleReconcileFn != nil {
					changed, err := s.lifecycleReconcileFn(ctx, now)
					// Counted on every tick, so the backoff measures ticks since the
					// latest sync whatever made this one run.
					policyRetryDue := s.policyRetry.tick(maxPolicyRetryWaitTicks)
					shouldSync = changed || err != nil || s.lastSyncFailed.Load() || s.identityRetryPending.Load() || policyRetryDue
					if err != nil {
						slog.Warn("lifecycle reconciliation failed; applying in-memory wall-clock guard", "error", err)
					}
				}
				// Rebuild listeners after desired state changed, reconciliation failed,
				// the latest sync failed, or it left a service blocked on a
				// preflight that only a retry can clear (a policy preflight on the
				// backoff schedule, an identity record on every tick). Reconciliation still runs
				// from the wall clock on every tick, so suspend/resume cannot preserve
				// an expired public listener without restoring unconditional sync traffic.
				if shouldSync {
					syncErr := s.syncNodes(ctx)
					if syncErr != nil && !errors.Is(syncErr, context.Canceled) && !errors.Is(syncErr, errServerShuttingDown) {
						slog.Warn("wall-clock lifecycle sync failed", "error", syncErr)
					}
				}
			}
		}
	}()
	return done
}

type syncOutcome struct {
	generation uint64
	committed  bool
	// policyRetry and identityRetry report a sync that succeeded but left a
	// service blocked on a condition only a retry can clear: a failed Funnel
	// policy preflight, or an unreadable node identity record. Nothing else
	// re-runs either check, so the lifecycle ticker must.
	policyRetry   bool
	identityRetry bool
	// registryFingerprint is the registry state the sync ran against; a
	// change restarts the policy retry backoff.
	registryFingerprint string
}

// policyRetryBackoff spaces out the lifecycle ticker's retries of syncs that
// leave a service blocked on a failed Funnel policy preflight. The first retry
// runs on the next tick and each further one waits twice as many ticks, up to
// policyRetryMaxWait. A sync that leaves no policy failure, or that ran against
// a changed registry, starts the schedule over. Retries for an unreadable
// identity record read only local files and are not spaced out.
type policyRetryBackoff struct {
	mu          sync.Mutex
	streak      int    // consecutive policy-blocked syncs against fingerprint
	fingerprint string // registry state the streak counts
	ticks       int    // ticks since the latest recorded sync
}

// record notes the outcome of a sync of the current generation.
func (b *policyRetryBackoff) record(blocked bool, fingerprint string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case !blocked:
		b.streak, b.fingerprint = 0, ""
	case b.streak == 0 || fingerprint != b.fingerprint:
		b.streak, b.fingerprint = 1, fingerprint
	default:
		b.streak++
	}
	b.ticks = 0
}

// tick counts one lifecycle tick and reports whether a policy retry is due.
func (b *policyRetryBackoff) tick(maxWaitTicks int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.streak == 0 {
		return false
	}
	b.ticks++
	return b.ticks >= policyRetryWaitTicks(b.streak, maxWaitTicks)
}

// policyRetryWaitTicks is how many ticks the retry after the streak-th
// consecutive policy-blocked sync waits: 1, 2, 4, ... up to maxWaitTicks.
func policyRetryWaitTicks(streak, maxWaitTicks int) int {
	wait := 1
	for i := 1; i < streak && wait < maxWaitTicks; i++ {
		wait *= 2
	}
	return min(wait, maxWaitTicks)
}

// policyRetryWaitCapTicks converts policyRetryMaxWait into ticks of interval.
func policyRetryWaitCapTicks(interval time.Duration) int {
	if interval <= 0 {
		return 1
	}
	return max(1, int(policyRetryMaxWait/interval))
}

type syncResult struct {
	generation uint64
	committed  bool
	err        error
}

// syncNodes compares registry to running nodes and starts/stops as needed.
func (s *Server) syncNodes(ctx context.Context) error {
	outcome, err := s.syncNodesWithOutcome(ctx, false)
	s.recordSyncOutcome(outcome, err)
	return err
}

func (s *Server) recordSyncOutcome(outcome syncOutcome, err error) {
	if outcome.generation == s.syncGeneration.Load() {
		s.lastSyncFailed.Store(err != nil)
		s.identityRetryPending.Store(outcome.identityRetry)
		s.policyRetry.record(outcome.policyRetry, outcome.registryFingerprint)
	}
}

func (s *Server) syncNodesAuthoritative(ctx context.Context) error {
	for {
		outcome, err := s.syncNodesWithOutcome(ctx, true)
		if outcome.generation == s.syncGeneration.Load() {
			s.lastSyncFailed.Store(err != nil)
			s.identityRetryPending.Store(outcome.identityRetry)
			s.policyRetry.record(outcome.policyRetry, outcome.registryFingerprint)
		}
		if err != nil {
			return err
		}
		if outcome.committed {
			return nil
		}
		latest := s.syncGeneration.Load()
		if latest > outcome.generation {
			result, waitErr := s.waitForSyncResult(ctx, latest)
			if waitErr != nil {
				return waitErr
			}
			if result.err != nil {
				return result.err
			}
			if result.committed {
				return nil
			}
			continue
		}
		if err := s.ensureRunning(ctx); err != nil {
			return err
		}
		slog.Info("retrying initial registry sync after stale generation", "generation", outcome.generation)
	}
}

// startup marks the authoritative sync that gates daemon start, where any
// sync error closes every node and exits the daemon.
func (s *Server) syncNodesWithOutcome(ctx context.Context, startup bool) (outcome syncOutcome, resultErr error) {
	generation := s.syncGeneration.Add(1)
	return s.syncNodesAtGeneration(ctx, startup, generation)
}

// Watcher decisions reserve their generation before releasing the decision
// gate. A delayed older caller cannot then supersede a newer decision. All
// registry loading, remote work and enrollment happen after that gate is free.
func (s *Server) syncNodesAtGeneration(ctx context.Context, startup bool, generation uint64) (outcome syncOutcome, resultErr error) {
	outcome = syncOutcome{generation: generation}
	defer func() {
		if generation != s.syncGeneration.Load() {
			resultErr = nil // a newer generation owns the startup result
		}
		s.publishSyncResult(syncResult{generation: generation, committed: outcome.committed, err: resultErr})
	}()
	generationCtx, cancelGeneration := context.WithCancel(ctx)
	if !s.installStartupGeneration(generation, cancelGeneration) {
		return outcome, nil
	}
	defer s.finishStartupGeneration(generation, cancelGeneration)

	if err := s.ensureRunning(generationCtx); err != nil {
		return outcome, err
	}

	regPath, err := registryPathFn()
	if err != nil {
		return outcome, err
	}

	// Capture file identity before reading. If a replacement races the load,
	// recording the older identity makes the next check reconcile again rather
	// than declaring a later, unread replacement applied.
	watchFile, _ := os.Stat(regPath)
	reg, registryIssues, err := loadRegistryForRuntimeSettled(generationCtx, regPath)
	if err != nil {
		s.failClosedGlobalRegistryError(generation, err)
		return outcome, err
	}
	registryFingerprint, err := runtimesnapshot.RegistryFingerprint(reg, registryIssues)
	if err != nil {
		return outcome, fmt.Errorf("runtime snapshot registry fingerprint: %w", err)
	}
	outcome.registryFingerprint = registryFingerprint
	watchFingerprint := watchedRegistryFingerprint(registryFingerprint, registryIssues)

	// Build desired state
	desired := make(map[string]registry.Service, len(reg.Services))
	desiredOrder := make([]string, 0, len(reg.Services))
	validationFailures := make(map[string]runtimesnapshot.ServiceState)
	for _, issue := range registryIssues {
		failure := serviceIssueFailure(issue)
		desiredOrder = append(desiredOrder, issue.Name)
		desired[issue.Name] = issue.Service
		validationFailures[issue.Name] = failure
		slog.Warn("registry service failed strict load; isolating service and continuing sync", "name", issue.Name, "code", failure.Error.Code, "error", issue.Err)
	}
	for _, svc := range reg.Services {
		// Re-read the wall clock for every reconciliation. Missing timestamps
		// remain legacy-never; expired services are never allowed into Funnel
		// policy or listener construction even if persisting the downgrade failed.
		svc = registry.EffectiveServiceAt(svc, serverNowFn())
		err := ValidateServiceForStartup(svc)
		if err == nil {
			err = s.mintedKeyControlURLError(svc.Name, effectiveControlURL(svc, s.controlURL))
		}
		if err != nil {
			if failure, recoverable := recoverableServiceFailure(svc, err); recoverable {
				slog.Warn("service validation failed; isolating service and continuing sync", "name", svc.Name, "code", failure.Error.Code, "error", err)
				if _, seen := desired[svc.Name]; !seen {
					desiredOrder = append(desiredOrder, svc.Name)
				}
				desired[svc.Name] = svc
				validationFailures[svc.Name] = failure
				continue
			}
			return outcome, err
		}
		if _, seen := desired[svc.Name]; !seen {
			desiredOrder = append(desiredOrder, svc.Name)
		}
		desired[svc.Name] = svc
	}
	if err := afterDesiredLoadedFn(generationCtx, generation); err != nil {
		if generation != s.syncGeneration.Load() {
			return outcome, nil
		}
		return outcome, err
	}
	select {
	case s.reconcileGate <- struct{}{}:
		defer func() { <-s.reconcileGate }()
	case <-generationCtx.Done():
		if generation != s.syncGeneration.Load() {
			return outcome, nil
		}
		return outcome, generationCtx.Err()
	}
	// An already-running node can predate the identity record (for example,
	// during an in-process upgrade). Capture the service used to construct it
	// before any preflight path can withdraw its listener.
	s.mu.Lock()
	identityFailures := s.recordRunningIdentitiesLocked()
	s.mu.Unlock()

	// Complete every policy read-modify-write before stopping a running node or
	// deleting its enrolled state. A failed Funnel preflight is isolated to the
	// affected services, so an existing private listener and node identity stay
	// intact while unrelated services continue reconciling.
	tagsToEnsure := uniqueDesiredTags(desired, validationFailures)
	policyFailures, provisionOutcomes, fusedTagsEnsured := s.ensureFunnelPolicyBeforeRestart(generationCtx, desired, validationFailures, tagsToEnsure)
	outcome.policyRetry = len(policyFailures) > 0
	if !fusedTagsEnsured && s.ensureTagsFn != nil && len(tagsToEnsure) > 0 {
		if err := ensureTagsBeforeRestart(generationCtx, s.ensureTagsFn, tagsToEnsure); err != nil {
			switch {
			case errors.Is(err, tailapi.ErrNoAPIClient):
				slog.Warn("degraded mode: skipped ACL tag ensure", "reason", err.Error(), "tags", tagsToEnsure, "degraded_mode", true)
			case errors.Is(err, tailapi.ErrPolicyConflict):
				// A concurrent editor won both fresh ETag races. Do not turn an
				// opportunistic, explicitly enabled tag ensure into an all-service
				// startup outage; per-service auth/start errors remain isolated.
				slog.Warn("degraded mode: skipped ACL tag ensure after fresh ETag retry", "reason", err.Error(), "tags", tagsToEnsure, "degraded_mode", true)
			case errors.Is(err, tailapi.ErrPolicyAccessDenied):
				// A stored OAuth client can authenticate tsnet successfully while
				// lacking ACL write scope. Keep startup available and let per-service
				// key/start errors identify any tag that truly cannot be used.
				slog.Warn("degraded mode: skipped ACL tag ensure because policy access was forbidden", "reason", err.Error(), "tags", tagsToEnsure, "degraded_mode", true)
			default:
				// A global policy failure must leave unchanged services alone,
				// but it cannot keep an older public surface or private
				// authorization reachable after the registry changes.
				s.mu.Lock()
				if generation == s.syncGeneration.Load() && s.stopDivergentNodesLocked(desired, validationFailures) {
					s.writeRuntimeSnapshotLocked(registryFingerprint, false)
				}
				s.mu.Unlock()
				return outcome, fmt.Errorf("ensure ACL tags before restart: %w", err)
			}
		}
	}

	s.mu.Lock()
	if err := s.ensureRunning(generationCtx); err != nil {
		s.mu.Unlock()
		return outcome, err
	}
	if generation != s.syncGeneration.Load() {
		s.mu.Unlock()
		slog.Info("skipping stale registry sync generation", "generation", generation)
		return outcome, nil
	}
	s.globalFailure = nil
	s.lastRegistryFingerprint = registryFingerprint
	if err := s.prepareCredentialUpgradeLocked(reg.Services); err != nil {
		if s.stopDivergentNodesLocked(desired, validationFailures) {
			s.writeRuntimeSnapshotLocked(registryFingerprint, false)
		}
		s.mu.Unlock()
		return outcome, err
	}

	// Stop nodes for absent or changed services. The common start path below
	// compares durable identity and resets old state before any replacement Up.
	// An absent service keeps its node state: absence from the registry just
	// loaded is not proof of removal (the file may have been lost, replaced or
	// mistyped). The lifecycle reconciler deletes a removed service's state
	// once the ownership ledger's retired_at, written by `tslink remove`, and
	// the remote side prove it.
	for name, node := range s.nodes {
		svc, exists := desired[name]
		if !exists {
			slog.Info("stopping node whose service is absent from the registry; its node state is kept", "name", name)
			s.stopNodeLocked(name)
		} else if failure, invalid := validationFailures[name]; invalid {
			slog.Warn("stopping node whose service no longer validates", "name", name, "code", failure.Error.Code)
			s.stopNodeLocked(name)
			s.serviceFailures[name] = failure
		} else if identityErr, failed := identityFailures[name]; failed {
			// A failed identity write must not leave a changed handler serving
			// its old allow list or surface. Keep the enrolled node state.
			if serviceChangedWithFallback(node.service, svc, s.controlURL) {
				slog.Warn("closing changed listener after node identity error", "name", name, "error", identityErr)
				s.stopNodeLocked(name)
			}
			s.serviceFailures[name] = nodeIdentityFailure(svc, identityErr)
		} else if failure, blocked := policyFailures[name]; blocked {
			if s.listenerMustWithdraw(node, svc) {
				// Close a changed public surface or private authorization while
				// keeping its local node state for a later retry.
				slog.Warn("closing changed listener after Funnel policy preflight failed", "name", name, "reason", failure.Error.Provision.Reason)
				s.stopNodeLocked(name)
			} else {
				// An unchanged public node or an older private node may keep
				// serving while Funnel enrollment is unavailable.
				slog.Warn("keeping existing node identity because Funnel policy preflight failed", "name", name, "reason", failure.Error.Provision.Reason)
			}
			s.serviceFailures[name] = failure
		} else if serviceChangedWithFallback(node.service, svc, s.controlURL) {
			slog.Info("restarting node", "name", name, "auth_identity_changed", s.authIdentityChanged(node.service, svc))
			s.stopNodeLocked(name)
		} else {
			node.service = svc // Health-only edits do not restart an enrolled node.
		}
	}
	// Prune records of removed services whose state is already gone. State of
	// a service that is not running here, for example a public node withdrawn
	// by a failed preflight, is left to the paths that hold ownership proof.
	removedIdentityErr := s.removeAbsentNodeIdentities(desired)
	for name, failure := range policyFailures {
		s.serviceFailures[name] = failure
	}
	for name, failure := range s.serviceFailures {
		svc, exists := desired[name]
		_, stillPolicyBlocked := policyFailures[name]
		resolvedPolicyFailure := failure.Error != nil && failure.Error.Provision != nil && !stillPolicyBlocked
		// A service that is not running re-reads its record in the start loop
		// below and is marked failed again if the record is still unreadable.
		_, stillIdentityFailed := identityFailures[name]
		resolvedIdentityFailure := isNodeIdentityFailure(failure) && !stillIdentityFailed
		if !exists || resolvedPolicyFailure || resolvedIdentityFailure || serviceChangedWithFallback(failure.Service, svc, s.controlURL) {
			delete(s.serviceFailures, name)
		}
	}
	s.mu.Unlock()

	// Start nodes for new or changed services. Identity record failures are
	// per-service failures recorded above, not sync errors: one unreadable
	// record must not fail the sync and withdraw runtime.json for everyone.
	var startErrs []error
	identityBlocked := len(identityFailures) > 0
	if err := s.ensureRunning(generationCtx); err != nil {
		s.removeRuntimeSnapshot()
		return outcome, errors.Join(removedIdentityErr, err)
	}
	for _, name := range desiredOrder {
		svc := desired[name]
		if _, failed := identityFailures[name]; failed {
			continue
		}
		if s.nodeRunning(name) {
			continue
		}
		if failure, invalid := validationFailures[name]; invalid {
			s.mu.Lock()
			s.serviceFailures[name] = failure
			s.writeRuntimeSnapshotLocked(registryFingerprint, false)
			s.mu.Unlock()
			continue
		}
		if _, blocked := policyFailures[name]; blocked {
			s.mu.Lock()
			s.writeRuntimeSnapshotLocked(registryFingerprint, false)
			s.mu.Unlock()
			continue
		}
		if err := s.ensureRunning(generationCtx); err != nil {
			startErrs = append(startErrs, err)
			break
		}
		cleanupErr, err := s.prepareNodeIdentity(generationCtx, svc)
		var recordErr *nodeIdentityReadError
		if errors.As(err, &recordErr) {
			slog.Error("not starting service; its node identity record cannot be read safely", "name", name, "error", err)
			s.mu.Lock()
			s.serviceFailures[name] = nodeIdentityFailure(svc, err)
			s.writeRuntimeSnapshotLocked(registryFingerprint, false)
			s.mu.Unlock()
			identityBlocked = true
			continue
		}
		if err != nil {
			startErrs = append(startErrs, fmt.Errorf("start service %q: %w", name, err))
			continue
		}
		if cleanupErr != nil && startup {
			// A failed cleanup leaves the old device in place, and refusing
			// to start would not remove it. At daemon start a sync error
			// takes every service down, which buys no safety here.
			slog.Warn("degraded mode: stale tailnet node cleanup failed after old state removal; continuing start", "name", name, "error", cleanupErr, "degraded_mode", true)
		} else if cleanupErr != nil {
			// Preserve the prior sync error signal without letting a remote
			// cleanup outage prevent safe local re-enrollment.
			startErrs = append(startErrs, cleanupErr)
			slog.Warn("remote cleanup failed after old state removal; continuing restart", "name", name, "error", cleanupErr)
		}
		provision := provisionOutcomes[name]
		if err := s.startNodeLocked(generationCtx, svc, provision); err != nil {
			if generation != s.syncGeneration.Load() {
				return outcome, nil
			}
			slog.Error("failed to start node", "name", name, "error", err)
			if failure, recoverable := recoverableServiceFailure(svc, err); recoverable {
				s.mu.Lock()
				s.serviceFailures[name] = failure
				s.writeRuntimeSnapshotLocked(registryFingerprint, false)
				s.mu.Unlock()
				continue
			}
			startErrs = append(startErrs, fmt.Errorf("start service %q: %w", name, err))
			continue
		}
		s.mu.Lock()
		delete(s.serviceFailures, name)
		// Persist each successfully running node before starting the next one.
		// Interactive enrollment is sequential, so this lets status pollers see
		// earlier services as up while the next service is awaiting its login.
		s.writeRuntimeSnapshotLocked(registryFingerprint, false)
		s.mu.Unlock()
	}

	// Retry while a record stays unreadable, so moving it aside takes effect
	// without a registry change or restart.
	outcome.identityRetry = identityBlocked
	syncErr := errors.Join(append([]error{removedIdentityErr}, startErrs...)...)
	if syncErr != nil {
		s.removeRuntimeSnapshot()
		return outcome, syncErr
	}

	// Re-write after a fully successful sync so an empty registry and a sync
	// that required no starts still publish authoritative runtime evidence.
	s.mu.Lock()
	s.writeRuntimeSnapshotLocked(registryFingerprint, true)
	if generation == s.syncGeneration.Load() {
		s.appliedWatchFingerprint = watchFingerprint
		s.appliedWatchFile = watchFile
	}
	s.mu.Unlock()
	outcome.committed = true
	return outcome, nil
}

func uniqueDesiredTags(desired map[string]registry.Service, validationFailures map[string]runtimesnapshot.ServiceState) []string {
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]struct{})
	var tags []string
	for _, name := range names {
		if _, invalid := validationFailures[name]; invalid {
			continue
		}
		for _, tag := range desired[name].Tags {
			if tag == registry.FunnelTag {
				continue
			}
			if _, ok := seen[tag]; ok {
				continue
			}
			seen[tag] = struct{}{}
			tags = append(tags, tag)
		}
	}
	return tags
}

func ensureTagsBeforeRestart(ctx context.Context, ensure EnsureTagsFunc, tags []string) error {
	err := ensure(ctx, tags)
	if !errors.Is(err, tailapi.ErrPolicyConflict) || ctx.Err() != nil {
		return err
	}
	slog.Warn("ACL tag ensure conflicted with a concurrent editor; retrying one fresh read-modify-write", "tags", tags)
	return ensure(ctx, tags)
}

func (s *Server) ensureFunnelPolicyBeforeRestart(
	ctx context.Context,
	desired map[string]registry.Service,
	validationFailures map[string]runtimesnapshot.ServiceState,
	tagsToEnsure []string,
) (map[string]runtimesnapshot.ServiceState, map[string]registry.ProvisionOutcome, bool) {
	failures := make(map[string]runtimesnapshot.ServiceState)
	outcomes := make(map[string]registry.ProvisionOutcome)
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)

	var candidates []registry.Service
	var owners []string
	for _, name := range names {
		svc := desired[name]
		if !svc.Funnel {
			continue
		}
		base := registry.ProvisionOutcome{Target: registry.FunnelTag, WriteOutcome: tailapi.PolicyWriteNotAttempted}
		if !s.autoProvisionFunnel {
			base.Reason = registry.ProvisionReasonDaemonDisabled
			outcomes[name] = base
			continue
		}
		if svc.NoAutoProvision {
			base.Reason = registry.ProvisionReasonServiceDisabled
			outcomes[name] = base
			continue
		}
		if _, invalid := validationFailures[name]; invalid {
			continue
		}
		owner, reason, err := deriveFunnelTagOwner(svc)
		if err != nil {
			base.Reason = reason
			outcomes[name] = base
			failures[name] = funnelProvisionFailure(svc, err, base, funnelOwnerRecovery(svc))
			continue
		}
		candidates = append(candidates, svc)
		if !containsString(owners, owner) {
			owners = append(owners, owner)
		}
	}
	if len(candidates) == 0 {
		return failures, outcomes, false
	}

	request := tailapi.FunnelPolicyRequest{
		Tags:   stringsExcept(tagsToEnsure, registry.FunnelTag),
		Target: registry.FunnelTag,
		Owners: owners,
	}
	provisionCtx, cancel, provisionBudget := boundedProvisionContext(ctx, funnelCapabilityWaitTimeout)
	defer cancel()
	ensure := s.ensureFunnelAttrFn
	if ensure == nil {
		ensure = tailapi.EnsureFunnelAttr
	}
	result, err := ensure(provisionCtx, request)
	tagsEnsured := policyTagEnsurePerformed(result)
	mutationAttempted := policyMutationAttempted(result)
	if errors.Is(err, tailapi.ErrPolicyConflict) && provisionCtx.Err() == nil {
		slog.Warn("Funnel policy provisioning conflicted with a concurrent editor; retrying one fresh read-modify-write", "target", request.Target)
		result, err = ensure(provisionCtx, request)
		tagsEnsured = tagsEnsured || policyTagEnsurePerformed(result)
		mutationAttempted = mutationAttempted || policyMutationAttempted(result)
	}
	if mutationAttempted {
		plan := security.FunnelAutoProvisionPlan(request.Target, request.Owners, true)
		slog.Info("remote ACL mutation plan",
			"plan_id", plan.ID,
			"operation", plan.Operation,
			"default", plan.Default,
			"disable_flag", plan.DisableFlag,
			"resources", plan.Resources,
		)
	}
	for _, svc := range candidates {
		provision := registry.ProvisionOutcome{
			Attempted:    true,
			Target:       request.Target,
			Changed:      result.Changed,
			WriteOutcome: result.WriteOutcome,
		}
		if err == nil {
			if result.Changed {
				provision.Reason = registry.ProvisionReasonPolicyUpdated
			} else {
				provision.Reason = registry.ProvisionReasonPolicySatisfied
			}
			outcomes[svc.Name] = provision
			continue
		}
		failureCause := err
		if errors.Is(provisionCtx.Err(), context.DeadlineExceeded) {
			failureCause = fmt.Errorf("provisioning the Funnel policy exceeded the actual %s request budget: %w", provisionBudget, err)
		}
		if result.WriteOutcome == tailapi.PolicyWriteUnknown {
			provision.Reason = registry.ProvisionReasonWriteUnknown
		} else if errors.Is(err, tailapi.ErrTailnetHTTPSDisabled) {
			provision.Reason = registry.ProvisionReasonHTTPSDisabled
		} else if errors.Is(err, tailapi.ErrTailnetSettingsUnavailable) {
			provision.Reason = registry.ProvisionReasonSettingsUnavailable
		} else {
			provision.Reason = registry.ProvisionReasonEnsureFailed
		}
		outcomes[svc.Name] = provision
		failures[svc.Name] = funnelProvisionFailure(svc, failureCause, provision, funnelPolicyFailureRecovery(svc, provision))
	}
	return failures, outcomes, tagsEnsured
}

func policyTagEnsurePerformed(result tailapi.PolicyMutationResult) bool {
	return result.WriteOutcome != "" && result.WriteOutcome != tailapi.PolicyWriteNotAttempted
}

func policyMutationAttempted(result tailapi.PolicyMutationResult) bool {
	switch result.WriteOutcome {
	case tailapi.PolicyWriteChanged, tailapi.PolicyWriteRejected, tailapi.PolicyWriteUnknown:
		return true
	default:
		return false
	}
}

func boundedProvisionContext(ctx context.Context, configured time.Duration) (context.Context, context.CancelFunc, time.Duration) {
	if configured <= 0 {
		return ctx, func() {}, 0
	}
	provisionCtx, cancel := context.WithTimeout(ctx, configured)
	budget := configured
	if deadline, ok := provisionCtx.Deadline(); ok {
		budget = time.Until(deadline)
		if budget < 0 {
			budget = 0
		}
	}
	return provisionCtx, cancel, budget.Truncate(time.Millisecond)
}

func deriveFunnelTagOwner(svc registry.Service) (string, string, error) {
	for _, tag := range svc.Tags {
		if tag == registry.FunnelTag {
			continue
		}
		if err := registry.ValidateTag(tag); err == nil {
			return tag, "", nil
		}
	}
	return "", registry.ProvisionReasonNoUsableOwner, fmt.Errorf("service %q has no existing non-Funnel tag that its OAuth client can use to own %q", svc.Name, registry.FunnelTag)
}

func serviceForNodeConstruction(svc registry.Service) registry.Service {
	if !svc.Funnel || containsString(svc.Tags, registry.FunnelTag) {
		return svc
	}
	effective := svc
	effective.Tags = append(append([]string(nil), svc.Tags...), registry.FunnelTag)
	return effective
}

func stringsExcept(values []string, excluded string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if value != excluded {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func funnelProvisionFailure(svc registry.Service, cause error, provision registry.ProvisionOutcome, next []string) runtimesnapshot.ServiceState {
	err := registry.FunnelCapabilityMissingProvisionError(svc.Name, cause, provision, next)
	failure, ok := recoverableServiceFailure(svc, err)
	if ok {
		return failure
	}
	return runtimesnapshot.ServiceState{
		Service:      svc,
		RuntimeState: runtimesnapshot.ServiceRuntimeFailed,
		FunnelState:  runtimesnapshot.FunnelStateCapabilityMissing,
		Error: &runtimesnapshot.ServiceError{
			Code:      registry.CodeFunnelCapabilityMissing,
			Message:   err.Error(),
			Next:      append([]string(nil), next...),
			Provision: copyProvisionOutcome(&provision),
		},
	}
}

// funnelOwnerRecovery returns the actions that resolve a missing Funnel tag
// owner, which is the only owner-derivation failure deriveFunnelTagOwner can
// report. The Funnel tag itself is derived at node construction and is skipped
// when hunting for an owner, so adding it by hand cannot fix this and is
// deliberately not suggested here.
func funnelOwnerRecovery(svc registry.Service) []string {
	return []string{
		fmt.Sprintf("tslink tags add %s <tag-held-by-the-oauth-client>", svc.Name),
		"Restart the managed daemon with `tslink install` or restart the foreground `tslink serve` process",
		fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
	}
}

func funnelPolicyFailureRecovery(svc registry.Service, provision registry.ProvisionOutcome) []string {
	if provision.Reason == registry.ProvisionReasonHTTPSDisabled {
		return funnelHTTPSRecovery(svc)
	}
	if provision.Reason == registry.ProvisionReasonSettingsUnavailable {
		return []string{
			"Grant the OAuth client networking_settings scope so TSLink can read httpsEnabled before any ACL mutation",
			"Inspect PATCH /api/v2/tailnet/{tailnet}/settings with {\"httpsEnabled\":true}; do not retry policy provisioning until the setting is known",
			fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
		}
	}
	if provision.Reason == registry.ProvisionReasonWriteUnknown {
		return []string{
			"Wait for policy propagation, then retry the reconcile or restart the daemon; the policy POST may already have committed",
			fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
			"tslink logs --level error --json",
		}
	}
	var coded registry.CodedError
	if errors.As(registry.FunnelCapabilityMissingError(svc.Name, errors.New("provisioning the Funnel policy failed")), &coded) {
		return coded.NextCommands()
	}
	return []string{fmt.Sprintf("tslink status --urls --name %s --json", svc.Name), "tslink logs --level error --json"}
}

func copyProvisionOutcome(source *registry.ProvisionOutcome) *registry.ProvisionOutcome {
	if source == nil {
		return nil
	}
	copied := *source
	return &copied
}

func (s *Server) installStartupGeneration(generation uint64, cancel context.CancelFunc) bool {
	s.startupCancelMu.Lock()
	defer s.startupCancelMu.Unlock()
	if generation < s.syncGeneration.Load() || generation < s.startupGeneration {
		cancel()
		return false
	}
	if s.startupCancel != nil {
		s.startupCancel()
	}
	s.startupCancel = cancel
	s.startupGeneration = generation
	return true
}

func (s *Server) finishStartupGeneration(generation uint64, cancel context.CancelFunc) {
	cancel()
	s.startupCancelMu.Lock()
	if s.startupGeneration == generation {
		s.startupCancel = nil
	}
	s.startupCancelMu.Unlock()
}

func (s *Server) nodeRunning(name string) bool {
	s.mu.RLock()
	_, ok := s.nodes[name]
	s.mu.RUnlock()
	return ok
}

// listenerMustWithdraw checks changes that cannot safely keep serving from a
// private handler while a replacement is blocked. A private-to-public request
// alone can keep its existing private listener during Funnel preflight.
func (s *Server) listenerMustWithdraw(node *ServiceNode, desired registry.Service) bool {
	if node.funnelListenerActive {
		return serviceChangedWithFallback(node.service, desired, s.controlURL)
	}
	old := node.service
	tagsCompatible := sameStringSet(old.Tags, desired.Tags)
	if !tagsCompatible && !old.Funnel && desired.Funnel {
		// The derived Funnel tag belongs to the replacement identity. It
		// does not change authorization on the still-private listener.
		tagsCompatible = sameStringSet(old.Tags, stringsExcept(desired.Tags, registry.FunnelTag))
	}
	return old.Type != desired.Type || old.Target != desired.Target || old.Path != desired.Path || old.File != desired.File ||
		old.Port != desired.Port || old.Ephemeral != desired.Ephemeral ||
		effectiveControlURL(old, s.controlURL) != effectiveControlURL(desired, s.controlURL) ||
		!tagsCompatible || !sameStringSet(old.AllowedUsers, desired.AllowedUsers)
}

// stopDivergentNodesLocked is used before the normal stop phase for public
// listeners and private listeners whose authorization or served surface changed.
// Durable node state remains available for retry.
func (s *Server) stopDivergentNodesLocked(desired map[string]registry.Service, invalid map[string]runtimesnapshot.ServiceState) bool {
	stopped := false
	for name, node := range s.nodes {
		svc, exists := desired[name]
		_, invalidService := invalid[name]
		if exists && !invalidService && !s.listenerMustWithdraw(node, svc) {
			continue
		}
		slog.Warn("closing divergent listener before failed registry sync returns", "name", name, "registered", exists)
		s.stopNodeLocked(name)
		stopped = true
	}
	return stopped
}

func (s *Server) publishSyncResult(result syncResult) {
	s.syncResultMu.Lock()
	if result.generation >= s.latestSyncResult.generation {
		s.latestSyncResult = result
		close(s.syncResultChanged)
		s.syncResultChanged = make(chan struct{})
	}
	s.syncResultMu.Unlock()
}

func (s *Server) waitForSyncResult(ctx context.Context, generation uint64) (syncResult, error) {
	for {
		s.syncResultMu.Lock()
		result := s.latestSyncResult
		changed := s.syncResultChanged
		s.syncResultMu.Unlock()
		if result.generation >= generation {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return syncResult{}, ctx.Err()
		case <-changed:
		}
	}
}

func loadRegistryForRuntimeSettled(ctx context.Context, path string) (*registry.Registry, []registry.ServiceIssue, error) {
	reg, issues, err := registryLoadRuntimeFn(path)
	if err == nil || registrySettleDelay <= 0 {
		return reg, issues, err
	}
	timer := time.NewTimer(registrySettleDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-timer.C:
	}
	return registryLoadRuntimeFn(path)
}

func serviceIssueFailure(issue registry.ServiceIssue) runtimesnapshot.ServiceState {
	if failure, ok := recoverableServiceFailure(issue.Service, issue.Err); ok {
		return failure
	}
	code, ok := registry.ErrorCode(issue.Err)
	if !ok {
		code = registry.CodeInvalidServiceConfig
	}
	return runtimesnapshot.ServiceState{
		Service:      issue.Service,
		RuntimeState: runtimesnapshot.ServiceRuntimeFailed,
		FunnelState:  funnelFailureState(issue.Service, code),
		Error: &runtimesnapshot.ServiceError{
			Code:    code,
			Message: issue.Error(),
		},
	}
}

func (s *Server) failClosedGlobalRegistryError(generation uint64, loadErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.syncGeneration.Load() {
		return
	}
	for name, node := range s.nodes {
		if !node.funnelListenerActive {
			continue
		}
		slog.Warn("closing active Funnel listener after invalid registry reload", "name", name, "code", registry.CodeRegistryReloadInvalid)
		s.stopNodeLocked(name)
	}
	s.globalFailure = &runtimesnapshot.ServiceError{
		Code:    registry.CodeRegistryReloadInvalid,
		Message: fmt.Sprintf("registry reload remained invalid after bounded re-read: %v", loadErr),
		Next:    []string{"tslink registry check --json"},
	}
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, false)
}

func (s *Server) beginShutdown() {
	s.shuttingDown.Store(true)
}

func (s *Server) ensureRunning(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.shuttingDown.Load() {
		return errServerShuttingDown
	}
	return nil
}

func serviceChanged(old, new registry.Service) bool {
	return serviceChangedWithFallback(old, new, "")
}

func serviceChangedWithFallback(old, new registry.Service, fallbackControlURL string) bool {
	if oldLimits, newLimits := old.EffectiveRequestLimits(), new.EffectiveRequestLimits(); !reflect.DeepEqual(oldLimits, newLimits) {
		return true
	}
	// File is part of this comparison because dropping it widens a single-file
	// share back to its whole parent directory. A change the daemon does not
	// notice here is a node that keeps serving the previous reachable surface.
	if old.Type != new.Type || old.Target != new.Target || old.Path != new.Path || old.File != new.File {
		return true
	}
	if old.AccessLogPathMode != new.AccessLogPathMode || !reflect.DeepEqual(old.AccessLogPath, new.AccessLogPath) || old.GuestGate != new.GuestGate || old.PreserveHost != new.PreserveHost {
		return true
	}
	if old.Port != new.Port || old.Ephemeral != new.Ephemeral || old.Funnel != new.Funnel || old.PublicAck != new.PublicAck || old.NoAutoProvision != new.NoAutoProvision {
		return true
	}
	if effectiveControlURL(old, fallbackControlURL) != effectiveControlURL(new, fallbackControlURL) {
		return true
	}
	if !sameStringSet(old.Tags, new.Tags) {
		return true
	}
	if !sameStringSet(old.AllowedUsers, new.AllowedUsers) {
		return true
	}
	return false
}

func sameStringSet(a, b []string) bool {
	aSet := make(map[string]struct{}, len(a))
	for _, v := range a {
		aSet[v] = struct{}{}
	}
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}
	if len(aSet) != len(bSet) {
		return false
	}
	for v := range aSet {
		if _, ok := bSet[v]; !ok {
			return false
		}
	}
	return true
}

func (s *Server) authIdentityChanged(old, new registry.Service) bool {
	if old.Ephemeral != new.Ephemeral {
		return true
	}
	// A Tier 1 node advertises no tags, so a tag change is not one there. A
	// Tier 2 node is built with the derived Funnel tag too, so a Funnel toggle
	// is one, as prepareNodeIdentity's reset decision already counts it.
	if s.credentialed && !sameStringSet(serviceForNodeConstruction(old).Tags, serviceForNodeConstruction(new).Tags) {
		return true
	}
	return effectiveControlURL(old, s.controlURL) != effectiveControlURL(new, s.controlURL)
}

// prepareCredentialUpgradeLocked applies the cross-process identity change
// recorded by login. tsnet deliberately ignores an auth key when enrolled
// state already exists, so a Tier 1 node must lose that state before the Tier 2
// auth key can create its tagged identity. The marker is cleared only after all
// target state directories are gone, and before any replacement node starts.
func (s *Server) prepareCredentialUpgradeLocked(services []registry.Service) error {
	if !s.credentialed {
		return nil
	}
	pending, err := credentialUpgradePendingFn()
	if err != nil {
		return fmt.Errorf("check credential-mode transition: %w", err)
	}
	if !pending {
		return nil
	}
	if len(s.nodes) != 0 {
		return fmt.Errorf("apply credential-mode transition: restart tslink serve before re-enrolling running services")
	}
	seen := make(map[string]struct{}, len(services))
	removed := 0
	for _, service := range services {
		name := service.Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if err := registry.ValidateName(name); err != nil {
			return fmt.Errorf("validate service for credential upgrade: %w", err)
		}
		if err := removeServiceStateDirFn(s.cfgDir, name); err != nil {
			return fmt.Errorf("remove Tier 1 state for credential upgrade %q: %w", name, err)
		}
		removed++
	}
	if err := clearCredentialUpgradeFn(); err != nil {
		return fmt.Errorf("complete credential-mode transition: %w", err)
	}
	slog.Info("prepared Tier 1 services for credentialed re-enrollment", "services", removed)
	return nil
}

func effectiveControlURL(svc registry.Service, fallback string) string {
	if svc.ControlURL != "" {
		return svc.ControlURL
	}
	return fallback
}

// mintedKeyControlURLError refuses a node that would carry an auth key minted
// through the Tailscale API to a control server that is not Tailscale's. A
// credentialed provider mints a preauthorized, tagged key for the owner's
// tailnet, and tsnet sends whatever key it holds in the registration request
// to the node's control URL; control_url is an ordinary registry field that
// the MCP add tool can set. So the provider is never asked for such a node.
//
// controlURL is the node's effective control URL. tsnet resolves an empty one
// from TS_CONTROL_URL before its built-in default, and upstream treats
// login.tailscale.com as a synonym of that default (ipn.IsLoginServerSynonym);
// nothing else is Tailscale's. A key the user supplied, the legacy authkey
// file, is not minted by TSLink and keeps reaching the server the user chose.
func (s *Server) mintedKeyControlURLError(name, controlURL string) error {
	if !s.credentialed || s.authKeyUserSupplied {
		return nil
	}
	resolved := controlURL
	if resolved == "" {
		resolved = os.Getenv("TS_CONTROL_URL")
	}
	if resolved == "" || ipn.IsLoginServerSynonym(resolved) {
		return nil
	}
	return registry.CodedError{
		Code:    registry.CodeCredentialURLMismatch,
		Message: fmt.Sprintf("%q would register with control server %s, which is not Tailscale's; TSLink does not send an auth key minted with its stored Tailscale credential to another control server, so the node is not started", name, resolved),
		Next: []string{
			fmt.Sprintf("To stay on Tailscale's control server, remove the control_url that names %s: the service's control_url in registry.json, or the global one (tslink config set control-url \"\", or the serve --control-url flag)", resolved),
			fmt.Sprintf("To enroll on %s, run TSLink without a stored Tailscale credential (tslink logout): the node then enrolls interactively on that server, or with an auth key that server issued in the legacy authkey file", resolved),
			fmt.Sprintf("tslink status --urls --name %s --json", name),
		},
	}
}

// HoldsNodeState reports whether this server runs a node for name, or is
// starting one, and therefore has a tsnet server holding that name's state
// directory open or about to write into it.
//
// It exists for the lifecycle reconciliation, which decides whether the local
// state of an orphan service is safe to delete. That decision needs a fact only
// this process has, and the honest form of the fact is "I am holding it right
// now" rather than "nothing is holding it anywhere": a separate `tslink
// cleanup` process gets no answer from here and does not delete. The
// reconciler asks under the ownership ledger's lock, the lock a startup takes
// to reserve the directory, so a "no" stays true until its removal is done.
func (s *Server) HoldsNodeState(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, running := s.nodes[name]
	return running || s.stateReservations[name] > 0
}

// reserveNodeState marks name's state directory as in use by a startup that
// is about to touch it; the returned release ends the mark. startNodeLocked
// takes it before its first access to the directory and releases it only
// after the node is published into s.nodes, which HoldsNodeState reads too,
// or after a failed start has closed its tsnet server.
//
// The mark is set under the ownership ledger's lock. The lifecycle reconciler
// holds that lock from its last reads of the registry, the ledger and
// HoldsNodeState until it has removed an orphan's state directory, so a
// startup either reserves first and the reconciler keeps the directory, or
// reserves after the removal is done and then creates a fresh one. It never
// writes into a directory whose removal was already decided.
func (s *Server) reserveNodeState(name string) (release func()) {
	reserved := false
	reserve := func() error {
		s.mu.Lock()
		if s.stateReservations == nil {
			s.stateReservations = make(map[string]int)
		}
		s.stateReservations[name]++
		s.mu.Unlock()
		reserved = true
		return nil
	}
	ownershipPath, err := nodeOwnershipPathFn()
	if err == nil {
		err = runtimesnapshot.WithOwnershipLock(ownershipPath, reserve)
	}
	if !reserved {
		// Starting is not refused over the ledger lock; the reservation
		// still answers every later HoldsNodeState.
		slog.Warn("node ownership ledger lock unavailable; reserving node state without it", "service", name, "error", err)
		_ = reserve()
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.stateReservations[name] <= 1 {
			delete(s.stateReservations, name)
			return
		}
		s.stateReservations[name]--
	}
}

// WithNodeStateLock excludes an in-progress sync from the final, local part
// of orphan cleanup. In particular, a node being started is not yet in nodes.
func (s *Server) WithNodeStateLock(fn func() error) error {
	if s.reconcileGate == nil {
		return errors.New("node-state startup gate unavailable")
	}
	s.reconcileGate <- struct{}{}
	defer func() { <-s.reconcileGate }()
	return fn()
}

// TryWithNodeStateLock acquires the same startup gate without waiting. Optional
// local cleanup may skip a tick when startup is active or shutdown has begun.
func (s *Server) TryWithNodeStateLock(ctx context.Context, fn func() error) (bool, error) {
	if s.reconcileGate == nil {
		return false, errors.New("node-state startup gate unavailable")
	}
	if ctx.Err() != nil {
		return false, nil
	}
	select {
	case s.reconcileGate <- struct{}{}:
		defer func() { <-s.reconcileGate }()
		if ctx.Err() != nil {
			return false, nil
		}
		return true, fn()
	default:
		return false, nil
	}
}

// removeServiceStateDirFn deletes a service's node state inside the daemon's
// configured directory. Every node-state deletion goes through
// runtime.RemoveServiceNodeState.
var removeServiceStateDirFn = runtimesnapshot.RemoveServiceNodeState

func cleanupTargetHostnames(targets []tailapi.CleanupTarget) []string {
	hostnames := make([]string, 0, len(targets))
	for _, target := range targets {
		hostnames = append(hostnames, target.Hostname)
	}
	return hostnames
}

// ownedNodeIDs returns the StableNodeIDs the ownership ledger records for
// name. An unreadable ledger yields none, which leaves a cleanup target
// hostname-only: it can list and protect matches but never delete.
func (s *Server) ownedNodeIDs(name string) []string {
	ownershipPath, err := nodeOwnershipPathFn()
	if err != nil {
		slog.Warn("node ownership path unavailable; stale device cleanup has no exact NodeID proof", "service", name, "error", err)
		return nil
	}
	ledger, err := runtimesnapshot.LoadOwnership(ownershipPath)
	if err != nil {
		slog.Warn("node ownership ledger unreadable; stale device cleanup has no exact NodeID proof", "service", name, "error", err)
		return nil
	}
	var ids []string
	for _, node := range ledger.Nodes {
		if node.ServiceName == name {
			ids = append(ids, node.NodeID)
		}
	}
	return ids
}

// forgetResolvedOwnership drops ledger rows whose devices a cleanup deleted or
// found already gone. A failure only leaves a row that the next cleanup
// resolves again.
func forgetResolvedOwnership(ids []string) {
	if len(ids) == 0 {
		return
	}
	ownershipPath, err := nodeOwnershipPathFn()
	if err == nil {
		err = runtimesnapshot.RemoveOwnedNodeIDs(ownershipPath, ids)
	}
	if err != nil {
		slog.Warn("could not forget ownership records of devices already removed", "error", err)
	}
}

func (s *Server) cleanupAuthIdentityNodes(ctx context.Context, targets []tailapi.CleanupTarget) error {
	if len(targets) == 0 || s.cleanupNodesFn == nil {
		return nil
	}
	hostnames := cleanupTargetHostnames(targets)
	cleanup, err := s.cleanupNodesFn(ctx, targets)
	forgetResolvedOwnership(cleanup.ResolvedOwnershipIDs)
	if err != nil {
		if errors.Is(err, tailapi.ErrNoAPIClient) {
			slog.Warn("degraded mode: skipped stale tailnet node cleanup", "reason", err.Error(), "hostnames", hostnames, "degraded_mode", true)
			return nil
		}
		return fmt.Errorf("cleanup stale tailnet nodes before auth identity restart: %w", err)
	}
	if cleanup.Skipped {
		slog.Warn("degraded mode: skipped stale tailnet node cleanup", "reason", cleanup.SkipReason, "hostnames", hostnames, "degraded_mode", true)
		return nil
	}
	if len(cleanup.Deleted) > 0 {
		slog.Info("removed stale tailnet nodes before auth identity restart", "matched", cleanup.Matched, "deleted", cleanup.Deleted)
	}
	return nil
}

func (s *Server) writeRuntimeSnapshotLocked(registryFingerprint string, complete bool) {
	// Retain failed publications for a later monitor cycle, even if the next
	// observations are identical to the values already held in memory.
	s.runtimeSnapshotDirty = true
	s.lastSnapshotComplete = complete
	path, err := runtimeSnapshotPathFn()
	if err != nil {
		slog.Warn("runtime snapshot path unavailable", "error", err)
		return
	}
	names := make([]string, 0, len(s.nodes)+len(s.serviceFailures))
	for name := range s.nodes {
		names = append(names, name)
	}
	for name := range s.serviceFailures {
		if _, running := s.nodes[name]; !running {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	guestWarnings := s.guestCounterWarningsLocked()
	states := make([]runtimesnapshot.ServiceState, 0, len(names))
	for _, name := range names {
		if failure, failed := s.serviceFailures[name]; failed {
			states = append(states, failure)
			continue
		}
		node := s.nodes[name]
		var certDomains []string
		if node.tsnetSrv != nil {
			certDomains = node.tsnetSrv.CertDomains()
		}
		warnings := node.limitWarnings.snapshot()
		if warning, ok := guestWarnings[name]; ok {
			warnings = append(warnings, warning)
		}
		states = append(states, runtimesnapshot.ServiceState{
			Warnings:     warnings,
			Service:      node.service,
			NodeID:       node.nodeID,
			RuntimeHost:  node.runtimeHost,
			RuntimeState: runtimesnapshot.ServiceRuntimeRunning,
			FunnelState:  funnelStateForRunning(node),
			CertDomains:  certDomains,
		})
	}
	var snapshot runtimesnapshot.Snapshot
	for i := range states {
		states[i].NodeKey = nodeKeyExpiry(nil, s.daemonStartedAt)
		if observed, ok := s.healthStates[states[i].Service.Name]; ok && observed.Identity == healthProbeIdentity(states[i].Service, registry.CanonicalProxyHost(states[i].CertDomains, states[i].RuntimeHost)) {
			states[i].Health = observed.Health
			if node := s.nodes[states[i].Service.Name]; node != nil && node == observed.Node {
				states[i].NodeKey = observed.NodeKey
			}
		} else {
			states[i].Health = health.Unchecked(states[i].Service.Type)
		}
	}
	if complete {
		snapshot = runtimesnapshot.NewSnapshot(s.daemonPID, s.daemonStartedAt, registryFingerprint, time.Now().UTC(), states)
	} else {
		snapshot = runtimesnapshot.NewPartialSnapshot(s.daemonPID, s.daemonStartedAt, registryFingerprint, time.Now().UTC(), states)
	}
	snapshot.Alerts = s.alerts
	if writer, ok := s.accessWriter.(interface{ Health() accesslog.Health }); ok {
		h := writer.Health()
		snapshot.AccessLog = &h
	}
	if s.globalFailure != nil {
		globalFailure := *s.globalFailure
		globalFailure.Next = append([]string(nil), s.globalFailure.Next...)
		globalFailure.Provision = copyProvisionOutcome(s.globalFailure.Provision)
		snapshot.GlobalError = &globalFailure
	}
	if err := runtimeSaveSnapshotFn(path, snapshot); err != nil {
		slog.Warn("runtime snapshot write failed; continuing with running services", "path", path, "error", err)
	} else {
		s.runtimeSnapshotDirty = false
		s.lastGuestCounterWarnings = guestWarnings
		if snapshot.AccessLog != nil {
			s.lastAccessHealth = *snapshot.AccessLog
		}
	}
	// Publish after the write, never before: an event stream rebuilds its
	// payload by reading runtime.json back, so notifying first would hand a
	// client the state it already had and call it fresh.
	//
	// Runtime changes — a registry edit picked up by the fsnotify watcher,
	// a lifecycle tick that expires a Funnel, a service that failed to start —
	// end in this function. The health monitor also notifies when an observation
	// ages to unknown; that projection change needs no snapshot write.
	s.events.publish()
}

func funnelStateForRunning(node *ServiceNode) string {
	if node.funnelListenerActive {
		return runtimesnapshot.FunnelStateActive
	}
	if node.service.Funnel {
		return runtimesnapshot.FunnelStateRequestedUnknown
	}
	return runtimesnapshot.FunnelStateNotRequested
}

func recoverableServiceFailure(svc registry.Service, err error) (runtimesnapshot.ServiceState, bool) {
	code, ok := registry.ErrorCode(err)
	if !ok {
		return runtimesnapshot.ServiceState{}, false
	}
	// The error-code table decides which codes describe one service; that
	// service is isolated with its code and next steps, and the others keep
	// running. Auth-key derivation rejected by Tailscale (api_token_unauthorized,
	// api_forbidden) is one of them: status and doctor then show the reason and
	// recovery steps instead of a bare log line.
	if !errcode.IsServiceScoped(code) {
		return runtimesnapshot.ServiceState{}, false
	}
	funnelState := funnelFailureState(svc, code)
	var recovery interface{ NextCommands() []string }
	var next []string
	if errors.As(err, &recovery) {
		next = recovery.NextCommands()
	}
	var coded registry.CodedError
	var provision *registry.ProvisionOutcome
	if errors.As(err, &coded) {
		provision = copyProvisionOutcome(coded.Provision)
	}
	return runtimesnapshot.ServiceState{
		Service:      svc,
		RuntimeState: runtimesnapshot.ServiceRuntimeFailed,
		FunnelState:  funnelState,
		Error: &runtimesnapshot.ServiceError{
			Code:      code,
			Message:   err.Error(),
			Next:      next,
			Provision: provision,
		},
	}, true
}

func funnelFailureState(svc registry.Service, code string) string {
	switch code {
	case registry.CodeFunnelCapabilityMissing:
		return runtimesnapshot.FunnelStateCapabilityMissing
	case registry.CodeFunnelListenFailed:
		return runtimesnapshot.FunnelStateListenFailed
	case registry.CodeServiceStartTimeout:
		if svc.Funnel {
			return runtimesnapshot.FunnelStateStartTimeout
		}
	}
	if svc.Funnel {
		return runtimesnapshot.FunnelStateRequestedUnknown
	}
	return runtimesnapshot.FunnelStateNotRequested
}

func (s *Server) removeRuntimeSnapshot() {
	path, err := runtimeSnapshotPathFn()
	if err != nil {
		slog.Warn("runtime snapshot path unavailable during shutdown", "error", err)
		return
	}
	if err := runtimeRemoveSnapshotFn(path); err != nil {
		slog.Warn("runtime snapshot remove failed during shutdown", "path", path, "error", err)
	}
	// Withdrawing runtime evidence is itself a state change a client must see:
	// a failed sync leaves services the client was told were running with no
	// runtime backing. At shutdown this is a no-op, because closeAllNodes
	// closes the control plane before it reaches here.
	s.events.publish()
}

// ValidateServiceForStartup validates a service definition before starting its node.
// It delegates to the canonical registry validator and adds startup context.
func ValidateServiceForStartup(svc registry.Service) error {
	if err := registry.ValidateService(svc); err != nil {
		return fmt.Errorf("service %q: %w; edit registry.json", svc.Name, err)
	}
	return nil
}

func recordOwnedNodeWithBackoff(ctx context.Context, ownershipPath, serviceName, nodeID string) bool {
	err := recordOwnedNodeFn(ownershipPath, serviceName, nodeID, serverNowFn().UTC())
	for _, delay := range ownershipRetryDelays {
		if err == nil {
			return true
		}
		slog.Warn("node ownership ledger write failed; retrying with backoff", "service", serviceName, "path", ownershipPath, "delay", delay, "error", err)
		if waitErr := ownershipRetryWaitFn(ctx, delay); waitErr != nil {
			slog.Warn("node ownership ledger retry canceled; continuing without durable cleanup proof", "service", serviceName, "path", ownershipPath, "error", waitErr)
			return false
		}
		err = recordOwnedNodeFn(ownershipPath, serviceName, nodeID, serverNowFn().UTC())
	}
	if err != nil {
		slog.Warn("node ownership ledger retries failed; continuing without durable cleanup proof", "service", serviceName, "path", ownershipPath, "error", err)
		return false
	}
	return true
}

func (s *Server) startNodeLocked(ctx context.Context, svc registry.Service, provisionOutcomes ...registry.ProvisionOutcome) error {
	if err := s.ensureRunning(ctx); err != nil {
		return err
	}
	// Final wall-clock gate immediately before any tsnet/Funnel listener work.
	// This closes the interval between desired-state loading and construction.
	svc = registry.EffectiveServiceAt(svc, serverNowFn())
	if err := ValidateServiceForStartup(svc); err != nil {
		return err
	}
	if err := s.mintedKeyControlURLError(svc.Name, effectiveControlURL(svc, s.controlURL)); err != nil {
		return err
	}
	// From here on this start touches the service's state directory, so the
	// lifecycle reconciler must not remove it until the node is published or
	// closed. The release runs after the deferred close below.
	release := s.reserveNodeState(svc.Name)
	defer release()
	// Direct callers use the same durable guard as registry reconciliation.
	// During reconciliation this is an inexpensive read after its preparation.
	cleanupErr, err := s.prepareNodeIdentity(ctx, svc)
	if err != nil {
		return err
	}
	if cleanupErr != nil {
		slog.Warn("remote cleanup failed after old state removal; continuing restart", "name", svc.Name, "error", cleanupErr)
	}

	stateDir := filepath.Join(config.NodesDirIn(s.cfgDir), svc.Name)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}

	// Resolve control URL: per-service > global > default
	controlURL := s.controlURL
	if svc.ControlURL != "" {
		controlURL = svc.ControlURL
	}

	nodeService := serviceForNodeConstruction(svc)
	authKey, err := s.authKeyProvider(ctx, nodeService)
	if err != nil {
		return fmt.Errorf("auth key for service %q: %w", svc.Name, err)
	}
	tsnetSrv := newTSNetServerFn(nodeService, stateDir, authKey, controlURL)

	nodeCtx, cancel := context.WithCancel(ctx)
	// The serving context must outlive the startup generation that created the
	// node. generationCtx is canceled as soon as syncNodesWithOutcome returns,
	// so deriving the TCP accept loop from nodeCtx would close its listener
	// immediately after "ready". The daemon root ctx still cascades through
	// WithoutCancel's values, while teardown is driven explicitly by
	// closeResources and stopNodeLocked via cancelAll.
	serveCtx, cancelServe := context.WithCancel(context.WithoutCancel(ctx))
	cancelAll := func() {
		cancelServe()
		cancel()
	}
	observeNodeContextFn(svc.Name, nodeCtx)
	var handlerCloser io.Closer
	var closeResourcesOnce sync.Once
	closeResources := func() {
		closeResourcesOnce.Do(func() {
			cancelAll()
			if handlerCloser != nil {
				_ = handlerCloser.Close()
			}
			CloseTSNetServer(svc.Name, tsnetSrv)
		})
	}
	committed := false
	defer func() {
		if !committed {
			closeResources()
		}
	}()

	var status *ipnstate.Status
	technicalCtx := nodeCtx
	cancelTechnical := func() {}
	if authKey != "" && nodeStartupTimeout > 0 {
		technicalCtx, cancelTechnical = context.WithTimeout(nodeCtx, nodeStartupTimeout)
	}
	defer cancelTechnical()
	if authKey == "" {
		// Human authorization is not a technical startup deadline. Keep the
		// published auth URL and tsnet node alive until the daemon is stopped or
		// the user completes enrollment.
		status, err = s.waitForInteractiveNode(nodeCtx, tsnetSrv, svc.Name)
	} else {
		status, err = tsnetSrv.Up(technicalCtx)
		timedOut := ctx.Err() == nil && errors.Is(technicalCtx.Err(), context.DeadlineExceeded)
		if err != nil && timedOut {
			closeResources()
			return registry.ServiceStartTimeoutError(svc.Name, nodeStartupTimeout)
		}
	}
	if err != nil {
		return fmt.Errorf("tsnet up for %q: %w", svc.Name, err)
	}
	runtimeHost := runtimeHostFromStatus(status)
	nodeID := runtimeNodeIDFromStatus(status)
	if nodeID != "" {
		ownershipPath, err := nodeOwnershipPathFn()
		if err != nil {
			slog.Warn("node ownership path unavailable; continuing without durable cleanup proof", "service", svc.Name, "error", err)
		} else {
			recordOwnedNodeWithBackoff(nodeCtx, ownershipPath, svc.Name, nodeID)
		}
	}
	if svc.Funnel {
		provision := registry.ProvisionOutcome{
			Target:       registry.FunnelTag,
			Reason:       registry.ProvisionReasonNotAttempted,
			WriteOutcome: tailapi.PolicyWriteNotAttempted,
		}
		if len(provisionOutcomes) > 0 {
			provision = provisionOutcomes[0]
		}
		verified, err := s.verifyFunnelAccess(technicalCtx, tsnetSrv, svc, status, provision)
		if err != nil {
			return err
		}
		// The capability wait can observe a netmap newer than the one tsnet.Up
		// returned. A node's DNS name only appears once the netmap carries it,
		// so a host derived from the Up status can still be empty here; every
		// consumer then falls back to the "<tailnet>" placeholder. Re-derive
		// from the newest status we hold, and never downgrade a known host.
		if host := runtimeHostFromStatus(verified); host != "" {
			runtimeHost = host
		}
	}
	listenerCtx := technicalCtx
	cancelListener := func() {}
	if authKey == "" && nodeStartupTimeout > 0 {
		listenerCtx, cancelListener = context.WithTimeout(nodeCtx, nodeStartupTimeout)
	}
	defer cancelListener()

	// TCP proxy: raw TCP forwarding, no HTTP/TLS
	if svc.Type == registry.TypeTCP {
		port := svc.Port
		if port == 0 {
			port = 443
		}
		ln, timedOut, err := activateListener(listenerCtx, ctx, closeResources, func() (net.Listener, error) {
			return tsnetSrv.Listen("tcp", fmt.Sprintf(":%d", port))
		})
		if err != nil {
			if timedOut {
				return registry.ServiceStartTimeoutError(svc.Name, nodeStartupTimeout)
			}
			return fmt.Errorf("listen TCP for %q: %w", svc.Name, err)
		}
		if err := s.ensureRunning(ctx); err != nil {
			ln.Close()
			return err
		}

		node := &ServiceNode{
			tsnetSrv:    tsnetSrv,
			service:     svc,
			nodeID:      nodeID,
			runtimeHost: runtimeHost,
			listener:    ln,
			cancel:      cancelAll,
		}

		// Read the serveTCP seam on this goroutine. The accept loop outlives
		// startNodeLocked, so a read from inside it is unordered with respect to
		// every later write of serveTCPFn -- and tests swap that seam (and restore
		// it from t.Cleanup) between test functions.
		serveTCP := serveTCPFn
		tcpAccess := &tcpAccessOptions{writer: s.accessWriter, identity: NewIdentityResolver(tsnetSrv.LocalClient), now: serverNowFn}
		tcpDone := make(chan struct{})
		node.tcpDone = tcpDone
		go func() {
			defer close(tcpDone)
			serveTCP(context.WithValue(serveCtx, tcpAccessKey{}, tcpAccess), ln, svc.Target, svc.Name)
		}()

		slog.Info("tcp node ready", "name", svc.Name, "target", svc.Target, "port", port)
		s.mu.Lock()
		if err := s.ensureRunning(nodeCtx); err != nil {
			s.mu.Unlock()
			_ = ln.Close()
			return err
		}
		s.nodes[svc.Name] = node
		s.mu.Unlock()
		committed = true
		return nil
	}

	// HTTP-based services (proxy, file)
	var handler http.Handler
	var lc *LocalClient
	// identity answers "who is behind this address" for every consumer on this
	// node, through one cache. It is assigned below rather than here because
	// how it gets its local client differs by what else on the node already
	// needs one.
	var identity *IdentityResolver
	switch svc.Type {
	case registry.TypeProxy:
		var err2 error
		lc, err2 = tsnetSrv.LocalClient()
		if err2 != nil {
			return fmt.Errorf("local client for %q: %w", svc.Name, err2)
		}
		identity = NewStaticIdentityResolver(lc)
		h, err2 := NewProxyHandlerWithOptions(svc.Target, identity, ProxyOptions{
			PreserveHost: svc.PreserveHost,
			CanonicalHost: func() string {
				return canonicalHostFor(tsnetSrv, runtimeHost)
			},
		})
		if err2 != nil {
			return fmt.Errorf("proxy handler for %q: %w", svc.Name, err2)
		}
		handler = h
	case registry.TypeFile:
		// svc.File is the narrowing set by a regular-file share. Absent, this is
		// a directory share and the whole subtree is served, which is also how
		// every registry written before the field existed reads.
		var h *FileHandler
		var err2 error
		if svc.File != "" {
			h, err2 = NewSingleFileHandler(svc.Path, svc.File)
		} else {
			h, err2 = NewFileHandler(svc.Path)
		}
		if err2 != nil {
			return fmt.Errorf("file handler for %q: %w", svc.Name, err2)
		}
		handler = h
		handlerCloser = h
	default:
		return fmt.Errorf("unknown service type %q", svc.Type)
	}

	var guestPrivate http.Handler
	var guestPath string

	// ACL middleware: enforce per-service access control
	if len(svc.AllowedUsers) > 0 && !svc.PeopleScoped {
		if lc == nil {
			var err2 error
			lc, err2 = tsnetSrv.LocalClient()
			if err2 != nil {
				return fmt.Errorf("local client for %q (acl): %w", svc.Name, err2)
			}
		}
	}
	if !svc.Funnel || svc.GuestGate {
		peoplePath, err := registryPathFn()
		if err != nil {
			return err
		}
		if svc.GuestGate {
			guestPrivate = peopleMiddleware(peoplePath, svc, tsnetSrv.LocalClient, serverNowFn)(handler)
			guestPath = peoplePath
		} else {
			handler = peopleMiddleware(peoplePath, svc, tsnetSrv.LocalClient, serverNowFn)(handler)
		}
	}

	if identity == nil {
		if lc != nil {
			// A file share with an allow list already opened a client, and
			// failing to do so already refused the service. Reuse it.
			identity = NewStaticIdentityResolver(lc)
		} else {
			// A file share with no allow list: nothing on this path
			// needs WhoIs to start, and the security-semantics test
			// FileNoAllowStartsWithoutWhoIsDependency pins that. So the
			// client is acquired on the first request instead, which is
			// late enough that the node is up and early enough that the
			// first logged request carries a login. Identity is still
			// reported for these shares, and a share still starts on a
			// node that cannot report one.
			identity = NewIdentityResolver(tsnetSrv.LocalClient)
		}
	}

	limitWarnings := &serviceLimitWarnings{}
	reportLimit := func(warning inspect.WarningView) {
		if limitWarnings.add(warning) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if node := s.nodes[svc.Name]; node != nil && node.limitWarnings == limitWarnings {
				s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
			}
		}
	}
	limitedHandler := RequestLimitsMiddleware(svc, reportLimit, handler)
	if svc.GuestGate {
		handler = newGuestGate(guestPath, svc, serverNowFn, s.accessWriter, RequestLimitsMiddleware(svc, reportLimit, guestPrivate), limitedHandler)
		handlerCloser = handler.(*guestGate)
	} else {
		handler = limitedHandler
	}
	handler = AccessEventMiddleware(svc, s.accessOptions, s.accessWriter, identity, serverNowFn, handler)

	var ln net.Listener
	funnelListenerActive := false
	if svc.Funnel && svc.Type == registry.TypeProxy {
		slog.Warn("funnel.listener.public", "code", "funnel.listener.public", "message", "Tailscale Funnel listener exposes this service to the public internet", "name", svc.Name)
		var timedOut bool
		ln, timedOut, err = activateListener(listenerCtx, ctx, closeResources, func() (net.Listener, error) {
			return tsnetSrv.ListenFunnel("tcp", ":443")
		})
		if timedOut {
			return registry.ServiceStartTimeoutError(svc.Name, nodeStartupTimeout)
		}
		funnelListenerActive = err == nil
	} else {
		var timedOut bool
		ln, timedOut, err = activateListener(listenerCtx, ctx, closeResources, func() (net.Listener, error) {
			return tsnetSrv.ListenTLS("tcp", ":443")
		})
		if timedOut {
			return registry.ServiceStartTimeoutError(svc.Name, nodeStartupTimeout)
		}
	}
	if err != nil {
		if svc.Funnel && svc.Type == registry.TypeProxy {
			return registry.FunnelListenFailedError(svc.Name, err)
		}
		return fmt.Errorf("listen TLS for %q: %w", svc.Name, err)
	}
	if err := s.ensureRunning(ctx); err != nil {
		ln.Close()
		return err
	}
	ln = newLimitedListener(ln, httpMaxActiveConns, "http", svc.Name)

	httpSrv := newHTTPServerFn(handler)
	configureAccessHTTP(httpSrv)
	ln = configureServiceHTTP(httpSrv, svc, ln, reportLimit)

	node := &ServiceNode{
		tsnetSrv:             tsnetSrv,
		service:              svc,
		nodeID:               nodeID,
		runtimeHost:          runtimeHost,
		funnelListenerActive: funnelListenerActive,
		listener:             ln,
		httpSrv:              httpSrv,
		limitWarnings:        limitWarnings,
		handlerCloser:        handlerCloser,
		cancel:               cancelAll,
	}

	// Serve in background
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !isClosedListenerError(err) {
			slog.Error("node serve error", "name", svc.Name, "error", err)
		}
	}()

	if domains := tsnetSrv.CertDomains(); len(domains) > 0 {
		slog.Info("node ready", "name", svc.Name, "url", "https://"+domains[0])
	}

	s.mu.Lock()
	if err := s.ensureRunning(nodeCtx); err != nil {
		s.mu.Unlock()
		_ = ln.Close()
		return err
	}
	s.nodes[svc.Name] = node
	s.mu.Unlock()
	committed = true
	return nil
}

func (s *Server) verifyFunnelAccess(
	ctx context.Context,
	tsnetSrv tsnetServer,
	svc registry.Service,
	status *ipnstate.Status,
	provision registry.ProvisionOutcome,
) (*ipnstate.Status, error) {
	reason, accessErr := funnelAccessCause(status)
	switch reason {
	case "":
		return status, nil
	case registry.ProvisionReasonHTTPSDisabled:
		provision.Reason = reason
		return nil, registry.FunnelCapabilityMissingProvisionError(svc.Name, accessErr, provision, funnelHTTPSRecovery(svc))
	case registry.ProvisionReasonPortUnsupported:
		provision.Reason = reason
		return nil, registry.FunnelCapabilityMissingProvisionError(svc.Name, accessErr, provision, funnelPortRecovery(svc))
	}

	if provision.Reason == registry.ProvisionReasonDaemonDisabled || provision.Reason == registry.ProvisionReasonServiceDisabled || provision.Reason == registry.ProvisionReasonNotAttempted {
		return nil, registry.FunnelCapabilityMissingProvisionError(svc.Name, accessErr, provision, funnelProvisionDisabledRecovery(svc))
	}

	statusClient, err := tsnetStatusClientFn(tsnetSrv)
	if err != nil {
		provision.Reason = registry.ProvisionReasonNetmapPollFailed
		return nil, registry.FunnelCapabilityMissingProvisionError(
			svc.Name,
			fmt.Errorf("the Funnel policy is prepared but netmap status cannot be polled: %w", err),
			provision,
			funnelNetmapRecovery(svc),
		)
	}

	waitCtx, cancel, budget := boundedFunnelWait(ctx, funnelCapabilityWaitTimeout)
	defer cancel()
	lastErr := accessErr
	pollInterval := funnelCapabilityPollInterval
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			provision.Reason = registry.ProvisionReasonNetmapTimeout
			return nil, registry.FunnelCapabilityMissingProvisionError(
				svc.Name,
				fmt.Errorf("the Funnel policy is prepared for %q, but the node capability did not propagate within the actual %s wait budget: %v", provision.Target, budget, lastErr),
				provision,
				funnelNetmapRecovery(svc),
			)
		case <-ticker.C:
			polled, statusErr := statusClient.Status(waitCtx)
			if statusErr != nil {
				lastErr = fmt.Errorf("poll tsnet status: %w", statusErr)
				continue
			}
			reason, checkErr := funnelAccessCause(polled)
			switch reason {
			case "":
				return polled, nil
			case registry.ProvisionReasonHTTPSDisabled:
				provision.Reason = reason
				return nil, registry.FunnelCapabilityMissingProvisionError(svc.Name, checkErr, provision, funnelHTTPSRecovery(svc))
			case registry.ProvisionReasonPortUnsupported:
				provision.Reason = reason
				return nil, registry.FunnelCapabilityMissingProvisionError(svc.Name, checkErr, provision, funnelPortRecovery(svc))
			default:
				lastErr = checkErr
			}
		}
	}
}

func boundedFunnelWait(ctx context.Context, configured time.Duration) (context.Context, context.CancelFunc, time.Duration) {
	if configured < 0 {
		configured = 0
	}
	waitCtx, cancel := context.WithTimeout(ctx, configured)
	budget := configured
	if deadline, ok := waitCtx.Deadline(); ok {
		budget = time.Until(deadline)
		if budget < 0 {
			budget = 0
		}
	}
	return waitCtx, cancel, budget.Truncate(time.Millisecond)
}

func funnelAccessCause(status *ipnstate.Status) (string, error) {
	if status == nil || status.Self == nil {
		return registry.ProvisionReasonNetmapTimeout, errors.New("tsnet status did not include the service node")
	}
	if !status.Self.HasCap(tailcfg.CapabilityHTTPS) {
		return registry.ProvisionReasonHTTPSDisabled, errors.New("HTTPS is disabled for the tailnet, so Funnel is unavailable")
	}
	if !status.Self.HasCap(tailcfg.NodeAttrFunnel) {
		return registry.ProvisionReasonNetmapTimeout, errors.New("the Funnel node attribute is not present in the service node netmap")
	}
	if err := ipn.CheckFunnelPort(443, status.Self); err != nil {
		return registry.ProvisionReasonPortUnsupported, err
	}
	return "", nil
}

func funnelHTTPSRecovery(svc registry.Service) []string {
	return []string{
		"PATCH /api/v2/tailnet/{tailnet}/settings with {\"httpsEnabled\":true} using OAuth scope networking_settings",
		"Retry or restart the daemon after the tailnet HTTPS setting propagates",
		fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
	}
}

func funnelPortRecovery(svc registry.Service) []string {
	return []string{
		"Tailscale Funnel supports ports 443, 8443, and 10000 per node DNS name; use one of those supported ports",
		"Retry or restart the daemon after the Funnel port capability propagates",
		fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
	}
}

func funnelNetmapRecovery(svc registry.Service) []string {
	return []string{
		"Wait for netmap propagation and retry; the tailnet policy is already prepared",
		"Restart the managed daemon with `tslink install` or restart the foreground `tslink serve` process",
		fmt.Sprintf("tslink status --urls --name %s --json", svc.Name),
	}
}

func funnelProvisionDisabledRecovery(svc registry.Service) []string {
	var coded registry.CodedError
	if errors.As(registry.FunnelCapabilityMissingError(svc.Name, errors.New("the Funnel node attribute is missing")), &coded) {
		return coded.NextCommands()
	}
	return []string{fmt.Sprintf("tslink status --urls --name %s --json", svc.Name)}
}

type listenerActivationResult struct {
	listener net.Listener
	err      error
}

// activateListener bounds tsnet listener activation even though the pinned
// tsnet API has no context parameter. On cancellation it cancels the node,
// closes tsnet to unblock the call, and joins the worker before returning.
func activateListener(listenerCtx, parentCtx context.Context, closeResources func(), listen func() (net.Listener, error)) (net.Listener, bool, error) {
	result := make(chan listenerActivationResult, 1)
	go func() {
		ln, err := listen()
		result <- listenerActivationResult{listener: ln, err: err}
	}()
	select {
	case completed := <-result:
		return completed.listener, false, completed.err
	case <-listenerCtx.Done():
		closeResources()
		completed := <-result
		if completed.listener != nil {
			_ = completed.listener.Close()
		}
		timedOut := parentCtx.Err() == nil && errors.Is(listenerCtx.Err(), context.DeadlineExceeded)
		return nil, timedOut, listenerCtx.Err()
	}
}

func (s *Server) waitForInteractiveNode(ctx context.Context, srv tsnetServer, service string) (*ipnstate.Status, error) {
	starter, ok := srv.(tsnetStarter)
	if !ok {
		return nil, fmt.Errorf("tsnet interactive start for %q is unavailable", service)
	}
	if err := starter.Start(); err != nil {
		return nil, fmt.Errorf("tsnet start for %q: %w", service, err)
	}

	lc, err := tsnetStatusClientFn(srv)
	if err != nil {
		return nil, fmt.Errorf("local client for %q interactive login: %w", service, err)
	}

	var publishedURL string
	for {
		status, err := lc.Status(ctx)
		if err != nil {
			return nil, fmt.Errorf("status for %q interactive login: %w", service, err)
		}
		if status != nil {
			if status.BackendState == ipn.Running.String() && len(status.TailscaleIPs) > 0 {
				return status, nil
			}
			authURL := strings.TrimSpace(status.AuthURL)
			if authURL != "" && authURL != publishedURL {
				if s.authHandoffFn != nil {
					if err := s.authHandoffFn(ctx, AuthHandoff{Service: service, AuthURL: authURL}); err != nil {
						return nil, fmt.Errorf("publish interactive login for %q: %w", service, err)
					}
				}
				publishedURL = authURL
			}
		}

		pollInterval := interactiveStatusPollInterval
		if pollInterval <= 0 {
			pollInterval = time.Millisecond
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func runtimeHostFromStatus(status *ipnstate.Status) string {
	if status == nil || status.Self == nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSpace(status.Self.DNSName), ".")
}

func runtimeNodeIDFromStatus(status *ipnstate.Status) string {
	if status == nil || status.Self == nil {
		return ""
	}
	return string(status.Self.ID)
}

// stopNodeLocked stops a node and keeps its tsnet state directory. Stopping is
// never a decision about the state: see runtime.RemoveServiceNodeState.
func (s *Server) stopNodeLocked(name string) {
	node, ok := s.nodes[name]
	if !ok {
		return
	}

	if !node.closed.CompareAndSwap(false, true) {
		// Already closed by another goroutine
		delete(s.nodes, name)
		return
	}

	node.cancel()
	if gate, ok := node.handlerCloser.(*guestGate); ok {
		if err := gate.Close(); err != nil {
			slog.Warn("guest counters flush failed", "name", name, "error", err)
		}
	}
	if node.httpSrv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		if err := shutdownHTTPServerFn(shutdownCtx, node.httpSrv); err != nil {
			slog.Warn("http server graceful shutdown failed; closing", "name", name, "error", err)
			if closeErr := closeHTTPServerFn(node.httpSrv); closeErr != nil && !errors.Is(closeErr, http.ErrServerClosed) && !isClosedListenerError(closeErr) {
				slog.Warn("http server close failed", "name", name, "error", closeErr)
			}
		}
		cancel()
	}
	if node.listener != nil {
		// Shutdown only closes listeners already registered by Serve; stop can race a just-started
		// Serve goroutine before registration, and TCP nodes still need this defensive final guard.
		_ = node.listener.Close()
	}
	if node.tsnetSrv != nil {
		node.tsnetSrv.Close()
	}
	if node.tcpDone != nil {
		timer := time.NewTimer(httpShutdownTimeout)
		defer timer.Stop()
		select {
		case <-node.tcpDone:
		case <-timer.C:
			slog.Warn("TCP access close records may be incomplete", "code", "access_log_tcp_drain_timeout", "name", name)
		}
	}
	if node.handlerCloser != nil {
		_ = node.handlerCloser.Close()
	}

	delete(s.nodes, name)
}

func (s *Server) closeAllNodes() {
	s.closeMCPControlPlane()
	s.mu.Lock()
	for name := range s.nodes {
		s.stopNodeLocked(name)
	}
	s.mu.Unlock()
	s.removeRuntimeSnapshot()
}

func (s *Server) watchRegistry(ctx context.Context) {
	done, err := s.startRegistryWatcher(ctx)
	if err != nil {
		slog.Error("registry watch setup failed", "error", err)
		return
	}
	<-done
}

const registryStateCheckInterval = 30 * time.Second

// The runtime snapshot fingerprint includes recognized service fields. Include
// decode errors too: removing an unknown key can make an isolated service valid
// without changing any of those fields, and must trigger a fresh sync.
func watchedRegistryFingerprint(runtimeFingerprint string, issues []registry.ServiceIssue) string {
	var state strings.Builder
	state.WriteString(runtimeFingerprint)
	for _, issue := range issues {
		fmt.Fprintf(&state, "\x00%d\x00%q", issue.Index, issue.Err.Error())
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(state.String())))
}

func sameWatchedRegistryFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

type watchedRegistryTarget struct {
	fingerprint string
	file        os.FileInfo
	generation  uint64
}

const (
	registryWatchDecisionOwned uint32 = 1 << iota
	registryWatchDecisionDirty
)

func (s *Server) decideWatchedRegistry(ctx context.Context, regPath string) *watchedRegistryTarget {
	if ctx.Err() != nil {
		return nil
	}
	// Claim ownership and record one wakeup in the same atomic operation.
	// A callback racing release either dirties this owner or becomes the next
	// owner; it never waits for the local decision or queues its own work.
	if s.registryWatchGate.Or(registryWatchDecisionOwned|registryWatchDecisionDirty)&registryWatchDecisionOwned != 0 {
		return nil
	}
	var target *watchedRegistryTarget
	for {
		// Consume the pending wakeup before reading fresh state. Notifications
		// arriving during this decision set it again, including unchanged reads.
		s.registryWatchGate.And(^registryWatchDecisionDirty)
		if next := s.readWatchedRegistryDecision(ctx, regPath); next != nil {
			target = next
		}
		if s.registryWatchGate.CompareAndSwap(registryWatchDecisionOwned, 0) {
			return target
		}
		if ctx.Err() != nil {
			s.registryWatchGate.Store(0)
			return nil
		}
		// Retain ownership for a fresh local decision before starting any remote
		// sync. Only the latest reserved target needs to be applied afterwards.
	}
}

func (s *Server) readWatchedRegistryDecision(ctx context.Context, regPath string) *watchedRegistryTarget {
	if s.ensureRunning(ctx) != nil {
		return nil
	}
	watchFile, statErr := os.Stat(regPath)
	reg, issues, err := registryWatchLoadFn(regPath)
	target := &watchedRegistryTarget{file: watchFile}
	if err == nil {
		fingerprint, fingerprintErr := runtimesnapshot.RegistryFingerprint(reg, issues)
		if fingerprintErr == nil {
			target.fingerprint = watchedRegistryFingerprint(fingerprint, issues)
		}
	}
	if target.fingerprint == "" {
		// Invalid input still has a target: do not repeatedly cancel the settled
		// load for identical malformed bytes. Failed syncs clear this target and
		// remain eligible for a later retry.
		data, readErr := os.ReadFile(regPath)
		target.fingerprint = fmt.Sprintf("unreadable:%x:%v", sha256.Sum256(data), readErr)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if pending := s.inFlightWatchTarget; pending != nil {
		if pending.fingerprint == target.fingerprint && sameWatchedRegistryFile(pending.file, target.file) {
			return nil
		}
		// Even an already-applied state must supersede a different pending
		// target when the registry reverts while enrollment is waiting.
	} else if s.appliedWatchFingerprint == target.fingerprint &&
		sameWatchedRegistryFile(s.appliedWatchFile, target.file) &&
		(statErr == nil || errors.Is(statErr, os.ErrNotExist)) && !s.lastSyncFailed.Load() {
		return nil
	}
	target.generation = s.syncGeneration.Add(1)
	s.inFlightWatchTarget = target
	return target
}

func (s *Server) reconcileWatchedRegistry(ctx context.Context, regPath string) {
	target := s.decideWatchedRegistry(ctx, regPath)
	if target == nil {
		return
	}
	s.syncWatchedRegistryTarget(ctx, target)
}

func (s *Server) syncWatchedRegistryTarget(ctx context.Context, target *watchedRegistryTarget) {
	// Use the existing settled load, fail-closed handling and retry bookkeeping
	// after releasing the decision gate. New targets can cancel this generation.
	outcome, err := s.syncNodesAtGeneration(ctx, false, target.generation)
	s.recordSyncOutcome(outcome, err)
	s.mu.Lock()
	if s.inFlightWatchTarget == target {
		s.inFlightWatchTarget = nil
	}
	s.mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, errServerShuttingDown) {
		slog.Warn("reload registry failed", "error", err)
	}
}

func (s *Server) startRegistryWatcher(ctx context.Context) (<-chan struct{}, error) {
	regPath, err := registryPathFn()
	if err != nil {
		return nil, err
	}

	watcher, err := newRegistryWatcherFn()
	if err != nil {
		return nil, fmt.Errorf("fsnotify setup: %w", err)
	}

	if err := watcher.Add(s.cfgDir); err != nil {
		_ = watcher.Close()
		return nil, fmt.Errorf("watch directory %s: %w", s.cfgDir, err)
	}

	// Record what the credential files look like now, before any event can
	// arrive, so the first frame published from this watcher corresponds to a
	// real change rather than to the watcher having just started.
	s.primeCredentialState()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer watcher.Close()
		s.runRegistryWatcher(ctx, watcher, filepath.Clean(regPath))
	}()
	return done, nil
}

func (s *Server) runRegistryWatcher(ctx context.Context, watcher registryWatcher, regPath string) {
	// Notifications accelerate state reconciliation; periodic checks repair
	// lost final events (including kqueue's Create-to-per-file-watch gap).
	ticker := time.NewTicker(registryStateCheckInterval)
	defer ticker.Stop()
	var debounce *time.Timer
	var debounceCallbacks sync.WaitGroup
	stopDebounce := func() {
		if debounce != nil {
			if debounce.Stop() {
				debounceCallbacks.Done()
			}
			debounce = nil
		}
	}
	// The credential timer is separate from the registry one on purpose: a
	// registry write must not postpone a pending credential answer, and vice
	// versa. Sharing one timer would let a busy registry starve the other.
	credentialPaths := s.credentialStatePaths()
	var credentialDebounce *time.Timer
	stopCredentialDebounce := func() {
		if credentialDebounce != nil {
			if credentialDebounce.Stop() {
				debounceCallbacks.Done()
			}
			credentialDebounce = nil
		}
	}
	scheduleRegistry := func(delay time.Duration) {
		stopDebounce()
		debounceCallbacks.Add(1)
		debounce = time.AfterFunc(delay, func() {
			defer debounceCallbacks.Done()
			s.reconcileWatchedRegistry(ctx, regPath)
		})
	}
	scheduleCredentials := func(delay time.Duration) {
		stopCredentialDebounce()
		debounceCallbacks.Add(1)
		credentialDebounce = time.AfterFunc(delay, func() {
			defer debounceCallbacks.Done()
			if ctx.Err() == nil {
				s.notifyCredentialStateChanged()
			}
		})
	}
	reconcileBoth := func() {
		scheduleRegistry(0)
		scheduleCredentials(0)
	}
	defer func() {
		stopDebounce()
		stopCredentialDebounce()
		debounceCallbacks.Wait()
	}()

	events, watcherErrors := watcher.Events(), watcher.Errors()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcileBoth()
		case event, ok := <-events:
			if !ok {
				events = nil
				reconcileBoth()
				continue
			}
			if err := s.ensureRunning(ctx); err != nil {
				return
			}
			name := filepath.Clean(event.Name)
			if _, isCredentialState := credentialPaths[name]; isCredentialState {
				// Removal counts. Logout is a delete, and a client that is
				// told about logins but not logouts holds the more dangerous
				// of the two stale beliefs.
				if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
					event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
					scheduleCredentials(credentialStateDebounce)
				}
				continue
			}
			if name != regPath {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
				event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
				scheduleRegistry(200 * time.Millisecond)
			}
		case err, ok := <-watcherErrors:
			if !ok {
				watcherErrors = nil
				reconcileBoth()
				continue
			}
			slog.Error("fsnotify error", "error", err)
			reconcileBoth()
		}
	}
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
