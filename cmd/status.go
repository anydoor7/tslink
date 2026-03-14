package cmd

import (
	"fmt"
	"io"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

var readPIDFn = daemon.ReadPID

// StatusResult holds the status information for display.
type StatusResult struct {
	DaemonRunning bool
	DaemonPID     int
	Authenticated bool
	ServiceCount  int
}

func getStatus(pidPath, regPath string) StatusResult {
	var r StatusResult
	if isRunningFn(pidPath) {
		r.DaemonRunning = true
		r.DaemonPID, _ = readPIDFn(pidPath)
	}
	if apiKey, _ := getAPIKeyFn(); apiKey != "" {
		r.Authenticated = true
	} else if hasClientSecretFn() {
		r.Authenticated = true
	}
	if reg, err := registry.Load(regPath); err == nil {
		r.ServiceCount = len(reg.Services)
	}
	return r
}

func formatStatus(r StatusResult, out io.Writer) {
	if r.DaemonRunning {
		fmt.Fprintf(out, "→ tslink: running (pid %d)\n", r.DaemonPID)
	} else {
		fmt.Fprintln(out, "→ tslink: not running")
	}
	if r.Authenticated {
		fmt.Fprintln(out, "→ tailnet: authenticated")
	} else {
		fmt.Fprintln(out, "→ tailnet: not authenticated (run: tslink login)")
	}
	fmt.Fprintf(out, "→ services: %d registered\n", r.ServiceCount)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show TSLink status",
	Long: `Show the current status of TSLink: whether the daemon is running,
Tailscale authentication state, and number of registered services.

Example:
  tslink status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDPath()
		if err != nil {
			return err
		}
		regPath, err := config.RegistryPath()
		if err != nil {
			return err
		}
		r := getStatus(pidPath, regPath)
		formatStatus(r, cmd.OutOrStdout())
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
