package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/monody0007/tslink/internal/authmode"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/logging"
	"github.com/monody0007/tslink/internal/metrics"
	"github.com/monody0007/tslink/internal/registry"
	runtimesnapshot "github.com/monody0007/tslink/internal/runtime"
	"github.com/monody0007/tslink/internal/tailapi"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
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
)

var (
	runtimeSnapshotPathFn   = config.RuntimeSnapshotPath
	runtimeSaveSnapshotFn   = runtimesnapshot.Save
	runtimeRemoveSnapshotFn = runtimesnapshot.Remove
	beforeInitialSyncFn     = func(context.Context) error { return nil }
	registryLoadFn          = registry.Load
	afterDesiredLoadedFn    = func(context.Context, uint64) error { return nil }
)

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
		ReadTimeout:       httpReadTimeout,
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
			return &limitedConn{Conn: conn, release: func() { <-l.sem }}, nil
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

func ResourceBudgetMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > httpMaxRequestBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, httpMaxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
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
	tsnetSrv    tsnetServer
	service     registry.Service
	runtimeHost string
	listener    net.Listener
	httpSrv     *http.Server
	cancel      context.CancelFunc
	closed      atomic.Bool
}

// EnsureTagsFunc is the signature for ensuring ACL tags exist.
type EnsureTagsFunc func(ctx context.Context, tags []string) error

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

// Server manages multiple tsnet nodes, one per registered service.
type Server struct {
	nodes           map[string]*ServiceNode
	authKey         string
	authKeyProvider AuthKeyProvider
	credentialed    bool
	controlURL      string
	mu              sync.RWMutex
	cfgDir          string
	metrics         *metrics.Metrics
	ensureTagsFn    EnsureTagsFunc
	authHandoffFn   AuthHandoffFunc
	cleanupNodesFn  CleanupStaleNodesFunc
	shuttingDown    atomic.Bool
	syncGeneration  atomic.Uint64
	daemonPID       int
	daemonStartedAt time.Time
	readyFn         func() error
}

// New creates a new multi-node server.
func New(authKey, controlURL string) (*Server, error) {
	cfgDir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return &Server{
		nodes:           make(map[string]*ServiceNode),
		authKey:         authKey,
		authKeyProvider: staticAuthKeyProvider(authKey),
		credentialed:    authKey != "",
		controlURL:      controlURL,
		cfgDir:          cfgDir,
		metrics:         metrics.New(),
		cleanupNodesFn:  tailapi.CleanupStaleNodesResult,
		daemonPID:       os.Getpid(),
		daemonStartedAt: time.Now().UTC(),
	}, nil
}

// SetEnsureTagsFn sets the function called to ensure ACL tags before starting nodes.
func (s *Server) SetEnsureTagsFn(fn EnsureTagsFunc) {
	s.ensureTagsFn = fn
}

// SetAuthKeyProvider sets the function used to resolve auth material per service.
func (s *Server) SetAuthKeyProvider(fn AuthKeyProvider) {
	if fn == nil {
		s.authKeyProvider = staticAuthKeyProvider(s.authKey)
		return
	}
	s.authKeyProvider = fn
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
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	watchDone, err := s.startRegistryWatcher(watchCtx)
	if err != nil {
		s.beginShutdown()
		s.closeAllNodes()
		return fmt.Errorf("registry watcher setup failed: %w", err)
	}

	if err := beforeInitialSyncFn(ctx); err != nil {
		s.beginShutdown()
		cancelWatch()
		<-watchDone
		s.closeAllNodes()
		return fmt.Errorf("before initial sync: %w", err)
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

	<-ctx.Done()
	s.beginShutdown()
	cancelWatch()
	<-watchDone
	s.closeAllNodes()
	return nil
}

type syncOutcome struct {
	generation uint64
	committed  bool
}

// syncNodes compares registry to running nodes and starts/stops as needed.
func (s *Server) syncNodes(ctx context.Context) error {
	_, err := s.syncNodesWithOutcome(ctx)
	return err
}

func (s *Server) syncNodesAuthoritative(ctx context.Context) error {
	for {
		outcome, err := s.syncNodesWithOutcome(ctx)
		if err != nil {
			return err
		}
		if outcome.committed {
			return nil
		}
		if err := s.ensureRunning(ctx); err != nil {
			return err
		}
		slog.Info("retrying initial registry sync after stale generation", "generation", outcome.generation)
	}
}

func (s *Server) syncNodesWithOutcome(ctx context.Context) (syncOutcome, error) {
	generation := s.syncGeneration.Add(1)
	outcome := syncOutcome{generation: generation}

	if err := s.ensureRunning(ctx); err != nil {
		return outcome, err
	}

	regPath, err := config.RegistryPath()
	if err != nil {
		return outcome, err
	}

	reg, err := registryLoadFn(regPath)
	if err != nil {
		return outcome, err
	}
	registryFingerprint, err := runtimesnapshot.RegistryFingerprint(reg)
	if err != nil {
		return outcome, fmt.Errorf("runtime snapshot registry fingerprint: %w", err)
	}

	// Build desired state
	desired := make(map[string]registry.Service, len(reg.Services))
	desiredOrder := make([]string, 0, len(reg.Services))
	for _, svc := range reg.Services {
		if err := ValidateServiceForStartup(svc); err != nil {
			if shouldSkipServiceForStartup(err) {
				warnSkippedStartupService(svc, err)
				continue
			}
			return outcome, err
		}
		if _, seen := desired[svc.Name]; !seen {
			desiredOrder = append(desiredOrder, svc.Name)
		}
		desired[svc.Name] = svc
	}
	if err := afterDesiredLoadedFn(ctx, generation); err != nil {
		return outcome, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureRunning(ctx); err != nil {
		return outcome, err
	}
	if generation != s.syncGeneration.Load() {
		slog.Info("skipping stale registry sync generation", "generation", generation)
		return outcome, nil
	}
	if err := s.prepareCredentialUpgradeLocked(reg.Services); err != nil {
		return outcome, err
	}

	// Stop nodes for removed or changed services
	var authIdentityRestartTargets []tailapi.CleanupTarget
	var reloadErrs []error
	for name, node := range s.nodes {
		svc, exists := desired[name]
		if !exists {
			slog.Info("removing node", "name", name)
			s.stopNodeLocked(name, true) // remove state for deleted services
		} else if serviceChangedWithFallback(node.service, svc, s.controlURL) {
			authIdentityChanged := s.authIdentityChanged(node.service, svc)
			slog.Info("restarting node", "name", name, "auth_identity_changed", authIdentityChanged)
			s.stopNodeLocked(name, false)
			if authIdentityChanged {
				authIdentityRestartTargets = append(authIdentityRestartTargets, tailapi.CleanupTargetForService(node.service))
				if err := removeServiceStateDirFn(name); err != nil {
					reloadErr := fmt.Errorf("remove state for auth identity change %q: %w", name, err)
					slog.Warn("failed to remove node state before auth identity restart; continuing restart", "name", name, "error", err)
					reloadErrs = append(reloadErrs, reloadErr)
				}
			}
		}
	}

	if err := s.cleanupAuthIdentityNodes(ctx, authIdentityRestartTargets); err != nil {
		slog.Warn("failed to cleanup stale tailnet nodes before auth identity restart; continuing restart", "error", err)
		reloadErrs = append(reloadErrs, err)
	}

	// Ensure ACL tags exist before starting new/changed nodes
	if s.ensureTagsFn != nil {
		var tagsToEnsure []string
		tagSet := make(map[string]struct{})
		for name, svc := range desired {
			if _, running := s.nodes[name]; running {
				continue
			}
			for _, tag := range svc.Tags {
				if _, seen := tagSet[tag]; !seen {
					tagSet[tag] = struct{}{}
					tagsToEnsure = append(tagsToEnsure, tag)
				}
			}
		}
		if len(tagsToEnsure) > 0 {
			if err := s.ensureTagsFn(ctx, tagsToEnsure); err != nil {
				if errors.Is(err, tailapi.ErrNoAPIClient) {
					slog.Warn("degraded mode: skipped ACL tag ensure", "reason", err.Error(), "tags", tagsToEnsure, "degraded_mode", true)
				} else {
					slog.Error("failed to ensure ACL tags", "error", err)
					return outcome, fmt.Errorf("ensure ACL tags before start: %w", err)
				}
			}
		}
	}

	// Start nodes for new or changed services
	var startErrs []error
	if err := s.ensureRunning(ctx); err != nil {
		syncErr := errors.Join(append(reloadErrs, err)...)
		if syncErr != nil {
			s.removeRuntimeSnapshot()
		}
		return outcome, syncErr
	}
	for _, name := range desiredOrder {
		svc := desired[name]
		if _, running := s.nodes[name]; running {
			continue
		}
		if err := s.ensureRunning(ctx); err != nil {
			startErrs = append(startErrs, err)
			break
		}
		if err := s.startNodeLocked(ctx, svc); err != nil {
			slog.Error("failed to start node", "name", name, "error", err)
			startErrs = append(startErrs, fmt.Errorf("start service %q: %w", name, err))
			continue
		}
		// Persist each successfully running node before starting the next one.
		// Interactive enrollment is sequential, so this lets status pollers see
		// earlier services as up while the next service is awaiting its login.
		s.writeRuntimeSnapshotLocked(registryFingerprint, false)
	}

	syncErr := errors.Join(append(reloadErrs, startErrs...)...)
	if syncErr != nil {
		s.removeRuntimeSnapshot()
		return outcome, syncErr
	}

	// Re-write after a fully successful sync so an empty registry and a sync
	// that required no starts still publish authoritative runtime evidence.
	s.writeRuntimeSnapshotLocked(registryFingerprint, true)
	outcome.committed = true
	return outcome, nil
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
	if old.Type != new.Type || old.Target != new.Target || old.Path != new.Path {
		return true
	}
	if old.Port != new.Port || old.Ephemeral != new.Ephemeral || old.Funnel != new.Funnel || old.PublicAck != new.PublicAck || old.Domain != new.Domain {
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
	if !sameStringSet(old.Tags, new.Tags) {
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
		if err := removeServiceStateDirFn(name); err != nil {
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

func removeServiceStateDir(name string) error {
	nodesDir, err := config.NodesDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(nodesDir, name))
}

var removeServiceStateDirFn = removeServiceStateDir

func cleanupTargetHostnames(targets []tailapi.CleanupTarget) []string {
	hostnames := make([]string, 0, len(targets))
	for _, target := range targets {
		hostnames = append(hostnames, target.Hostname)
	}
	return hostnames
}

func (s *Server) cleanupAuthIdentityNodes(ctx context.Context, targets []tailapi.CleanupTarget) error {
	if len(targets) == 0 || s.cleanupNodesFn == nil {
		return nil
	}
	hostnames := cleanupTargetHostnames(targets)
	cleanup, err := s.cleanupNodesFn(ctx, targets)
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
	path, err := runtimeSnapshotPathFn()
	if err != nil {
		slog.Warn("runtime snapshot path unavailable", "error", err)
		return
	}
	names := make([]string, 0, len(s.nodes))
	for name := range s.nodes {
		names = append(names, name)
	}
	sort.Strings(names)

	states := make([]runtimesnapshot.ServiceState, 0, len(names))
	for _, name := range names {
		node := s.nodes[name]
		var certDomains []string
		if node.tsnetSrv != nil {
			certDomains = node.tsnetSrv.CertDomains()
		}
		states = append(states, runtimesnapshot.ServiceState{
			Service:     node.service,
			RuntimeHost: node.runtimeHost,
			CertDomains: certDomains,
		})
	}
	var snapshot runtimesnapshot.Snapshot
	if complete {
		snapshot = runtimesnapshot.NewSnapshot(s.daemonPID, s.daemonStartedAt, registryFingerprint, time.Now().UTC(), states)
	} else {
		snapshot = runtimesnapshot.NewPartialSnapshot(s.daemonPID, s.daemonStartedAt, registryFingerprint, time.Now().UTC(), states)
	}
	if err := runtimeSaveSnapshotFn(path, snapshot); err != nil {
		slog.Warn("runtime snapshot write failed; continuing with running services", "path", path, "error", err)
	}
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
}

// ValidateServiceForStartup validates a service definition before starting its node.
// It delegates to the canonical registry validator and adds startup context.
func ValidateServiceForStartup(svc registry.Service) error {
	if err := registry.ValidateService(svc); err != nil {
		return fmt.Errorf("service %q: %w; edit registry.json", svc.Name, err)
	}
	return nil
}

func shouldSkipServiceForStartup(err error) bool {
	code, ok := registry.ErrorCode(err)
	return ok && code == registry.CodeFunnelPublicAckRequired
}

func warnSkippedStartupService(svc registry.Service, err error) {
	slog.Warn("skipping service with invalid startup config",
		"name", svc.Name,
		"code", registry.CodeFunnelPublicAckRequired,
		"error", err,
		"remediation", fmt.Sprintf("re-run `tslink add %s --funnel --public` or set public_ack:true after confirming public internet exposure", svc.Name),
	)
}

func middlewareConfigured(mw *registry.MiddlewareConfig) bool {
	if mw == nil {
		return false
	}
	return mw.BasicAuth != "" || mw.RateLimit != 0 || len(mw.IPAllowList) > 0 || len(mw.CORSOrigins) > 0
}

func (s *Server) startNodeLocked(ctx context.Context, svc registry.Service) error {
	if err := s.ensureRunning(ctx); err != nil {
		return err
	}
	if err := ValidateServiceForStartup(svc); err != nil {
		return err
	}

	nodesDir, err := config.NodesDir()
	if err != nil {
		return err
	}

	stateDir := filepath.Join(nodesDir, svc.Name)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}

	// Resolve control URL: per-service > global > default
	controlURL := s.controlURL
	if svc.ControlURL != "" {
		controlURL = svc.ControlURL
	}

	authKey, err := s.authKeyProvider(ctx, svc)
	if err != nil {
		return fmt.Errorf("auth key for service %q: %w", svc.Name, err)
	}
	tsnetSrv := newTSNetServerFn(svc, stateDir, authKey, controlURL)

	nodeCtx, cancel := context.WithCancel(ctx)

	var status *ipnstate.Status
	if authKey == "" {
		status, err = s.waitForInteractiveNode(nodeCtx, tsnetSrv, svc.Name)
	} else {
		status, err = tsnetSrv.Up(nodeCtx)
	}
	if err != nil {
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("tsnet up for %q: %w", svc.Name, err)
	}
	runtimeHost := runtimeHostFromStatus(status)

	// TCP proxy: raw TCP forwarding, no HTTP/TLS
	if svc.Type == registry.TypeTCP {
		port := svc.Port
		if port == 0 {
			port = 443
		}
		ln, err := tsnetSrv.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("listen TCP for %q: %w", svc.Name, err)
		}
		if err := s.ensureRunning(ctx); err != nil {
			ln.Close()
			cancel()
			tsnetSrv.Close()
			return err
		}

		node := &ServiceNode{
			tsnetSrv:    tsnetSrv,
			service:     svc,
			runtimeHost: runtimeHost,
			listener:    ln,
			cancel:      cancel,
		}

		go func() {
			serveTCP(nodeCtx, ln, svc.Target, svc.Name)
		}()

		slog.Info("tcp node ready", "name", svc.Name, "target", svc.Target, "port", port)
		s.nodes[svc.Name] = node
		return nil
	}

	// HTTP-based services (proxy, file)
	var handler http.Handler
	var lc *LocalClient
	switch svc.Type {
	case registry.TypeProxy:
		var err2 error
		lc, err2 = tsnetSrv.LocalClient()
		if err2 != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("local client for %q: %w", svc.Name, err2)
		}
		h, err2 := NewProxyHandler(svc.Target, lc)
		if err2 != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("proxy handler for %q: %w", svc.Name, err2)
		}
		handler = h
	case registry.TypeFile:
		h, err2 := NewFileHandler(svc.Path)
		if err2 != nil {
			cancel()
			tsnetSrv.Close()
			return fmt.Errorf("file handler for %q: %w", svc.Name, err2)
		}
		handler = h
	default:
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("unknown service type %q", svc.Type)
	}

	// ACL middleware: enforce per-service access control
	if len(svc.AllowedUsers) > 0 {
		if lc == nil {
			var err2 error
			lc, err2 = tsnetSrv.LocalClient()
			if err2 != nil {
				cancel()
				tsnetSrv.Close()
				return fmt.Errorf("local client for %q (acl): %w", svc.Name, err2)
			}
		}
		handler = ACLMiddleware(svc.AllowedUsers, lc)(handler)
	}

	if middlewareConfigured(svc.Middleware) {
		slog.Warn("middleware configured but not enforced", "code", "middleware.not_enforced", "name", svc.Name, "message", "service middleware is configured but NOT enforced; middleware pipeline is roadmap/experimental and is not wired into serve")
	}

	handler = ResourceBudgetMiddleware(handler)
	handler = AccessLogMiddleware(svc.Name, handler)
	handler = s.metrics.Middleware(svc.Name, handler)

	var ln net.Listener
	if svc.Funnel && svc.Type == registry.TypeProxy {
		slog.Warn("funnel.listener.public", "code", "funnel.listener.public", "message", "Tailscale Funnel listener exposes this service to the public internet", "name", svc.Name)
		ln, err = tsnetSrv.ListenFunnel("tcp", ":443")
	} else {
		ln, err = tsnetSrv.ListenTLS("tcp", ":443")
	}
	if err != nil {
		cancel()
		tsnetSrv.Close()
		return fmt.Errorf("listen TLS for %q: %w", svc.Name, err)
	}
	if err := s.ensureRunning(ctx); err != nil {
		ln.Close()
		cancel()
		tsnetSrv.Close()
		return err
	}
	ln = newLimitedListener(ln, httpMaxActiveConns, "http", svc.Name)

	httpSrv := newHTTPServerFn(handler)

	node := &ServiceNode{
		tsnetSrv:    tsnetSrv,
		service:     svc,
		runtimeHost: runtimeHost,
		listener:    ln,
		httpSrv:     httpSrv,
		cancel:      cancel,
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

	if svc.Domain != "" {
		slog.Warn("custom domain configured but runtime TLS is not wired", "code", "custom_domain.tls_not_wired", "name", svc.Name, "domain", svc.Domain, "acme_email_configured", svc.AcmeEmail != "", "message", "custom-domain/ACME runtime TLS is roadmap and is not wired into serve")
	}

	s.nodes[svc.Name] = node
	return nil
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

// stopNodeLocked stops a node. If removeState is true, its tsnet state dir is deleted.
// Use removeState=true only when a service is removed from the registry.
func (s *Server) stopNodeLocked(name string, removeState bool) {
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

	if removeState {
		nodesDir, err := config.NodesDir()
		if err == nil {
			os.RemoveAll(filepath.Join(nodesDir, name))
		}
	}

	delete(s.nodes, name)
}

func (s *Server) closeAllNodes() {
	s.mu.Lock()
	for name := range s.nodes {
		s.stopNodeLocked(name, false) // keep state on graceful shutdown
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

func (s *Server) startRegistryWatcher(ctx context.Context) (<-chan struct{}, error) {
	regPath, err := config.RegistryPath()
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

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer watcher.Close()
		s.runRegistryWatcher(ctx, watcher, filepath.Clean(regPath))
	}()
	return done, nil
}

func (s *Server) runRegistryWatcher(ctx context.Context, watcher registryWatcher, regPath string) {
	var debounce *time.Timer
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-watcher.Events():
			if !ok {
				return
			}
			if err := s.ensureRunning(ctx); err != nil {
				return
			}
			if filepath.Clean(event.Name) != regPath {
				continue
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(200*time.Millisecond, func() {
					if err := s.syncNodes(ctx); err != nil {
						if errors.Is(err, context.Canceled) || errors.Is(err, errServerShuttingDown) {
							return
						}
						slog.Warn("reload registry failed", "error", err)
					}
				})
			}
		case err, ok := <-watcher.Errors():
			if !ok {
				return
			}
			slog.Error("fsnotify error", "error", err)
		}
	}
}

// MetricsHandler returns the Prometheus metrics HTTP handler.
func (s *Server) MetricsHandler() http.Handler {
	return s.metrics.Handler()
}

func isClosedListenerError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection")
}
