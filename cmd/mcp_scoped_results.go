package cmd

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
)

// A reduced role (viewer, app-operator, people-manager) sees only part of
// what the owner sees. Its doctor, status, health and list results use their
// own types, carrying only fields whose owner meaning still holds for the
// role, plus withheld: the owner fields it may not see. A hidden field is
// absent and named, never a zero value: on the owner's shape a zero reads as
// a fact, such as "daemon not running", "healthy" or "no supervisor".
//
// withheld is derived from the owner types, so a field added there later is
// withheld from reduced roles, and says so, until a reduced type adopts it.
// Owner types that reduced results reuse whole (per-service entries,
// findings, counts) are pinned by a test, so a new field in them is reviewed
// before reduced roles see it.

const (
	// Per-service warning text can quote other apps or host configuration.
	mcpWithheldServiceWarnings = "services[].warnings"
	// The list projection replaces an app error's message with a fixed
	// placeholder and drops its next steps and provision outcome.
	mcpWithheldServiceErrorMessage   = "services[].error.message"
	mcpWithheldServiceErrorNext      = "services[].error.next"
	mcpWithheldServiceErrorProvision = "services[].error.provision"

	// mcpFindingsScopeAppHealth says what a reduced doctor evaluated: the
	// health observations of the role's apps, nothing about the daemon,
	// credentials, supervision, configuration or other apps.
	mcpFindingsScopeAppHealth = "app_health"
)

// mcpScopedStatusSummary is the status a reduced role receives. Services and
// counts cover the role's apps; each service's authorized flag and the count
// are the owner status reader's own per-service decision. daemon_running and
// daemon_state are the status reader's, exactly as the owner sees them. The
// host-level authenticated and node_authorized also judge the portal node and
// pending logins outside the role's apps, so they are withheld.
type mcpScopedStatusSummary struct {
	Services               []mcpHealthService `json:"services"`
	AuthorizedServiceCount int                `json:"authorized_service_count"`
	DaemonRunning          bool               `json:"daemon_running"`
	DaemonState            string             `json:"daemon_state"`
	ServiceCount           int                `json:"service_count"`
	Withheld               []string           `json:"withheld"`
}

type mcpScopedHealthResult struct {
	Services []mcpHealthService `json:"services"`
	Withheld []string           `json:"withheld"`
}

type mcpScopedListResult struct {
	Services []ListServiceSummary `json:"services"`
	Withheld []string             `json:"withheld"`
}

// mcpScopedDoctorDaemon states only what the status reader states. The owner
// doctor additionally classifies a foreign live PID as stopped and asks the
// OS supervisor whether it still holds a process; the reduced path skips that
// probe, so it makes no identity claim of its own.
type mcpScopedDoctorDaemon struct {
	Running bool   `json:"running"`
	State   string `json:"state"`
}

// mcpScopedDoctorResult is the doctor result a reduced role receives. The
// host-wide verdict (status, health_status, health_exit_code) is withheld
// because the owner's verdict also judges other apps, credentials and the
// host.
type mcpScopedDoctorResult struct {
	SchemaVersion   int                   `json:"schema_version"`
	ExecutionStatus string                `json:"execution_status"`
	FindingsScope   string                `json:"findings_scope"`
	Daemon          mcpScopedDoctorDaemon `json:"daemon"`
	Counts          DoctorCounts          `json:"counts"`
	Findings        []DoctorFinding       `json:"findings"`
	Withheld        []string              `json:"withheld"`
}

// The withheld lists are derived once, when the package initializes, so a
// derivation that panics (an unnamed embedded struct) stops the process or
// the test binary at start-up, never a request handler. Each result gets a
// fresh copy, so no result shares or reorders another's list.
var (
	mcpScopedStatusWithheldBase = mcpWithheld(mcpWithheldFields("", reflect.TypeFor[mcpStatusSummary](), reflect.TypeFor[mcpScopedStatusSummary]()), mcpWithheldServiceWarnings)
	mcpScopedHealthWithheldBase = mcpWithheld(nil, mcpWithheldServiceWarnings)
	mcpScopedListWithheldBase   = mcpWithheld(nil, mcpWithheldServiceWarnings, mcpWithheldServiceErrorMessage, mcpWithheldServiceErrorNext, mcpWithheldServiceErrorProvision)
	mcpScopedDoctorWithheldBase = mcpWithheld(mcpWithheldFields("", reflect.TypeFor[DoctorResult](), reflect.TypeFor[mcpScopedDoctorResult]()), mcpWithheldFields("daemon.", reflect.TypeFor[DoctorDaemon](), reflect.TypeFor[mcpScopedDoctorDaemon]())...)
)

func mcpScopedStatusWithheld() []string { return slices.Clone(mcpScopedStatusWithheldBase) }
func mcpScopedHealthWithheld() []string { return slices.Clone(mcpScopedHealthWithheldBase) }
func mcpScopedListWithheld() []string   { return slices.Clone(mcpScopedListWithheldBase) }
func mcpScopedDoctorWithheld() []string { return slices.Clone(mcpScopedDoctorWithheldBase) }

func mcpWithheld(derived []string, extra ...string) []string {
	out := append(slices.Clone(derived), extra...)
	slices.Sort(out)
	return out
}

// mcpWithheldFields returns, with prefix, the JSON names of owner's fields
// that reduced does not carry.
func mcpWithheldFields(prefix string, owner, reduced reflect.Type) []string {
	kept := map[string]bool{}
	for _, name := range mcpJSONFieldNames(reduced) {
		kept[name] = true
	}
	withheld := []string{}
	for _, name := range mcpJSONFieldNames(owner) {
		if !kept[name] {
			withheld = append(withheld, prefix+name)
		}
	}
	return withheld
}

// mcpJSONFieldNames lists the JSON object keys of struct type t. An embedded
// struct without a JSON name would contribute promoted keys this function
// does not flatten, so it is refused rather than misreported.
func mcpJSONFieldNames(t reflect.Type) []string {
	names := []string{}
	for i := range t.NumField() {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.Anonymous && name == "" {
			panic(fmt.Sprintf("mcpJSONFieldNames: %s embeds %s without a JSON name; flatten it before deriving withheld", t, field.Type))
		}
		if !field.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		names = append(names, name)
	}
	return names
}

// mcpScopedStatus filters an owner status summary to scope. withCount says
// the input is a full status summary carrying authorized_service_count; the
// owner health payload carries only services.
func mcpScopedStatus(data any, scope mcpscope.Scope, withCount bool) (mcpScopedStatusSummary, error) {
	var v mcpStatusSummary
	if err := reprojectJSON(data, &v); err != nil {
		return mcpScopedStatusSummary{}, err
	}
	// The owner count and the per-service flags are one decision. A summary
	// whose flags do not add up to its count was built without that decision,
	// and filtering it would report the role's apps as unauthorized: refuse it.
	flagged := 0
	for _, svc := range v.Services {
		if svc.Authorized {
			flagged++
		}
	}
	if withCount && flagged != v.AuthorizedServiceCount {
		return mcpScopedStatusSummary{}, fmt.Errorf("status summary is inconsistent: %d services flagged authorized, authorized_service_count %d", flagged, v.AuthorizedServiceCount)
	}
	out := mcpScopedStatusSummary{Services: []mcpHealthService{}, DaemonRunning: v.DaemonRunning, DaemonState: v.DaemonState, Withheld: mcpScopedStatusWithheld()}
	for _, svc := range v.Services {
		if scope.AllowsApp(svc.Name) {
			svc.Warnings = nil
			out.Services = append(out.Services, svc)
			if svc.Authorized {
				out.AuthorizedServiceCount++
			}
		}
	}
	out.ServiceCount = len(out.Services)
	return out, nil
}

// Reduced doctor projects app observations and daemon liveness from the
// shared read-only status reader, including caller-bound supervision and
// credential inspection. It avoids the full doctor host-discovery and
// external probes.
func mcpScopedDoctor(ctx context.Context, actions mcpActions, scope mcpscope.Scope) (mcpScopedDoctorResult, error) {
	data, err := actions.status(ctx)
	if err != nil {
		return mcpScopedDoctorResult{}, err
	}
	v, err := mcpScopedStatus(data, scope, true)
	if err != nil {
		return mcpScopedDoctorResult{}, err
	}
	// Suggestions widen the stored health range, read without changing the
	// registry. Without it they assume the default settings.
	stored := map[string]*registry.HealthConfig{}
	if actions.registryPath != "" {
		if reg, _, err := registry.Preflight(actions.registryPath); err == nil {
			for _, svc := range reg.Services {
				stored[svc.Name] = svc.Health
			}
		}
	}
	result := mcpScopedDoctorResult{
		SchemaVersion:   1,
		ExecutionStatus: doctorExecutionCompleted,
		FindingsScope:   mcpFindingsScopeAppHealth,
		Daemon:          mcpScopedDoctorDaemon{Running: v.DaemonRunning, State: v.DaemonState},
		Findings:        []DoctorFinding{},
		Withheld:        mcpScopedDoctorWithheld(),
	}
	result.Counts.Services = len(v.Services)
	for _, svc := range v.Services {
		if svc.Health.State == health.Degraded && health.UnexpectedResponse(svc.Health.LastError) {
			message, evidence := healthMismatchFinding(svc.Name, svc.Health.LastError, svc.Health.StatusCode, stored[svc.Name])
			result.Findings = append(result.Findings, DoctorFinding{Code: inspect.WarningCodeAppProbeUnexpected, Severity: doctorSeverityWarning, Service: svc.Name, Area: "health", Message: message, Evidence: evidence})
		} else if svc.Health.State == health.Down || svc.Health.State == health.Degraded {
			result.Findings = append(result.Findings, DoctorFinding{Code: "app_health_unhealthy", Severity: doctorSeverityWarning, Service: svc.Name, Area: "health", Message: "App health observation is unhealthy"})
		}
	}
	result.Counts.tally(result.Findings)
	return result, nil
}

// mcpWithheldSchema describes withheld for one tool; only reduced-role
// schemas carry it, and each names only entries that tool can report.
func mcpWithheldSchema(tool string) map[string]any {
	description := "Fields of the owner's result this role may not see."
	switch tool {
	case "doctor":
		description += " A top-level name, or a dotted daemon.<field>, is absent from this result, not zero or empty."
	case "status":
		description += " A top-level name is absent from this result, not zero or empty. services[].warnings: per-service warnings were removed."
	case "health":
		description += " services[].warnings: per-service warnings were removed."
	case "list":
		description += " services[].warnings, services[].error.next and services[].error.provision were removed from each service; services[].error.message was replaced by a fixed placeholder."
	}
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": description}
}

// The reduced status reports the status reader's PID-file classification
// with the same caveat as the reduced doctor: the owner's own status wording
// ("absent: no daemon runs") is stronger than this role can be told.
var (
	mcpScopedDaemonRunningDescription = "The status reader's daemon_running: true when the PID file names a verified TSLink daemon. false does not prove no daemon process exists; see the state."
	mcpScopedDaemonStateDescription   = "The status reader's daemon_state, verbatim. absent: no PID file, or its process is gone. unknown: a PID file exists but its process could not be verified either way. Neither rules out a daemon process still held by the OS supervisor; only the owner doctor checks that."
)

// mcpScopedOutputSchema returns the output schema advertised to reduced
// roles for a tool whose reduced result has its own shape, or nil when the
// owner schema applies to every role.
func mcpScopedOutputSchema(tool string) map[string]any {
	ownerProperty := func(schema map[string]any, name string) any {
		return schema["properties"].(map[string]any)[name]
	}
	switch tool {
	case "doctor":
		return objectSchema(map[string]any{
			"schema_version":   map[string]any{"type": "integer"},
			"execution_status": map[string]any{"type": "string"},
			"findings_scope":   map[string]any{"type": "string", "enum": []string{mcpFindingsScopeAppHealth}, "description": "app_health: findings and counts cover only the health observations of this role's apps. Daemon, credential, supervision, configuration and other apps' checks were not evaluated, so an empty list does not mean nothing is wrong; ask the owner to run tslink doctor for that."},
			"daemon": objectSchema(map[string]any{
				"running": map[string]any{"type": "boolean", "description": mcpScopedDaemonRunningDescription},
				"state":   map[string]any{"type": "string", "enum": []string{daemonStateRunning, daemonStateAbsent, daemonStateUnknown}, "description": mcpScopedDaemonStateDescription},
			}, "running", "state"),
			"counts":   nestedObjectSchema("Service count of this role's apps plus finding counts by severity, over the returned findings only."),
			"findings": ownerProperty(mcpDoctorOutputSchema, "findings"),
			"withheld": mcpWithheldSchema("doctor"),
		}, "schema_version", "execution_status", "findings_scope", "daemon", "counts", "findings", "withheld")
	case "status":
		return objectSchema(map[string]any{
			"services":                 ownerProperty(mcpStatusOutputSchema, "services"),
			"authorized_service_count": map[string]any{"type": "integer", "minimum": 0, "description": "Services of this role whose authorized flag is true: the owner status reader's own per-service decision."},
			"daemon_running":           map[string]any{"type": "boolean", "description": mcpScopedDaemonRunningDescription},
			"daemon_state":             map[string]any{"type": "string", "enum": []string{daemonStateRunning, daemonStateAbsent, daemonStateUnknown}, "description": mcpScopedDaemonStateDescription},
			"service_count":            map[string]any{"type": "integer", "minimum": 0, "description": "Services of this role."},
			"withheld":                 mcpWithheldSchema("status"),
		}, "services", "authorized_service_count", "daemon_running", "daemon_state", "service_count", "withheld")
	case "health":
		return objectSchema(map[string]any{"services": ownerProperty(mcpStatusOutputSchema, "services"), "withheld": mcpWithheldSchema("health")}, "services", "withheld")
	case "list":
		return objectSchema(map[string]any{"services": ownerProperty(mcpListOutputSchema, "services"), "withheld": mcpWithheldSchema("list")}, "services", "withheld")
	}
	return nil
}

// mcpAdvertisedOutputSchema is the output schema tools/list gives this scope.
func mcpAdvertisedOutputSchema(definition mcpToolDefinition, scope mcpscope.Scope) map[string]any {
	if scope.Role != "owner" {
		if schema := mcpScopedOutputSchema(definition.Name); schema != nil {
			return schema
		}
	}
	return definition.OutputSchema
}
