package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

var registryPathFn = config.RegistryPath

// ListResult holds the result for JSON output.
type ListResult struct {
	SchemaVersion string `json:"schema_version,omitempty"`
	Services      any    `json:"services"`
	Count         int    `json:"count"`
}

func (r ListResult) MarshalJSON() ([]byte, error) {
	schemaVersion := r.SchemaVersion
	if schemaVersion == "" {
		schemaVersion = inspect.SchemaVersion
	}
	type publicListResult struct {
		SchemaVersion string                `json:"schema_version"`
		Services      []inspect.ServiceView `json:"services"`
		Count         int                   `json:"count"`
	}
	return json.Marshal(publicListResult{
		SchemaVersion: schemaVersion,
		Services:      r.serviceViews(),
		Count:         r.Count,
	})
}

func (r ListResult) serviceViews() []inspect.ServiceView {
	switch services := r.Services.(type) {
	case []inspect.ServiceView:
		return services
	case []registry.Service:
		return inspect.ServiceViews(services)
	case nil:
		return nil
	default:
		return nil
	}
}

func buildListResult(services []registry.Service) ListResult {
	return ListResult{
		SchemaVersion: inspect.SchemaVersion,
		Services:      inspect.ServiceViews(services),
		Count:         len(services),
	}
}

func loadListResult(regPath string) (ListResult, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return ListResult{}, err
	}
	return buildListResult(reg.Services), nil
}

func listServices(regPath string, out io.Writer) error {
	result, err := loadListResult(regPath)
	if err != nil {
		return err
	}

	services := result.serviceViews()
	if len(services) == 0 {
		fmt.Fprintln(out, "No services registered.")
		return nil
	}

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tTYPE\tBACKEND\tENDPOINT\tEXPOSURE")
	for _, svc := range services {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", svc.Name, svc.Type, svc.Backend.Display, svc.Endpoint.Display, svc.Exposure.Kind)
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
  BACKEND  Local target (host:port for proxy/tcp, path for file)
  ENDPOINT Expected typed tailnet endpoint
  EXPOSURE Tailnet, allow-list, custom-domain, or Funnel exposure

The list reflects the contents of ~/.config/tslink/registry.json. Services
are shown whether or not the gateway is currently running.

Example output:
  NAME      TYPE   BACKEND                ENDPOINT                      EXPOSURE
  myapp     proxy  http://localhost:3000  https://myapp.<tailnet>.ts.net  tailnet
  docs      file   /Users/testuser/Documents   https://docs.<tailnet>.ts.net   tailnet
  mydb      tcp    localhost:5432         mydb.<tailnet>.ts.net:5432      tailnet

Examples:
  tslink list              Show all registered services`,
		RunE: func(cmd *cobra.Command, args []string) error {
			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				result, err := loadListResult(regPath)
				if err != nil {
					return err
				}
				output.Success("list", result)
				return nil
			}
			return listServices(regPath, cmd.OutOrStdout())
		},
	}

	rootCmd.AddCommand(listCmd)
}
