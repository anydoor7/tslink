package cmd

import (
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/output"
	"github.com/spf13/cobra"
)

var isRunningFn = daemon.IsRunning
var stopDaemonFn = daemon.StopDaemon
var removePIDFn = daemon.RemovePID
var pidPathFn = config.PIDPath

// StopResult holds the result for JSON output.
type StopResult struct {
	WasRunning bool `json:"was_running"`
	Stopped    bool `json:"stopped"`
}

func stopService(pidPath string, isJSON bool, out io.Writer) error {
	if !isRunningFn(pidPath) {
		removePIDFn(pidPath)
		if isJSON {
			output.Success("stop", StopResult{WasRunning: false, Stopped: false})
			return nil
		}
		fmt.Fprintln(out, "tslink is not running")
		return nil
	}

	if err := stopDaemonFn(pidPath); err != nil {
		return err
	}

	if isJSON {
		output.Success("stop", StopResult{WasRunning: true, Stopped: true})
		return nil
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

Reads the PID from ~/.config/tslink/tslink.pid and verifies it still belongs
to TSLink before stopping it. On macOS/Linux, TSLink sends SIGTERM so the
daemon can shut down tsnet nodes gracefully. On Windows, TSLink currently uses
process termination, so stop is not graceful there.

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
			return stopService(pidPath, jsonOutput(cmd), cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(stopCmd)
}
