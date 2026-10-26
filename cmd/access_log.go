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
	allowedApps []string
	App         string `json:"app,omitempty"`
	Who         string `json:"who,omitempty"`
	Since       string `json:"since,omitempty"`
	Until       string `json:"until,omitempty"`
	Decision    string `json:"decision,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

var accessLogNowFn = time.Now

func accessLogFilter(a accessLogArguments, now time.Time) (accesslog.Filter, error) {
	f := accesslog.Filter{AllowedApps: a.allowedApps, App: a.App, Who: a.Who, Decision: a.Decision, Limit: a.Limit}
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
		if e.MCP != nil {
			who = e.MCP.Principal
		}
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
	formatHTTPTraffic("HTTP", r.Summary.Traffic.HTTP, out)
	for _, p := range r.Summary.Traffic.PeopleApps {
		subject := p.Login
		if subject == "" {
			subject = p.Subject
		} else {
			subject = p.SubjectKind + " " + subject
		}
		formatHTTPTraffic(fmt.Sprintf("traffic %s app %s", subject, p.App), p.HTTPTraffic, out)
	}
	for _, a := range r.Summary.Traffic.Apps {
		formatHTTPTraffic("traffic app "+a.App, a.HTTPTraffic, out)
	}
	tcp := r.Summary.Traffic.TCP
	fmt.Fprintf(out, "TCP: %d connections (%d allowed, %d denied), %d closed, %d bytes in, %d bytes out, %d recorded gate drops\n", tcp.Connections, tcp.Allowed, tcp.Denied, tcp.Closed, tcp.BytesIn, tcp.BytesOut, tcp.GateDrops)
}

func formatHTTPTraffic(label string, h accesslog.HTTPTraffic, out io.Writer) {
	request, allowed := "never", "never"
	if h.LastRequestAt != nil {
		request = h.LastRequestAt.Format(time.RFC3339Nano)
	}
	if h.LastAllowedAt != nil {
		allowed = h.LastAllowedAt.Format(time.RFC3339Nano)
	}
	fmt.Fprintf(out, "%s: %d requests (%d allowed, %d denied), %d bytes in, %d bytes out, last request %s, last allowed %s\n", label, h.Requests, h.Allowed, h.Denied, h.BytesIn, h.BytesOut, request, allowed)
}
func accessHealthForRegistry(path, pidPath, snapshotPath string) accesslog.Health {
	h := accesslog.ReadHealth(filepath.Dir(path))
	h.Current = false // the disk journal snapshot is historical evidence only
	if !isRunningFn(pidPath) {
		return h
	}
	snapshot, err := runtimeLoadSnapshotFn(snapshotPath)
	pid, pidErr := readPIDFn(pidPath)
	lowerBound, timeErr := pidFileModTimeFn(pidPath)
	if err != nil || pidErr != nil || timeErr != nil || snapshot == nil || pid <= 0 || lowerBound.IsZero() || snapshot.DaemonPID != pid || snapshot.DaemonStartedAt.Before(lowerBound) || snapshot.UpdatedAt.Before(lowerBound) || snapshot.AccessLog == nil {
		return accesslog.Health{Enabled: true, Error: "access_log_runtime_unavailable"}
	}
	h = *snapshot.AccessLog
	h.Current = true
	return h
}
func formatAccessHealth(h accesslog.Health, out io.Writer) {
	last := "never"
	if h.LastWrite != nil {
		last = h.LastWrite.Format(time.RFC3339)
	}
	fmt.Fprintf(out, "Access log: current=%t enabled=%t last_write=%s drops=%d size=%d %s\n", h.Current, h.Enabled, last, h.Drops, h.Size, h.Error)
	for _, gap := range h.MissingHistory {
		end := "ongoing"
		if gap.End != nil {
			end = gap.End.Format(time.RFC3339Nano)
		}
		fmt.Fprintf(out, "Missing access history: %s to %s (%s)\n", gap.Start.Format(time.RFC3339Nano), end, gap.Reason)
	}
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
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: name, Description: map[string]string{
			"access_log":     "Return individual local access events, newest first, with all-match counts and a truncation flag. Use access_log to inspect specific events; use access_summary for counts without event records. The event list is limited; summaries cover all matches. No credentials, network calls or writes; suitable for viewer scopes. Query strings, headers and bodies are absent.",
			"access_summary": "Aggregate matching local access events into audit counts and traffic: HTTP requests, upload/download bytes and last request/allowed times by canonical login and app, with separate public/tagged/unknown buckets and TCP counters. Use access_summary for totals and trends; use access_log for individual events. Summaries cover all matches regardless of the event limit; denied events do not advance last allowed access. No credentials, network calls or writes; suitable for viewer scopes. Query strings, headers and bodies are absent.",
		}[name], InputSchema: objectSchema(map[string]any{
			"app": map[string]any{"type": "string"}, "who": map[string]any{"type": "string"}, "since": map[string]any{"type": "string"}, "until": map[string]any{"type": "string"}, "decision": map[string]any{"type": "string", "enum": []string{"allowed", "denied"}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000},
		}), OutputSchema: accessLogOutputSchema(name)})
	}
}
func accessLogOutputSchema(name string) map[string]any {
	summary := objectSchema(map[string]any{"count": map[string]any{"type": "integer"}, "allowed": map[string]any{"type": "integer"}, "denied": map[string]any{"type": "integer"}, "people": map[string]any{"type": "array"}, "apps": map[string]any{"type": "array"}, "reasons": map[string]any{"type": "array"}, "traffic": accessTrafficOutputSchema()}, "count", "allowed", "denied", "people", "apps", "traffic")
	if name == "access_summary" {
		return summary
	}
	return objectSchema(map[string]any{"events": map[string]any{"type": "array", "items": nestedObjectSchema("Version 1 privacy-preserving access event.")}, "summary": summary, "truncated": map[string]any{"type": "boolean"}}, "events", "summary", "truncated")
}

func accessTrafficOutputSchema() map[string]any {
	httpSchema := func(extra map[string]any, required ...string) map[string]any {
		props := map[string]any{}
		for _, field := range []string{"requests", "allowed", "denied", "bytes_in", "bytes_out"} {
			props[field] = map[string]any{"type": "integer"}
			required = append(required, field)
		}
		for _, field := range []string{"last_request_at", "last_allowed_at"} {
			props[field] = map[string]any{"type": []string{"string", "null"}, "format": "date-time"}
			required = append(required, field)
		}
		for field, schema := range extra {
			props[field] = schema
		}
		return objectSchema(props, required...)
	}
	tcp := map[string]any{}
	fields := []string{"connections", "allowed", "denied", "closed", "bytes_in", "bytes_out", "gate_drops"}
	for _, field := range fields {
		tcp[field] = map[string]any{"type": "integer"}
	}
	return objectSchema(map[string]any{
		"http": httpSchema(nil),
		"people_apps": map[string]any{"type": "array", "items": httpSchema(map[string]any{
			"subject_kind": map[string]any{"type": "string", "enum": []string{"human", "tagged", "public", "unknown"}},
			"login":        map[string]any{"type": "string"}, "subject": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"},
		}, "subject_kind", "app")},
		"apps": map[string]any{"type": "array", "items": httpSchema(map[string]any{"app": map[string]any{"type": "string"}}, "app")},
		"tcp":  objectSchema(tcp, fields...),
	}, "http", "people_apps", "apps", "tcp")
}

func init() {
	cmd := &cobra.Command{Use: "path <app> <prefix|full|off|inherit|true|false>", Short: "Set per-app access path mode (global opt-out takes precedence)", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		var value *bool
		var mode string
		switch args[1] {
		case "true", "false":
			b := args[1] == "true"
			value = &b
		case "prefix", "full", "off":
			mode = args[1]
		case "inherit":
		default:
			return output.ErrUsage("path must be prefix, full, off, inherit, true or false")
		}
		path, err := config.RegistryPath()
		if err != nil {
			return err
		}
		svc, err := registry.MutateService(path, args[0], func(svc registry.Service) (registry.Service, error) {
			svc.AccessLogPath = value
			svc.AccessLogPathMode = mode
			return svc, nil
		})
		if err != nil {
			return err
		}
		result := struct {
			App        string `json:"app"`
			RecordPath *bool  `json:"record_path"`
			PathMode   string `json:"path_mode"`
		}{svc.Name, svc.AccessLogPath, svc.AccessLogPathMode}
		if jsonOutput(cmd) {
			output.Success("access path", result)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "%s access path recording: %s\n", args[0], args[1])
		}
		return nil
	}}
	accessCmd.AddCommand(cmd)
}
