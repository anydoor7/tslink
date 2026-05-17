package cmd

import "github.com/spf13/cobra"

// jsonOutput returns true if the --json flag is set on the command or any parent.
func jsonOutput(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	if root := cmd.Root(); root != nil {
		if v, err := root.PersistentFlags().GetBool("json"); err == nil && v {
			return true
		}
	}
	if v, err := cmd.Flags().GetBool("json"); err == nil {
		return v
	}
	if v, err := cmd.InheritedFlags().GetBool("json"); err == nil {
		return v
	}
	return false
}
