// Package mcpscope defines transport-independent MCP capabilities. It never
// reads configuration or grants capabilities from tool arguments.
package mcpscope

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anydoor7/tslink/internal/duration"
)

const DeniedCode = "mcp_scope_denied"

type Scope struct {
	Role        string   `json:"role"`
	Apps        []string `json:"apps,omitempty"`
	Inventory   bool     `json:"inventory,omitempty"`
	MaxDuration string   `json:"max_duration,omitempty"`
}

// For is anchored to IssuedAt, never to server startup or a request. ExpiresAt
// is an alternative absolute deadline. No custom roles are accepted.
type Binding struct {
	Principal string `json:"principal"`
	Scope
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	For       string     `json:"for,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Identity retains the authenticated caller separately from the binding principal.
// Only bounded login/node identifiers are carried; no transport arguments.
type Identity struct {
	Login string `json:"login"`
	Node  string `json:"node"`
}

type Session struct {
	Identity  Identity
	Who       string
	Scope     Scope
	ExpiresAt *time.Time
}

type Denied struct{}

func (Denied) Error() string      { return "MCP scope does not permit this action" }
func (Denied) StableCode() string { return DeniedCode }

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var tagPattern = regexp.MustCompile(`^tag:[A-Za-z][A-Za-z0-9-]*$`)

// Principal uses the people-store login rule: ASCII trim and case only.
// Tags retain exact spelling, including valid legacy uppercase tag names.
func Principal(raw string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", fmt.Errorf("invalid principal")
	}
	original := strings.Trim(raw, " \t\r\n\v\f")
	if strings.HasPrefix(original, "tag:") {
		if len(original) > 512 || !tagPattern.MatchString(original) {
			return "", fmt.Errorf("invalid tag principal")
		}
		return original, nil
	}
	value := []byte(original)
	for i, b := range value {
		if b >= 'A' && b <= 'Z' {
			value[i] = b + ('a' - 'A')
		}
	}
	p := string(value)
	if p == "" || len(p) > 512 || strings.IndexFunc(p, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return "", fmt.Errorf("invalid principal")
	}
	if strings.HasPrefix(p, "tag:") {
		return "", fmt.Errorf("invalid tag principal")
	}
	return p, nil
}

// ParseDuration is the sole scope-duration adapter for the shared grammar.
func ParseDuration(raw string) (time.Duration, error) {
	d, err := duration.Parse(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("scope duration must be positive")
	}
	return d, nil
}

func (s Scope) Validate() error {
	switch s.Role {
	case "owner":
		if len(s.Apps) != 0 || s.Inventory || s.MaxDuration != "" {
			return fmt.Errorf("owner scope cannot have restrictions")
		}
	case "viewer":
		if s.MaxDuration != "" {
			return fmt.Errorf("viewer cannot grant people")
		}
	case "app-operator", "people-manager":
		if s.Inventory {
			return fmt.Errorf("operator scopes require explicit apps")
		}
		if _, err := ParseDuration(s.MaxDuration); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown MCP role")
	}
	if s.Role != "owner" && !s.Inventory && len(s.Apps) == 0 {
		return fmt.Errorf("scope requires apps or explicit viewer inventory")
	}
	seen := map[string]bool{}
	for _, app := range s.Apps {
		if !namePattern.MatchString(app) || app == "all" || seen[app] {
			return fmt.Errorf("invalid or duplicate scope app")
		}
		seen[app] = true
	}
	return nil
}

func (b Binding) Deadline() (*time.Time, error) {
	if b.For != "" {
		if b.IssuedAt == nil || b.IssuedAt.IsZero() || b.ExpiresAt != nil {
			return nil, fmt.Errorf("scope for requires issued_at and excludes expires_at")
		}
		d, err := ParseDuration(b.For)
		if err != nil {
			return nil, err
		}
		t := b.IssuedAt.Add(d).UTC()
		return &t, nil
	}
	if b.IssuedAt != nil {
		return nil, fmt.Errorf("issued_at requires for")
	}
	if b.ExpiresAt != nil && b.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("invalid scope expires_at")
	}
	return b.ExpiresAt, nil
}

func ValidateBindings(allow []string, bindings []Binding) error {
	seen := map[string]bool{}
	for _, raw := range allow {
		// Retain legacy blank-entry handling. Blanks never authorize anyone.
		if strings.TrimSpace(raw) == "" {
			continue
		}
		p, err := Principal(raw)
		if err != nil {
			return err
		}
		seen[p] = true
	}
	for _, b := range bindings {
		p, err := Principal(b.Principal)
		if err != nil || p != b.Principal || seen[p] {
			return fmt.Errorf("invalid, noncanonical or duplicate scope principal")
		}
		seen[p] = true
		if err := b.Scope.Validate(); err != nil {
			return err
		}
		if _, err := b.Deadline(); err != nil {
			return err
		}
	}
	return nil
}

func (s Session) Active(now time.Time) bool { return s.ExpiresAt == nil || now.Before(*s.ExpiresAt) }
func (s Scope) AllowsApp(app string) bool {
	if s.Role == "owner" || s.Inventory {
		return true
	}
	for _, allowed := range s.Apps {
		if app == allowed {
			return true
		}
	}
	return false
}

// ToolAllowed is an explicit allowlist. Adding a registry tool fails closed
// for reduced roles until its behavior and egress have been reviewed here.
func (s Scope) ToolAllowed(tool string) bool {
	if s.Role == "owner" {
		return true
	}
	switch tool {
	case "list", "status", "url", "health", "doctor", "access_explain", "tags_list", "people_list", "recipe_list", "template_list":
		return s.Role == "viewer" || s.Role == "app-operator" || s.Role == "people-manager"
	case "people_grant", "people_revoke":
		return s.Role == "app-operator" || s.Role == "people-manager"
	case "app_restart":
		return s.Role == "app-operator"
	}
	return false
}

func (s Session) Authorize(tool string, apps []string, now time.Time) error {
	if !s.Active(now) || !s.Scope.ToolAllowed(tool) {
		return Denied{}
	}
	for _, app := range apps {
		if !s.Scope.AllowsApp(app) {
			return Denied{}
		}
	}
	return nil
}

// GrantDeadline bounds a people grant by both capability lifetime and binding
// expiry. Registry writers reuse this after acquiring their lock.
func (s Session) GrantDeadline(lifetime string, now time.Time) (*time.Time, error) {
	d, err := ParseDuration(lifetime)
	if err != nil {
		return nil, Denied{}
	}
	if s.Scope.Role != "owner" {
		max, err := ParseDuration(s.Scope.MaxDuration)
		if err != nil || d > max {
			return nil, Denied{}
		}
	}
	t := now.Add(d).UTC()
	if !s.Active(now) || s.ExpiresAt != nil && t.After(*s.ExpiresAt) {
		return nil, Denied{}
	}
	return &t, nil
}

// Resolve never unions roles. An explicit login binding wins over tag
// bindings. Multiple matching tags are ambiguous and fail closed. Legacy
// allow matches retain their owner semantics.
func Resolve(login string, tags, allow []string, bindings []Binding, now time.Time) (Session, error) {
	if err := ValidateBindings(allow, bindings); err != nil {
		return Session{}, Denied{}
	}
	p, err := Principal(login)
	if err != nil {
		return Session{}, Denied{}
	}
	match := func(principal string) bool {
		if !strings.HasPrefix(principal, "tag:") {
			return principal == p
		}
		for _, t := range tags {
			if t == principal {
				return true
			}
		}
		return false
	}
	for _, b := range bindings {
		if !strings.HasPrefix(b.Principal, "tag:") && b.Principal == p {
			deadline, _ := b.Deadline()
			s := Session{Who: p, Identity: Identity{Login: p}, Scope: b.Scope, ExpiresAt: deadline}
			if !s.Active(now) {
				return Session{}, Denied{}
			}
			return s, nil
		}
	}
	// A legacy login entry is also an explicit login binding.
	for _, raw := range allow {
		principal, e := Principal(raw)
		if e == nil && !strings.HasPrefix(principal, "tag:") && principal == p {
			return Session{Who: principal, Identity: Identity{Login: p}, Scope: Scope{Role: "owner"}}, nil
		}
	}
	var resolved *Session
	seen := map[string]bool{}
	for _, raw := range allow {
		principal, e := Principal(raw)
		if e == nil && strings.HasPrefix(principal, "tag:") && match(principal) && !seen[principal] {
			if resolved != nil {
				return Session{}, Denied{}
			}
			s := Session{Who: principal, Identity: Identity{Login: p}, Scope: Scope{Role: "owner"}}
			resolved = &s
			seen[principal] = true
		}
	}
	for _, b := range bindings {
		if match(b.Principal) {
			deadline, _ := b.Deadline()
			s := Session{Who: b.Principal, Identity: Identity{Login: p}, Scope: b.Scope, ExpiresAt: deadline}
			if resolved != nil || !s.Active(now) {
				return Session{}, Denied{}
			}
			resolved = &s
		}
	}
	if resolved == nil {
		return Session{}, Denied{}
	}
	return *resolved, nil
}

type contextKey struct{}

func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, contextKey{}, s)
}
func FromContext(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(contextKey{}).(Session)
	return s, ok
}

// NodeIdentity accepts only a bounded non-secret node identifier.
func NodeIdentity(raw string) string {
	if len(raw) > 256 || !utf8.ValidString(raw) || strings.Contains(raw, "://") || strings.Contains(raw, "tskey-") || strings.Contains(raw, "Bearer ") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return ""
	}
	return raw
}

type clockKey struct{}

// WithClock captures the policy clock once at dispatch for downstream writers.
func WithClock(ctx context.Context, now func() time.Time) context.Context {
	return context.WithValue(ctx, clockKey{}, now)
}

// CheckEffect is repeated after waits at transaction and external-effect boundaries.
// Audit completion uses a different context and cannot grant mutation authority.
func CheckEffect(ctx context.Context) error {
	s, ok := FromContext(ctx)
	if ctx.Err() != nil {
		if ok {
			return Denied{}
		}
		return ctx.Err()
	}
	now := time.Now
	if clock, ok := ctx.Value(clockKey{}).(func() time.Time); ok {
		now = clock
	}
	if ok && !s.Active(now()) {
		return Denied{}
	}
	return nil
}
