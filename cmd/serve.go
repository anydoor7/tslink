package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/server"
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

			stateDir, err := config.TsnetStateDir()
			if err != nil {
				return err
			}
			info, err := os.Stat(stateDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("not authenticated — run 'tslink login' first")
				}
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("not authenticated — run 'tslink login' first")
			}

			pidPath, err := config.PIDPath()
			if err != nil {
				return err
			}

			if daemon.IsRunning(pidPath) {
				return fmt.Errorf("tslink is already running (see: tslink status)")
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

			return runForeground(pidPath)
		},
	}

	serveCmd.Flags().BoolVar(&serveDaemon, "daemon", false, "Run as background daemon")
	rootCmd.AddCommand(serveCmd)
}

func runForeground(pidPath string) error {
	if err := daemon.WritePID(pidPath); err != nil {
		return fmt.Errorf("write PID: %w", err)
	}
	defer daemon.RemovePID(pidPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := server.New()
	if err != nil {
		return err
	}

	if err := srv.Run(ctx); err != nil {
		return err
	}

	return nil
}
