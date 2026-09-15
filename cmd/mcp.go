package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

const (
	// mcpProtocolVersion is the newest MCP revision this server speaks. It is
	// the current spec revision, which replaced the session-oriented remote
	// transport with a stateless one and moved version negotiation into each
	// request's _meta; the initialize handshake it deprecates is still served
	// for older clients.
	mcpProtocolVersion = "2026-07-28"
	// mcpLegacyHandshakeVersion is what the deprecated initialize handshake
	// settles on when the client asks for a revision this server does not know.
	// A client that already speaks mcpProtocolVersion does not send initialize
	// at all, so the handshake never reports it.
	mcpLegacyHandshakeVersion = "2025-11-25"
	// mcpMaxRecordBytes bounds one newline-delimited record. See
	// mcpRecordLimitReader for why the bound exists rather than what it frames.
	mcpMaxRecordBytes = 1024 * 1024
)

// mcpSupportedProtocolVersions is the revision set this server accepts, newest
// first. It is documented in `tslink mcp --help` and asserted against the SDK's
// own negotiation in TestMCPProtocolVersionSupportMatrix, so it cannot drift
// into being a comment that says something the server does not do.
var mcpSupportedProtocolVersions = []string{
	"2026-07-28",
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

type mcpToolDefinition struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// nestedObjectSchema describes a payload branch whose internal shape is the
// CLI's own JSON contract (documented in docs/cli-manifest.json) rather than a
// second copy maintained here. It stays an open object on purpose: closing it
// would mean restating a large struct in two places, and the copy would be the
// one that rots.
func nestedObjectSchema(description string) map[string]any {
	return map[string]any{"type": "object", "description": description}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
}

var (
	mcpEndpointViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
		"state":   map[string]any{"type": "string"},
		"host":    map[string]any{"type": "string"},
		"port":    map[string]any{"type": "integer"},
	}, "kind", "display", "state")
	mcpExposureViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
		"public":  map[string]any{"type": "boolean"},
	}, "kind", "display", "public")
	mcpSummaryViewSchema = objectSchema(map[string]any{
		"mode":     map[string]any{"type": "string"},
		"count":    map[string]any{"type": "integer", "minimum": 0},
		"entries":  stringArraySchema(),
		"redacted": map[string]any{"type": "boolean"},
	}, "count")
	mcpBackendViewSchema = objectSchema(map[string]any{
		"kind":    map[string]any{"type": "string"},
		"display": map[string]any{"type": "string"},
	}, "kind")
	mcpWarningViewSchema = objectSchema(map[string]any{
		"code":     map[string]any{"type": "string"},
		"severity": map[string]any{"type": "string"},
		"message":  map[string]any{"type": "string"},
		"source":   map[string]any{"type": "string"},
	}, "code", "severity", "message", "source")
	mcpWarningArraySchema = map[string]any{"type": "array", "items": mcpWarningViewSchema}
	mcpServiceViewSchema  = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"name":           map[string]any{"type": "string"},
		"type":           map[string]any{"type": "string", "enum": serviceTypeValues()},
		"endpoint":       mcpEndpointViewSchema,
		"exposure":       mcpExposureViewSchema,
		"tags":           mcpSummaryViewSchema,
		"allow":          mcpSummaryViewSchema,
		"backend":        mcpBackendViewSchema,
		"funnel":         map[string]any{"type": "boolean"},
		"middleware":     nestedObjectSchema("Reserved middleware view; the middleware runtime is unavailable and rejected at admission."),
		"warnings":       mcpWarningArraySchema,
	}, "schema_version", "name", "type", "endpoint", "exposure", "tags", "allow", "backend")
	mcpRemoteSideEffectPlanSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "integer"},
		"id":             map[string]any{"type": "string"},
		"operation":      map[string]any{"type": "string"},
		"remote_system":  map[string]any{"type": "string"},
		"mutates":        map[string]any{"type": "boolean"},
		"default":        map[string]any{"type": "string"},
		"opt_in_flag":    map[string]any{"type": "string"},
		"disable_flag":   map[string]any{"type": "string"},
		"resources":      stringArraySchema(),
		"boundaries":     stringArraySchema(),
	}, "schema_version", "id", "operation", "remote_system", "mutates", "default", "resources", "boundaries")
	mcpInviteProperties = map[string]any{
		"kind":               map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                 map[string]any{"type": "string"},
		"recipient":          map[string]any{"type": "string"},
		"email":              map[string]any{"type": "string"},
		"emailed":            map[string]any{"type": "boolean"},
		"invite_url":         map[string]any{"type": "string", "description": "Bearer invitation URL. Present only when the invite was created with print_link true or when invite_list was called with show_urls true; anyone holding it can accept the invitation."},
		"role":               map[string]any{"type": "string"},
		"service":            map[string]any{"type": "string"},
		"device_id":          map[string]any{"type": "integer"},
		"multi_use":          map[string]any{"type": "boolean"},
		"allow_exit_node":    map[string]any{"type": "boolean"},
		"accepted":           map[string]any{"type": "boolean"},
		"created":            map[string]any{"type": "string"},
		"last_email_sent_at": map[string]any{"type": "string"},
	}
	mcpInviteSchema       = objectSchema(mcpInviteProperties, "kind", "id", "emailed")
	mcpInviteArraySchema  = map[string]any{"type": "array", "items": mcpInviteSchema}
	mcpServiceErrorSchema = objectSchema(map[string]any{
		"code":    map[string]any{"type": "string"},
		"message": map[string]any{"type": "string"},
		"next":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"provision": objectSchema(map[string]any{
			"attempted":     map[string]any{"type": "boolean"},
			"target":        map[string]any{"type": "string"},
			"changed":       map[string]any{"type": "boolean"},
			"reason":        map[string]any{"type": "string"},
			"write_outcome": map[string]any{"type": "string", "enum": []string{tailapi.PolicyWriteNotAttempted, tailapi.PolicyWriteUnchanged, tailapi.PolicyWriteChanged, tailapi.PolicyWriteRejected, tailapi.PolicyWriteUnknown}},
		}, "attempted", "changed", "reason"),
	}, "code", "message")
	mcpShareOutputSchema = objectSchema(map[string]any{
		"url":      map[string]any{"type": "string"},
		"name":     map[string]any{"type": "string"},
		"status":   map[string]any{"type": "string", "enum": []string{shareStatusReady, authStatusNeedsLogin}},
		"auth_url": map[string]any{"type": "string"},
	}, "status")
	mcpListOutputSchema = objectSchema(map[string]any{
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":             map[string]any{"type": "string"},
				"type":             map[string]any{"type": "string", "enum": serviceTypeValues()},
				"url":              map[string]any{"type": []string{"string", "null"}},
				"url_pending":      map[string]any{"type": "boolean"},
				"state":            map[string]any{"type": "string", "enum": listStateValues()},
				"funnel_requested": map[string]any{"type": "boolean"},
				"funnel_active":    map[string]any{"type": "boolean"},
				"funnel_state":     map[string]any{"type": "string", "enum": funnelStateValues()},
				// Present only for a service with a Funnel deadline. They were
				// missing while no MCP tool could create one; share and add now
				// can, so a strict client would have started rejecting list.
				"funnel_expires_at": map[string]any{"type": "string"},
				"funnel_remaining":  map[string]any{"type": "string"},
				"error":             mcpServiceErrorSchema,
			}, "name", "type", "url", "url_pending", "state", "funnel_requested", "funnel_active", "funnel_state"),
		},
	}, "services")
	mcpUnshareOutputSchema = objectSchema(map[string]any{
		"ok":                     map[string]any{"type": "boolean", "description": "Whether the idempotent unshare request removed the local registry entry or found the service absent (removed false); ok true does not guarantee tailnet device cleanup, so check device_cleaned and device_warning."},
		"name":                   map[string]any{"type": "string"},
		"removed":                map[string]any{"type": "boolean"},
		"device_cleaned":         map[string]any{"type": "boolean"},
		"device_cleanup_skipped": map[string]any{"type": "boolean"},
		"device_skip_reason":     map[string]any{"type": "string"},
		"device_warning":         map[string]any{"type": "string"},
	}, "ok", "name", "removed", "device_cleaned", "device_cleanup_skipped")
	mcpStatusOutputSchema = objectSchema(map[string]any{
		"supervision":              nestedObjectSchema("Verified manager, autostart, restart policy, and diagnostic evidence."),
		"authenticated":            map[string]any{"type": "boolean", "description": "Legacy alias for node_authorized; it is not a stored-credential indicator."},
		"credential_stored":        map[string]any{"type": "boolean"},
		"node_authorized":          map[string]any{"type": "boolean"},
		"authorized_service_count": map[string]any{"type": "integer", "minimum": 0},
		"daemon_running":           map[string]any{"type": "boolean"},
		"service_count":            map[string]any{"type": "integer", "minimum": 0},
		"status":                   map[string]any{"type": "string"},
		"auth_url":                 map[string]any{"type": "string"},
		"next":                     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	}, "authenticated", "credential_stored", "node_authorized", "authorized_service_count", "daemon_running", "service_count")
	mcpAddOutputSchema = objectSchema(map[string]any{
		"daemon_running":    map[string]any{"type": "boolean"},
		"auth_url":          map[string]any{"type": "string"},
		"next":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"name":              map[string]any{"type": "string"},
		"type":              map[string]any{"type": "string", "enum": serviceTypeValues()},
		"created":           map[string]any{"type": "boolean", "description": "True when this call created the registry entry; false when it replaced an existing entry with the same name."},
		"funnel_expires_at": map[string]any{"type": "string"},
		"funnel_rearmed":    map[string]any{"type": "boolean"},
		"url":               map[string]any{"type": []string{"string", "null"}},
		"url_pending":       map[string]any{"type": "boolean"},
		"endpoint":          mcpEndpointViewSchema,
		"exposure":          mcpExposureViewSchema,
		"warnings":          mcpWarningArraySchema,
	}, "name", "type", "created", "funnel_rearmed", "url", "url_pending", "endpoint", "exposure")
	mcpURLOutputSchema = objectSchema(map[string]any{
		"name":  map[string]any{"type": "string"},
		"url":   map[string]any{"type": "string"},
		"state": map[string]any{"type": "string"},
	}, "name", "url", "state")
	mcpTagsListOutputSchema = objectSchema(map[string]any{
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name": map[string]any{"type": "string"},
				"tags": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}},
			}, "name", "tags"),
		},
	}, "services")
	mcpTagsSetOutputSchema = objectSchema(map[string]any{
		"service": map[string]any{"type": "string"},
		"tags":    stringArraySchema(),
	}, "service", "tags")
	mcpAccessExplainOutputSchema = objectSchema(map[string]any{
		"schema_version":           map[string]any{"type": "string"},
		"service":                  map[string]any{"type": "string"},
		"summary":                  map[string]any{"type": "string"},
		"tslink_known":             nestedObjectSchema("What the local registry records: service type, endpoint, exposure, tags, redacted allow-list, backend, and target loopback classification."),
		"tslink_local_enforcement": nestedObjectSchema("Which TSLink HTTP enforcement applies, its failure mode, and whether public Funnel exposure is configured."),
		"external_policy_unknown":  nestedObjectSchema("Layers this command does not evaluate: tailnet membership, ACL/grants, sharing, tag ownership, Funnel policy."),
		"backend_auth_assumption":  nestedObjectSchema("Backend application, database and SSH authentication are outside TSLink and are not proven here."),
	}, "schema_version", "service", "summary", "tslink_known", "tslink_local_enforcement", "external_policy_unknown", "backend_auth_assumption")
	mcpDoctorOutputSchema = objectSchema(map[string]any{
		"supervision":      nestedObjectSchema("Verified OS supervision, autostart, restart policy, and diagnostic evidence."),
		"schema_version":   map[string]any{"type": "string"},
		"execution_status": map[string]any{"type": "string"},
		"status":           map[string]any{"type": "string"},
		"health_status":    map[string]any{"type": "string"},
		"health_exit_code": map[string]any{"type": "integer", "description": "Exit code `tslink doctor` would return for this health status. It is reported, not applied: the tool result itself is successful."},
		"counts":           nestedObjectSchema("Service count plus finding counts by severity."),
		"paths":            nestedObjectSchema("Resolved config, registry, runtime snapshot, auth handoff and PID paths."),
		"credential_mode":  map[string]any{"type": "string"},
		"credential_tier":  map[string]any{"type": "string"},
		"daemon":           nestedObjectSchema("Whether the local TSLink daemon is running, and its PID when it is."),
		"runtime_snapshot": nestedObjectSchema("Freshness classification of runtime.json against the current daemon and registry fingerprint."),
		"tailscale_ssh":    nestedObjectSchema("Tailscale SSH enablement for this node, read from the local Tailscale client: state is enabled, disabled, or unknown, and acl_rule_required records that a tailnet ACL ssh rule is also needed. Informational only; it never changes health_status or health_exit_code."),
		"findings": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"code":     map[string]any{"type": "string"},
				"severity": map[string]any{"type": "string"},
				"area":     map[string]any{"type": "string"},
				"message":  map[string]any{"type": "string"},
				"service":  map[string]any{"type": "string"},
				"evidence": nestedObjectSchema("Redacted string evidence for this finding; credential material and URLs are replaced before it is emitted."),
			}, "code", "severity", "area", "message"),
		},
	}, "schema_version", "execution_status", "status", "health_status", "health_exit_code", "counts", "paths", "credential_mode", "credential_tier", "daemon", "runtime_snapshot", "tailscale_ssh", "findings")
	mcpLogsOutputSchema = objectSchema(map[string]any{
		"source":           map[string]any{"type": "string", "enum": []string{"err", "out"}},
		"file":             map[string]any{"type": "string"},
		"level":            map[string]any{"type": "string"},
		"since":            map[string]any{"type": "string", "description": "The requested window as a Go duration."},
		"since_at":         map[string]any{"type": "string", "description": "Absolute cutoff the window resolved to, in the daemon's clock."},
		"lines":            stringArraySchema(),
		"count":            map[string]any{"type": "integer", "minimum": 0},
		"matched":          map[string]any{"type": "integer", "minimum": 0, "description": "Lines passing the level and time filters before the line and byte bounds; compare with count to see how much was cut."},
		"truncated":        map[string]any{"type": "boolean", "description": "True when lines is not the whole of what matched."},
		"truncated_reason": map[string]any{"type": "string", "enum": []string{mcpLogsTruncatedByLines, mcpLogsTruncatedByBytes}, "description": "Which bound cut the answer: line_limit is the last argument, byte_limit is the response size ceiling."},
		"redacted":         map[string]any{"type": "boolean", "description": "Constant true. Credential material, Tailscale login and invitation URLs, and email addresses are replaced before the lines are returned, so they are not byte-identical to the file on disk."},
	}, "source", "file", "since", "since_at", "lines", "count", "matched", "truncated", "redacted")
	mcpInviteCreateOutputSchema = objectSchema(mergeSchemaProperties(mcpInviteProperties, map[string]any{
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}), "kind", "id", "emailed", "remote_side_effect_plan")
	mcpInviteListOutputSchema = objectSchema(map[string]any{
		"complete":       map[string]any{"type": "boolean", "description": "False when at least one TSLink-owned device could not be checked; device_targets carries the per-service reason."},
		"user_invites":   mcpInviteArraySchema,
		"device_invites": mcpInviteArraySchema,
		"device_targets": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"service":      map[string]any{"type": "string"},
				"checked":      map[string]any{"type": "boolean"},
				"invite_count": map[string]any{"type": "integer", "minimum": 0},
				"error": objectSchema(map[string]any{
					"code":    map[string]any{"type": "string"},
					"message": map[string]any{"type": "string"},
					"next":    stringArraySchema(),
				}, "code", "message"),
			}, "service", "checked", "invite_count"),
		},
		"count": map[string]any{"type": "integer", "minimum": 0},
	}, "complete", "user_invites", "device_invites", "device_targets", "count")
	mcpInviteRevokeOutputSchema = objectSchema(map[string]any{
		"kind":                    map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                      map[string]any{"type": "string"},
		"service":                 map[string]any{"type": "string"},
		"revoked":                 map[string]any{"type": "boolean"},
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}, "kind", "id", "revoked", "remote_side_effect_plan")
	mcpInviteResendOutputSchema = objectSchema(map[string]any{
		"kind":                    map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}},
		"id":                      map[string]any{"type": "string"},
		"recipient":               map[string]any{"type": "string"},
		"email":                   map[string]any{"type": "string"},
		"emailed":                 map[string]any{"type": "boolean"},
		"service":                 map[string]any{"type": "string"},
		"remote_side_effect_plan": mcpRemoteSideEffectPlanSchema,
	}, "kind", "id", "email", "emailed", "remote_side_effect_plan")
	mcpTemplateListOutputSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"templates": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":          map[string]any{"type": "string"},
				"summary":       map[string]any{"type": "string"},
				"service_count": map[string]any{"type": "integer", "minimum": 0},
			}, "name", "summary", "service_count"),
		},
		"count": map[string]any{"type": "integer", "minimum": 0},
	}, "schema_version", "templates", "count")
	mcpTemplateApplyOutputSchema = objectSchema(map[string]any{
		"schema_version": map[string]any{"type": "string"},
		"name":           map[string]any{"type": "string"},
		"summary":        map[string]any{"type": "string"},
		"dry_run":        map[string]any{"type": "boolean"},
		"applied":        map[string]any{"type": "boolean", "description": "True only when the registry was written; template_plan always reports false."},
		"services": map[string]any{
			"type": "array",
			"items": objectSchema(map[string]any{
				"name":    map[string]any{"type": "string"},
				"action":  map[string]any{"type": "string", "enum": []string{templateActionCreate, templateActionCreated, templateActionSkipExisting}},
				"message": map[string]any{"type": "string"},
				"service": mcpServiceViewSchema,
			}, "name", "action", "service"),
		},
		"created": map[string]any{"type": "integer", "minimum": 0},
		"skipped": map[string]any{"type": "integer", "minimum": 0},
	}, "schema_version", "name", "summary", "dry_run", "applied", "services", "created", "skipped")
)

// mergeSchemaProperties copies base and overlays extra, so a schema built from
// an embedded struct's properties cannot mutate the map the embedded schema
// itself uses.
func mergeSchemaProperties(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for name, schema := range base {
		merged[name] = schema
	}
	for name, schema := range extra {
		merged[name] = schema
	}
	return merged
}

var mcpToolDefinitions = []mcpToolDefinition{
	{
		Name:        "share",
		Description: "Setting funnel true on this tool publishes the target to the entire public internet, so ask the user before doing that; with funnel false (the default) it exposes a local directory, one file, or an HTTP port only on the user's private Tailscale network. Without allow, every member of the user's tailnet can read the share; pass allow to restrict it to named principals. Use this after creating a local page or report that the user wants to open on another tailnet device. If status is needs_login, open auth_url in a browser and retry after authorization.",
		InputSchema: objectSchema(map[string]any{
			"no_daemon_install": map[string]any{"type": "boolean", "description": "Require an already running TSLink service; do not automatically install its background service."},
			"target":            map[string]any{"type": "string", "minLength": 1, "description": "Existing file or directory path, bare port from 1 to 65535, or host:port HTTP target."},
			"name":              map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Optional requested DNS-label service name. A matching target is reused only if it already has this name; unrelated name collisions receive a numeric suffix."},
			"ephemeral":         map[string]any{"type": "boolean", "default": true, "description": "Keep true for temporary shares; set false only when the user wants durable tailnet node state."},
			"allow":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Principals allowed to reach the share over HTTP: email addresses, or tag:<name> ACL tags. Omitting it leaves the share readable by every member of the user's tailnet. Rejected together with funnel."},
			"tags":              map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": `^tag:`}, "description": "ACL tags applied to the tailnet node, each prefixed tag:. Defaults to the configured default tag."},
			"funnel":            map[string]any{"type": "boolean", "default": false, "description": "Publish to the public internet through Tailscale Funnel. Requires public_ack true, an HTTP port target, and no allow entries."},
			"public_ack":        map[string]any{"type": "boolean", "default": false, "description": "Explicit acknowledgement that funnel exposes the target publicly. funnel true without it is rejected."},
			"funnel_ttl":        map[string]any{"type": "string", "enum": []string{"1h", "8h", "24h", "72h", "7d", "never"}, "description": "Public Funnel lifetime; defaults to 24h. Only valid with funnel true."},
		}, "target"),
		OutputSchema: mcpShareOutputSchema,
	},
	{
		Name:        "add",
		Description: "Setting funnel true on this tool publishes the service to the entire public internet, so ask the user before doing that; otherwise it writes a registry entry for a proxy, file, or TCP service reachable on the user's private Tailscale network. Without allow, every member of the user's tailnet can reach an HTTP service. Use this instead of share when the user wants a named, configured service rather than a one-shot share; it installs the background service when absent unless no_daemon_install is true. Installation announcements go to stderr. After setup it returns current URL/enrollment evidence without an additional URL wait; use url to poll pending endpoints.",
		InputSchema: objectSchema(map[string]any{
			"name":              map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Registry service name (DNS label). An existing entry with this name is replaced."},
			"type":              map[string]any{"type": "string", "enum": serviceTypeValues(), "description": "proxy forwards HTTP to target; file serves the directory dir; tcp forwards a raw stream to target."},
			"target":            map[string]any{"type": "string", "description": "host:port or URL for proxy, host:port for tcp. Rejected for file."},
			"dir":               map[string]any{"type": "string", "description": "Absolute directory path for file. Rejected for proxy and tcp."},
			"allow":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Principals allowed to reach the service over HTTP: email addresses, or tag:<name> ACL tags. Omitting it leaves an HTTP service readable by every member of the user's tailnet. Unsupported for tcp and rejected together with funnel."},
			"tags":              map[string]any{"type": "array", "items": map[string]any{"type": "string", "pattern": `^tag:`}, "description": "ACL tags applied to the tailnet node, each prefixed tag:. Defaults to the configured default tag."},
			"ephemeral":         map[string]any{"type": "boolean", "default": false, "description": "Register an ephemeral tailnet node that disappears on disconnect."},
			"funnel":            map[string]any{"type": "boolean", "default": false, "description": "Publish to the public internet through Tailscale Funnel. Requires type proxy, public_ack true, no allow entries, and no control_url."},
			"public_ack":        map[string]any{"type": "boolean", "default": false, "description": "Explicit acknowledgement that funnel exposes the service publicly. funnel true without it is rejected."},
			"funnel_ttl":        map[string]any{"type": "string", "enum": []string{"1h", "8h", "24h", "72h", "7d", "never"}, "description": "Public Funnel lifetime; defaults to 24h on a new entry. Omitting it preserves an existing entry's deadline. Only valid with funnel true."},
			"no_daemon_install": map[string]any{"type": "boolean", "description": "Save configuration without installing the background service."},
			"no_auto_provision": map[string]any{"type": "boolean", "default": false, "description": "Disable automatic Funnel policy provisioning. Only valid with funnel true."},
			"control_url":       map[string]any{"type": "string", "description": "Per-service custom control server URL, for example a Headscale deployment. Rejected together with funnel."},
		}, "name", "type"),
		OutputSchema: mcpAddOutputSchema,
	},
	{
		Name:         "list",
		Description:  "List locally registered TSLink services with exact runtime URLs and explicit Funnel requested/active/reason state. Use this to discover current shares or check whether public exposure actually activated.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpListOutputSchema,
	},
	{
		Name:        "unshare",
		Description: "Remove one named service from the local TSLink registry. Use this when the user asks to stop sharing a specific service; it does not expose credentials or open a network listener.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name to remove."},
		}, "name"),
		OutputSchema: mcpUnshareOutputSchema,
	},
	{
		Name:         "status",
		Description:  "Report local TSLink daemon, stored-credential, node-authorization, and service-count state. Use this before retrying a share or when diagnosing why a URL is not ready; a pending needs_login handoff includes auth_url even if the daemon stopped.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpStatusOutputSchema,
	},
	{
		Name:        "url",
		Description: "Return one registered service's exact runtime URL. Use this after add, or after a needs_login share was authorized; it reads local runtime evidence only and never guesses a hostname. It fails with url_not_ready until the daemon has published an exact URL, so pass wait to poll.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
			"wait": map[string]any{"type": "string", "description": "Go duration such as 30s to poll for an exact URL. Omitted or 0s returns immediately."},
		}, "name"),
		OutputSchema: mcpURLOutputSchema,
	},
	{
		Name:         "tags_list",
		Description:  "List every registered service with the ACL tags recorded for it locally. Use this before tags_set to see what a service currently carries; it reads the local registry and does not contact the tailnet.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpTagsListOutputSchema,
	},
	{
		Name:        "tags_set",
		Description: "Replace one service's ACL tags with a single tag in the local registry. This discards the service's other tags and can change who the tailnet policy lets reach it once the daemon restarts; it makes no remote ACL change.",
		InputSchema: objectSchema(map[string]any{
			"service": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
			"tag":     map[string]any{"type": "string", "pattern": `^tag:`, "description": "Single ACL tag, prefixed tag:. It replaces every tag currently on the service."},
		}, "service", "tag"),
		OutputSchema: mcpTagsSetOutputSchema,
	},
	{
		Name:        "access_explain",
		Description: "Explain what TSLink knows locally about one service's access: its exposure, whether an allow-list is enforced, and which layers this answer does not cover. Use this to answer \"who can reach this?\" honestly; it reads the local registry and proves nothing about live tailnet policy or backend authentication.",
		InputSchema: objectSchema(map[string]any{
			"service": map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name."},
		}, "service"),
		OutputSchema: mcpAccessExplainOutputSchema,
	},
	{
		Name:        "doctor",
		Description: "Run local TSLink diagnostics and return findings by severity. Use this when a share or URL is not working and status was not enough; it inspects local config, registry, credentials metadata and runtime state, writes nothing, and reports health_exit_code instead of failing.",
		InputSchema: objectSchema(map[string]any{
			"probe_external": map[string]any{"type": "boolean", "default": false, "description": "Also attempt a short TCP connection to non-loopback service targets. Leave false unless the user asked to test reachability of a remote backend."},
		}),
		OutputSchema: mcpDoctorOutputSchema,
	},
	{
		Name:        "logs",
		Description: "Read recent lines from the local TSLink daemon log. Use this when status and doctor did not explain a failure and the daemon's own account of what happened is needed; it opens the local log file read-only, returns a recent window rather than the whole file, and redacts credential material, Tailscale login and invitation URLs, and email addresses before returning anything.",
		InputSchema: objectSchema(map[string]any{
			"source": map[string]any{"type": "string", "enum": []string{"err", "out"}, "default": "err", "description": "err is the structured application log and is almost always the one wanted; out is the daemon's stdout."},
			"last":   map[string]any{"type": "integer", "minimum": 1, "maximum": mcpLogsMaxLast, "default": mcpLogsDefaultLast, "description": "Maximum number of lines to return, counted from the newest."},
			"level":  map[string]any{"type": "string", "enum": []string{"debug", "info", "warn", "error"}, "description": "Minimum producer log level. Omitting it returns every level."},
			"since":  map[string]any{"type": "string", "description": "Go duration such as 15m or 6h bounding how far back to read; defaults to 1h and may not exceed 168h. Widen it only after the default window came back empty."},
		}),
		OutputSchema: mcpLogsOutputSchema,
	},
	{
		Name:        "invite_user",
		Description: "Sends a real Tailscale invitation to a real email address, so confirm the address and role with the user before calling this. The invitation joins the recipient to the user's tailnet. Set print_link true to receive a bearer invite URL instead of having Tailscale send the email.",
		InputSchema: objectSchema(map[string]any{
			"email":      map[string]any{"type": "string", "description": "Recipient email address."},
			"role":       map[string]any{"type": "string", "enum": tailapi.InviteRoles(), "description": "Role assigned on acceptance. Defaults to member."},
			"print_link": map[string]any{"type": "boolean", "default": false, "description": "Do not send email; return the API-provided invite URL for the user to deliver. Anyone holding that URL can accept."},
		}, "email"),
		OutputSchema: mcpInviteCreateOutputSchema,
	},
	{
		Name:        "invite_device",
		Description: "Sends a real device-sharing invitation to a real email address outside the tailnet, so confirm the service and address with the user before calling this. It shares one TSLink-owned service device with that person. Set print_link true to receive a bearer invite URL instead of having Tailscale send the email.",
		InputSchema: objectSchema(map[string]any{
			"service":         map[string]any{"type": "string", "pattern": `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, "maxLength": 63, "description": "Exact registered service name whose device is shared."},
			"email":           map[string]any{"type": "string", "description": "Recipient email address."},
			"print_link":      map[string]any{"type": "boolean", "default": false, "description": "Do not send email; return the API-provided invite URL for the user to deliver. Anyone holding that URL can accept."},
			"multi_use":       map[string]any{"type": "boolean", "default": false, "description": "Allow the invitation to be accepted more than once."},
			"allow_exit_node": map[string]any{"type": "boolean", "default": false, "description": "Allow the recipient to route traffic through the shared device as an exit node."},
		}, "service", "email"),
		OutputSchema: mcpInviteCreateOutputSchema,
	},
	{
		Name:        "invite_list",
		Description: "List open tailnet user invitations and TSLink-owned device invitations. Use this before invite_revoke or invite_resend to find an invite id; it reads the Tailscale API and sends nothing. complete false means at least one device could not be checked.",
		InputSchema: objectSchema(map[string]any{
			"show_urls": map[string]any{"type": "boolean", "default": false, "description": "Include bearer invite URLs. They are redacted by default because anyone holding one can accept the invitation."},
		}),
		OutputSchema: mcpInviteListOutputSchema,
	},
	{
		Name:        "invite_revoke",
		Description: "Cancels a real outstanding invitation on the user's tailnet, so confirm with the user before calling this. A revoked invitation can no longer be accepted and cannot be un-revoked; issue a new one instead. Use invite_list to find the id.",
		InputSchema: objectSchema(map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}, "description": "Invite namespace the id belongs to."},
			"invite_id": map[string]any{"type": "string", "description": "Invite id from invite_list."},
		}, "kind", "invite_id"),
		OutputSchema: mcpInviteRevokeOutputSchema,
	},
	{
		Name:        "invite_resend",
		Description: "Sends another real invitation email to the original recipient, so confirm with the user before calling this. It only works for an invitation Tailscale emailed; a print_link invitation has no recipient to email. Use invite_list to find the id.",
		InputSchema: objectSchema(map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{tailapi.InviteKindUser, tailapi.InviteKindDevice}, "description": "Invite namespace the id belongs to."},
			"invite_id": map[string]any{"type": "string", "description": "Invite id from invite_list."},
		}, "kind", "invite_id"),
		OutputSchema: mcpInviteResendOutputSchema,
	},
	{
		Name:         "template_list",
		Description:  "List the built-in service templates. Use this to offer the user a ready-made private stack; templates only write registry entries and never install or start third-party applications.",
		InputSchema:  objectSchema(map[string]any{}),
		OutputSchema: mcpTemplateListOutputSchema,
	},
	{
		Name:        "template_plan",
		Description: "Preview which services a built-in template would create, without writing the registry. Use this before template_apply so the user can see what will be created and what already exists.",
		InputSchema: objectSchema(map[string]any{
			"name": map[string]any{"type": "string", "description": "Template name from template_list."},
		}, "name"),
		OutputSchema: mcpTemplateApplyOutputSchema,
	},
	{
		Name:        "template_apply",
		Description: "Write a built-in template's missing services into the local registry. Existing entries with the same names are left untouched, and the background service is installed when absent unless no_daemon_install is true; call template_plan first so the user has seen the plan.",
		InputSchema: objectSchema(map[string]any{
			"no_daemon_install": map[string]any{"type": "boolean", "description": "Save configuration without installing the background service."},
			"name":              map[string]any{"type": "string", "description": "Template name from template_list."},
		}, "name"),
		OutputSchema: mcpTemplateApplyOutputSchema,
	},
}

// mcpActions is the seam between the JSON-RPC layer and the command
// implementations. Every field forwards to the same internal function the
// matching CLI command calls, so the tool surface cannot acquire behaviour the
// CLI does not have — including its refusals, which stay in the domain layer.
type mcpActions struct {
	share         func(context.Context, shareRequest) (ShareResult, error)
	add           func(context.Context, AddParams, bool) (any, error)
	list          func() (any, error)
	unshare       func(string) (any, error)
	status        func() (any, error)
	url           func(context.Context, string, time.Duration) (any, error)
	tagsList      func() (any, error)
	tagsSet       func(string, string) (any, error)
	accessExplain func(string) (any, error)
	doctor        func(bool) (any, error)
	logs          func(mcpLogsArguments) (any, error)
	inviteUser    func(context.Context, string, string, bool) (any, error)
	inviteDevice  func(context.Context, mcpInviteDeviceArguments) (any, error)
	inviteList    func(context.Context, bool) (any, error)
	inviteRevoke  func(context.Context, string, string) (any, error)
	inviteResend  func(context.Context, string, string) (any, error)
	templateList  func() (any, error)
	templatePlan  func(string) (any, error)
	templateApply func(context.Context, string, bool) (any, error)
}

// mcpAddArguments is the wire shape of the add tool's arguments.
type mcpAddArguments struct {
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Target          string   `json:"target,omitempty"`
	Dir             string   `json:"dir,omitempty"`
	Allow           []string `json:"allow,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Ephemeral       bool     `json:"ephemeral,omitempty"`
	Funnel          bool     `json:"funnel,omitempty"`
	PublicAck       bool     `json:"public_ack,omitempty"`
	FunnelTTL       *string  `json:"funnel_ttl,omitempty"`
	NoAutoProvision bool     `json:"no_auto_provision,omitempty"`
	NoDaemonInstall bool     `json:"no_daemon_install,omitempty"`
	ControlURL      string   `json:"control_url,omitempty"`
}

// mcpInviteDeviceArguments is the wire shape of the invite_device arguments.
type mcpInviteDeviceArguments struct {
	Service       string `json:"service"`
	Email         string `json:"email"`
	PrintLink     bool   `json:"print_link,omitempty"`
	MultiUse      bool   `json:"multi_use,omitempty"`
	AllowExitNode bool   `json:"allow_exit_node,omitempty"`
}

// addParamsFromMCPArguments maps the tool's type/target/dir triple onto the
// AddParams shape `tslink add` builds from --proxy/--dir/--tcp. It is pure
// argument translation: every admission rule stays in buildService and
// registry.ValidateService, which both surfaces then run identically. The
// second return value is buildService's PreserveFunnelExpiry input: an absent
// funnel_ttl preserves an existing entry's deadline, exactly as an unset
// --funnel-ttl does.
func addParamsFromMCPArguments(args mcpAddArguments) (AddParams, bool, error) {
	params := AddParams{
		Name:            args.Name,
		Ephemeral:       args.Ephemeral,
		Tags:            strings.Join(args.Tags, ","),
		Allow:           strings.Join(args.Allow, ","),
		Funnel:          args.Funnel,
		Public:          args.PublicAck,
		NoAutoProvision: args.NoAutoProvision,
		NoDaemonInstall: args.NoDaemonInstall,
		ControlURL:      args.ControlURL,
	}
	if args.FunnelTTL != nil {
		params.FunnelTTL = *args.FunnelTTL
		params.FunnelTTLSet = true
	}
	switch args.Type {
	case registry.TypeProxy:
		if args.Target == "" {
			return AddParams{}, false, output.ErrUsage("target is required for proxy type")
		}
		if args.Dir != "" {
			return AddParams{}, false, output.ErrUsage("dir is not supported for proxy type")
		}
		params.Proxy = args.Target
	case registry.TypeFile:
		if args.Dir == "" {
			return AddParams{}, false, output.ErrUsage("dir is required for file type")
		}
		if args.Target != "" {
			return AddParams{}, false, output.ErrUsage("target is not supported for file type")
		}
		params.Dir = args.Dir
	case registry.TypeTCP:
		if args.Target == "" {
			return AddParams{}, false, output.ErrUsage("target is required for tcp type")
		}
		if args.Dir != "" {
			return AddParams{}, false, output.ErrUsage("dir is not supported for tcp type")
		}
		params.TCP = args.Target
	default:
		return AddParams{}, false, registry.ServiceTypeAmbiguousError()
	}
	return params, args.FunnelTTL == nil, nil
}

// parseMCPWait reads the url tool's optional Go duration. It mirrors
// `tslink url --wait`, where a non-positive duration means "do not poll".
func parseMCPWait(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	wait, err := time.ParseDuration(raw)
	if err != nil {
		return 0, output.ErrUsage(fmt.Sprintf("invalid wait duration %q: %v", raw, err))
	}
	return wait, nil
}

type mcpServiceSummary = ListServiceSummary

type mcpStatusSummary struct {
	Supervision            Supervision `json:"supervision"`
	Authenticated          bool        `json:"authenticated"`
	CredentialStored       bool        `json:"credential_stored"`
	NodeAuthorized         bool        `json:"node_authorized"`
	AuthorizedServiceCount int         `json:"authorized_service_count"`
	DaemonRunning          bool        `json:"daemon_running"`
	ServiceCount           int         `json:"service_count"`
	Status                 string      `json:"status,omitempty"`
	AuthURL                string      `json:"auth_url,omitempty"`
	Next                   []string    `json:"next,omitempty"`
}

type mcpUnshareSummary struct {
	OK                   bool   `json:"ok"`
	Name                 string `json:"name"`
	Removed              bool   `json:"removed"`
	DeviceCleaned        bool   `json:"device_cleaned"`
	DeviceCleanupSkipped bool   `json:"device_cleanup_skipped"`
	DeviceSkipReason     string `json:"device_skip_reason,omitempty"`
	DeviceWarning        string `json:"device_warning,omitempty"`
}

func defaultMCPActions(paths sharePaths, errOut io.Writer) mcpActions {
	return mcpActions{
		share: func(ctx context.Context, req shareRequest) (ShareResult, error) {
			return executeShare(ctx, paths, req, defaultURLWait, errOut)
		},
		add: func(ctx context.Context, params AddParams, preserveFunnelExpiry bool) (any, error) {
			svc, err := buildService(params)
			if err != nil {
				return nil, err
			}
			svc, err = resolveAddService(svc, params)
			if err != nil {
				return nil, err
			}
			result, _, err := executeAdd(ctx, svc, paths.Registry, paths.PID, paths.Snapshot, preserveFunnelExpiry, 0, func() error {
				return ensureDaemonFn(ctx, errOut, params.NoDaemonInstall)
			})
			if err != nil {
				return nil, err
			}
			return result, nil
		},
		list: func() (any, error) {
			result, err := loadListResultForPaths(paths.Registry, paths.PID, paths.Snapshot, listOptions{})
			if err != nil {
				return nil, err
			}
			summaries, ok := result.Services.([]ListServiceSummary)
			if !ok {
				return nil, fmt.Errorf("unexpected list result type %T", result.Services)
			}
			services := make([]mcpServiceSummary, 0, len(summaries))
			for _, service := range summaries {
				services = append(services, mcpServiceSummary(service))
			}
			return map[string]any{"services": services}, nil
		},
		unshare: func(name string) (any, error) {
			if err := registry.ValidateName(name); err != nil {
				return nil, err
			}
			removed, err := removeServiceResult(paths.Registry, paths.Ownership, name)
			if err != nil {
				return nil, err
			}
			return mcpUnshareSummary{
				OK:                   true,
				Name:                 removed.Name,
				Removed:              removed.Removed,
				DeviceCleaned:        removed.DeviceCleaned,
				DeviceCleanupSkipped: removed.DeviceCleanupSkipped,
				DeviceSkipReason:     removed.DeviceSkipReason,
				DeviceWarning:        removed.DeviceWarning,
			}, nil
		},
		status: func() (any, error) {
			status, err := sharePollableStatusFn(paths.PID, paths.Registry, paths.Snapshot, paths.AuthHandoff)
			if err != nil {
				return nil, err
			}
			result := mcpStatusSummary{
				Supervision:            status.Supervision,
				Authenticated:          status.NodeAuthorized,
				CredentialStored:       status.CredentialStored,
				NodeAuthorized:         status.NodeAuthorized,
				AuthorizedServiceCount: status.AuthorizedServiceCount,
				DaemonRunning:          status.DaemonRunning,
				ServiceCount:           status.ServiceCount,
				Next:                   append([]string(nil), status.Next...),
			}
			if status.AuthStatus == authStatusNeedsLogin && status.AuthURL != "" {
				result.Status = authStatusNeedsLogin
				result.AuthURL = status.AuthURL
			}
			return result, nil
		},
		url: func(ctx context.Context, name string, wait time.Duration) (any, error) {
			return resolveServiceURL(ctx, paths.PID, paths.Registry, paths.Snapshot, name, wait)
		},
		tagsList: func() (any, error) {
			return tagsListResultForPath(paths.Registry)
		},
		tagsSet: func(service, tag string) (any, error) {
			result, err := tagsSetForPath(paths.Registry, service, tag)
			if err != nil {
				return nil, tagsServiceError(err)
			}
			return result, nil
		},
		accessExplain: func(service string) (any, error) {
			return accessExplainResultForPath(paths.Registry, service)
		},
		doctor: func(probeExternal bool) (any, error) {
			return buildDoctorResult(doctorOptions{
				ProbeExternal:       probeExternal,
				RegistryPath:        paths.Registry,
				PIDPath:             paths.PID,
				RuntimeSnapshotPath: paths.Snapshot,
				AuthHandoffPath:     paths.AuthHandoff,
			}), nil
		},
		logs: func(args mcpLogsArguments) (any, error) {
			logDir, err := logsLogDirFn()
			if err != nil {
				return nil, err
			}
			return collectMCPLogs(logDir, args)
		},
		inviteUser: func(ctx context.Context, email, role string, printLink bool) (any, error) {
			invite, err := inviteUserCreate(ctx, email, role, printLink)
			if err != nil {
				return nil, err
			}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteDevice: func(ctx context.Context, args mcpInviteDeviceArguments) (any, error) {
			invite, err := inviteDeviceCreate(ctx, paths.Registry, paths.PID, paths.Snapshot, args.Service, args.Email, args.PrintLink, args.MultiUse, args.AllowExitNode)
			if err != nil {
				return nil, err
			}
			return InviteMutationResult{Invite: invite, RemoteSideEffectPlan: invitePlan(invite, "create")}, nil
		},
		inviteList: func(ctx context.Context, showURLs bool) (any, error) {
			return inviteListCollect(ctx, paths.Registry, paths.PID, paths.Snapshot, showURLs)
		},
		inviteRevoke: func(ctx context.Context, kind, id string) (any, error) {
			return inviteRevokeExecute(ctx, staticInvitePaths(paths.Registry, paths.PID, paths.Snapshot), kind, id)
		},
		inviteResend: func(ctx context.Context, kind, id string) (any, error) {
			return inviteResendExecute(ctx, staticInvitePaths(paths.Registry, paths.PID, paths.Snapshot), kind, id)
		},
		templateList: func() (any, error) {
			return listTemplatesResult(), nil
		},
		templatePlan: func(name string) (any, error) {
			return applyTemplate(name, paths.Registry, true)
		},
		templateApply: func(ctx context.Context, name string, noInstall bool) (any, error) {
			result, err := applyTemplate(name, paths.Registry, false)
			if err != nil {
				return nil, err
			}
			if err := ensureDaemonFn(ctx, errOut, noInstall); err != nil {
				return nil, daemonRegistryRetainedError(err)
			}
			return result, nil
		},
	}
}

// --- Transport ------------------------------------------------------------
//
// Framing, session lifecycle and protocol-version negotiation belong to the
// official Go SDK (github.com/modelcontextprotocol/go-sdk). Everything above
// this line — the tool definitions, their schemas, and the mcpActions seam
// onto the CLI's own functions — is transport-independent and is handed to the
// SDK unchanged.

// mcpServerName is the server identity reported in initialize results.
const mcpServerName = "tslink"

// mcpInstructions is the session-level guidance handed to a connecting client.
// It names the tools whose descriptions carry a confirmation requirement, so a
// client that reads instructions before tool descriptions still gets the
// warning.
const mcpInstructions = "Use share to expose a local page to the private tailnet. A needs_login tool result is successful: open auth_url and retry after authorization. Confirm with the user before any tool whose description says it publishes publicly or sends a real invitation: share/add with funnel true, and invite_user, invite_device, invite_revoke, invite_resend."

// mcpServerVersion is the version reported in serverInfo. Unstamped
// development builds report "dev" rather than an empty string, which some
// clients render as a missing field.
func mcpServerVersion() string {
	if Version == "" {
		return "dev"
	}
	return Version
}

// newMCPServer builds the SDK server carrying the tool surface declared in
// mcpToolDefinitions.
//
// Tools are registered with [mcp.Server.AddTool], the low-level entry point,
// rather than the generic mcp.AddTool: the schemas in this file are
// hand-written to say things to a model that Go struct inference cannot
// express, and the generic path would replace them with reflected ones. The
// cost is that argument decoding and result construction stay this package's
// responsibility, which is what keeps them byte-identical to the pre-SDK
// server.
func newMCPServer(actions mcpActions) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion()},
		&mcp.ServerOptions{
			Instructions: mcpInstructions,
			// The tool set is fixed at build time, so listChanged would promise
			// a notification that never arrives, and there is no logging
			// feature here for a client to configure. Declaring capabilities
			// explicitly keeps the advertisement to what the server does.
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		},
	)
	for _, definition := range mcpToolDefinitions {
		server.AddTool(&mcp.Tool{
			Name:         definition.Name,
			Description:  definition.Description,
			InputSchema:  definition.InputSchema,
			OutputSchema: definition.OutputSchema,
		}, mcpToolHandler(definition.Name, actions))
	}
	return server
}

// runMCPStdio serves one MCP session over newline-delimited JSON-RPC on in and
// out. It returns when the peer closes the stream, when ctx is cancelled, or
// when the transport fails.
//
// It drives the session with [mcp.Server.Connect] rather than
// [mcp.Server.Run], because the two disagree about what end-of-input means.
// Run lets the reader's io.EOF reach the JSON-RPC layer, which treats a dead
// reader as a reason to cancel every request still in flight. That is right
// for a socket that vanished and wrong for a pipe whose writer simply finished
// sending: a client that writes its requests and closes stdin — which is how
// `tslink mcp` is scripted, and how this repository's compiled-binary tests
// drive it — would race the server for its own last answer, and usually win.
// So end-of-input starts a drain instead, and the session is closed only once
// every message that was read has been answered.
func runMCPStdio(ctx context.Context, in io.Reader, out io.Writer, actions mcpActions) error {
	if ctx == nil {
		// cobra leaves Command.Context nil until the command tree is executed,
		// and the SDK selects on Done.
		ctx = context.Background()
	}
	reader := newMCPRecordLimitReader(in, mcpMaxRecordBytes)
	writer := &mcpActivityWriter{inner: out}
	drain := &mcpDrainTracker{}
	server := newMCPServer(actions)
	server.AddReceivingMiddleware(drain.middleware)
	session, err := server.Connect(ctx, &mcp.IOTransport{Reader: reader, Writer: writer}, nil)
	if err != nil {
		return err
	}
	settled := make(chan struct{})
	go func() {
		select {
		case <-reader.EndOfInput():
			drain.wait(reader.Records, writer.Writes)
			_ = session.Close()
		case <-ctx.Done():
			_ = session.Close()
		case <-settled:
		}
	}()
	waitErr := session.Wait()
	close(settled)
	// Release the SDK's decoder goroutine, which is parked in a read that will
	// never return on its own now that end-of-input no longer ends the stream.
	reader.Release()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return waitErr
}

// mcpDrainSettle is how long the drain waits on a session that has gone
// completely silent — no handler running, no byte written — before concluding
// that whatever is unaccounted for was refused before it ever reached a
// handler, and that there is nothing left to wait for.
const mcpDrainSettle = 100 * time.Millisecond

// mcpDrainPoll is the drain's observation interval.
const mcpDrainPoll = 250 * time.Microsecond

// mcpDrainTracker counts messages through the SDK's receiving middleware so
// that end-of-input can be turned into "everything that was read has been
// answered".
type mcpDrainTracker struct {
	mu       sync.Mutex
	started  int
	finished int
	answers  int
}

func (d *mcpDrainTracker) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		d.mu.Lock()
		d.started++
		d.mu.Unlock()
		result, err := next(ctx, method, req)
		d.mu.Lock()
		d.finished++
		// Per the MethodHandler contract a notification returns nothing at all,
		// and anything else — a result or an error — becomes a response frame.
		// Counting those is what lets the drain wait for the frame rather than
		// for the handler, which finishes one step earlier.
		if result != nil || err != nil {
			d.answers++
		}
		d.mu.Unlock()
		return result, err
	}
}

func (d *mcpDrainTracker) counts() (started, finished, answers int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.started, d.finished, d.answers
}

// wait blocks until the session has answered every message the reader handed
// over, or until it has been silent for mcpDrainSettle.
//
// records is final by the time wait is called, because the reader only reports
// end of input once it has stopped producing. The settle branch exists because
// a message can be refused before it reaches a handler — an unsupported
// protocol version, a method sent before initialization — and would otherwise
// leave the count permanently short; it re-arms on any sign of life, so a tool
// that takes its time is never cut off.
func (d *mcpDrainTracker) wait(records func() int, writes func() int) {
	type observation struct{ started, finished, answers, records, writes int }
	var last observation
	quietSince := time.Now()
	for {
		started, finished, answers := d.counts()
		current := observation{started, finished, answers, records(), writes()}
		if current != last {
			last = current
			quietSince = time.Now()
		}
		inFlight := current.started > current.finished
		if !inFlight &&
			current.finished >= current.records &&
			current.writes >= current.answers {
			return
		}
		// The settle timeout only covers the short gap between a record being
		// read and its handler being entered. A handler that is already running
		// is waited for without a deadline, the same way the SDK's own Close
		// drains in-flight calls: a tool that takes 30s to answer is entitled to
		// answer even though the client has finished writing.
		if !inFlight && time.Since(quietSince) >= mcpDrainSettle {
			return
		}
		time.Sleep(mcpDrainPoll)
	}
}

// mcpActivityWriter is the transport's output side. It counts writes so the
// drain can tell a silent session from a busy one, and it deliberately does not
// close the underlying writer, which the command owns.
type mcpActivityWriter struct {
	inner io.Writer
	mu    sync.Mutex
	count int
}

func (w *mcpActivityWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	w.mu.Lock()
	w.count++
	w.mu.Unlock()
	return n, err
}

func (w *mcpActivityWriter) Writes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func (w *mcpActivityWriter) Close() error { return nil }

// mcpRecordLimitReader bounds the bytes a single newline-delimited record may
// contribute before the stream is abandoned.
//
// The SDK decodes straight off the reader, so without this an unbounded line is
// an unbounded allocation in a process the user did not intend to hand a memory
// budget to. The limit is a stream guard, not a framer: it counts bytes since
// the last newline and fails the read, leaving every JSON-level decision to the
// SDK.
//
// It also holds end-of-input rather than reporting it, so that the reader
// cannot end the session out from under a request that is still being answered,
// and counts the records it handed over so the drain knows how many answers to
// expect.
type mcpRecordLimitReader struct {
	inner io.Reader
	limit int

	mu       sync.Mutex
	count    int
	records  int
	inRecord bool
	err      error

	eof      chan struct{}
	eofOnce  sync.Once
	release  chan struct{}
	stopOnce sync.Once
}

// errMCPBatchUnsupported ends a session that sent a JSON-RPC batch.
var errMCPBatchUnsupported = errors.New("JSON-RPC batch requests are not supported")

func newMCPRecordLimitReader(inner io.Reader, limit int) *mcpRecordLimitReader {
	return &mcpRecordLimitReader{
		inner:   inner,
		limit:   limit,
		eof:     make(chan struct{}),
		release: make(chan struct{}),
	}
}

// EndOfInput is closed once the wrapped reader has reported io.EOF.
func (r *mcpRecordLimitReader) EndOfInput() <-chan struct{} { return r.eof }

// Records reports how many newline-delimited records have been handed over.
func (r *mcpRecordLimitReader) Records() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.records
}

// Release unblocks a read that is parked on end-of-input.
func (r *mcpRecordLimitReader) Release() {
	r.stopOnce.Do(func() { close(r.release) })
}

// Close implements io.ReadCloser. It releases a parked read and leaves the
// wrapped reader, which this type does not own, alone.
func (r *mcpRecordLimitReader) Close() error {
	r.Release()
	return nil
}

func (r *mcpRecordLimitReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	failed := r.err
	r.mu.Unlock()
	if failed != nil {
		return 0, failed
	}
	n, err := r.inner.Read(p)
	r.mu.Lock()
	for _, b := range p[:n] {
		if b == '\n' {
			if r.count > 0 {
				r.records++
			}
			r.count = 0
			r.inRecord = false
			continue
		}
		if !r.inRecord && b != ' ' && b != '\t' && b != '\r' {
			r.inRecord = true
			if b == '[' {
				// A record that opens with an array is a JSON-RPC batch. This
				// server has never supported batching, and the revisions it
				// speaks from 2025-06-18 onward removed it from the protocol;
				// refusing it here refuses it at every revision, rather than at
				// whichever one the session happens to have negotiated by the
				// time the line is read.
				r.err = errMCPBatchUnsupported
				r.mu.Unlock()
				return n, r.err
			}
		}
		r.count++
		if r.count > r.limit {
			r.err = fmt.Errorf("JSON-RPC message exceeds maximum size of %d bytes", r.limit)
			r.mu.Unlock()
			return n, r.err
		}
	}
	if err == io.EOF && r.count > 0 {
		// A final record without a trailing newline is still a record.
		r.records++
		r.count = 0
	}
	r.mu.Unlock()
	if err == io.EOF {
		r.eofOnce.Do(func() { close(r.eof) })
		if n > 0 {
			// Hand the trailing bytes over first; the next call parks.
			return n, nil
		}
		<-r.release
		r.mu.Lock()
		r.err = io.EOF
		r.mu.Unlock()
		return 0, io.EOF
	}
	if err != nil {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
	}
	return n, err
}

// mcpToolHandler binds one declared tool to the action that executes it.
func mcpToolHandler(name string, actions mcpActions) mcp.ToolHandler {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return callMCPTool(ctx, actions, name, request.Params.Arguments)
	}
}

// mcpInvalidArgumentsError reports a malformed tools/call as a JSON-RPC
// protocol error rather than a tool result. The distinction is deliberate and
// predates the SDK: a tool result with isError means "the tool ran and
// refused", which a model should read and act on, while arguments that do not
// satisfy the declared schema never reached the tool at all.
func mcpInvalidArgumentsError(tool string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "Invalid " + tool + " arguments"}
}

// callMCPTool decodes one tool's arguments and forwards to its action.
//
// Decoding is strict (DisallowUnknownFields) for every tool, so a client that
// invents a parameter is told so instead of having it silently dropped — which
// for a parameter like allow would mean a share exposed more widely than the
// caller asked for.
func callMCPTool(ctx context.Context, actions mcpActions, name string, arguments json.RawMessage) (*mcp.CallToolResult, error) {
	var data any
	var err error
	switch name {
	case "share":
		var args struct {
			NoDaemonInstall bool     `json:"no_daemon_install,omitempty"`
			Target          string   `json:"target"`
			Name            string   `json:"name,omitempty"`
			Ephemeral       *bool    `json:"ephemeral,omitempty"`
			Allow           []string `json:"allow,omitempty"`
			Tags            []string `json:"tags,omitempty"`
			Funnel          bool     `json:"funnel,omitempty"`
			PublicAck       bool     `json:"public_ack,omitempty"`
			FunnelTTL       *string  `json:"funnel_ttl,omitempty"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Target == "" {
			return nil, mcpInvalidArgumentsError("share")
		}
		req := shareRequest{
			NoDaemonInstall: args.NoDaemonInstall,
			Target:          args.Target,
			Name:            args.Name,
			Ephemeral:       true,
			Allow:           args.Allow,
			Tags:            args.Tags,
			Funnel:          args.Funnel,
			PublicAck:       args.PublicAck,
		}
		if args.Ephemeral != nil {
			req.Ephemeral = *args.Ephemeral
		}
		if args.FunnelTTL != nil {
			req.FunnelTTL = *args.FunnelTTL
			req.FunnelTTLSet = true
		}
		data, err = actions.share(ctx, req)
	case "add":
		var args mcpAddArguments
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Name == "" || args.Type == "" {
			return nil, mcpInvalidArgumentsError("add")
		}
		params, preserveFunnelExpiry, paramsErr := addParamsFromMCPArguments(args)
		if paramsErr != nil {
			data, err = nil, paramsErr
		} else {
			data, err = actions.add(ctx, params, preserveFunnelExpiry)
		}
	case "list":
		var args struct{}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("list")
		}
		data, err = actions.list()
	case "unshare":
		var args struct {
			Name string `json:"name"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Name == "" {
			return nil, mcpInvalidArgumentsError("unshare")
		}
		data, err = actions.unshare(args.Name)
	case "status":
		var args struct{}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("status")
		}
		data, err = actions.status()
	case "url":
		var args struct {
			Name string `json:"name"`
			Wait string `json:"wait,omitempty"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Name == "" {
			return nil, mcpInvalidArgumentsError("url")
		}
		wait, waitErr := parseMCPWait(args.Wait)
		if waitErr != nil {
			data, err = nil, waitErr
		} else {
			data, err = actions.url(ctx, args.Name, wait)
		}
	case "tags_list":
		var args struct{}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("tags_list")
		}
		data, err = actions.tagsList()
	case "tags_set":
		var args struct {
			Service string `json:"service"`
			Tag     string `json:"tag"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Service == "" || args.Tag == "" {
			return nil, mcpInvalidArgumentsError("tags_set")
		}
		data, err = actions.tagsSet(args.Service, args.Tag)
	case "access_explain":
		var args struct {
			Service string `json:"service"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Service == "" {
			return nil, mcpInvalidArgumentsError("access_explain")
		}
		data, err = actions.accessExplain(args.Service)
	case "doctor":
		var args struct {
			ProbeExternal bool `json:"probe_external,omitempty"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("doctor")
		}
		data, err = actions.doctor(args.ProbeExternal)
	case "logs":
		var args mcpLogsArguments
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("logs")
		}
		data, err = actions.logs(args)
	case "invite_user":
		var args struct {
			Email     string `json:"email"`
			Role      string `json:"role,omitempty"`
			PrintLink bool   `json:"print_link,omitempty"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Email == "" {
			return nil, mcpInvalidArgumentsError("invite_user")
		}
		data, err = actions.inviteUser(ctx, args.Email, args.Role, args.PrintLink)
	case "invite_device":
		var args mcpInviteDeviceArguments
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Service == "" || args.Email == "" {
			return nil, mcpInvalidArgumentsError("invite_device")
		}
		data, err = actions.inviteDevice(ctx, args)
	case "invite_list":
		var args struct {
			ShowURLs bool `json:"show_urls,omitempty"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("invite_list")
		}
		data, err = actions.inviteList(ctx, args.ShowURLs)
	case "invite_revoke":
		var args struct {
			Kind     string `json:"kind"`
			InviteID string `json:"invite_id"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Kind == "" || args.InviteID == "" {
			return nil, mcpInvalidArgumentsError("invite_revoke")
		}
		data, err = actions.inviteRevoke(ctx, args.Kind, args.InviteID)
	case "invite_resend":
		var args struct {
			Kind     string `json:"kind"`
			InviteID string `json:"invite_id"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Kind == "" || args.InviteID == "" {
			return nil, mcpInvalidArgumentsError("invite_resend")
		}
		data, err = actions.inviteResend(ctx, args.Kind, args.InviteID)
	case "template_list":
		var args struct{}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil {
			return nil, mcpInvalidArgumentsError("template_list")
		}
		data, err = actions.templateList()
	case "template_plan":
		var args struct {
			Name string `json:"name"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Name == "" {
			return nil, mcpInvalidArgumentsError("template_plan")
		}
		data, err = actions.templatePlan(args.Name)
	case "template_apply":
		var args struct {
			NoDaemonInstall bool   `json:"no_daemon_install,omitempty"`
			Name            string `json:"name"`
		}
		if decodeErr := decodeMCPArguments(arguments, &args); decodeErr != nil || args.Name == "" {
			return nil, mcpInvalidArgumentsError("template_apply")
		}
		data, err = actions.templateApply(ctx, args.Name, args.NoDaemonInstall)
	default:
		// Unreachable through the SDK, which rejects an unregistered tool name
		// with the same -32602 before any handler runs. Kept so a tool added to
		// mcpToolDefinitions without a case here fails loudly instead of
		// answering with a zero value.
		return nil, mcpInvalidArgumentsError(name)
	}
	return makeMCPToolResult(data, err), nil
}

func decodeMCPArguments(raw json.RawMessage, target any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

// makeMCPToolResult renders one action's return value as a tool result. The
// text content is the exact json.Marshal of the value, which is what makes an
// MCP payload byte-comparable with the same struct inside a `--json` envelope.
func makeMCPToolResult(data any, callErr error) *mcp.CallToolResult {
	if callErr != nil {
		return makeMCPToolErrorResult(callErr)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return makeMCPToolErrorResult(err)
	}
	structured := map[string]any{}
	if err := json.Unmarshal(encoded, &structured); err != nil {
		return makeMCPToolErrorResult(err)
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: structured,
	}
}

// makeMCPToolErrorResult reports a refusal the way the CLI reports it: the same
// output.Failure envelope, carrying the same error code and next steps. It is a
// tool result rather than a protocol error so the model can read the refusal
// and act on it.
func makeMCPToolErrorResult(err error) *mcp.CallToolResult {
	failure := output.NewFailureForError("", err)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: failure.Error.Message}},
		StructuredContent: map[string]any{
			"ok":    false,
			"code":  failure.Code,
			"error": failure.Error,
		},
		IsError: true,
	}
}

func init() {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the TSLink MCP server over stdio",
		Long: `Run a local Model Context Protocol server using newline-delimited JSON-RPC
over stdin/stdout. The server exposes the per-service surface of the CLI:
share, add, list, unshare, status, url, tags_list, tags_set, access_explain,
doctor, logs, invite_user, invite_device, invite_list, invite_revoke,
invite_resend, template_list, template_plan, and template_apply. Run
"tslink mcp" and send a tools/list request to see the current set.

Protocol handling comes from the official Go SDK, so this server speaks the
current MCP revision ` + mcpProtocolVersion + ` and negotiates down to any of
` + strings.Join(mcpSupportedProtocolVersions, ", ") + `. A ` + mcpProtocolVersion + ` client sends its version
in each request's _meta and needs no handshake; an older client negotiates one
with initialize.

Daemon lifecycle, installation, login/logout and configuration are deliberately
not exposed; use the CLI for those. Log reading is exposed read-only through the
logs tool, which bounds and redacts what it returns.

The MCP process itself opens no network listener. Invoking share, add or
template_apply installs the background service when absent unless no_daemon_install
is true; announcements go to stderr. This starts the separate TSLink daemon
and its requested tsnet services. share and add with
funnel true publish to the public internet, and the invite_* tools send or
cancel real invitations through the Tailscale API, so a client should confirm
those with its user first. Protocol frames are written only to stdout;
diagnostics and logs are written only to stderr.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput(cmd) {
				fmt.Fprintln(cmd.ErrOrStderr(), "Error: --json is not valid with tslink mcp; stdout is reserved for JSON-RPC frames")
				return output.SilentExit(output.ExitUsage)
			}
			if err := shareEnsureDirFn(); err != nil {
				return err
			}
			paths, err := resolveSharePaths()
			if err != nil {
				return err
			}
			if err := runMCPStdio(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), defaultMCPActions(paths, cmd.ErrOrStderr())); err != nil {
				return fmt.Errorf("mcp stdio: %w", err)
			}
			return nil
		},
	}
	rootCmd.AddCommand(mcpCmd)
}
