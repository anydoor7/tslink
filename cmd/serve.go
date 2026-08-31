package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/daemon"
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
	serveWritePIDFn            = daemon.WritePID
	serveWritePIDForProcessFn  = daemon.WritePIDForProcess
	serveRemovePIDFn           = daemon.RemovePID
	serveWithPIDLockFn         = daemon.WithPIDLock
	serveNewServerFn           = func(authKey, controlURL string) (serverRunner, error) { return server.New(authKey, controlURL) }
	serveEnsureDirFn           = config.EnsureDir
	serveMigrateFn             = credentials.MigrateFromLegacy
	serveRegistryPathFn        = config.RegistryPath
	serveLoadRegistryFn        = registry.Load
	serveGetAuthKeyFn          = credentials.GetAuthKey
	serveHasStoredCredentialFn = credentials.HasStoredCredential
	servePIDPathFn             = config.PIDPath
	serveIsRunningFn           = daemon.IsRunning
	serveIsPIDRunningFn        = daemon.IsProcessRunning
	serveEnsureTagsFn          = tailapi.EnsureTags
	serveCleanupFn             = tailapi.CleanupStaleNodesResult
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

// serverRunner abstracts server.Server for testing.
type serverRunner interface {
	Run(ctx context.Context) error
}

type ensureTagsSetter interface {
	SetEnsureTagsFn(server.EnsureTagsFunc)
}

type authKeyProviderSetter interface {
	SetAuthKeyProvider(server.AuthKeyProvider)
}

type credentialModeSetter interface {
	SetCredentialed(bool)
}

type authHandoffSetter interface {
	SetAuthHandoffFunc(server.AuthHandoffFunc)
}

type readySetter interface {
	SetReadyFunc(func() error)
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

			// Migrate file-based API key to keychain if possible. In JSON mode the
			// fact belongs in the result data; stdout must remain one envelope.
			credentialMigrated := serveMigrateFn()
			if credentialMigrated && !jsonOutput(cmd) {
				fmt.Fprintln(cmd.OutOrStdout(), "→ migrated API key to system keychain")
			}

			// Resolve control URL: flag > config > default
			controlURL, _ := cmd.Flags().GetString("control-url")
			if controlURL == "" {
				if globalCfg, err := serveLoadGlobalFn(); err == nil {
					controlURL = globalCfg.ControlURL
				}
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
				errLog := filepath.Join(logDir, "tslink.err.log")
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
					pid, err = serveDaemonizeFn(outLog, errLog, controlURL, manageACL)
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

			// Collect unique tags for startup ACL preflight. Auth keys are resolved per service.
			tagSet := make(map[string]struct{})
			for _, svc := range reg.Services {
				for _, tag := range svc.Tags {
					tagSet[tag] = struct{}{}
				}
			}
			var allTags []string
			for tag := range tagSet {
				allTags = append(allTags, tag)
			}

			effectiveEnsureTagsFn := serveEnsureTagsFn
			if !credentialed {
				effectiveEnsureTagsFn = func(context.Context, []string) error { return nil }
			} else if !manageACL {
				effectiveEnsureTagsFn = serveRemoteACLMutationDisabledFn("serve_ensure_tags")
			}

			// Ensure all required tags exist in tailnet ACL only after explicit opt-in
			// on the credentialed tier. Interactive nodes advertise no tags.
			if credentialed {
				if err := effectiveEnsureTagsFn(context.Background(), allTags); err != nil {
					if errors.Is(err, tailapi.ErrNoAPIClient) {
						slog.Warn("degraded mode: skipped ACL tag ensure", "reason", err.Error(), "tags", allTags, "degraded_mode", true)
					} else {
						return fmt.Errorf("ensure tags in ACL: %w", err)
					}
				}
			}

			if credentialed {
				// Clean up stale tailnet nodes before starting. User-owned nodes do
				// not require an administrative device API.
				cleanupTargets := tailapi.CleanupTargetsForServices(reg.Services)
				cleanup, err := serveCleanupFn(context.Background(), cleanupTargets)
				if err != nil {
					if !errors.Is(err, tailapi.ErrNoAPIClient) {
						return fmt.Errorf("cleanup stale nodes: %w", err)
					}
					cleanup = tailapi.CleanupResult{Skipped: true, SkipReason: err.Error()}
				}
				if cleanup.Skipped {
					slog.Warn("degraded mode: skipped stale tailnet node cleanup", "reason", cleanup.SkipReason, "degraded_mode", true)
				} else if len(cleanup.Deleted) > 0 {
					slog.Info("removed stale tailnet nodes", "matched", cleanup.Matched, "deleted", cleanup.Deleted)
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
				ReadyPath:       os.Getenv("TSLINK_DAEMON_READY_PATH"),
				AuthHandoffPath: authHandoffPath,
				Credentialed:    credentialed,
				PresentAuth: func(record authHandoffRecord) {
					presentAuthHandoff(cmd, record)
				},
			})
		},
	}

	serveCmd.Flags().BoolVar(&serveDaemon, "daemon", false, "Run as background daemon")
	serveCmd.Flags().Bool("no-browser", false, "Print the Tailscale login URL without opening a browser")
	serveCmd.Flags().String("control-url", "", "Custom control server URL (e.g., Headscale)")
	serveCmd.Flags().Bool("manage-acl", false, "Opt in to remote Tailscale ACL tag-owner mutation using a machine-readable side-effect plan")
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

func loadValidatedRegistryForServe() (*registry.Registry, error) {
	regPath, err := serveRegistryPathFn()
	if err != nil {
		return nil, err
	}
	reg, err := serveLoadRegistryFn(regPath)
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	validServices := make([]registry.Service, 0, len(reg.Services))
	for _, svc := range reg.Services {
		if err := server.ValidateServiceForStartup(svc); err != nil {
			if shouldSkipServiceForServeStartup(err) {
				warnSkippedServiceForServeStartup(svc, err)
				continue
			}
			return nil, err
		}
		validServices = append(validServices, svc)
	}
	reg.Services = validServices
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
	ReadyPath       string
	AuthHandoffPath string
	Credentialed    bool
	PresentAuth     func(authHandoffRecord)
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
	if setter, ok := srv.(credentialModeSetter); ok {
		setter.SetCredentialed(options.Credentialed)
	}
	if setter, ok := srv.(ensureTagsSetter); ok {
		setter.SetEnsureTagsFn(serveEnsureTagsFn)
	}
	if setter, ok := srv.(authKeyProviderSetter); ok {
		setter.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
			if !options.Credentialed {
				return "", nil
			}
			return serveGetAuthKeyFn(ctx, credentials.AuthKeyOptions{
				Tags:        svc.Tags,
				Ephemeral:   svc.Ephemeral,
				Description: fmt.Sprintf("TSLink service %q startup auth key", svc.Name),
			})
		})
	}
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
