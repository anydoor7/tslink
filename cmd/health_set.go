package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// healthSetArguments is the wire shape of the health_set tool and the parsed
// form of `tslink health set`. A nil field keeps the stored value.
type healthSetArguments struct {
	App          string  `json:"app"`
	Path         *string `json:"path,omitempty"`
	StatusMin    *int    `json:"status_min,omitempty"`
	StatusMax    *int    `json:"status_max,omitempty"`
	BodyContains *string `json:"body_contains,omitempty"`
	Timeout      *string `json:"timeout,omitempty"`
	Interval     *string `json:"interval,omitempty"`
	Reset        bool    `json:"reset,omitempty"`
}

func (a healthSetArguments) update() registry.HealthUpdate {
	return registry.HealthUpdate{Reset: a.Reset, Path: a.Path, BodyContains: a.BodyContains, StatusMin: a.StatusMin, StatusMax: a.StatusMax, Timeout: a.Timeout, Interval: a.Interval}
}

// HealthSettingsView is a service's effective health check. Like the other
// diagnostic views it reports whether a body check exists, never its text.
type HealthSettingsView struct {
	Path      string `json:"path,omitempty"`
	StatusMin int    `json:"status_min,omitempty"`
	StatusMax int    `json:"status_max,omitempty"`
	BodyCheck bool   `json:"body_check,omitempty"`
	Timeout   string `json:"timeout"`
	Interval  string `json:"interval"`
}

type HealthSetResult struct {
	App  string `json:"app"`
	Type string `json:"type"`
	// Changed is false when the stored settings already matched; nothing was written.
	Changed bool `json:"changed"`
	// Defaults is true when the service stores no health settings.
	Defaults bool               `json:"defaults"`
	Health   HealthSettingsView `json:"health"`
}

func healthSettingsView(svc registry.Service) HealthSettingsView {
	c := registry.HealthConfig{}
	if svc.Health != nil {
		c = *svc.Health
	}
	c = c.Effective()
	v := HealthSettingsView{Timeout: c.Timeout, Interval: c.Interval}
	if svc.Type == registry.TypeProxy {
		v.Path, v.StatusMin, v.StatusMax, v.BodyCheck = c.Path, c.StatusMin, c.StatusMax, c.BodyContains != ""
	}
	return v
}

// setHealthContext is shared by the CLI and the health_set tool. A routed app
// requires the current portal owner for remote MCP callers, checked inside the
// registry write transaction like the other route-affecting changes.
func setHealthContext(ctx context.Context, regPath string, args healthSetArguments) (HealthSetResult, error) {
	u := args.update()
	if !u.Reset && u == (registry.HealthUpdate{}) {
		return HealthSetResult{}, output.ErrUsage("nothing to change: pass a health option or --reset")
	}
	svc, changed, err := registry.SetHealthContext(ctx, regPath, args.App, u, func(reg *registry.Registry, svc registry.Service) error {
		return authorizePortalServiceMutation(ctx, reg, svc)
	})
	if err != nil {
		return HealthSetResult{}, err
	}
	return HealthSetResult{App: svc.Name, Type: svc.Type, Changed: changed, Defaults: svc.Health == nil, Health: healthSettingsView(svc)}, nil
}

func formatHealthSet(out io.Writer, r HealthSetResult) {
	h := r.Health
	verb := "updated"
	if !r.Changed {
		verb = "unchanged"
	}
	fmt.Fprintf(out, humanText("✓ %s health check %s:"), r.App, verb)
	if r.Type == registry.TypeProxy {
		fmt.Fprintf(out, " GET %s, status %d-%d", h.Path, h.StatusMin, h.StatusMax)
		if h.BodyCheck {
			fmt.Fprint(out, ", body check")
		}
		fmt.Fprint(out, ",")
	}
	fmt.Fprintf(out, " timeout %s, interval %s", h.Timeout, h.Interval)
	if r.Defaults {
		fmt.Fprint(out, " (defaults)")
	}
	fmt.Fprintln(out)
}

var healthCmd = &cobra.Command{Use: "health", Short: "Manage app health checks", Args: cobra.NoArgs, RunE: runCommandGroup}

func init() {
	set := &cobra.Command{
		Use:   "set <app>",
		Short: "Change only an app's health check, keeping access, routes and node identity",
		Long: "Change only the health check of a registered app in one registry transaction. " +
			"Access lists, tags, people scope, portal routes and the enrolled node are kept, and the running node is not restarted. " +
			"Unset options keep their stored value; an empty value or 0 selects the default. --reset returns to the defaults: " +
			"GET / accepting 200-399, 5s timeout, 1m interval. Path, status range and body apply to proxy services only. " +
			"Use this instead of re-running add, which replaces the whole service.",
		Example: "  tslink health set micwave-phone --path /health\n  tslink health set opencode-server --status-max 401\n  tslink health set photos --reset",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			args := healthSetArguments{App: a[0]}
			flags := cmd.Flags()
			str := func(name string) *string {
				if !flags.Changed(name) {
					return nil
				}
				v, _ := flags.GetString(name)
				return &v
			}
			num := func(name string) *int {
				if !flags.Changed(name) {
					return nil
				}
				v, _ := flags.GetInt(name)
				return &v
			}
			args.Path, args.BodyContains, args.Timeout, args.Interval = str("path"), str("body"), str("timeout"), str("interval")
			args.StatusMin, args.StatusMax = num("status-min"), num("status-max")
			args.Reset, _ = flags.GetBool("reset")
			path, err := config.RegistryPath()
			if err != nil {
				return err
			}
			result, err := setHealthContext(cmd.Context(), path, args)
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("health set", result)
			} else {
				formatHealthSet(cmd.OutOrStdout(), result)
			}
			return nil
		},
	}
	set.Flags().String("path", "", "HTTP probe path joined to the backend base path (default /; proxy only)")
	set.Flags().Int("status-min", 0, "Lowest accepted HTTP status (default 200; proxy only)")
	set.Flags().Int("status-max", 0, "Highest accepted HTTP status (default 399 with no lower bound set, otherwise 299; proxy only)")
	set.Flags().String("body", "", "Expected substring in the first 64 KiB; empty removes the check (proxy only; avoid secrets in argv)")
	set.Flags().String("timeout", "", "Probe timeout, 100ms to 30s (default 5s)")
	set.Flags().String("interval", "", "Probe interval, 10s to 1d and at least the timeout (default 1m)")
	set.Flags().Bool("reset", false, "Discard stored health settings before applying the other options")
	healthCmd.AddCommand(set)
	rootCmd.AddCommand(healthCmd)

	mcpToolHints["health_set"] = mcpHints(false, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
		Name: "health_set",
		Description: "Change only one app's health check in a single registry transaction. Keeps access lists, tags, people scope, portal routes and the enrolled node; never re-enrolls. " +
			"Omitted fields keep their stored value; empty or 0 selects the default; reset returns to defaults (GET / accepting 200-399). path, status range and body_contains apply to proxy apps only. " +
			"HTTP MCP requires the current portal owner for a routed app, checked in the write transaction. Owner scope only.",
		InputSchema: objectSchema(map[string]any{
			"app":           map[string]any{"type": "string", "minLength": 1},
			"path":          map[string]any{"type": "string", "description": "HTTP probe path joined to the backend base path; empty means /."},
			"status_min":    map[string]any{"type": "integer", "minimum": 0, "maximum": 599, "description": "Lowest accepted status; 0 means the default."},
			"status_max":    map[string]any{"type": "integer", "minimum": 0, "maximum": 599, "description": "Highest accepted status; 0 means the default (399 when status_min is also unset, otherwise 299)."},
			"body_contains": map[string]any{"type": "string", "maxLength": 4096, "description": "Expected substring in the first 64 KiB; empty removes the check."},
			"timeout":       map[string]any{"type": "string", "description": "100ms..30s; empty means 5s."},
			"interval":      map[string]any{"type": "string", "description": "10s..1d and at least timeout; empty means 1m."},
			"reset":         map[string]any{"type": "boolean", "description": "Discard stored settings before applying the other fields."},
		}, "app"),
		OutputSchema: objectSchema(map[string]any{
			"app":      map[string]any{"type": "string"},
			"type":     map[string]any{"type": "string"},
			"changed":  map[string]any{"type": "boolean"},
			"defaults": map[string]any{"type": "boolean"},
			"health": objectSchema(map[string]any{
				"path": map[string]any{"type": "string"}, "status_min": map[string]any{"type": "integer"}, "status_max": map[string]any{"type": "integer"},
				"body_check": map[string]any{"type": "boolean"}, "timeout": map[string]any{"type": "string"}, "interval": map[string]any{"type": "string"},
			}, "timeout", "interval"),
		}, "app", "type", "changed", "defaults", "health"),
	})
}
