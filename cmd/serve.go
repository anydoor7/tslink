package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/server"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var serveDaemon bool

func init() {
	serveCmd := &cobra.Command{
		Use:   "serve",
		Args:  cobra.NoArgs,
		Short: "Start the TSLink server",
		Long: `Start the TSLink server to expose registered services on your Tailscale network.

Examples:
  tslink serve
  tslink serve --daemon`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.EnsureDir(); err != nil {
				return err
			}

			// Migrate file-based API key to keychain if possible
			if credentials.MigrateFromLegacy() {
				fmt.Fprintln(cmd.OutOrStdout(), "→ migrated API key to system keychain")
			}

			// Get auth key (derive from API key, or fall back to legacy authkey file)
			authKey, err := credentials.GetAuthKey(context.Background())
			if err != nil {
				return err
			}

			pidPath, err := config.PIDPath()
			if err != nil {
				return err
			}

			if daemon.IsRunning(pidPath) {
				return fmt.Errorf("tslink is already running (see: tslink status)")
			}

			// Clean up stale tailnet nodes before starting
			regPath, err := config.RegistryPath()
			if err == nil {
				if reg, err := registry.Load(regPath); err == nil {
					var names []string
					for _, s := range reg.Services {
						names = append(names, s.Name)
					}
					_ = tailapi.CleanupStaleNodes(context.Background(), names)
				}
			}

			if serveDaemon {
				logDir, err := config.LogDir()
				if err != nil {
					return err
				}

				outLog := filepath.Join(logDir, "tslink.out.log")
				errLog := filepath.Join(logDir, "tslink.err.log")

				pid, err := daemon.Daemonize(outLog, errLog)
				if err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "tslink started as daemon (pid %d)\n", pid)
				return nil
			}

			return runForeground(pidPath, authKey)
		},
	}

	serveCmd.Flags().BoolVar(&serveDaemon, "daemon", false, "Run as background daemon")
	rootCmd.AddCommand(serveCmd)
}

func runForeground(pidPath, authKey string) error {
	if err := daemon.WritePID(pidPath); err != nil {
		return fmt.Errorf("write PID: %w", err)
	}
	defer daemon.RemovePID(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New(authKey)
	if err != nil {
		return err
	}

	return srv.Run(ctx)
}
