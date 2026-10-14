package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

func nodeExpiryNext() []string {
	return []string{"tslink doctor --json", "Open https://login.tailscale.com/admin/machines and reauthenticate this service node or review its key expiry setting"}
}

func currentHealth(h health.State, svc registry.Service, now time.Time) health.State {
	return health.CurrentAt(h, svc, now)
}

func formatAppHealth(out io.Writer, name string, h health.State, e health.Expiry) {
	state := h.State
	if state == "" {
		state = health.Unknown
	}
	checked := "never"
	if h.LastChecked != nil {
		checked = h.LastChecked.UTC().Format(time.RFC3339)
	}
	fmt.Fprintf(out, humanText("→ %s app: %s (checked %s; consecutive failures %d)"), name, state, checked, h.ConsecutiveFailures)
	if h.LastError != "" {
		fmt.Fprintf(out, " error=%s", h.LastError)
	}
	if h.StatusCode != 0 {
		fmt.Fprintf(out, " status=%d", h.StatusCode)
	}
	fmt.Fprintln(out)
	if e.DaysLeft == nil {
		fmt.Fprintf(out, "  node key expiry: unknown (%s)\n", e.Source)
	} else {
		fmt.Fprintf(out, "  node key expiry: %dd left (%s) %s\n", *e.DaysLeft, e.Source, e.Warning)
	}
	for _, step := range e.Next {
		fmt.Fprintf(out, "  Next: %s\n", step)
	}
}

func formatEarlyWarnings(out io.Writer, c StatusCredentials) {
	for _, slot := range []StatusCredentialSlot{c.APIKey, c.ClientSecret} {
		if slot.EarlyWarning.Warning == "" {
			continue
		}
		fmt.Fprintf(out, humanText("→ credential early warning: %s (%s)\n"), slot.EarlyWarning.Warning, slot.EarlyWarning.Source)
		for _, step := range slot.EarlyWarning.Next {
			fmt.Fprintf(out, "  Next: %s\n", step)
		}
	}
}

func credentialEarlyWarning(slot StatusCredentialSlot, now time.Time) health.Expiry {
	e := health.ExpiryAt(slot.ExpiresAt, slot.ExpiresAtSource, now, credentials.NextAPIKeyBootstrap())
	if !slot.Present {
		e.State = "none"
	}
	if slot.Present && slot.ExpiryState == "ok" && slot.ExpiresAt == nil && slot.Fingerprint != "" {
		e.State = "never"
		e.Source = "credential_metadata"
	}
	return e
}

func formatAlerts(out io.Writer, a health.AlertsView) {
	if a.Notifier == "" {
		a.Notifier = "none"
	}
	fmt.Fprintf(out, humanText("→ alerts: notifier=%s events=%d"), a.Notifier, len(a.Events))
	if a.Destination != "" {
		fmt.Fprint(out, " destination=[redacted]")
	}
	if a.Error != "" {
		fmt.Fprintf(out, " error=%s", a.Error)
	}
	if a.MonitorError != "" {
		fmt.Fprintf(out, " monitor_error=%s", a.MonitorError)
	}
	fmt.Fprintln(out)
}

func healthConfigFromFlags(cmd *cobra.Command) *registry.HealthConfig {
	var cfg registry.HealthConfig
	changed := false
	for _, name := range []string{"health-path", "health-status-min", "health-status-max", "health-body", "health-timeout", "health-interval"} {
		changed = changed || cmd.Flags().Changed(name)
	}
	if !changed {
		return nil
	}
	if cmd.Flags().Changed("health-path") {
		cfg.Path, _ = cmd.Flags().GetString("health-path")
	}
	if cmd.Flags().Changed("health-status-min") {
		cfg.StatusMin, _ = cmd.Flags().GetInt("health-status-min")
	}
	if cmd.Flags().Changed("health-status-max") {
		cfg.StatusMax, _ = cmd.Flags().GetInt("health-status-max")
	}
	if cmd.Flags().Changed("health-body") {
		cfg.BodyContains, _ = cmd.Flags().GetString("health-body")
	}
	cfg.Timeout, _ = cmd.Flags().GetString("health-timeout")
	cfg.Interval, _ = cmd.Flags().GetString("health-interval")
	return &cfg
}

var doctorHTTPProbeFn = func(ctx context.Context, svc registry.Service) string { return health.Probe(ctx, svc) }

// healthMismatchFinding explains a backend that answered outside its health
// settings and names the one command that changes only the check. Re-running
// add would replace the whole service, so it is never suggested. stored is the
// app's saved health block: the suggestion widens its effective range just
// enough for the answer, so health set always accepts it.
func healthMismatchFinding(app, code string, status int, stored *registry.HealthConfig) (string, map[string]string) {
	next := "tslink health set " + app + " --path /health"
	answered := "an unexpected body"
	if code == "health_status_mismatch" {
		answered = "a status outside the accepted range"
		if flags := statusRangeFlags(status, stored); flags != "" {
			next = "tslink health set " + app + flags
		}
	}
	evidence := map[string]string{"error_code": code, "next": next}
	if status != 0 {
		evidence["status_code"] = strconv.Itoa(status)
		answered = fmt.Sprintf("HTTP %d", status)
		if code == "health_body_mismatch" {
			answered += " without the expected body text"
		}
	}
	message := fmt.Sprintf("The app answered its health check with %s, so it is reachable but the check does not match it. Point the check at an endpoint that answers 2xx or 3xx, or accept this answer, changing only the health settings: %s", answered, next)
	return message, evidence
}

// statusRangeFlags returns the health set flags that widen the stored range
// to accept a redirect (as the whole 3xx class) or a 401/403 answer: raise the
// maximum above it, lower the minimum below it. Only a changed bound is named.
// It returns "" for other answers, which a health path fixes instead.
func statusRangeFlags(status int, stored *registry.HealthConfig) string {
	low, high := status, status
	switch {
	case status == 401 || status == 403:
	case status >= 300 && status <= 399:
		low, high = 300, 399
	default:
		return ""
	}
	current := registry.HealthConfig{}
	if stored != nil {
		current = *stored
	}
	effective := current.Effective()
	if status >= effective.StatusMin && status <= effective.StatusMax {
		return ""
	}
	wantMin, wantMax := min(effective.StatusMin, low), max(effective.StatusMax, high)
	var update registry.HealthUpdate
	if wantMin != effective.StatusMin {
		update.StatusMin = &wantMin
	}
	if wantMax != effective.StatusMax {
		update.StatusMax = &wantMax
	}
	// Check the command against health set's own merge and validation, so a
	// suggestion it would refuse is never printed.
	next := update.Apply(stored)
	if result := next.Effective(); result.StatusMin != wantMin || result.StatusMax != wantMax || registry.ValidateHealthConfig(registry.TypeProxy, next) != nil {
		return ""
	}
	flags := ""
	if update.StatusMin != nil {
		flags += " --status-min " + strconv.Itoa(*update.StatusMin)
	}
	if update.StatusMax != nil {
		flags += " --status-max " + strconv.Itoa(*update.StatusMax)
	}
	return flags
}

// The journal owns events, monitor state and current read/config errors. Only
// a live, usable snapshot can supplement an error that could not be persisted.
func alertsWithSnapshot(journal health.AlertsView, snapshot health.AlertsView, live bool) health.AlertsView {
	if live && journal.Error == "" && snapshot.Error == "alert_state_write_failed" {
		journal.Error = snapshot.Error
	}
	return journal
}

func readAlertsForRegistry(regPath string) health.AlertsView {
	dir := filepath.Dir(regPath)
	c, err := health.LoadNotifier(filepath.Join(dir, health.ConfigFile))
	r := health.NewRecorder(filepath.Join(dir, health.StateFile), c)
	if err != nil {
		r.Error = err.Error()
	}
	return r.View()
}

type mcpHealthService struct {
	RequestLimits *registry.EffectiveRequestLimits `json:"request_limits,omitempty"`
	Warnings      []inspect.WarningView            `json:"warnings,omitempty"`
	PreserveHost  *bool                            `json:"preserve_host,omitempty"`
	Name          string                           `json:"name"`
	Status        string                           `json:"status"`
	Health        health.State                     `json:"health"`
	NodeKey       health.Expiry                    `json:"node_key"`
}

func mcpHealthServices(states []StatusServiceState) []mcpHealthService {
	services := make([]mcpHealthService, 0, len(states))
	for _, s := range states {
		services = append(services, mcpHealthService{RequestLimits: s.RequestLimits, Warnings: s.Warnings, PreserveHost: s.PreserveHost, Name: s.Name, Status: s.Status, Health: s.Health, NodeKey: s.NodeKey})
	}
	return services
}

func addHealthFlags(cmd *cobra.Command) {
	cmd.Flags().String("health-path", "", "HTTP business probe path (default /; proxy only)")
	cmd.Flags().Int("health-status-min", 0, "Lowest accepted HTTP probe status (default 200; proxy only)")
	cmd.Flags().Int("health-status-max", 0, "Highest accepted HTTP probe status (default 399, or 299 when only --health-status-min is set; proxy only)")
	cmd.Flags().String("health-body", "", "Expected body substring within first 64 KiB (proxy only; avoid secrets in argv)")
	cmd.Flags().String("health-timeout", "5s", "Backend probe timeout, 100ms to 30s")
	cmd.Flags().String("health-interval", "1m", "Backend probe interval, 10s to 1d")
}

func healthInputSchema() map[string]any {
	return objectSchema(map[string]any{
		"path":          map[string]any{"type": "string", "description": "HTTP business path joined to backend base path; default /."},
		"status_min":    map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
		"status_max":    map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
		"body_contains": map[string]any{"type": "string", "maxLength": 4096},
		"timeout":       map[string]any{"type": "string", "description": "100ms..30s; default 5s."},
		"interval":      map[string]any{"type": "string", "description": "10s..1d; default 1m."},
	})
}
