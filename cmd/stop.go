package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/daemon"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/spf13/cobra"
)

var isRunningFn = commandIsDaemonRunning
var stopDaemonFn = daemon.StopDaemon
var removePIDFn = daemon.RemovePID // retained as a compatibility test seam; not called on an inconclusive stop path
var isProcessAbsentFromPIDFileFn = daemon.IsProcessAbsentFromPIDFile
var pidPathFn = config.PIDPath

func commandIsDaemonRunning(pidPath string) bool {
	if daemon.IsRunning(pidPath) {
		return true
	}
	// A command whose own PID appears in the daemon PID file must not destructively
	// clean up that file. Keep the command layer conservative while the daemon
	// identity layer still requires an explicit `serve` argv.
	pid, err := daemon.ReadPID(pidPath)
	return err == nil && pid == os.Getpid()
}

// StopResult holds the result for JSON output.
type StopResult struct {
	WasRunning bool `json:"was_running"`
	Stopped    bool `json:"stopped"`
}

func stopService(pidPath string, isJSON bool, out io.Writer) error {
	if !isRunningFn(pidPath) {
		if isProcessAbsentFromPIDFileFn(pidPath) {
			removePIDFn(pidPath)
		}
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
daemon can shut down tsnet nodes gracefully. On Windows, a current-user named
event requests the same graceful shutdown. An older daemon without that event
returns an explicit error; it is never force-terminated by this command.

If TSLink was installed as a macOS LaunchAgent, launchd KeepAlive will restart
the daemon after 'tslink stop', throttled by ThrottleInterval=30. Run
'tslink uninstall' first when you want to disable macOS autostart instead of
restarting.

If TSLink was installed as a Linux systemd user service, a graceful stop exits
successfully, so Restart=on-failure leaves it stopped. Run 'tslink install' or
'systemctl --user start tslink.service' to start the installed service again.

A Windows scheduled task likewise leaves a successful graceful stop stopped;
run 'tslink install' to start it again. Crash restart uses a 60-second delay.

If the daemon is not running, a "not running" message is displayed. Stale PID
identity files are cleaned up only after process absence is confirmed; an
inconclusive check leaves them in place so it cannot orphan a live daemon.

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
