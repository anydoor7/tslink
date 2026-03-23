package cmd

import "github.com/spf13/cobra"

// jsonOutput returns true if the --json flag is set on the command or any parent.
func jsonOutput(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}
