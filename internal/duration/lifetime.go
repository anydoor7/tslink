package duration

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

const Examples = "90m, 36h, 3d, 1w, 1d12h, until 2030-06-01, until 2030-06-01T18:00, until 2030-06-01T18:00:00Z, never"
const Suggestions = "1h, 8h, 24h, 3d, 7d"
const DefaultPublicMax = 7 * 24 * time.Hour

// Lifetime has exactly one representation: a UTC deadline, or Never.
type Lifetime struct {
	Deadline *time.Time
	Never    bool
}

var lifetimeComponent = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(w|d|h|ms|us|ns|m|s)`)
var lifetimeRFC3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)
var lifetimeUnits = map[string]time.Duration{"w": 7 * 24 * time.Hour, "d": 24 * time.Hour, "h": time.Hour, "m": time.Minute, "s": time.Second, "ms": time.Millisecond, "us": time.Microsecond, "ns": time.Nanosecond}

func lifetimeError(reason string) error {
	return fmt.Errorf("%s; valid examples: %s; presets: %s", reason, Examples, Suggestions)
}

// ParseRelative accepts positive decimal components in strictly descending
// unit order. Exact rational arithmetic rejects overflow and sub-nanoseconds.
// Days and weeks are elapsed 24h and 168h, including across DST transitions.
func ParseRelative(value string) (time.Duration, error) {
	rest := strings.TrimSpace(value)
	total := new(big.Rat)
	previous := time.Duration(1<<63 - 1)
	for rest != "" {
		parts := lifetimeComponent.FindStringSubmatch(rest)
		if parts == nil || lifetimeUnits[parts[2]] >= previous {
			return 0, lifetimeError("invalid relative duration or repeated/out-of-order units")
		}
		previous = lifetimeUnits[parts[2]]
		number, _ := new(big.Rat).SetString(parts[1])
		total.Add(total, number.Mul(number, new(big.Rat).SetInt64(int64(previous))))
		rest = rest[len(parts[0]):]
	}
	if !total.IsInt() || !total.Num().IsInt64() || total.Sign() <= 0 {
		return 0, lifetimeError("duration must be positive, representable and precise to nanoseconds")
	}
	return time.Duration(total.Num().Int64()), nil
}

// ParseLifetime never reads wall time. loc nil means time.Local. Local gaps and
// folds are refused: the owner must supply an RFC3339 offset to choose an instant.
func ParseLifetime(value string, now time.Time, loc *time.Location) (Lifetime, error) {
	value = strings.TrimSpace(value)
	if value == "never" {
		return Lifetime{Never: true}, nil
	}
	var deadline time.Time
	if strings.HasPrefix(value, "until ") {
		text := strings.TrimSpace(strings.TrimPrefix(value, "until "))
		var err error
		deadline, err = time.Parse(time.RFC3339, text)
		// Go's parser also admits single-digit hours, decimal commas and an
		// offset of +24:00. Those spellings are outside this RFC3339 contract.
		if !lifetimeRFC3339.MatchString(text) {
			err = lifetimeError("invalid RFC3339 shape")
		}
		if err != nil {
			if loc == nil {
				loc = time.Local
			}
			layout := "2006-01-02T15:04"
			if len(text) == len("2006-01-02") {
				layout = "2006-01-02"
			}
			deadline, err = time.ParseInLocation(layout, text, loc)
			if err != nil || deadline.Format(layout) != text {
				return Lifetime{}, lifetimeError("invalid or nonexistent local time; use an RFC3339 offset at a DST transition")
			}
			// Collect offsets around the civil date, including half-hour folds.
			// Matching another offset to the same civil fields proves ambiguity.
			_, chosen := deadline.Zone()
			for h := -48; h <= 48; h++ {
				_, offset := deadline.Add(time.Duration(h) * time.Hour).Zone()
				if offset != chosen && deadline.Add(time.Duration(chosen-offset)*time.Second).In(loc).Format("2006-01-02T15:04:05") == deadline.Format("2006-01-02T15:04:05") {
					return Lifetime{}, lifetimeError("ambiguous local time; supply an RFC3339 offset")
				}
			}
		}
	} else {
		d, err := ParseRelative(value)
		if err != nil {
			return Lifetime{}, err
		}
		deadline = now.Add(d)
	}
	deadline = deadline.UTC()
	if !deadline.After(now) {
		return Lifetime{}, lifetimeError("until must be in the future")
	}
	return Lifetime{Deadline: &deadline}, nil
}

type Audience string

const (
	TailnetMember Audience = "tailnet_member"
	Guest         Audience = "guest"
	Public        Audience = "public"
)

// Policy is shared by people, Funnel, and future guest/approval callers.
// A zero PublicMax selects the default. Policy validation never uses wall time.
type Policy struct{ PublicMax time.Duration }

func (p Policy) Validate() error {
	if p.PublicMax != 0 && p.PublicMax < time.Hour {
		return lifetimeError("public/guest maximum must be at least 1h")
	}
	return nil
}

func (p Policy) Check(l Lifetime, audience Audience, acknowledgeNever bool, now time.Time) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if audience != TailnetMember && audience != Guest && audience != Public {
		return lifetimeError("unknown duration audience")
	}
	if l.Never {
		if l.Deadline != nil {
			return lifetimeError("never cannot have a deadline")
		}
		if audience != TailnetMember {
			return lifetimeError("never is allowed only for tailnet-member grants")
		}
		if !acknowledgeNever {
			return lifetimeError("never requires explicit --ack-never (MCP ack_never)")
		}
		return nil
	}
	if l.Deadline == nil || l.Deadline.Sub(now) < time.Hour {
		return lifetimeError("share/access duration must be at least 1h")
	}
	max := p.PublicMax
	if max == 0 {
		max = DefaultPublicMax
	}
	if audience != TailnetMember && l.Deadline.Sub(now) > max {
		return lifetimeError(fmt.Sprintf("public/guest duration exceeds maximum %s", max))
	}
	return nil
}

func (p Policy) Resolve(value string, audience Audience, acknowledgeNever bool, now time.Time, loc *time.Location) (Lifetime, error) {
	l, err := ParseLifetime(value, now, loc)
	if err == nil {
		err = p.Check(l, audience, acknowledgeNever, now)
	}
	return l, err
}
