package duration

import (
	"testing"
	"time"
)

func TestParseIsGoSyntaxPlusDays(t *testing.T) {
	for input, want := range map[string]time.Duration{
		"7d":       7 * Day,
		"168h":     7 * Day,
		"1d12h":    36 * time.Hour,
		"1.5d":     36 * time.Hour,
		"90d":      90 * Day,
		"15m":      15 * time.Minute,
		"300ms":    300 * time.Millisecond,
		" 20s ":    20 * time.Second,
		"-1d":      -Day,
		"0":        0,
		"2h45m30s": 2*time.Hour + 45*time.Minute + 30*time.Second,
	} {
		got, err := Parse(input)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "d", "7", "7x", "7dd", "1.2.3d", "never", "d7", "7d?"} {
		if got, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) = %v, want an error", input, got)
		}
	}
}

// TestParseReadsWhatTSLinkPrints: TSLink prints durations (funnel_remaining,
// log windows) with time.Duration.String, so every such value parses back.
func TestParseReadsWhatTSLinkPrints(t *testing.T) {
	for _, d := range []time.Duration{0, time.Second, 167*time.Hour + 59*time.Minute + 59*time.Second, 7 * Day, 1500 * time.Millisecond} {
		got, err := Parse(d.String())
		if err != nil || got != d {
			t.Errorf("Parse(%q) = %v, %v; want %v", d.String(), got, err, d)
		}
	}
}
