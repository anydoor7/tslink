package mcpaudit

import (
	"context"
	"crypto/rand"
	"fmt"
	"os/user"
	"path/filepath"
	"time"

	"github.com/anydoor7/tslink/internal/mcpscope"
)

// Change is a committed authority change. Notes, reasons, URLs, PINs and tokens
// are deliberately unrepresentable. A nil deadline means permanent access.
type Change struct {
	Action            string     `json:"action"`
	App               string     `json:"app"`
	Subject           string     `json:"subject,omitempty"`
	ID                string     `json:"id,omitempty"`
	PreviousExpiresAt *time.Time `json:"previous_expires_at,omitempty"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
}

type changesKey struct{}

// Collect attaches committed changes to the enclosing MCP completion receipt.
// Tool actions are synchronous; this collector is local to one invocation.
func Collect(ctx context.Context) (context.Context, *[]Change) {
	changes := &[]Change{}
	return context.WithValue(ctx, changesKey{}, changes), changes
}

func Surface(ctx context.Context) string {
	if session, ok := mcpscope.FromContext(ctx); ok {
		if session.Scope.Role != "owner" {
			return "scoped_mcp"
		}
		return "mcp"
	}
	return "cli"
}

// RecordChange never rolls back a committed mutation. A failure explicitly
// reports that the mutation committed without its audit receipt.
func RecordChange(ctx context.Context, registryPath, surface, actor string, at time.Time, change Change) error {
	if collector, ok := ctx.Value(changesKey{}).(*[]Change); ok {
		*collector = append(*collector, change)
		return nil
	}
	entry := Entry{Kind: "lifecycle", ID: rand.Text(), Time: at.UTC(), Surface: surface,
		Principal: actor, Phase: "completion", Tool: change.Action, Apps: []string{change.App},
		Result: "ok", Changes: []Change{change}}
	if s, ok := mcpscope.FromContext(ctx); ok {
		entry.Identity, entry.Principal, entry.Role = s.Identity, s.Who, s.Scope.Role
		entry.Capabilities, entry.ScopeExpiresAt = s.Scope, s.ExpiresAt
	}
	if err := (Journal{Path: filepath.Join(filepath.Dir(registryPath), "mcp-audit.json")}).Record(context.WithoutCancel(ctx), entry); err != nil {
		return fmt.Errorf("change committed but audit receipt unavailable: %w", err)
	}
	return nil
}

func LocalActor() string {
	if u, err := user.Current(); err == nil {
		return "local-user:" + u.Username
	}
	return "local-os-user"
}
