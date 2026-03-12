package cmd

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tslink",
	Short: "Expose local services to your Tailscale network",
	Long: `TSLink is a local service gateway that exposes web services and
file directories on your Mac to your private Tailscale network.

Examples:
  tslink login                          Login to Tailscale
  tslink serve --daemon                 Start the daemon
  tslink add myapp --proxy localhost:3000   Expose a local web service
  tslink add docs --dir ~/Documents     Expose a file directory
  tslink list                           List registered services
  tslink status                         Show running status`,
}

func Execute() error {
	return rootCmd.Execute()
}
