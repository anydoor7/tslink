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
		RunE: func(cmd *cobra.Command, args []string) error {
			pidPath, err := config.PIDPath()
			if err != nil {
				return err
			}
			return stopService(pidPath, cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(stopCmd)
}
