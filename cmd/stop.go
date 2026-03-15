package cmd

import (
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/spf13/cobra"
)

var isRunningFn = daemon.IsRunning
var stopDaemonFn = daemon.StopDaemon
var removePIDFn = daemon.RemovePID
var pidPathFn = config.PIDPath

func stopService(pidPath string, out io.Writer) error {
	if !isRunningFn(pidPath) {
		removePIDFn(pidPath)
		fmt.Fprintln(out, "tslink is not running")
		return nil
	}

	if err := stopDaemonFn(pidPath); err != nil {
		return err
	}

	fmt.Fprintln(out, "tslink stopped")
	return nil
}

func init() {
	stopCmd := &cobra.Command{
		Use:   "stop",
		Args:  cobra.NoArgs,
		Short: "Stop the TSLink daemon",
		Long: `Stop the running TSLink gateway daemon.

Reads the PID from ~/.config/tslink/tslink.pid and sends a termination signal
(SIGTERM on macOS/Linux, process kill on Windows). The daemon shuts down all
tsnet nodes gracefully before exiting.

If the daemon is not running, the stale PID file (if any) is cleaned up and
a "not running" message is displayed.

Examples:
  tslink stop                  Stop the background daemon
  tslink stop && tslink serve  Restart the gateway`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pidPath, err := pidPathFn()
			if err != nil {
				return err
			}
			return stopService(pidPath, cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(stopCmd)
}
