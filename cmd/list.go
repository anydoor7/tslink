package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

var registryPathFn = config.RegistryPath

// ListResult holds the result for JSON output.
type ListResult struct {
	Services []registry.Service `json:"services"`
	Count    int                `json:"count"`
}

func listServices(regPath string, out io.Writer) error {
	reg, err := registry.Load(regPath)
	if err != nil {
		return err
	}

	if len(reg.Services) == 0 {
		fmt.Fprintln(out, "No services registered.")
		return nil
	}

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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
}

func init() {
	listCmd := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "List registered services",
		Long: `List all services registered in the TSLink registry.

Displays a table with columns:

  NAME     Service hostname on your tailnet
  TYPE     Service type: proxy, file, or tcp
  TARGET   Local target (host:port for proxy/tcp, path for file)
  URL      Expected tailnet URL (https://<name>.<tailnet>.ts.net)

The list reflects the contents of ~/.config/tslink/registry.json. Services
are shown whether or not the gateway is currently running.

Example output:
  NAME      TYPE   TARGET                URL
  myapp     proxy  http://localhost:3000  https://myapp.<tailnet>.ts.net
  docs      file   /Users/testuser/Documents  https://docs.<tailnet>.ts.net
  mydb      tcp    localhost:5432         https://mydb.<tailnet>.ts.net

Examples:
  tslink list              Show all registered services`,
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				reg, err := registry.Load(regPath)
				if err != nil {
					return err
				}
				result := ListResult{
					Services: reg.Services,
					Count:    len(reg.Services),
				}
				output.Success("list", result)
				return nil
			}
			return listServices(regPath, cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(listCmd)
}
