package health

import (
	"math"
	"time"
)

type Expiry struct {
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	DaysLeft  *int       `json:"days_left,omitempty"`
	Source    string     `json:"source"`
	Warning   string     `json:"warning,omitempty"`
	Next      []string   `json:"next,omitempty"`
}

// ExpiryAt does not infer a deadline from the node's age or its tags. A nil
// LocalClient KeyExpiry cannot prove that expiry is disabled.
func ExpiryAt(expires *time.Time, source string, now time.Time, next []string) Expiry {
	e := Expiry{State: Unknown, Source: source}
	if expires == nil || expires.IsZero() {
		return e
	}
	t := expires.UTC()
	e.ExpiresAt = &t
	days := int(math.Floor(t.Sub(now).Hours() / 24))
	e.DaysLeft = &days
	e.State = "ok"
	switch {
	case !t.After(now):
		e.State, e.Warning = "expired", "expired"
	case t.Sub(now) <= 3*24*time.Hour:
		e.State, e.Warning = "expiring", "critical_3d"
	case t.Sub(now) <= 14*24*time.Hour:
		e.State, e.Warning = "expiring", "warning_14d"
	}
	if e.Warning != "" {
		e.Next = append([]string(nil), next...)
	}
	return e
}
