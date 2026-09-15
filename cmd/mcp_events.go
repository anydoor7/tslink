package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/monody0007/tslink/internal/inspect"
	tsruntime "github.com/monody0007/tslink/internal/runtime"
)

// mcpEventState is the body of one control-plane event frame.
//
// It answers exactly what the list and status tools answer, so a client that
// received an event has already been told what a follow-up list + status round
// trip would tell it. That is the point of the stream: replacing polling, not
// signalling that polling is due.
//
// Two deliberate differences from the tool results, both narrowing:
//
//   - status carries no auth_url. The interactive enrollment URL is a bearer
//     capability: whoever opens it enrolls a device into the tailnet. A UI does
//     not need it to render — status "needs_login" is enough to show the
//     banner — and the URL is one status tool call away when the user acts on
//     that banner. Pushing a bearer value continuously to every open stream is
//     a larger exposure than answering for it on request, so the stream does
//     not carry it.
//   - free-text error strings are run through the doctor evidence sanitizer.
//     These strings originate in Tailscale API and dial failures and can quote
//     the thing that failed, including a target URL with credentials in it.
//
// The projection is done by decoding into these types rather than by deleting
// fields from a map. A field added to ListServiceSummary or mcpStatusSummary
// later is dropped here by construction; a blacklist would have to be updated
// by whoever added it, and would not be.
type mcpEventState struct {
	SchemaVersion string               `json:"schema_version"`
	Services      []ListServiceSummary `json:"services"`
	Status        mcpEventStatus       `json:"status"`
}

// mcpEventStatus is the status tool's payload minus auth_url. Field names and
// JSON tags match mcpStatusSummary so a client parses one shape.
type mcpEventStatus struct {
	Supervision            Supervision `json:"supervision"`
	Authenticated          bool        `json:"authenticated"`
	CredentialStored       bool        `json:"credential_stored"`
	NodeAuthorized         bool        `json:"node_authorized"`
	AuthorizedServiceCount int         `json:"authorized_service_count"`
	DaemonRunning          bool        `json:"daemon_running"`
	ServiceCount           int         `json:"service_count"`
	Status                 string      `json:"status,omitempty"`
	Next                   []string    `json:"next,omitempty"`
}

// mcpEventsSnapshotFn returns the snapshot builder the daemon hands to the
// event transport.
//
// It runs the same actions the list and status tools run, which is what keeps
// a pushed payload and a polled one from drifting: there is one implementation
// of "what is the current state", and both surfaces call it.
func mcpEventsSnapshotFn(actions mcpActions) func(context.Context) (any, error) {
	if actions.list == nil || actions.status == nil {
		return nil
	}
	return func(context.Context) (any, error) {
		listValue, err := actions.list()
		if err != nil {
			return nil, err
		}
		statusValue, err := actions.status()
		if err != nil {
			return nil, err
		}
		return buildMCPEventState(listValue, statusValue)
	}
}

// buildMCPEventState projects one list result and one status result onto the
// event payload.
func buildMCPEventState(listValue, statusValue any) (mcpEventState, error) {
	var listed struct {
		Services []ListServiceSummary `json:"services"`
	}
	if err := reprojectJSON(listValue, &listed); err != nil {
		return mcpEventState{}, fmt.Errorf("project list result for event stream: %w", err)
	}
	var status mcpEventStatus
	if err := reprojectJSON(statusValue, &status); err != nil {
		return mcpEventState{}, fmt.Errorf("project status result for event stream: %w", err)
	}

	// An empty registry emits an empty array, never null: a client that renders
	// a list should show "no services", not fall into its null branch.
	services := make([]ListServiceSummary, 0, len(listed.Services))
	for _, service := range listed.Services {
		service.Error = sanitizedServiceError(service.Error)
		services = append(services, service)
	}
	return mcpEventState{
		SchemaVersion: inspect.SchemaVersion,
		Services:      services,
		Status:        status,
	}, nil
}

// reprojectJSON re-encodes a value and decodes it into target. Unknown fields
// are dropped on purpose; that drop is the whitelist.
func reprojectJSON(value, target any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

// sanitizedServiceError copies a service error with its free-text fields run
// through the doctor evidence sanitizer. It returns a copy so the caller's
// value, which may be shared with a tool result, is not mutated.
func sanitizedServiceError(in *tsruntime.ServiceError) *tsruntime.ServiceError {
	if in == nil {
		return nil
	}
	out := *in
	out.Message = sanitizeDoctorEvidenceValue(in.Message)
	if len(in.Next) > 0 {
		next := make([]string, 0, len(in.Next))
		for _, step := range in.Next {
			next = append(next, sanitizeDoctorEvidenceValue(step))
		}
		out.Next = next
	}
	if in.Provision != nil {
		provision := *in.Provision
		provision.Reason = sanitizeDoctorEvidenceValue(in.Provision.Reason)
		out.Provision = &provision
	}
	return &out
}
