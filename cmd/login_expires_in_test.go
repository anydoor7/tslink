package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// A wrong --expires-in parse silently shortens or lengthens a credential's life.
// The unit boundaries matter more than the error strings: "90d" must be 90 days,
// and a non-positive duration must be refused rather than rounded to "now".

func TestParseLoginExpiresInAcceptsDaysAndGoDurations(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"90d", 90 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"365d", 365 * 24 * time.Hour},
		{"  90d  ", 90 * 24 * time.Hour},
		{"720h", 720 * time.Hour},
		{"30m", 30 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"1s", time.Second},
		{"1.5h", 90 * time.Minute},
		// One duration grammar (B3-5): days take Go's fractions and
		// combine with other units, as hours do.
		{"1.5d", 36 * time.Hour},
		{"1d12h", 36 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := parseLoginExpiresIn(tc.raw)
			if err != nil {
				t.Fatalf("parseLoginExpiresIn(%q) error = %v", tc.raw, err)
			}
			if got != tc.want {
				t.Fatalf("parseLoginExpiresIn(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseLoginExpiresInRejectsNonPositiveAndMalformedValues(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", "empty duration"},
		{"whitespace only", "   ", "empty duration"},
		{"zero days", "0d", "must be positive"},
		{"negative days", "-5d", "must be positive"},
		{"day suffix without a number", "d", "invalid day count"},
		{"non numeric days", "abcd", "invalid day count"},
		{"zero duration", "0", "must be positive"},
		{"zero hours", "0h", "must be positive"},
		{"negative duration", "-1h", "must be positive"},
		{"uppercase day suffix", "90D", ""},
		{"missing unit", "90", ""},
		{"garbage", "soon", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLoginExpiresIn(tc.raw)
			if err == nil {
				t.Fatalf("parseLoginExpiresIn(%q) = %v, want an error", tc.raw, got)
			}
			if got != 0 {
				t.Fatalf("parseLoginExpiresIn(%q) = %v alongside an error, want 0", tc.raw, got)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseLoginExpiresIn(%q) error = %v, want it to mention %q", tc.raw, err, tc.want)
			}
		})
	}
}

func TestParseLoginExpiresInDaySuffixIsExactlyTwentyFourHours(t *testing.T) {
	// A "d" handled as anything but 24h (seconds, minutes, or Go's lack of a day
	// unit) would set a credential expiry that is wrong by orders of magnitude.
	oneDay, err := parseLoginExpiresIn("1d")
	if err != nil {
		t.Fatalf("parseLoginExpiresIn(1d) error = %v", err)
	}
	sameInHours, err := parseLoginExpiresIn("24h")
	if err != nil {
		t.Fatalf("parseLoginExpiresIn(24h) error = %v", err)
	}
	if oneDay != sameInHours {
		t.Fatalf("parseLoginExpiresIn(1d) = %v, want it to equal 24h = %v", oneDay, sameInHours)
	}
	ninety, err := parseLoginExpiresIn("90d")
	if err != nil {
		t.Fatalf("parseLoginExpiresIn(90d) error = %v", err)
	}
	if ninety != 90*oneDay {
		t.Fatalf("parseLoginExpiresIn(90d) = %v, want 90 * %v", ninety, oneDay)
	}
}

func TestResolveLoginExpiryUsesTheParsedExpiresInDuration(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	oldNow := loginNowFn
	t.Cleanup(func() { loginNowFn = oldNow })
	loginNowFn = func() time.Time { return now }

	cmd := &cobra.Command{}
	cmd.Flags().String("expires-in", "", "")
	cmd.Flags().String("expires-at", "", "")
	if err := cmd.Flags().Set("expires-in", "90d"); err != nil {
		t.Fatalf("Set(expires-in) error = %v", err)
	}

	expiry, source, err := resolveLoginExpiry(cmd)
	if err != nil {
		t.Fatalf("resolveLoginExpiry() error = %v", err)
	}
	if expiry == nil {
		t.Fatal("resolveLoginExpiry() expiry = nil, want the parsed --expires-in applied")
	}
	if want := now.Add(90 * 24 * time.Hour); !expiry.Equal(want) {
		t.Fatalf("resolveLoginExpiry() expiry = %v, want %v", expiry, want)
	}
	if source == "" {
		t.Fatal("resolveLoginExpiry() source = empty, want the user-supplied expiry source")
	}
}

func TestResolveLoginExpiryRejectsAnInvalidExpiresInAsUsage(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().String("expires-in", "", "")
	cmd.Flags().String("expires-at", "", "")
	if err := cmd.Flags().Set("expires-in", "0d"); err != nil {
		t.Fatalf("Set(expires-in) error = %v", err)
	}

	expiry, _, err := resolveLoginExpiry(cmd)
	if err == nil {
		t.Fatalf("resolveLoginExpiry() error = nil, want a usage refusal; expiry = %v", expiry)
	}
	if expiry != nil {
		t.Fatalf("resolveLoginExpiry() expiry = %v alongside an error, want nil", expiry)
	}
	if !strings.Contains(err.Error(), `invalid --expires-in "0d"`) {
		t.Fatalf("resolveLoginExpiry() error = %v, want the offending flag echoed", err)
	}
}
