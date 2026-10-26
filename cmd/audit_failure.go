package cmd

import (
	"encoding/json"
	"errors"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// auditedFailure is a failure whose audit receipt also failed. Its chain holds
// both, the primary failure first, so errors.Is and errors.As reach either and
// the stable code, recovery steps and data are the primary's. Its envelope
// message names both: a coded primary failure alone would otherwise render
// its own message and hide that the receipt is missing.
type auditedFailure struct{ primary, audit error }

// joinAuditFailure returns the failure to report when a receipt for an
// operation that already failed cannot be written. With no primary failure it
// is the audit failure alone.
func joinAuditFailure(primary, audit error) error {
	if primary == nil {
		return audit
	}
	if audit == nil {
		return primary
	}
	return auditedFailure{primary: primary, audit: audit}
}

func (e auditedFailure) Error() string { return errors.Join(e.primary, e.audit).Error() }

func (e auditedFailure) Unwrap() []error { return []error{e.primary, e.audit} }

func (e auditedFailure) EnvelopeMessage() string {
	return output.NewFailureForError("", e.primary).Error.Message + "\n" + output.NewFailureForError("", e.audit).Error.Message
}

// mcpToolFailure is the failure a tool result already carries, read back from
// its error object so it can be joined with a later failure without changing
// its code, message, next steps or data.
type mcpToolFailure struct {
	// Stable is the stable error code the result already carries, forwarded
	// unchanged; it is never constructed here.
	Stable  string          `json:"code"`
	Message string          `json:"message"`
	Next    []string        `json:"next,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (f mcpToolFailure) Error() string          { return f.Message }
func (f mcpToolFailure) StableCode() string     { return f.Stable }
func (f mcpToolFailure) NextCommands() []string { return append([]string(nil), f.Next...) }
func (f mcpToolFailure) ErrorData() any {
	if len(f.Data) == 0 {
		return nil
	}
	return f.Data
}

// mcpResultFailure reads the failure out of a failed tool result.
func mcpResultFailure(result *mcp.CallToolResult) (error, bool) {
	if result == nil || !result.IsError {
		return nil, false
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			var failure mcpToolFailure
			if json.Unmarshal([]byte(text.Text), &failure) == nil && failure.Stable != "" {
				return failure, true
			}
		}
	}
	return nil, false
}
