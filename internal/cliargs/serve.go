// Package cliargs holds the shared Cobra flag grammar used by the CLI and
// process identity validation. It never executes a command.
package cliargs

import "github.com/spf13/cobra"

func RegisterRootFlags(root *cobra.Command) {
	root.PersistentFlags().Bool("json", false, "Output as JSON")
}

func RegisterServeFlags(serve *cobra.Command, daemon *bool) {
	serve.Flags().BoolVar(daemon, "daemon", false, "Run as background daemon")
	serve.Flags().Bool("no-browser", false, "Print the Tailscale login URL without opening a browser")
	serve.Flags().String("control-url", "", "Custom control server URL (e.g., Headscale)")
	serve.Flags().Bool("manage-acl", false, "Opt in to remote Tailscale ACL tag-owner mutation using a machine-readable side-effect plan")
	serve.Flags().Bool("no-auto-provision", false, "Disable automatic Funnel policy provisioning for every service in this serve process")
	serve.Flags().Bool("mcp", false, "Serve the MCP control plane on a dedicated tailnet-only node; every authorized peer can then change services, publish Funnel and send invitations")
}

// IsRunnableServe asks Cobra's Find, flag parser and argument validator whether
// argv selects a running serve command. This uses fresh commands for every
// identity check, avoiding mutations to the live CLI's flags.
func IsRunnableServe(argv []string) bool {
	root := &cobra.Command{Use: "tslink", Version: "identity-check"}
	serve := &cobra.Command{Use: "serve", Args: cobra.NoArgs}
	RegisterRootFlags(root)
	var daemon bool
	RegisterServeFlags(serve, &daemon)
	root.AddCommand(serve)
	root.InitDefaultHelpFlag()
	root.InitDefaultVersionFlag()
	serve.InitDefaultHelpFlag()
	command, remaining, err := root.Find(argv)
	if err != nil || command != serve {
		return false
	}
	if err := command.ParseFlags(remaining); err != nil {
		return false
	}
	if err := command.ValidateArgs(command.Flags().Args()); err != nil {
		return false
	}
	if help, _ := command.Flags().GetBool("help"); help {
		return false
	}
	if version, _ := root.Flags().GetBool("version"); version {
		return false
	}
	return true
}
