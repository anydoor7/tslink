package registry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/errcode"
	"golang.org/x/crypto/pbkdf2"
)

// GuestGrant is a separate browser authorization ledger. Authentication is the
// extension point for a future identity provider; only token is supported now.
// Secret hashes stay in the private registry and never in GuestView.
type GuestGrant struct {
	ID             string     `json:"id"`
	App            string     `json:"app"`
	Label          string     `json:"label"`
	Authentication string     `json:"authentication"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Revoked        bool       `json:"revoked"`
	Expired        bool       `json:"expired,omitempty"`
	Salt           string     `json:"salt"`
	TokenHash      string     `json:"token_hash"`
	PINHash        string     `json:"pin_hash,omitempty"`
	Uses           uint64     `json:"uses"`
	Sessions       uint64     `json:"sessions"`
	LastUsedAt     *time.Time `json:"last_used_at,omitempty"`
	PINFailures    uint64     `json:"pin_failures"`
	PINAttempts    int        `json:"pin_attempts,omitempty"`
	PINLockedUntil *time.Time `json:"pin_locked_until,omitempty"`
}

type GuestView struct {
	ID             string     `json:"id"`
	App            string     `json:"app"`
	Label          string     `json:"label"`
	Authentication string     `json:"authentication"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Revoked        bool       `json:"revoked"`
	Expired        bool       `json:"expired"`
	PINRequired    bool       `json:"pin_required"`
	Uses           uint64     `json:"uses"`
	Sessions       uint64     `json:"sessions"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	PINFailures    uint64     `json:"pin_failures"`
}

func (g GuestGrant) View(now time.Time) GuestView {
	return GuestView{g.ID, g.App, g.Label, g.Authentication, g.CreatedAt, g.ExpiresAt, g.Revoked, g.Expired || !now.Before(g.ExpiresAt), g.PINHash != "", g.Uses, g.Sessions, g.LastUsedAt, g.PINFailures}
}
func guestHash(salt, value string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + value))
	return hex.EncodeToString(sum[:])
}
func guestPINHash(salt, pin string) string {
	return hex.EncodeToString(pbkdf2.Key([]byte(pin), []byte("tslink-guest-pin\x00"+salt), 100000, 32, sha256.New))
}
func constantHash(a, b string) bool {
	return len(a) == 64 && len(b) == 64 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func randomGuestValue(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func guestError(code, message string) error { return CodedError{Code: code, Message: message} }
func validateGuests(grants []GuestGrant) error {
	seen := map[string]bool{}
	if len(grants) > 4096 {
		return fmt.Errorf("guest grant limit exceeded")
	}
	for _, g := range grants {
		if ValidateName(g.ID) != nil || ValidateName(g.App) != nil || seen[g.ID] || g.Authentication != "token" || !g.ExpiresAt.After(g.CreatedAt) || len(g.Salt) != 43 || !validGuestDigest(g.TokenHash) || (g.PINHash != "" && !validGuestDigest(g.PINHash)) || g.PINAttempts < 0 || g.PINAttempts > 5 || !validGuestLabel(g.Label) {
			return fmt.Errorf("invalid guest grant")
		}
		seen[g.ID] = true
	}
	return nil
}
func validGuestDigest(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func validGuestLabel(s string) bool {
	return utf8.ValidString(s) && len(s) <= 200 && strings.IndexFunc(s, unicode.IsControl) < 0
}

type CreateGuestOptions struct {
	Context                context.Context
	App, Label, Value, PIN string
	PublicAck              bool
	Policy                 duration.Policy
	Now                    time.Time
}

// CreateGuest enables the gate and the public edge atomically. An existing
// open Funnel must be disabled first, so this command never silently adopts it.
func CreateGuest(path string, o CreateGuestOptions) (view GuestView, token string, err error) {
	if !validGuestLabel(o.Label) {
		return view, "", guestError(errcode.UsageError, "label must be valid text of at most 200 bytes")
	}
	if o.PIN != "" && (len(o.PIN) < 6 || len(o.PIN) > 64 || strings.IndexFunc(o.PIN, func(r rune) bool { return r < '0' || r > '9' }) >= 0) {
		return view, "", guestError(errcode.UsageError, "PIN must contain 6..64 ASCII digits")
	}
	l, e := o.Policy.Resolve(o.Value, duration.Guest, false, o.Now, time.Local)
	if e != nil {
		return view, "", guestError(errcode.UsageError, e.Error())
	}
	token, e = randomGuestValue(32)
	if e != nil {
		return view, "", e
	}
	salt, e := randomGuestValue(32)
	if e != nil {
		return view, "", e
	}
	id, e := randomGuestValue(16)
	if e != nil {
		return view, "", e
	}
	// IDs are DNS-label compatible and deliberately unrelated to bearer tokens.
	id = "guest-" + hex.EncodeToString([]byte(id))
	g := GuestGrant{ID: id, App: o.App, Label: o.Label, Authentication: "token", CreatedAt: o.Now.UTC(), ExpiresAt: *l.Deadline, Salt: salt, TokenHash: guestHash(salt, token)}
	if o.PIN != "" {
		g.PINHash = guestPINHash(salt, o.PIN)
	}
	err = withLockContext(mutationContext(o.Context), path, func() error {
		if err := authorizeLifetime(o.Context, "guest_create", o.App, l, o.Now); err != nil {
			return err
		}
		reg, e := loadForMutation(path)
		if e != nil {
			return e
		}
		if len(reg.Guests) >= 4096 {
			return guestError(errcode.Conflict, "guest grant limit reached")
		}
		found := false
		for i := range reg.Services {
			s := &reg.Services[i]
			if s.Name != o.App {
				continue
			}
			found = true
			if s.Type != TypeProxy || s.ControlURL != "" {
				return guestError(errcode.UsageError, "guest links require a proxy using the default Tailscale control server")
			}
			if s.Funnel && !s.GuestGate {
				return guestError(errcode.Conflict, "disable the existing open Funnel before enabling guest links")
			}
			if !s.GuestGate && !o.PublicAck {
				return FunnelPublicAckError()
			}
			s.GuestGate, s.Funnel, s.PublicAck = true, true, true
			if s.FunnelExpiresAt == nil || s.FunnelExpiresAt.Before(g.ExpiresAt) {
				s.FunnelExpiresAt = &g.ExpiresAt
			}
			if e = ValidateService(*s); e != nil {
				return e
			}
		}
		if !found {
			return guestError(errcode.NotFound, "service not found")
		}
		reg.Guests = append(reg.Guests, g)
		return save(path, reg)
	})
	if err != nil {
		return GuestView{}, "", err
	}
	return g.View(o.Now), token, nil
}
func ListGuests(path string, now time.Time) ([]GuestView, error) {
	reg, issues, e := readGuestState(path)
	if os.IsNotExist(e) {
		return []GuestView{}, nil
	}
	if e != nil {
		return nil, e
	}
	if len(issues) > 0 {
		return nil, issues[0]
	}
	pendingGuestViews(path, reg)
	out := make([]GuestView, 0, len(reg.Guests))
	for _, g := range reg.Guests {
		out = append(out, g.View(now))
	}
	return out, nil
}
func ShowGuest(path, id string, now time.Time) (GuestView, error) {
	list, e := ListGuests(path, now)
	if e != nil {
		return GuestView{}, e
	}
	for _, g := range list {
		if g.ID == id {
			return g, nil
		}
	}
	return GuestView{}, guestError(errcode.NotFound, "guest link not found")
}
func RevokeGuest(path, id string, now time.Time) (view GuestView, err error) {
	return RevokeGuestContext(context.Background(), path, id, now)
}

func RevokeGuestContext(ctx context.Context, path, id string, now time.Time) (view GuestView, err error) {
	changed := false
	err = withLockContext(ctx, path, func() error {
		reg, e := loadForMutation(path)
		if e != nil {
			return e
		}
		for i := range reg.Guests {
			g := &reg.Guests[i]
			if g.ID == id {
				if session, ok := mcpscope.FromContext(ctx); ok {
					if e := session.Authorize("guest_revoke", []string{g.App}, now); e != nil {
						return e
					}
				}
				changed = !g.Revoked
				g.Revoked = true
				view = g.View(now)
				if e := save(path, reg); e != nil {
					return e
				}
				view = g.View(now)
				return nil
			}
		}
		return guestError(errcode.NotFound, "guest link not found")
	})
	if err == nil && changed {
		err = mcpaudit.RecordChange(ctx, path, mcpaudit.Surface(ctx), mcpaudit.LocalActor(), now, mcpaudit.Change{Action: "guest_revoked", App: view.App, ID: view.ID, ExpiresAt: &view.ExpiresAt})
	}
	return
}

// FindGuestToken compares every app candidate in constant time. No bearer
// value is ever used as an ID or included in an error message.
func FindGuestToken(path, app, token string, now time.Time) (GuestView, string) {
	if len(token) != 43 {
		return GuestView{}, "invalid_token"
	}
	reg, issues, e := readGuestState(path)
	if e != nil || len(issues) > 0 {
		return GuestView{}, "unavailable"
	}
	var id string
	for _, g := range reg.Guests {
		if g.App == app && constantHash(g.TokenHash, guestHash(g.Salt, token)) {
			id = g.ID
		}
	}
	if id == "" {
		return GuestView{}, "invalid_token"
	}
	return CheckGuest(path, app, id, now, false, false)
}

// CheckGuest reads current authorization with a shared lock. Only the first
// observation of expiry takes the writer lock to persist the rollback latch.
func CheckGuest(path, app, id string, now time.Time, use, session bool) (view GuestView, reason string) {
	reg, issues, e := readGuestState(path)
	if e != nil || len(issues) > 0 {
		return view, "unavailable"
	}
	gated := false
	for _, s := range reg.Services {
		if s.Name == app {
			gated = s.GuestGate && s.Funnel && s.PublicAck && !FunnelExpiredAt(s, now)
		}
	}
	for _, g := range reg.Guests {
		if g.ID != id || g.App != app {
			continue
		}
		view = g.View(now)
		if g.Revoked {
			return view, "revoked"
		}
		if g.Expired {
			return view, "expired"
		}
		if !now.Before(g.ExpiresAt) {
			expired := false
			acquired, e := tryWithLock(path, func() error {
				current, e := loadForMutation(path)
				if e != nil {
					return e
				}
				for i := range current.Guests {
					grant := &current.Guests[i]
					if grant.ID == id && grant.App == app {
						if !grant.Expired {
							grant.Expired = true
							expired = true
							return save(path, current)
						}
						return nil
					}
				}
				return fmt.Errorf("guest grant unavailable")
			})
			if e != nil || !acquired {
				return view, "unavailable"
			}
			if expired {
				if err := recordExpiry(path, now, mcpaudit.Change{Action: "guest_expired", App: app, ID: id, ExpiresAt: &g.ExpiresAt}); err != nil {
					return view, "unavailable"
				}
			}
			return view, "expired"
		}
		if !gated {
			return view, "mismatched"
		}
		addGuestUsage(path, id, now, use, session)
		return view, "allowed"
	}
	return view, "invalid_token"
}

// CheckGuestPIN is bounded by a persisted per-grant five-attempt/15m window.
// Only hashes are stored. Invalid PINs still consume a slot after restart.
func CheckGuestPIN(path, app, id, pin string, now time.Time) (view GuestView, reason string) {
	view, reason = CheckGuest(path, app, id, now, false, false)
	if reason != "allowed" {
		return
	}
	reason = "unavailable"
	if _, _, e := guestPreflight(path); e != nil {
		return view, reason
	}
	acquired, e := tryWithLock(path, func() error {
		reg, e := loadForMutation(path)
		if e != nil {
			return e
		}
		for i := range reg.Guests {
			g := &reg.Guests[i]
			if g.ID != id || g.App != app {
				continue
			}
			view = g.View(now)
			if g.Revoked {
				reason = "revoked"
				return nil
			}
			if g.Expired || !now.Before(g.ExpiresAt) {
				reason = "expired"
				return nil
			}
			if g.PINLockedUntil != nil && now.Before(*g.PINLockedUntil) && g.PINAttempts >= 5 {
				reason = "rate_limited"
				return nil
			}
			if g.PINLockedUntil == nil || !now.Before(*g.PINLockedUntil) {
				at := now.Add(15 * time.Minute).UTC()
				g.PINLockedUntil = &at
				g.PINAttempts = 0
			}
			g.PINAttempts++
			if len(pin) <= 64 && g.PINHash != "" && constantHash(g.PINHash, guestPINHash(g.Salt, pin)) {
				reason = "allowed"
			} else {
				reason = "bad_pin"
				g.PINFailures++
				if g.PINAttempts >= 5 {
					reason = "rate_limited"
				}
			}
			view = g.View(now)
			return save(path, reg)
		}
		return nil
	})
	if e != nil || !acquired {
		reason = "unavailable"
	}
	return
}

// Guest reads refuse special files and oversized input before reading. The
// private registry is owner-controlled; atomic replacement remains the writer.
func guestPreflight(path string) (*Registry, []ServiceIssue, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, nil, fmt.Errorf("unsafe guest registry file")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	current, e := f.Stat()
	if e != nil || !current.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("unsafe guest registry file")
	}
	data, e := io.ReadAll(io.LimitReader(f, 16<<20+1))
	if e != nil {
		return nil, nil, e
	}
	if len(data) > 16<<20 {
		return nil, nil, fmt.Errorf("oversized guest registry")
	}
	return decodeForRuntime(data)
}

func guestServiceError(svc Service, grants []GuestGrant) error {
	if svc.Funnel && !svc.GuestGate {
		for _, g := range grants {
			if g.App == svc.Name {
				return guestError("conflict", "Funnel service with guest history requires the mandatory guest gate")
			}
		}
	}
	return nil
}
