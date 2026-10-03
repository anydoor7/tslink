package cmd

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func mcpSession(a mcpActions) mcpscope.Session {
	if a.session != nil {
		s := *a.session
		if s.Identity.Login == "" && !strings.HasPrefix(s.Who, "tag:") {
			s.Identity.Login = s.Who
		}
		return s
	}
	return mcpscope.Session{Who: mcpLocalActor(), Identity: mcpscope.Identity{Login: mcpLocalActor()}, Scope: mcpscope.Scope{Role: "owner"}}
}

func mcpLocalActor() string {
	if u, err := user.Current(); err == nil {
		return "local-user:" + u.Username
	}
	return "local-os-user"
}
func mcpNow(a mcpActions) time.Time {
	if a.nowFn != nil {
		return a.nowFn()
	}
	return time.Now()
}
func mcpAuditPath(regPath string) string {
	return filepath.Join(filepath.Dir(regPath), "mcp-audit.json")
}

func mcpStdioScope(role, apps, max string, inventory bool) (mcpscope.Session, error) {
	s := mcpscope.Session{Who: mcpLocalActor(), Scope: mcpscope.Scope{Role: role, Inventory: inventory, MaxDuration: max}}
	if apps != "" {
		s.Scope.Apps = strings.Split(apps, ",")
	}
	if (role == "app-operator" || role == "people-manager") && max == "" {
		s.Scope.MaxDuration = "24h"
	}
	return s, s.Scope.Validate()
}

// mcpCallApps extracts references for authorization and value-free audit. No
// field from this document can change the authenticated Session.
func mcpCallApps(a mcpActions, tool string, raw json.RawMessage) ([]string, error) {
	var args struct {
		App       string            `json:"app"`
		Name      string            `json:"name"`
		Service   string            `json:"service"`
		Apps      []string          `json:"apps"`
		Recipe    string            `json:"recipe_id"`
		Who       string            `json:"who"`
		Reconcile map[string]string `json:"reconcile_invites"`
		Replace   map[string]string `json:"replace_invites"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			if mcpSession(a).Scope.Role == "owner" {
				return []string{}, nil
			}
			return nil, output.ErrUsage("invalid MCP arguments")
		}
	}
	apps := []string{}
	switch tool {
	case "app_restart", "people_grant", "people_revoke":
		apps = append(apps, args.App)
	case "url", "unshare", "add", "share":
		if args.Name != "" {
			apps = append(apps, args.Name)
		}
	case "tags_set", "access_explain", "invite_device":
		apps = append(apps, args.Service)
	case "recipe_apply", "recipe_plan":
		name := args.Name
		if name == "" {
			if recipe, ok := recipes.Lookup(args.Recipe); ok {
				name = recipe.RecommendedName
			}
		}
		if name != "" {
			apps = append(apps, name)
		}
	case "template_apply", "template_plan":
		if tmpl, ok := templateByName(args.Name); ok {
			for _, spec := range tmpl.Services {
				apps = append(apps, spec.Params.Name)
			}
		}
	case "people_add", "people_update", "people_remove":
		for _, app := range args.Apps {
			if app != "all" {
				apps = append(apps, app)
			}
		}
		for app := range args.Reconcile {
			apps = append(apps, app)
		}
		for app := range args.Replace {
			apps = append(apps, app)
		}
	}
	// Owner operations with indirect/global effects include current registry
	// references. This is for attribution, never a grant of inventory.
	if a.registryPath != "" && mcpMutatingTool(tool) {
		reg, _, err := registry.Preflight(a.registryPath)
		if err == nil {
			if tool == "invite_user" || tool == "invite_revoke" || tool == "invite_resend" {
				for _, svc := range reg.Services {
					apps = append(apps, svc.Name)
				}
			}
			if tool == "people_remove" || tool == "people_update" {
				who, _ := registry.NormalizePerson(args.Who)
				for _, p := range reg.People {
					if p.Login == who {
						for _, g := range p.Grants {
							apps = append(apps, g.App)
						}
						for _, i := range p.Invites {
							apps = append(apps, i.App)
						}
					}
				}
			}
			if tool == "people_add" || tool == "people_update" {
				for _, app := range args.Apps {
					if app == "all" {
						for _, svc := range reg.Services {
							apps = append(apps, svc.Name)
						}
						break
					}
				}
			}
		}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, app := range apps {
		if registry.ValidateName(app) == nil && !seen[app] {
			result = append(result, app)
			seen[app] = true
		}
	}
	sort.Strings(result)
	// Invalid references cannot be dropped when authorizing reduced clients.
	if mcpSession(a).Scope.Role != "owner" {
		for _, app := range apps {
			if registry.ValidateName(app) != nil {
				return nil, mcpscope.Denied{}
			}
		}
	}
	return result, nil
}

func mcpMutatingTool(name string) bool {
	hints := mcpToolHints[name]
	return hints != nil && !hints.ReadOnlyHint
}

// callMCPTool is the single tool choke point for both transports. Dispatch
// happens only after authorization and durable mutation intent. Result egress
// is projected before it can reach either text or structuredContent.
func callMCPTool(ctx context.Context, actions mcpActions, name string, raw json.RawMessage) (*mcp.CallToolResult, error) {
	session := mcpSession(actions)
	if !session.Scope.ToolAllowed(name) {
		return nil, mcpUnknownToolError(name)
	}
	apps, authErr := mcpCallApps(actions, name, raw)
	if authErr == nil {
		authErr = mcpscope.CheckEffect(ctx)
		if authErr == nil {
			authErr = session.Authorize(name, apps, mcpNow(actions))
		}
	}
	if authErr == nil && name == "people_grant" {
		var args struct {
			For string `json:"for"`
		}
		if json.Unmarshal(raw, &args) == nil {
			_, authErr = session.GrantDeadline(args.For, mcpNow(actions))
		}
	}
	mutating := mcpMutatingTool(name)
	entry := mcpaudit.Entry{Kind: "mcp", Time: mcpNow(actions).UTC(), Identity: session.Identity, Principal: session.Who, Role: session.Scope.Role, Phase: "intent", Capabilities: session.Scope, ScopeExpiresAt: session.ExpiresAt, Tool: name, Apps: apps, Result: "started"}
	if mutating && actions.audit != nil {
		entry.ID = rand.Text()
		if authErr != nil {
			entry.Result = "denied"
		}
		if err := actions.audit(ctx, entry); err != nil {
			return makeMCPToolErrorResult(mcpAuditUnavailable(false)), nil
		}
	}
	// Journal lock waiting must not extend an expiring capability. Recheck at
	// dispatch, and close the intent receipt without calling the domain action.
	if authErr == nil {
		authErr = mcpscope.CheckEffect(ctx)
		if authErr == nil {
			authErr = session.Authorize(name, apps, mcpNow(actions))
		}
		if authErr != nil && mutating && actions.audit != nil {
			entry.Time, entry.Result, entry.Phase = mcpNow(actions).UTC(), "denied", "completion"
			if err := actions.audit(context.WithoutCancel(ctx), entry); err != nil {
				return makeMCPToolErrorResult(mcpAuditUnavailable(false)), nil
			}
		}
	}
	if authErr != nil {
		return makeMCPToolErrorResult(authErr), nil
	}
	ctx = mcpscope.WithClock(mcpscope.WithSession(ctx, session), func() time.Time { return mcpNow(actions) })
	if session.ExpiresAt != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, session.ExpiresAt.Sub(mcpNow(actions)))
		defer cancel()
	}
	affected := &mcpAffectedApps{apps: []string{}}
	ctx = context.WithValue(ctx, mcpAffectedKey{}, affected)
	var result *mcp.CallToolResult
	var err error
	if name == "doctor" && session.Scope.Role != "owner" {
		var args struct {
			ProbeExternal bool `json:"probe_external,omitempty"`
		}
		if refusal := mcpArgumentsRefusal(name, decodeMCPArguments(raw, &args)); refusal != nil {
			return refusal, nil
		}
		if args.ProbeExternal {
			return makeMCPToolErrorResult(mcpscope.Denied{}), nil
		}
		data, callErr := mcpScopedDoctor(ctx, actions, session.Scope)
		result = makeMCPToolResult(data, callErr)
	} else {
		result, err = executeMCPTool(ctx, actions, name, raw)
	}
	if session.Scope.Role != "owner" && result != nil {
		if result.IsError {
			// Domain error messages may quote global registry/config contents.
			// Preserve the stable code but expose no raw evidence to this role.
			code := mcpResultCode(result, err)
			result = mcpScopedFailure(code)
		} else {
			data, filterErr := mcpProject(name, result.StructuredContent, session.Scope)
			result = makeMCPToolResult(data, filterErr)
		}
	}
	if mutating && actions.audit != nil {
		entry.Time, entry.Result, entry.Phase = mcpNow(actions).UTC(), mcpResultCode(result, err), "completion"
		if name == "share" {
			entry.Apps = affected.apps
		}
		// A cancelled client cannot suppress its completion receipt.
		if recordErr := actions.audit(context.WithoutCancel(ctx), entry); recordErr != nil {
			return makeMCPToolErrorResult(mcpAuditUnavailable(true)), nil
		}
	}
	return result, err
}

// The code is forwarded from a domain result, never constructed from arguments.
func mcpScopedFailure(code string) *mcp.CallToolResult {
	if code == "mcp_person_owner_required" {
		return makeMCPToolErrorResult(registry.CodedError{Code: code, Message: "The owner must add the person first"})
	}
	return makeMCPToolErrorResult(registry.CodedError{Code: code, Message: "Scoped MCP call failed; ask the owner to inspect the app"})
}

func mcpResultCode(result *mcp.CallToolResult, err error) string {
	if err != nil {
		return "protocol_error"
	}
	if result == nil {
		return "unknown"
	}
	if !result.IsError {
		return "ok"
	}
	for _, c := range result.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			var failure struct {
				Code string `json:"code"`
			}
			if json.Unmarshal([]byte(t.Text), &failure) == nil && failure.Code != "" {
				return failure.Code
			}
		}
	}
	return "error"
}
func mcpAuditUnavailable(effected bool) error {
	message := "MCP audit journal unavailable; no mutation started"
	if effected {
		message = "MCP completion audit failed; mutation may have occurred; inspect the started receipt"
	}
	return registry.CodedError{Code: "mcp_audit_unavailable", Message: message}
}

// Explicit result projections keep future global fields from silently
// appearing in a reduced client's context. App fields use the existing views.
func mcpProject(tool string, data any, scope mcpscope.Scope) (any, error) {
	switch tool {
	case "list":
		var v struct {
			Services []ListServiceSummary `json:"services"`
		}
		if err := reprojectJSON(data, &v); err != nil {
			return nil, err
		}
		out := []ListServiceSummary{}
		for _, svc := range v.Services {
			if scope.AllowsApp(svc.Name) {
				svc.Warnings = nil
				if svc.Error != nil {
					svc.Error.Message = "App state unavailable"
					svc.Error.Next = nil
					svc.Error.Provision = nil
				}
				out = append(out, svc)
			}
		}
		return map[string]any{"services": out}, nil
	case "status", "health":
		var v mcpStatusSummary
		if err := reprojectJSON(data, &v); err != nil {
			return nil, err
		}
		out := mcpStatusSummary{Services: []mcpHealthService{}, DaemonRunning: v.DaemonRunning, DaemonState: v.DaemonState}
		for _, svc := range v.Services {
			if scope.AllowsApp(svc.Name) {
				svc.Warnings = nil
				out.Services = append(out.Services, svc)
				if svc.Status == "up" {
					out.AuthorizedServiceCount++
				}
			}
		}
		out.ServiceCount = len(out.Services)
		out.Authenticated, out.NodeAuthorized = out.AuthorizedServiceCount > 0, out.AuthorizedServiceCount > 0
		if tool == "health" {
			return map[string]any{"services": out.Services}, nil
		}
		return out, nil
	case "people_list", "people_grant", "people_revoke":
		if tool != "people_list" {
			var v struct {
				Person  PeopleView `json:"person"`
				App     string     `json:"app"`
				Revoked bool       `json:"revoked"`
			}
			if err := reprojectJSON(data, &v); err != nil {
				return nil, err
			}
			v.Person = mcpScopedPerson(v.Person, scope)
			return v, nil
		}
		var v PeopleListResult
		if err := reprojectJSON(data, &v); err != nil {
			return nil, err
		}
		out := PeopleListResult{People: []PeopleView{}}
		for _, person := range v.People {
			p := mcpScopedPerson(person, scope)
			if len(p.Grants) > 0 {
				out.People = append(out.People, p)
			}
		}
		return out, nil
	case "tags_list":
		var v struct {
			Services []struct {
				Name string   `json:"name"`
				Tags []string `json:"tags"`
			} `json:"services"`
		}
		if err := reprojectJSON(data, &v); err != nil {
			return nil, err
		}
		out := v.Services[:0]
		for _, svc := range v.Services {
			if scope.AllowsApp(svc.Name) {
				out = append(out, svc)
			}
		}
		v.Services = out
		if v.Services == nil {
			v.Services = make([]struct {
				Name string   `json:"name"`
				Tags []string `json:"tags"`
			}, 0)
		}
		return v, nil
	case "access_explain":
		var v AccessExplainResult
		if err := reprojectJSON(data, &v); err != nil {
			return nil, err
		}
		// Global people counters are not app inventory.
		v.TSLinkLocalEnforcement.People = nil
		return v, nil
	default:
		// Single-app tools were authorized by their required app reference;
		// catalogs contain only static built-in recipes/templates.
		return data, nil
	}
}

func mcpScopedPerson(p PeopleView, scope mcpscope.Scope) PeopleView {
	out := PeopleView{Login: p.Login, Revoked: p.Revoked, Grants: []PeopleGrantView{}}
	for _, g := range p.Grants {
		if scope.AllowsApp(g.App) {
			out.Grants = append(out.Grants, g)
		}
	}
	return out
}

// Reduced doctor projects app observations from the shared read-only status
// reader, including caller-bound supervision and credential inspection. It
// avoids the full doctor host-discovery and external probes.
func mcpScopedDoctor(ctx context.Context, actions mcpActions, scope mcpscope.Scope) (any, error) {
	data, err := actions.status(ctx)
	if err != nil {
		return nil, err
	}
	data, err = mcpProject("health", data, scope)
	if err != nil {
		return nil, err
	}
	var v struct {
		Services []mcpHealthService `json:"services"`
	}
	if err := reprojectJSON(data, &v); err != nil {
		return nil, err
	}
	result := DoctorResult{SchemaVersion: 1, ExecutionStatus: doctorExecutionCompleted, Findings: []DoctorFinding{}}
	result.Counts.Services = len(v.Services)
	for _, svc := range v.Services {
		if svc.Health.State == health.Down || svc.Health.State == health.Degraded {
			result.Findings = append(result.Findings, DoctorFinding{Code: "app_health_unhealthy", Severity: doctorSeverityWarning, Service: svc.Name, Area: "health", Message: "App health observation is unhealthy"})
		}
	}
	result.finalize()
	return result, nil
}

type mcpBindingView struct {
	mcpscope.Binding
	Expired bool `json:"expired"`
	Legacy  bool `json:"legacy,omitempty"`
}

func mcpBindingViews(now time.Time) []mcpBindingView {
	cfg, err := config.LoadGlobalConfig()
	if err != nil || cfg.MCP == nil {
		return nil
	}
	views := []mcpBindingView{}
	for _, raw := range cfg.MCP.Allow {
		if p, err := mcpscope.Principal(raw); err == nil {
			views = append(views, mcpBindingView{Binding: mcpscope.Binding{Principal: p, Scope: mcpscope.Scope{Role: "owner"}}, Legacy: true})
		}
	}
	for _, b := range cfg.MCP.Bindings {
		deadline, _ := b.Deadline()
		views = append(views, mcpBindingView{Binding: b, Expired: deadline != nil && !now.Before(*deadline)})
	}
	return views
}

func diagnoseMCPBindings(result *DoctorResult, cfg config.GlobalConfig, now time.Time) {
	if cfg.MCP == nil {
		return
	}
	bindings := append([]mcpscope.Binding(nil), cfg.MCP.Bindings...)
	for _, raw := range cfg.MCP.Allow {
		if p, err := mcpscope.Principal(raw); err == nil {
			bindings = append(bindings, mcpscope.Binding{Principal: p, Scope: mcpscope.Scope{Role: "owner"}})
		}
	}
	tags := map[string]bool{}
	for _, b := range bindings {
		if strings.HasPrefix(b.Principal, "tag:") {
			tags[b.Principal] = true
		}
	}
	if len(tags) > 1 {
		result.Findings = append(result.Findings, DoctorFinding{Code: "mcp_multiple_tags", Severity: doctorSeverityWarning, Area: "mcp", Message: "A node carrying multiple configured MCP tags is denied unless an explicit login binding matches"})
	}
	for _, b := range bindings {
		if b.Role == "owner" && strings.HasPrefix(b.Principal, "tag:") {
			result.Findings = append(result.Findings, DoctorFinding{Code: "mcp_owner_tag", Severity: doctorSeverityWarning, Area: "mcp", Message: "Every device carrying this tag has owner MCP authority", Evidence: map[string]string{"principal": b.Principal}})
		}
		deadline, err := b.Deadline()
		if err == nil && deadline != nil && !now.Before(*deadline) {
			result.Findings = append(result.Findings, DoctorFinding{Code: "mcp_binding_expired", Severity: doctorSeverityWarning, Area: "mcp", Message: "MCP scope binding has expired", Evidence: map[string]string{"principal": b.Principal}})
		}
	}
}

func init() {
	for _, tool := range []string{"people_grant", "people_revoke"} {
		props := map[string]any{"who": map[string]any{"type": "string"}, "app": map[string]any{"type": "string"}}
		required := []string{"who", "app"}
		if tool == "people_grant" {
			props["for"] = map[string]any{"type": "string", "description": "Positive lifetime within the scope maximum and binding expiry; never is refused."}
			required = append(required, "for")
		}
		mcpToolHints[tool] = mcpHints(false, true, tool == "people_revoke", false)
		mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: tool, Description: "Change one person's grant for one private HTTP/file app. Reduced scopes require an existing person; only the owner can create one. Revoking an unknown login is a no-op. Preserves all other grants and invites; cannot clear an owner revocation. Creates no network invitation. A revoke denies HTTP access but leaves accepted network shares and in-flight streams; ask the owner to clean up invitations.", InputSchema: objectSchema(props, required...), OutputSchema: objectSchema(map[string]any{"person": peopleViewSchema(), "app": map[string]any{"type": "string"}, "revoked": map[string]any{"type": "boolean"}}, "person", "app", "revoked")})
	}
	mcpToolHints["app_restart"] = mcpHints(false, true, false, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "app_restart", Description: "Queue restart of this app's TSLink gateway node on daemon reconciliation. Keeps the enrolled identity and other apps; does not restart the third-party app process or install a daemon. queued is not evidence that reconciliation finished. Reduced operators cannot restart a public Funnel app.", InputSchema: objectSchema(map[string]any{"app": map[string]any{"type": "string"}}, "app"), OutputSchema: objectSchema(map[string]any{"app": map[string]any{"type": "string"}, "queued": map[string]any{"type": "boolean"}, "restart_generation": map[string]any{"type": "integer"}}, "app", "queued", "restart_generation")})
	mcpToolHints["health"] = mcpHints(true, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "health", Description: "Read app health and node expiry observations without probing or changing configuration.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{"services": mcpStatusOutputSchema["properties"].(map[string]any)["services"]}, "services")})
	mcpToolHints["mcp_audit"] = mcpHints(true, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{Name: "mcp_audit", Description: "Owner-only durable mutation journal; stable results, no raw arguments, links or tokens. A started entry without completion means the outcome is unknown.", InputSchema: objectSchema(map[string]any{}), OutputSchema: objectSchema(map[string]any{"entries": map[string]any{"type": "array", "items": nestedObjectSchema("Value-free MCP mutation receipt")}}, "entries")})
	mcpStatusOutputSchema["properties"].(map[string]any)["mcp_bindings"] = map[string]any{"type": "array", "items": nestedObjectSchema("Scope bindings without secrets")}
	mcpAuditCmd := &cobra.Command{Use: "mcp-audit", Short: "Read the owner's bounded MCP mutation journal", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.RegistryPath()
		if err != nil {
			return err
		}
		entries, err := (mcpaudit.Journal{Path: mcpAuditPath(path)}).Read()
		if err != nil {
			return err
		}
		if jsonOutput(cmd) {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(output.NewSuccess("mcp-audit", map[string]any{"entries": entries}))
		}
		for _, e := range entries {
			fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s %v %s\n", e.Time.Format(time.RFC3339), e.Principal, e.Role, e.Tool, e.Apps, e.Result)
		}
		return nil
	}}
	rootCmd.AddCommand(mcpAuditCmd)
}

type mcpAffectedKey struct{}
type mcpAffectedApps struct{ apps []string }

// Share allocation reports its transaction outcome, never a predicted name or inventory.
func recordMCPShareApp(ctx context.Context, app string) {
	if affected, ok := ctx.Value(mcpAffectedKey{}).(*mcpAffectedApps); ok && registry.ValidateName(app) == nil {
		affected.apps = []string{app}
	}
}
