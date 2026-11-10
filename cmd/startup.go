package cmd

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

type StartupEntry struct {
	Name     string `json:"name"`
	Priority int    `json:"priority"`
}

// StartupListResult lists the registered services in the order the daemon
// starts their nodes.
type StartupListResult struct {
	Path     string         `json:"path"`
	Services []StartupEntry `json:"services"`
}

type StartupSetResult struct {
	Service  string `json:"service"`
	Priority int    `json:"priority"`
}

func startupListRun(out io.Writer, isJSON bool) error {
	regPath, err := config.RegistryPath()
	if err != nil {
		return err
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return err
	}
	priorities, err := registry.LoadStartupPriorities(regPath)
	if err != nil {
		return err
	}
	names := make([]string, len(reg.Services))
	for i, svc := range reg.Services {
		names[i] = svc.Name
	}
	result := StartupListResult{Path: registry.StartupPriorityPath(regPath), Services: []StartupEntry{}}
	for _, name := range registry.StartupOrder(names, priorities) {
		result.Services = append(result.Services, StartupEntry{Name: name, Priority: priorities[name]})
	}
	if isJSON {
		output.Success("startup list", result)
		return nil
	}
	if len(result.Services) == 0 {
		fmt.Fprintln(out, "No services registered")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 4, ' ', 0)
	fmt.Fprintln(w, "ORDER\tSERVICE\tPRIORITY")
	for i, entry := range result.Services {
		fmt.Fprintf(w, "%d\t%s\t%d\n", i+1, entry.Name, entry.Priority)
	}
	return w.Flush()
}

func startupSetRun(cmd *cobra.Command, out io.Writer, serviceName, value string, isJSON bool) error {
	priority, err := strconv.Atoi(value)
	if err != nil {
		return output.ErrUsage(fmt.Sprintf("startup priority must be an integer, got %q", value))
	}
	regPath, err := config.RegistryPath()
	if err != nil {
		return err
	}
	if err := registry.SetStartupPriority(cmd.Context(), regPath, serviceName, priority); err != nil {
		return err
	}
	if isJSON {
		output.Success("startup set", StartupSetResult{Service: serviceName, Priority: priority})
		return nil
	}
	if priority == 0 {
		fmt.Fprintf(out, humanText("→ Cleared %s startup priority; it starts in registry order\n"), serviceName)
		return nil
	}
	fmt.Fprintf(out, humanText("→ Set %s startup priority to %d\n"), serviceName, priority)
	return nil
}

func init() {
	startupCmd := &cobra.Command{
		Use:   "startup",
		Short: "Choose which services the daemon starts first",
		Long: `Choose which services the daemon starts first.

The daemon starts service nodes one at a time, so after a restart the last
service in the registry is reachable a while after the first. A service with
a higher startup priority starts earlier; services with equal priority keep
registry order. The default priority is 0; a negative priority starts later
than unlisted services (pass it after --, for example: set myapp -- -5).

Priorities are stored in startup-priority.json beside registry.json and are
read on every reconciliation; they only reorder nodes that still have to
start. Builds without this command ignore the file.

Subcommands:
  list   Show the start order
  set    Set a service's startup priority (0 clears it)

Examples:
  tslink startup list
  tslink startup set myapp 10
  tslink startup set myapp 0`,
		Args: cobra.NoArgs,
		RunE: runCommandGroup,
	}

	startupListCmd := &cobra.Command{
		Use:   "list",
		Short: "Show the order the daemon starts service nodes in",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return startupListRun(cmd.OutOrStdout(), jsonOutput(cmd))
		},
	}

	startupSetCmd := &cobra.Command{
		Use:   "set <service> <priority>",
		Short: "Set a service's startup priority; higher starts first, 0 clears it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return startupSetRun(cmd, cmd.OutOrStdout(), args[0], args[1], jsonOutput(cmd))
		},
	}

	startupCmd.AddCommand(startupListCmd, startupSetCmd)
	rootCmd.AddCommand(startupCmd)
}
