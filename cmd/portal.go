package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsRuntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/spf13/cobra"
)

type portalArguments struct {
	Hostname string   `json:"hostname,omitempty"`
	Owner    string   `json:"owner"`
	Admins   []string `json:"admins,omitempty"`
	Funnel   bool     `json:"funnel,omitempty"`
}

// portalView never promotes old, stopped or registry-mismatched runtime URLs.
func portalView(reg *registry.Registry, snapshot *tsRuntime.Snapshot, exact bool) tsRuntime.PortalState {
	view := tsRuntime.PortalState{State: "disabled"}
	if reg == nil || reg.Portal == nil {
		return view
	}
	view.Enabled, view.Hostname = reg.Portal.Enabled, reg.Portal.Hostname
	if !view.Enabled {
		return view
	}
	view.State = "pending"
	if exact && snapshot != nil && snapshot.Portal.Enabled && snapshot.Portal.Hostname == view.Hostname {
		view = snapshot.Portal
		if view.State != "running" {
			view.URL = ""
		}
	}
	return view
}

func readPortalView(reg *registry.Registry, regPath string, running bool, pid int) tsRuntime.PortalState {
	if reg == nil || reg.Portal == nil || !reg.Portal.Enabled {
		return portalView(reg, nil, false)
	}
	snapshot, err := runtimeLoadSnapshotFn(filepath.Join(filepath.Dir(regPath), "runtime.json"))
	expected := tsRuntime.ExpectedRuntime{DaemonPID: pid, CurrentRegistryFingerprint: currentRegistryFingerprint(regPath)}
	if lower, err := pidFileModTimeFn(filepath.Join(filepath.Dir(regPath), "tslink.pid")); err == nil {
		expected.DaemonStartedAtLowerBound = lower
	}
	freshness := tsRuntime.Classify(snapshot, err, expected)
	return portalView(reg, snapshot, running && freshness.Exact)
}

func changePortal(paths sharePaths, args portalArguments, enable bool) (tsRuntime.PortalState, error) {
	return changePortalContext(context.Background(), paths, args, enable)
}

func changePortalContext(ctx context.Context, paths sharePaths, args portalArguments, enable bool) (tsRuntime.PortalState, error) {
	if enable {
		if err := requireRequestOwner(ctx, paths.Registry); err != nil {
			return tsRuntime.PortalState{}, err
		}
	}
	if args.Funnel {
		return tsRuntime.PortalState{}, registry.CodedError{Code: registry.CodePortalFunnelRefused, Message: "the portal is Tailnet-only and cannot use Funnel"}
	}
	if enable {
		if args.Hostname == "" {
			args.Hostname = registry.DefaultPortalHostname
		}
		var err error
		args.Owner, err = registry.NormalizePerson(args.Owner)
		if err != nil {
			return tsRuntime.PortalState{}, err
		}
		for i := range args.Admins {
			args.Admins[i], err = registry.NormalizePerson(args.Admins[i])
			if err != nil {
				return tsRuntime.PortalState{}, err
			}
		}
		cfg, err := config.LoadGlobalConfig()
		if err != nil {
			return tsRuntime.PortalState{}, err
		}
		if cfg.MCP != nil && cfg.MCP.Enabled {
			name := cfg.MCP.NodeName
			if name == "" {
				name = "tslink-mcp"
			}
			if name == args.Hostname {
				return tsRuntime.PortalState{}, output.ErrConflict("portal hostname is already used by the MCP node")
			}
		}
		if err := registry.SetPortalAuthorized(paths.Registry, &registry.PortalConfig{Enabled: true, Hostname: args.Hostname, Owner: args.Owner, Admins: args.Admins}, func(reg *registry.Registry) error {
			return requireRequestOwnerInRegistry(ctx, reg)
		}, ctx); err != nil {
			return tsRuntime.PortalState{}, err
		}
	} else if err := registry.DisablePortalContext(ctx, paths.Registry); err != nil {
		return tsRuntime.PortalState{}, err
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		if !enable && os.IsNotExist(err) {
			return tsRuntime.PortalState{State: "disabled"}, nil
		}
		return tsRuntime.PortalState{}, err
	}
	running := inviteIsRunningFn(paths.PID)
	pid, _ := inviteReadPIDFn(paths.PID)
	return readPortalView(reg, paths.Registry, running, pid), nil
}

func newPortalCmd() *cobra.Command {
	group := &cobra.Command{Use: "portal", Short: "Manage the private home page for each visitor", RunE: runCommandGroup}
	var args portalArguments
	enable := &cobra.Command{Use: "enable", Short: "Enable an independent Tailnet-only portal node", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		paths, err := resolveSharePaths()
		if err != nil {
			return err
		}
		view, err := changePortal(paths, args, true)
		if err != nil {
			return err
		}
		if jsonOutput(c) {
			output.WriteJSON(c.OutOrStdout(), output.NewSuccess("portal enable", view))
		} else {
			fmt.Fprintf(c.OutOrStdout(), "Portal %s: %s\n", view.Hostname, view.State)
			if view.URL != "" {
				fmt.Fprintln(c.OutOrStdout(), view.URL)
			} else {
				fmt.Fprintln(c.OutOrStdout(), "Saved. The running daemon will apply this setting. If stopped, run tslink serve; then check tslink status --urls for the portal address.")
			}
		}
		return nil
	}}
	enable.Flags().StringVar(&args.Hostname, "hostname", registry.DefaultPortalHostname, "Portal node hostname")
	enable.Flags().StringVar(&args.Owner, "owner", "", "Owner's verified Tailscale login (required)")
	enable.Flags().StringSliceVar(&args.Admins, "admins", nil, "Additional administrator logins; these identities can open every private app")
	enable.Flags().BoolVar(&args.Funnel, "funnel", false, "Refused: the portal must stay Tailnet-only")
	disable := &cobra.Command{Use: "disable", Short: "Stop the portal without changing app services", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		paths, err := resolveSharePaths()
		if err != nil {
			return err
		}
		view, err := changePortal(paths, portalArguments{}, false)
		if err != nil {
			return err
		}
		if jsonOutput(c) {
			output.WriteJSON(c.OutOrStdout(), output.NewSuccess("portal disable", view))
		} else {
			fmt.Fprintln(c.OutOrStdout(), "Portal disabled. The running daemon will close its listener.")
		}
		return nil
	}}
	group.AddCommand(enable, disable)
	return group
}

func init() { rootCmd.AddCommand(newPortalCmd()) }

func formatPortal(out io.Writer, p tsRuntime.PortalState) {
	state := p.State
	if state == "" {
		state = "disabled"
	}
	fmt.Fprintf(out, "Portal: %s", state)
	if p.Hostname != "" {
		fmt.Fprintf(out, " (%s)", p.Hostname)
	}
	if p.URL != "" {
		fmt.Fprintf(out, " %s", p.URL)
	}
	if p.Error != "" {
		fmt.Fprintf(out, " [%s]", p.Error)
	}
	fmt.Fprintln(out)
}
