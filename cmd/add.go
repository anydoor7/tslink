package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

func hasScheme(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

func init() {
	addCmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a local service or file directory",
		Long: `Register a local service or file directory to expose on the Tailscale network.

Examples:
  tslink add myapp --proxy localhost:3000    Expose a web service
  tslink add docs --dir ~/Documents          Expose a file directory`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := registry.ValidateName(name); err != nil {
				return err
			}

			proxyTarget, err := cmd.Flags().GetString("proxy")
			if err != nil {
				return err
			}

			dirPath, err := cmd.Flags().GetString("dir")
			if err != nil {
				return err
			}

			if (proxyTarget == "" && dirPath == "") || (proxyTarget != "" && dirPath != "") {
				return fmt.Errorf("exactly one of --proxy or --dir must be provided")
			}

			if err := config.EnsureDir(); err != nil {
				return err
			}

			regPath, err := config.RegistryPath()
			if err != nil {
				return err
			}

			if proxyTarget != "" {
				if !hasScheme(proxyTarget) {
					proxyTarget = "http://" + proxyTarget
				}

				if err := registry.Add(regPath, registry.Service{
					Name:   name,
					Type:   registry.TypeProxy,
					Target: proxyTarget,
				}); err != nil {
					return err
				}

				fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ registered: /s/%s (→ %s)\n", name, proxyTarget)
				fmt.Fprintln(cmd.OutOrStdout(), "Full URL available after 'tslink serve' starts")
				return nil
			}

			absPath, err := filepath.Abs(dirPath)
			if err != nil {
				return err
			}

			info, err := os.Stat(absPath)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", absPath)
			}

			if err := registry.Add(regPath, registry.Service{
				Name: name,
				Type: registry.TypeFile,
				Path: absPath,
			}); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ registered: /f/%s/ (→ %s)\n", name, absPath)
			fmt.Fprintln(cmd.OutOrStdout(), "Full URL available after 'tslink serve' starts")
			return nil
		},
	}

	addCmd.Flags().String("proxy", "", "Proxy target in host:port or URL form")
	addCmd.Flags().String("dir", "", "Directory to expose")
	rootCmd.AddCommand(addCmd)
}
