package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

type accessLogArguments struct {
	App      string `json:"app,omitempty"`
	Who      string `json:"who,omitempty"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
	Decision string `json:"decision,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

var accessLogNowFn = time.Now

func accessLogFilter(a accessLogArguments, now time.Time) (accesslog.Filter, error) {
	f := accesslog.Filter{App: a.App, Who: a.Who, Decision: a.Decision, Limit: a.Limit}
	if a.Since != "" {
		at, err := time.Parse(time.RFC3339Nano, a.Since)
		if err != nil {
			d, e := time.ParseDuration(a.Since)
			if e != nil || d <= 0 {
				return f, output.ErrUsage("since must be a positive duration or RFC3339 UTC timestamp")
			}
			at = now.Add(-d)
		}
		at = at.UTC()
		f.Since = &at
	}
	if a.Until != "" {
		at, err := time.Parse(time.RFC3339Nano, a.Until)
		if err != nil {
			return f, output.ErrUsage("until must be an RFC3339 timestamp")
		}
		at = at.UTC()
		f.Until = &at
	}
	if err := f.Validate(); err != nil {
		return f, output.ErrUsage(err.Error())
	}
	return f, nil
}
func readAccessLog(a accessLogArguments) (accesslog.Result, error) {
	dir, err := config.Dir()
	if err != nil {
		return accesslog.Result{}, err
	}
	return readAccessLogAt(dir, a)
}
func readAccessLogAt(dir string, a accessLogArguments) (accesslog.Result, error) {
	f, err := accessLogFilter(a, accessLogNowFn())
	if err != nil {
		return accesslog.Result{}, err
	}
	return accesslog.Query(dir, f)
}
func formatAccessLog(r accesslog.Result, out io.Writer) {
	for _, e := range r.Events {
		who := e.Identity.Login
		if who == "" {
			who = e.Identity.Node
		}
		if who == "" {
			who = e.Identity.Remote
		}
		fmt.Fprintf(out, "%s %s %s %s %s %d %s %s\n", e.Time.Format(time.RFC3339), e.App, who, e.Kind, e.Method, e.Status, e.Decision, e.Reason)
	}
	fmt.Fprintf(out, "%d events (%d allowed, %d denied)\n", r.Summary.Count, r.Summary.Allowed, r.Summary.Denied)
	for _, p := range r.Summary.People {
		fmt.Fprintf(out, "person %s: %d events\n", p.Key, p.Count)
	}
	for _, a := range r.Summary.Apps {
		last := "never"
		if a.LastSeen != nil {
			last = a.LastSeen.Format(time.RFC3339)
		}
		fmt.Fprintf(out, "app %s: %d events, last seen %s\n", a.Key, a.Count, last)
	}
}
func accessHealthForRegistry(path string) accesslog.Health {
	return accesslog.ReadHealth(filepath.Dir(path))
}
func formatAccessHealth(h accesslog.Health, out io.Writer) {
	last := "never"
	if h.LastWrite != nil {
		last = h.LastWrite.Format(time.RFC3339)
	}
	fmt.Fprintf(out, "Access log: enabled=%t last_write=%s drops=%d size=%d %s\n", h.Enabled, last, h.Drops, h.Size, h.Error)
}
func init() {
	var a accessLogArguments
	command := &cobra.Command{Use: "log", Short: "Read local app access history and summaries", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("limit") && a.Limit == 0 {
			return output.ErrUsage("limit must be 1..10000")
		}
		r, err := readAccessLog(a)
		if err != nil {
			return err
		}
		if jsonOutput(cmd) {
			output.Success("access log", r)
		} else {
			formatAccessLog(r, cmd.OutOrStdout())
		}
		return nil
	}}
	command.Flags().StringVar(&a.App, "app", "", "Filter by app/service")
	command.Flags().StringVar(&a.Who, "who", "", "Filter by login, node name or tag")
	command.Flags().StringVar(&a.Since, "since", "", "Positive duration (24h) or RFC3339 lower bound")
	command.Flags().StringVar(&a.Until, "until", "", "RFC3339 upper bound (inclusive)")
	command.Flags().StringVar(&a.Decision, "decision", "", "Filter allowed or denied")
	command.Flags().IntVar(&a.Limit, "limit", 100, "Maximum returned events (1..10000); summaries cover all matches")
	accessCmd.AddCommand(command)
	for _, name := range []string{"access_log", "access_summary"} {
		mcpToolHints[name] = mcpHints(true, false, true, false)
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: name, Description: "Read local app access history. No credentials, network calls or writes. Suitable for viewer scopes. Query strings, headers and bodies are absent. Summaries count all matching events; the event list is limited.", InputSchema: objectSchema(map[string]any{
			"app": map[string]any{"type": "string"}, "who": map[string]any{"type": "string"}, "since": map[string]any{"type": "string"}, "until": map[string]any{"type": "string"}, "decision": map[string]any{"type": "string", "enum": []string{"allowed", "denied"}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
		}), OutputSchema: accessLogOutputSchema(name)})
	}
}
func accessLogOutputSchema(name string) map[string]any {
	summary := nestedObjectSchema("Counts, per-person counts and per-app counts/last-seen for all matching events; denied events never advance last-seen.")
	if name == "access_summary" {
		return objectSchema(map[string]any{"count": map[string]any{"type": "integer"}, "allowed": map[string]any{"type": "integer"}, "denied": map[string]any{"type": "integer"}, "people": map[string]any{"type": "array"}, "apps": map[string]any{"type": "array"}}, "count", "allowed", "denied", "people", "apps")
	}
	return objectSchema(map[string]any{"events": map[string]any{"type": "array", "items": nestedObjectSchema("Version 1 privacy-preserving access event.")}, "summary": summary, "truncated": map[string]any{"type": "boolean"}}, "events", "summary", "truncated")
}

func init() {
	cmd := &cobra.Command{Use: "path <app> <true|false|inherit>", Short: "Set per-app access path recording (global opt-out takes precedence)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		var value *bool
		switch args[1] {
		case "true", "false":
			b := args[1] == "true"
			value = &b
		case "inherit":
		default:
			return output.ErrUsage("path must be true, false or inherit")
		}
		path, err := config.RegistryPath()
		if err != nil {
			return err
		}
		svc, err := registry.MutateService(path, args[0], func(svc registry.Service) (registry.Service, error) { svc.AccessLogPath = value; return svc, nil })
		if err != nil {
			return err
		}
		result := struct {
			App        string `json:"app"`
			RecordPath *bool  `json:"record_path"`
		}{svc.Name, svc.AccessLogPath}
		if jsonOutput(cmd) {
			output.Success("access path", result)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s access path recording: %s\n", args[0], args[1])
		}
		return nil
	}}
	accessCmd.AddCommand(cmd)
}
