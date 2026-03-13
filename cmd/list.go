package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

func init() {
	listCmd := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List registered services",
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath, err := config.RegistryPath()
			if err != nil {
				return err
			}

			reg, err := registry.Load(regPath)
			if err != nil {
				return err
			}

			if len(reg.Services) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No services registered.")
				return nil
			}

			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(writer, "NAME\tTYPE\tTARGET\tURL")
			for _, svc := range reg.Services {
				target := svc.Target
				if svc.Type == registry.TypeFile {
					target = svc.Path
				}
				url := fmt.Sprintf("https://%s.<tailnet>.ts.net", svc.Name)
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", svc.Name, svc.Type, target, url)
			}

			return writer.Flush()
		},
	}

	rootCmd.AddCommand(listCmd)
}
