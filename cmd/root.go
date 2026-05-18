package cmd

import (
	"github.com/spf13/cobra"
)

// Version is set by main before Execute() is called.
var Version string

var rootCmd = &cobra.Command{
	Use:   "tslink",
	Short: "Expose local services to your Tailscale network",
	Long: `TSLink is a local service gateway that exposes web services, file
directories, and TCP endpoints to your private Tailscale network.

Each registered service gets its own tailnet hostname with automatic TLS.
No port forwarding, no public exposure, no external Tailscale daemon required.

Supported on macOS, Linux, and Windows.

Examples:
  tslink login                                  Authenticate with Tailscale
  tslink add myapp --proxy localhost:3000        Expose a web service
  tslink add docs --dir ~/Documents              Expose a file directory
  tslink add mydb --tcp localhost:5432            Expose a TCP endpoint
  tslink serve --daemon                          Start as background daemon
  tslink list                                    List registered services
  tslink status                                  Show running status
  tslink config set control-url https://hs.example.com   Use Headscale

Use "tslink <command> --help" for detailed information about each command.`,
}

func init() {
	rootCmd.PersistentFlags().Bool("json", false, "Output as JSON")
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
}

// WasJSONRequested returns true if --json was passed on the command line.
// Safe to call after rootCmd.Execute() returns.
func WasJSONRequested() bool {
	v, _ := rootCmd.PersistentFlags().GetBool("json")
	return v
}

func Execute() error {
	rootCmd.Version = Version
	return rootCmd.Execute()
}
