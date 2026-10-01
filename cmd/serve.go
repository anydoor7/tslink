package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/monody0007/tslink/internal/cliargs"
	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/lifecycle"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/security"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ServeResult is the JSON payload for the serve command.
type ServeResult struct {
	Daemon             bool `json:"daemon"`
	PID                int  `json:"pid"`
	CredentialMigrated bool `json:"credential_migrated,omitempty"`
}

const (
	daemonParentLifetimeEnv = "TSLINK_TEST_DAEMON_PARENT_LIFETIME"
	daemonParentPIDEnv      = "TSLINK_TEST_DAEMON_PARENT_PID"
	daemonParentPoll        = 10 * time.Millisecond
)

var serveDaemon bool

// Testable function variables for serve
var (
	serveWritePIDFn = func(path string) error {
		return daemon.WritePIDWithBuildIdentity(path, selfBuildIdentity())
	}
	serveRemovePIDFn              = daemon.RemovePID
	serveWithPIDLockFn            = daemon.WithPIDLock
	serveNewServerFn              = func(authKey, controlURL string) (serverRunner, error) { return server.New(authKey, controlURL) }
	serveEnsureDirFn              = config.EnsureDir
	serveMigrateFn                = credentials.MigrateFromLegacy
	serveBackfillCredentialMetaFn = func() ([]string, error) {
		return credentials.BackfillMetadata(time.Now().UTC())
	}
	serveRegistryPathFn        = config.RegistryPath
	serveLoadRegistryFn        = registry.LoadForRuntime
	serveGetAuthKeyFn          = credentials.GetAuthKey
	serveHasStoredCredentialFn = credentials.HasStoredCredential
	servePIDPathFn             = config.PIDPath
	serveIsRunningFn           = daemon.IsRunning
	serveIsPIDRunningFn        = daemon.IsProcessRunning
	serveEnsureTagsFn          = tailapi.EnsureTags
	serveEnsureFunnelAttrFn    = tailapi.EnsureFunnelAttr
	serveCleanupFn             = tailapi.CleanupStaleNodesResult
	serveLifecycleReconcileFn  = lifecycle.Reconcile
	serveLoadGlobalFn          = config.LoadGlobalConfig
	serveLogDirFn              = config.LogDir
	serveDaemonizeFn           = daemon.Daemonize
	serveReadPIDFn             = daemon.ReadPID
	serveReadyPathFn           = daemonReadyPath
	serveAuthHandoffPathFn     = config.AuthHandoffPath
	serveWriteReadyFn          = daemon.WritePIDForProcess
	serveReadReadyFn           = daemon.ReadPID
	serveRemoveReadyFn         = daemon.RemovePID
	serveSaveAuthHandoffFn     = saveAuthHandoff
	serveLoadAuthHandoffFn     = loadAuthHandoff
	serveRemoveAuthHandoffFn   = removeAuthHandoff
	serveOpenBrowserFn         = openBrowser
	serveCIEnvironmentSetFn    = ciEnvironmentSet
	serveIsTerminalFn          = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	serveSignalContextFn       = func() (context.Context, context.CancelFunc) {
		return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	}

	serveDaemonReadyTimeout      = 10 * time.Second
	serveDaemonReadyPollInterval = 50 * time.Millisecond
)

// serveUserSuppliedAuthKeyFn reports whether the stored credential the
// auth-key provider uses is the legacy authkey file, a key the user supplied,
// rather than an OAuth client secret or API access token that mints keys
// through the Tailscale API. It is asked only when a stored credential exists,
// and follows credentials.GetAuthKey's order: client secret, then API key,
// then the legacy file.
var serveUserSuppliedAuthKeyFn = func() (bool, error) {
	clientSecret, err := credentials.GetClientSecret()
	if err != nil || clientSecret != "" {
		return false, err
	}
	apiKey, err := credentials.GetAPIKey()
	if err != nil || apiKey != "" {
		return false, err
	}
	return true, nil
}

// serverRunner abstracts server.Server for testing.
type serverRunner interface {
	Run(ctx context.Context) error
}

// nodeStateHolder is the runner's answer to "are you holding this service's
// tsnet state directory right now?". server.Server implements it; a test double
// that does not simply leaves local node-state cleanup off.
type nodeStateHolder interface {
	HoldsNodeState(name string) bool
}

type nodeStateSynchronizer interface {
	WithNodeStateLock(func() error) error
	TryWithNodeStateLock(context.Context, func() error) (bool, error)
}

type ensureTagsSetter interface {
	SetEnsureTagsFn(server.EnsureTagsFunc)
}

type ensureFunnelAttrSetter interface {
	SetEnsureFunnelAttrFn(server.EnsureFunnelAttrFunc)
}

type autoProvisionFunnelSetter interface {
	SetAutoProvisionFunnel(bool)
}

type authKeyProviderSetter interface {
	SetAuthKeyProvider(server.AuthKeyProvider)
}

type credentialModeSetter interface {
	SetCredentialed(bool)
}

type userSuppliedAuthKeySetter interface {
	SetUserSuppliedAuthKey(bool)
}

type controlURLTrustSetter interface {
	SetControlURLUnverified(bool)
}

type authHandoffSetter interface {
	SetAuthHandoffFunc(server.AuthHandoffFunc)
}

type readySetter interface {
	SetReadyFunc(func() error)
}

type mcpControlPlaneSetter interface {
	SetMCPControlPlane(*server.MCPControlPlane)
}

type lifecycleReconcileSetter interface {
	SetLifecycleReconcileFn(server.LifecycleReconcileFunc)
}

// daemonSettings is every setting serve passes the daemon on every start.
// serve refuses a runner that lacks one: a daemon started without one runs on
// a default nobody chose (no lifecycle reconciliation, so no Funnel expiry;
// the wrong credential mode; no Funnel policy), and nothing would say so.
type daemonSettings interface {
	credentialModeSetter
	controlURLTrustSetter
	ensureTagsSetter
	ensureFunnelAttrSetter
	autoProvisionFunnelSetter
	lifecycleReconcileSetter
	authKeyProviderSetter
	userSuppliedAuthKeySetter
}

// server.Server must take everything serve passes it, so a setter renamed or
// dropped on it fails the build here instead of every daemon start. The last
// three settings are passed only in some modes: interactive enrollment, the
// MCP control plane and daemon readiness.
var (
	_ serverRunner          = (*server.Server)(nil)
	_ nodeStateHolder       = (*server.Server)(nil)
	_ daemonSettings        = (*server.Server)(nil)
	_ authHandoffSetter     = (*server.Server)(nil)
	_ mcpControlPlaneSetter = (*server.Server)(nil)
	_ readySetter           = (*server.Server)(nil)
)

// missingDaemonSettings names the daemonSettings methods runner lacks.
func missingDaemonSettings(runner serverRunner) []string {
	want := reflect.TypeFor[daemonSettings]()
	var missing []string
	for i := range want.NumMethod() {
		name := want.Method(i).Name
		if runner == nil {
			missing = append(missing, name)
		} else if _, ok := reflect.TypeOf(runner).MethodByName(name); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

func init() {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Args:  cobra.NoArgs,
		Short: "Start the TSLink server",
		Long: `Start the TSLink server to expose registered services on your Tailscale network.

Authentication tiers:
  Tier 1 (default): no stored credential. TSLink enrolls a user-owned node
  with no tags or ACL edits and opens one Tailscale login URL. This is the
  least-privilege path for a quick or ephemeral share. User-owned node keys
  expire, so a node left running for months may eventually require re-auth.

  Tier 2 (opt-in): run "tslink login" with an API access token or OAuth client
  secret. TSLink keeps the existing tagged, per-service behavior intended for
  durable multi-service installations. After upgrading an already-enrolled
  Tier 1 install, restart serve so TSLink can replace its user-owned node state
  with tagged Tier 2 identities.

In --json mode, a zero-credential launch runs as a background daemon and
returns a needs_login record immediately. --json, --no-browser, CI, and
non-terminal sessions never try to open a browser.

MCP control plane (off by default):
  --mcp, or "mcp": {"enabled": true} in config.json, serves the same 19 MCP
  tools "tslink mcp" exposes over stdio on a dedicated tailnet-only node at
  https://<node>.<tailnet>.ts.net/mcp. This is a control plane, not a page:
  every authorized tailnet peer that reaches it can register and remove
  services, publish a service to the public internet with Funnel, and send or
  revoke real Tailscale invitations. It is never published through Funnel and
  never binds a host interface or 0.0.0.0.

  Authorization is mandatory. The endpoint answers only callers whose Tailscale
  identity matches mcp.allow in config.json, a list of login emails and/or
  "tag:..." entries. An empty list is not "everyone": serve refuses to start
  and says so.

  The same node also serves a read-only server-sent event stream at
  https://<node>.<tailnet>.ts.net/events, behind the identical Origin and
  mcp.allow authorization. It pushes the list and status views on every runtime
  change and on every credential-state change — including a "tslink login" or
  "tslink logout" run from another process — so a client stops polling, and
  sends a heartbeat so a silent stream can be told from a dead one. Set
  "events_keepalive" to a duration (Go syntax, or d for days of 24 hours)
  between 5s and 5m to change the heartbeat; the default is 20s.

  "allow_elevated_invites" (bool, default false) lets MCP clients send user
  invitations with a role other than member and device invitations that
  allow exit-node use. The CLI never needs this opt-in.

  config.json:
    {"mcp": {"enabled": true, "allow": ["you@example.com", "tag:ops"]}}

Examples:
  tslink serve
  tslink serve --no-browser
  tslink serve --daemon
  tslink serve --control-url https://headscale.example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := serveEnsureDirFn(); err != nil {
				return err
			}
			manageACL, _ := cmd.Flags().GetBool("manage-acl")
			noAutoProvision, _ := cmd.Flags().GetBool("no-auto-provision")
			mcpFlag, _ := cmd.Flags().GetBool("mcp")

			// Migrate file-based API key to keychain if possible. In JSON mode the
			// fact belongs in the result data; stdout must remain one envelope.
			credentialMigrated := serveMigrateFn()
			if credentialMigrated && !jsonOutput(cmd) {
				fmt.Fprintln(cmd.OutOrStdout(), "→ migrated API key to system keychain")
			}
			// Credentials stored before expiry tracking existed get value-free
			// metadata on the next daemon start, like the legacy file migration.
			if backfilled, err := serveBackfillCredentialMetaFn(); err != nil {
				slog.Warn("credential metadata backfill failed; expiry state reports unknown", "error", err)
			} else if len(backfilled) > 0 {
				slog.Info("credential metadata backfilled", "slots", backfilled)
			}

			// Resolve control URL: flag > config > default. The same load
			// supplies the persisted mcp control-plane settings, which follow
			// the identical flag-over-config precedence.
			globalCfg, globalCfgErr := serveLoadGlobalFn()
			if globalCfgErr != nil {
				globalCfg = config.GlobalConfig{}
			}
			controlURL, _ := cmd.Flags().GetString("control-url")
			controlURLUnverified := false
			if controlURL == "" && globalCfgErr == nil {
				controlURL = globalCfg.ControlURL
			} else if controlURL == "" {
				// Keep starting on the default control URL, but never let that
				// fallback reset a node enrolled against the configured one.
				controlURLUnverified = true
				configPath, _ := config.ConfigPath()
				slog.Warn("config.json could not be loaded; using the default control URL, which will not reset any node identity by itself", "path", configPath, "error", globalCfgErr)
			}
			mcpSettings := resolveMCPControlPlaneSettings(mcpFlag, globalCfg)
			if err := validateMCPNodeName(mcpSettings.NodeName); err != nil {
				return err
			}
			mcpEventsKeepalive, err := parseMCPEventsKeepalive(mcpSettings.EventsKeepalive)
			if err != nil {
				return err
			}
			if err := registry.ValidateControlURL(controlURL); err != nil {
				return output.ErrUsage(fmt.Sprintf("invalid control-url: %v", err))
			}

			pidPath, err := servePIDPathFn()
			if err != nil {
				return err
			}

			daemonMode := serveDaemon
			if jsonOutput(cmd) && !daemonMode {
				hasCredential, err := serveHasStoredCredentialFn()
				if err != nil {
					return err
				}
				// A machine-readable login handoff must outlive this parent
				// process so an agent can open the URL and poll status.
				daemonMode = !hasCredential
			}

			if !daemonMode && serveIsRunningFn(pidPath) {
				return output.ErrConflict("tslink is already running (see: tslink status)")
			}

			reg, err := loadValidatedRegistryForServe()
			if err != nil {
				return err
			}

			if daemonMode {
				logDir, err := serveLogDirFn()
				if err != nil {
					return err
				}

				outLog := filepath.Join(logDir, "tslink.out.log")
				errLog := filepath.Join(logDir, stderrLogFileName)
				readyPath, err := serveReadyPathFn()
				if err != nil {
					return err
				}
				authHandoffPath, err := serveAuthHandoffPathFn()
				if err != nil {
					return err
				}

				var pid int
				if err := serveWithPIDLockFn(pidPath, func() error {
					if serveIsRunningFn(pidPath) {
						return output.ErrConflict("tslink is already running (see: tslink status)")
					}
					// Startup signals belong to the daemon identified by pidPath.
					// Clear them only after the PID lock proves that no live daemon
					// owns them; otherwise a second launch could erase a pollable
					// login URL from the already-running process.
					serveRemoveReadyFn(readyPath)
					if err := serveRemoveAuthHandoffFn(authHandoffPath); err != nil {
						return fmt.Errorf("clear stale auth handoff: %w", err)
					}
					restoreStartupEnv := setDaemonStartupEnv(readyPath, authHandoffPath)
					defer restoreStartupEnv()
					var err error
					// Propagate --manage-acl to the daemon child. The child
					// re-execs foreground `serve`, where the ACL ensure runs;
					// dropping the flag here would silently ignore the opt-in.
					pid, err = serveDaemonizeFn(outLog, errLog, controlURL, manageACL, noAutoProvision, mcpSettings.Enabled)
					if err != nil {
						return err
					}
					return nil
				}); err != nil {
					return err
				}

				startup, err := waitForDaemonStartup(pidPath, readyPath, authHandoffPath, pid, serveDaemonReadyTimeout, serveDaemonReadyPollInterval)
				if err != nil {
					return fmt.Errorf("daemon startup did not complete: %w; check logs: %s and %s", err, outLog, errLog)
				}
				if startup.AuthHandoff != nil {
					presentAuthHandoff(cmd, *startup.AuthHandoff)
					if jsonOutput(cmd) {
						result := startup.AuthHandoff.serveResult()
						result.CredentialMigrated = credentialMigrated
						output.Success("serve", result)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "tslink is waiting for Tailscale login as daemon (pid %d)\n", pid)
					}
					return nil
				}

				if jsonOutput(cmd) {
					output.Success("serve", ServeResult{Daemon: true, PID: pid, CredentialMigrated: credentialMigrated})
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "tslink started as daemon (pid %d)\n", pid)
				}
				return nil
			}

			credentialed, err := serveHasStoredCredentialFn()
			if err != nil {
				return err
			}
			userSuppliedAuthKey := false
			if credentialed {
				userSuppliedAuthKey, err = serveUserSuppliedAuthKeyFn()
				if err != nil {
					return err
				}
			}

			effectiveEnsureTagsFn := serveEnsureTagsFn
			if !credentialed {
				effectiveEnsureTagsFn = func(context.Context, []string) error { return nil }
			} else if !manageACL {
				effectiveEnsureTagsFn = serveRemoteACLMutationDisabledFn("serve_ensure_tags")
			}
			baseEnsureFunnelAttrFn := serveEnsureFunnelAttrFn
			effectiveEnsureFunnelAttrFn := baseEnsureFunnelAttrFn
			if !manageACL {
				// Funnel auto-provisioning is independently default-on. Preserve the
				// --manage-acl boundary for ordinary tag creation while still allowing
				// the fused raw-policy transaction to add the shared Funnel tag owner and
				// exact nodeAttrs grant.
				effectiveEnsureFunnelAttrFn = func(ctx context.Context, request tailapi.FunnelPolicyRequest) (tailapi.PolicyMutationResult, error) {
					request.Tags = nil
					return baseEnsureFunnelAttrFn(ctx, request)
				}
			}

			if credentialed {
				// Preserve the conservative active-service discovery pass. It has no
				// NodeID proof and therefore can only report Protected; orphan DELETE
				// is performed by the durable lifecycle reconciler below.
				cleanupTargets := tailapi.CleanupTargetsForServices(reg.Services)
				cleanup, err := serveCleanupFn(context.Background(), cleanupTargets)
				if err != nil {
					cleanup = tailapi.CleanupResult{Skipped: true, SkipReason: err.Error()}
				}
				if cleanup.Skipped {
					slog.Warn("degraded mode: skipped active-service tailnet node discovery", "reason", cleanup.SkipReason, "degraded_mode", true)
				}
			}

			authHandoffPath := os.Getenv("TSLINK_DAEMON_AUTH_HANDOFF_PATH")
			if !credentialed && authHandoffPath == "" {
				authHandoffPath, err = serveAuthHandoffPathFn()
				if err != nil {
					return err
				}
			}
			restoreEnsureTags := temporarilySetServeEnsureTags(effectiveEnsureTagsFn)
			defer restoreEnsureTags()
			return runForegroundWithOptions(pidPath, "", controlURL, foregroundOptions{
				ReadyPath:          os.Getenv("TSLINK_DAEMON_READY_PATH"),
				AuthHandoffPath:    authHandoffPath,
				Credentialed:       credentialed,
				ManageACL:          manageACL,
				NoAutoProvision:    noAutoProvision,
				MCP:                mcpSettings,
				MCPEventsKeepalive: mcpEventsKeepalive,
				EnsureFunnelAttrFn: effectiveEnsureFunnelAttrFn,
				PresentAuth: func(record authHandoffRecord) {
					presentAuthHandoff(cmd, record)
				},
				ControlURLUnverified: controlURLUnverified,
				UserSuppliedAuthKey:  userSuppliedAuthKey,
			})
		},
	}

	cliargs.RegisterServeFlags(serveCmd, &serveDaemon)
	rootCmd.AddCommand(serveCmd)
}

func temporarilySetServeEnsureTags(fn server.EnsureTagsFunc) func() {
	old := serveEnsureTagsFn
	serveEnsureTagsFn = fn
	return func() { serveEnsureTagsFn = old }
}

func serveRemoteACLMutationDisabledFn(operation string) server.EnsureTagsFunc {
	return func(ctx context.Context, tags []string) error {
		if len(tags) == 0 {
			return nil
		}
		plan := security.ACLMutationPlan(operation, tags, false)
		slog.Warn("remote ACL mutation disabled by default", "plan_id", plan.ID, "opt_in_flag", plan.OptInFlag, "tags", tags)
		return nil
	}
}

func daemonReadyPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tslink.ready"), nil
}

func setDaemonStartupEnv(readyPath, authHandoffPath string) func() {
	restoreReady := setTemporaryEnv("TSLINK_DAEMON_READY_PATH", readyPath)
	restoreAuth := setTemporaryEnv("TSLINK_DAEMON_AUTH_HANDOFF_PATH", authHandoffPath)
	restoreTestParent := func() {}
	if os.Getenv(daemonParentLifetimeEnv) == "1" {
		// Compiled-binary tests explicitly opt in to launcher-owned daemon
		// lifetime. The foreground child inherits this PID before Daemonize
		// releases it; production launches do not set the opt-in variable.
		restoreTestParent = setTemporaryEnv(daemonParentPIDEnv, strconv.Itoa(os.Getpid()))
	}
	return func() {
		restoreTestParent()
		restoreAuth()
		restoreReady()
	}
}

func setTemporaryEnv(key, value string) func() {
	old, hadOld := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	return func() {
		if hadOld {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	}
}

// loadValidatedRegistryForServe reads registry.json the way the daemon does.
// Only a problem of the document itself (unreadable, empty, malformed, a
// service without a usable name) refuses to start. A problem of one service,
// such as a missing share directory or a refused target, is isolated by the
// daemon's sync, which reports it for that service in runtime.json and
// status; refusing here would keep every other service down with it. The
// returned registry holds the services that validated.
func loadValidatedRegistryForServe() (*registry.Registry, error) {
	regPath, err := serveRegistryPathFn()
	if err != nil {
		return nil, err
	}
	reg, issues, err := serveLoadRegistryFn(regPath)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	for _, issue := range issues {
		if shouldSkipServiceForServeStartup(issue.Err) {
			warnSkippedServiceForServeStartup(issue.Service, issue.Err)
		}
	}
	return reg, nil
}

func shouldSkipServiceForServeStartup(err error) bool {
	code, ok := registry.ErrorCode(err)
	return ok && code == registry.CodeFunnelPublicAckRequired
}

func warnSkippedServiceForServeStartup(svc registry.Service, err error) {
	slog.Warn("skipping service with invalid startup config",
		"name", svc.Name,
		"code", registry.CodeFunnelPublicAckRequired,
		"error", err,
		"remediation", fmt.Sprintf("re-run `tslink add %s --funnel --public` or set public_ack:true after confirming public internet exposure", svc.Name),
	)
}

type daemonStartupResult struct {
	AuthHandoff *authHandoffRecord
}

func waitForDaemonStartup(pidPath, readyPath, authHandoffPath string, expectedPID int, timeout, pollInterval time.Duration) (daemonStartupResult, error) {
	if pollInterval <= 0 {
		pollInterval = 50 * time.Millisecond
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		readyPID, err := serveReadReadyFn(readyPath)
		if err == nil {
			if readyPID == expectedPID {
				if evidenceErr := validateDaemonStartupEvidence(pidPath, expectedPID, "ready"); evidenceErr == nil {
					return daemonStartupResult{}, nil
				} else {
					lastErr = evidenceErr
				}
			} else {
				lastErr = fmt.Errorf("ready file %s contains pid %d, expected %d", readyPath, readyPID, expectedPID)
			}
		} else {
			lastErr = err
		}

		handoff, handoffErr := serveLoadAuthHandoffFn(authHandoffPath)
		if handoffErr == nil {
			if handoff.DaemonPID != expectedPID {
				lastErr = fmt.Errorf("auth handoff daemon pid %d, expected %d", handoff.DaemonPID, expectedPID)
			} else if evidenceErr := validateDaemonStartupEvidence(pidPath, expectedPID, "auth handoff"); evidenceErr != nil {
				lastErr = evidenceErr
			} else {
				return daemonStartupResult{AuthHandoff: &handoff}, nil
			}
		}

		if !serveIsPIDRunningFn(expectedPID) {
			return daemonStartupResult{}, fmt.Errorf("daemon process %d exited before readiness: %w", expectedPID, lastErr)
		}

		if !time.Now().Before(deadline) {
			if lastErr != nil {
				return daemonStartupResult{}, fmt.Errorf("expected daemon ready or auth handoff before timeout: %w", lastErr)
			}
			return daemonStartupResult{}, fmt.Errorf("expected daemon ready or auth handoff before timeout")
		}
		time.Sleep(pollInterval)
	}
}

func validateDaemonStartupEvidence(pidPath string, expectedPID int, kind string) error {
	pid, err := serveReadPIDFn(pidPath)
	if err != nil {
		return fmt.Errorf("read daemon PID file %s after %s signal: %w", pidPath, kind, err)
	}
	if pid != expectedPID {
		return fmt.Errorf("PID file %s contains pid %d, expected %d", pidPath, pid, expectedPID)
	}
	if !serveIsPIDRunningFn(expectedPID) {
		return fmt.Errorf("daemon process %d emitted %s signal but is not running", expectedPID, kind)
	}
	return nil
}

func presentAuthHandoff(cmd *cobra.Command, record authHandoffRecord) {
	if jsonOutput(cmd) {
		return
	}
	noBrowser, _ := cmd.Flags().GetBool("no-browser")
	allowBrowser := !noBrowser && !serveCIEnvironmentSetFn() && serveIsTerminalFn()
	if allowBrowser {
		if err := serveOpenBrowserFn(record.AuthURL); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "→ Could not open a browser automatically: %v\n", err)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "→ Opened browser for Tailscale login")
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "→ Complete Tailscale login: %s\n", record.AuthURL)
}

type foregroundOptions struct {
	ReadyPath          string
	AuthHandoffPath    string
	Credentialed       bool
	ManageACL          bool
	NoAutoProvision    bool
	MCP                mcpControlPlaneSettings
	MCPEventsKeepalive time.Duration
	EnsureFunnelAttrFn server.EnsureFunnelAttrFunc
	PresentAuth        func(authHandoffRecord)
	// ControlURLUnverified reports that controlURL is the default fallback
	// used because config.json failed to load.
	ControlURLUnverified bool
	// UserSuppliedAuthKey reports that the stored credential is the legacy
	// authkey file rather than one that mints keys through the Tailscale API.
	UserSuppliedAuthKey bool
}

func runForeground(pidPath, readyPath, authKey, controlURL string) error {
	return runForegroundWithOptions(pidPath, authKey, controlURL, foregroundOptions{
		ReadyPath:    readyPath,
		Credentialed: true,
	})
}

func runForegroundWithOptions(pidPath, authKey, controlURL string, options foregroundOptions) error {
	if err := serveWithPIDLockFn(pidPath, func() error {
		if serveIsRunningFn(pidPath) {
			pid, err := serveReadPIDFn(pidPath)
			if err != nil || pid != os.Getpid() {
				return output.ErrConflict("tslink is already running (see: tslink status)")
			}
		}
		if err := serveWritePIDFn(pidPath); err != nil {
			return fmt.Errorf("write PID: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	defer serveRemovePIDFn(pidPath)
	if options.ReadyPath != "" {
		defer serveRemoveReadyFn(options.ReadyPath)
	}
	if options.AuthHandoffPath != "" {
		if err := serveRemoveAuthHandoffFn(options.AuthHandoffPath); err != nil {
			return fmt.Errorf("clear stale auth handoff: %w", err)
		}
		defer func() {
			if err := serveRemoveAuthHandoffFn(options.AuthHandoffPath); err != nil {
				slog.Warn("failed to remove auth handoff during shutdown", "error", err)
			}
		}()
	}

	ctx, stop := serveSignalContextFn()
	defer stop()
	ctx, stopTestParent, err := withTestDaemonParentLifetime(ctx)
	if err != nil {
		return err
	}
	defer stopTestParent()

	srv, err := serveNewServerFn(authKey, controlURL)
	if err != nil {
		return err
	}
	settings, ok := srv.(daemonSettings)
	if !ok {
		return fmt.Errorf("server does not take every daemon setting serve passes it; missing or different: %s", strings.Join(missingDaemonSettings(srv), ", "))
	}
	settings.SetCredentialed(options.Credentialed)
	settings.SetControlURLUnverified(options.ControlURLUnverified)
	settings.SetEnsureTagsFn(serveEnsureTagsFn)
	ensure := options.EnsureFunnelAttrFn
	if ensure == nil {
		ensure = serveEnsureFunnelAttrFn
	}
	settings.SetEnsureFunnelAttrFn(ensure)
	settings.SetAutoProvisionFunnel(!options.NoAutoProvision)
	regPath, err := config.RegistryPath()
	if err != nil {
		return err
	}
	ownershipPath, err := config.NodeOwnershipPath()
	if err != nil {
		return err
	}
	// Local node-state cleanup needs both a holder check and a startup
	// gate. A runner missing either cannot prove deletion is safe.
	cleanLocalNodeState := false
	var localNodeStateInUse func(string) bool
	var withNodeStateLock func(func() error) error
	var tryWithNodeStateLock func(context.Context, func() error) (bool, error)
	if holder, holds := srv.(nodeStateHolder); holds {
		if synchronizer, ok := srv.(nodeStateSynchronizer); ok {
			cleanLocalNodeState = true
			localNodeStateInUse = holder.HoldsNodeState
			withNodeStateLock = synchronizer.WithNodeStateLock
			tryWithNodeStateLock = synchronizer.TryWithNodeStateLock
		}
	}
	firstLifecycleReconcile := true
	hadActiveFunnel := false
	settings.SetLifecycleReconcileFn(func(ctx context.Context, now time.Time) (bool, error) {
		hasActiveFunnel, err := registryHasActiveFunnelAt(regPath, now)
		if err != nil {
			return false, err
		}
		checkUnusedACL := options.ManageACL && (firstLifecycleReconcile || (hadActiveFunnel && !hasActiveFunnel))
		result, err := serveLifecycleReconcileFn(ctx, lifecycle.Options{
			RegistryPath:         regPath,
			OwnershipPath:        ownershipPath,
			Now:                  now,
			DryRun:               false,
			ManageACL:            options.ManageACL,
			CheckUnusedACL:       checkUnusedACL,
			CleanLocalNodeState:  cleanLocalNodeState,
			LocalNodeStateInUse:  localNodeStateInUse,
			WithNodeStateLock:    withNodeStateLock,
			TryWithNodeStateLock: tryWithNodeStateLock,
		})
		if err != nil {
			return false, err
		}
		firstLifecycleReconcile = false
		hadActiveFunnel = hasActiveFunnel
		if len(result.ExpiredFunnels) > 0 || len(result.DevicesDeleted) > 0 || len(result.Warnings) > 0 {
			slog.Info("lifecycle reconciliation completed",
				"expired_funnels", result.ExpiredFunnels,
				"devices_deleted", result.DevicesDeleted,
				"devices_protected", result.DevicesProtected,
				"acl_action", result.ACLAction,
				"warnings", result.Warnings,
			)
		}
		return result.RegistryChanged || len(result.ExpiredFunnelsNotWritten) > 0, nil
	})
	settings.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
		if !options.Credentialed {
			return "", nil
		}
		// Description uses %s, never %q: the Tailscale create-key API
		// rejects a description containing double quotes ("description had
		// invalid characters"). svc.Name is a registry-validated DNS label
		// ([a-z0-9-]) or the validated MCP node name, so the result stays
		// within letters, digits, spaces and hyphens.
		return serveGetAuthKeyFn(ctx, credentials.AuthKeyOptions{
			Tags:          svc.Tags,
			Ephemeral:     svc.Ephemeral,
			Description:   fmt.Sprintf("TSLink service %s startup auth key", svc.Name),
			ClientFactory: tailapi.NewTailscaleClient,
		})
	})
	settings.SetUserSuppliedAuthKey(options.UserSuppliedAuthKey)
	if !options.Credentialed {
		setter, ok := srv.(authHandoffSetter)
		if !ok {
			return fmt.Errorf("server does not support interactive auth handoff")
		}
		setter.SetAuthHandoffFunc(func(ctx context.Context, handoff server.AuthHandoff) error {
			record := newAuthHandoffRecord(handoff.Service, handoff.AuthURL, os.Getpid())
			if options.AuthHandoffPath != "" {
				if err := serveSaveAuthHandoffFn(options.AuthHandoffPath, record); err != nil {
					return err
				}
			}
			if options.PresentAuth != nil {
				options.PresentAuth(record)
			}
			return nil
		})
	}
	if options.MCP.Enabled {
		setter, ok := srv.(mcpControlPlaneSetter)
		if !ok {
			return fmt.Errorf("server does not support the MCP control plane")
		}
		paths, err := resolveSharePaths()
		if err != nil {
			return err
		}
		// The control plane runs the same actions the stdio transport runs;
		// tool diagnostics go to the daemon's stderr log, never to a client.
		actions := defaultMCPActions(paths, os.Stderr)
		setter.SetMCPControlPlane(buildMCPControlPlane(options.MCP, actions, []string{config.GetDefaultTag()}, options.MCPEventsKeepalive))
	}
	if options.ReadyPath != "" {
		setter, ok := srv.(readySetter)
		if !ok {
			return fmt.Errorf("server does not support daemon readiness")
		}
		setter.SetReadyFunc(func() error {
			if err := serveWriteReadyFn(options.ReadyPath, os.Getpid()); err != nil {
				return err
			}
			if options.AuthHandoffPath != "" {
				if err := serveRemoveAuthHandoffFn(options.AuthHandoffPath); err != nil {
					slog.Warn("failed to remove completed auth handoff", "error", err)
				}
			}
			return nil
		})
	}

	// Bound the daemon's own stderr log. This process holds the descriptor the
	// supervisor opened, so it is the only one that can verify O_APPEND before
	// truncating; see internal/logrotate. The call is a no-op in any run whose
	// stderr is not that file, which is every foreground and test invocation.
	// The rotation context is derived and cancelled here rather than being ctx
	// itself: srv.Run can return on an error while ctx is still live, and a
	// deferred receive on a goroutine that only stops with ctx would then hang
	// the daemon's own shutdown path forever.
	rotationCtx, stopRotation := context.WithCancel(ctx)
	rotationDone := startStderrLogRotation(rotationCtx)
	defer func() {
		stopRotation()
		<-rotationDone
	}()

	runErr := srv.Run(ctx)
	// A signal-driven shutdown can cancel the initial sync before it has
	// committed. Only suppress that cancellation when both sides agree on the
	// cause: the command context was canceled and the returned error wraps
	// context.Canceled. A live context or any non-cancellation startup failure
	// remains an error so Restart=on-failure continues to supervise crashes.
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func registryHasActiveFunnelAt(regPath string, now time.Time) (bool, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return false, err
	}
	for _, svc := range reg.Services {
		if registry.EffectiveServiceAt(svc, now).Funnel {
			return true, nil
		}
	}
	return false, nil
}

func withTestDaemonParentLifetime(parent context.Context) (context.Context, context.CancelFunc, error) {
	noop := func() {}
	if os.Getenv(daemonParentLifetimeEnv) != "1" {
		return parent, noop, nil
	}
	encodedPID := os.Getenv(daemonParentPIDEnv)
	if encodedPID == "" {
		// A test may execute foreground `serve` directly. Only daemon children
		// receive the launcher PID from setDaemonStartupEnv.
		return parent, noop, nil
	}
	parentPID, err := strconv.Atoi(encodedPID)
	if err != nil || parentPID <= 0 || parentPID == os.Getpid() {
		return nil, nil, fmt.Errorf("invalid %s %q", daemonParentPIDEnv, encodedPID)
	}

	ctx, cancel := context.WithCancel(parent)
	go func() {
		ticker := time.NewTicker(daemonParentPoll)
		defer ticker.Stop()
		for {
			if !daemon.IsProcessRunning(parentPID) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return ctx, cancel, nil
}
