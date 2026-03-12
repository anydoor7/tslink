package cmd

import (
	"fmt"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/spf13/cobra"
)

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

			if !daemon.IsRunning(pidPath) {
				daemon.RemovePID(pidPath)
				fmt.Fprintln(cmd.OutOrStdout(), "tslink is not running")
				return nil
			}

			if err := daemon.StopDaemon(pidPath); err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "tslink stopped")
			return nil
		},
	}

	rootCmd.AddCommand(stopCmd)
}
