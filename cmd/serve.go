package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

// ServeResult is the JSON payload for the serve command.
type ServeResult struct {
	Daemon bool `json:"daemon"`
	PID    int  `json:"pid"`
}

var serveDaemon bool

// Testable function variables for serve
var (
	serveWritePIDFn     = daemon.WritePID
	serveRemovePIDFn    = daemon.RemovePID
	serveNewServerFn    = func(authKey, controlURL string) (serverRunner, error) { return server.New(authKey, controlURL) }
	serveEnsureDirFn    = config.EnsureDir
	serveMigrateFn      = credentials.MigrateFromLegacy
	serveRegistryPathFn = config.RegistryPath
	serveLoadRegistryFn = registry.Load
	serveGetAuthKeyFn   = credentials.GetAuthKey
	serveCheckAuthFn    = credentials.RequireStoredCredential
	servePIDPathFn      = config.PIDPath
	serveIsRunningFn    = daemon.IsRunning
	serveEnsureTagsFn   = tailapi.EnsureTags
	serveCleanupFn      = tailapi.CleanupStaleNodesResult
	serveLoadGlobalFn   = config.LoadGlobalConfig
	serveLogDirFn       = config.LogDir
	serveDaemonizeFn    = daemon.Daemonize
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

func init() {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Args:  cobra.NoArgs,
		Short: "Start the TSLink server",
		Long: `Start the TSLink server to expose registered services on your Tailscale network.

Examples:
  tslink serve
  tslink serve --daemon
  tslink serve --control-url https://headscale.example.com`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := serveEnsureDirFn(); err != nil {
				return err
			}

			// Migrate file-based API key to keychain if possible
			if serveMigrateFn() {
				fmt.Fprintln(cmd.OutOrStdout(), "→ migrated API key to system keychain")
			}

			// Load registry to collect tags and ephemeral flags
			regPath, err := serveRegistryPathFn()
			if err != nil {
				return err
			}
			reg, err := serveLoadRegistryFn(regPath)
			if err != nil {
				return fmt.Errorf("load registry: %w", err)
			}

			// Collect unique tags for startup ACL preflight. Auth keys are resolved per service.
			tagSet := make(map[string]struct{})
			for _, svc := range reg.Services {
				if err := validateServiceForServe(svc); err != nil {
					return err
				}
				for _, tag := range svc.Tags {
					tagSet[tag] = struct{}{}
				}
			}
			var allTags []string
			for tag := range tagSet {
				allTags = append(allTags, tag)
			}

			// Ensure all required tags exist in tailnet ACL
			if err := serveEnsureTagsFn(context.Background(), allTags); err != nil {
				if errors.Is(err, tailapi.ErrNoAPIClient) {
					slog.Info("skipped ACL tag ensure", "reason", err.Error(), "tags", allTags)
				} else {
					return fmt.Errorf("ensure tags in ACL: %w", err)
				}
			}

			// Verify that some credential exists without deriving or consuming a one-shot auth key.
			if err := serveCheckAuthFn(); err != nil {
				return output.ErrAuth(err.Error())
			}

			pidPath, err := servePIDPathFn()
			if err != nil {
				return err
			}

			if serveIsRunningFn(pidPath) {
				return output.ErrConflict("tslink is already running (see: tslink status)")
			}

			// Clean up stale tailnet nodes before starting
			var names []string
			for _, s := range reg.Services {
				names = append(names, s.Name)
			}
			cleanup, err := serveCleanupFn(context.Background(), names)
			if err != nil {
				if !errors.Is(err, tailapi.ErrNoAPIClient) {
					return fmt.Errorf("cleanup stale nodes: %w", err)
				}
				cleanup = tailapi.CleanupResult{Skipped: true, SkipReason: err.Error()}
			}
			if cleanup.Skipped {
				slog.Info("skipped stale tailnet node cleanup", "reason", cleanup.SkipReason)
			} else if len(cleanup.Deleted) > 0 {
				slog.Info("removed stale tailnet nodes", "matched", cleanup.Matched, "deleted", cleanup.Deleted)
			}

			// Resolve control URL: flag > config > default
			controlURL, _ := cmd.Flags().GetString("control-url")
			if controlURL == "" {
				if globalCfg, err := serveLoadGlobalFn(); err == nil {
					controlURL = globalCfg.ControlURL
				}
			}

			if serveDaemon {
				logDir, err := serveLogDirFn()
				if err != nil {
					return err
				}

				outLog := filepath.Join(logDir, "tslink.out.log")
				errLog := filepath.Join(logDir, "tslink.err.log")

				pid, err := serveDaemonizeFn(outLog, errLog)
				if err != nil {
					return err
				}

				if jsonOutput(cmd) {
					output.Success("serve", ServeResult{Daemon: true, PID: pid})
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "tslink started as daemon (pid %d)\n", pid)
				}
				return nil
			}

			return runForeground(pidPath, "", controlURL)
		},
	}

	serveCmd.Flags().BoolVar(&serveDaemon, "daemon", false, "Run as background daemon")
	serveCmd.Flags().String("control-url", "", "Custom control server URL (e.g., Headscale)")
	rootCmd.AddCommand(serveCmd)
}

func validateServiceForServe(svc registry.Service) error {
	if err := registry.ValidateName(svc.Name); err != nil {
		return fmt.Errorf("service %q: %w", svc.Name, err)
	}
	for _, tag := range svc.Tags {
		if err := registry.ValidateTag(tag); err != nil {
			return fmt.Errorf("service %q has invalid tag %q: %w; fix with `tslink tags set %s tag:<lowercase-hyphen-name>` or edit registry.json", svc.Name, tag, err, svc.Name)
		}
	}
	return nil
}

func runForeground(pidPath, authKey, controlURL string) error {
	if err := serveWritePIDFn(pidPath); err != nil {
		return fmt.Errorf("write PID: %w", err)
	}
	defer serveRemovePIDFn(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := serveNewServerFn(authKey, controlURL)
	if err != nil {
		return err
	}
	if setter, ok := srv.(ensureTagsSetter); ok {
		setter.SetEnsureTagsFn(serveEnsureTagsFn)
	}
	if setter, ok := srv.(authKeyProviderSetter); ok {
		setter.SetAuthKeyProvider(func(ctx context.Context, svc registry.Service) (string, error) {
			return serveGetAuthKeyFn(ctx, credentials.AuthKeyOptions{
				Tags:        svc.Tags,
				Ephemeral:   svc.Ephemeral,
				Description: fmt.Sprintf("TSLink service %q startup auth key", svc.Name),
			})
		})
	}

	return srv.Run(ctx)
}
