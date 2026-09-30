package duration

import (
	"testing"
	"time"
)

func TestParseGrammarAndPrintedRoundTrip(t *testing.T) {
	for input, want := range map[string]time.Duration{"7d": 168 * time.Hour, "1d12h": 36 * time.Hour, "1.5d": 36 * time.Hour, "-1d": -24 * time.Hour, "+1d": 24 * time.Hour, " 20s ": 20 * time.Second, "300ms": 300 * time.Millisecond, "0": 0} {
		got, err := Parse(input)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", input, got, err, want)
		}
		if back, err := Parse(want.String()); err != nil || back != want {
			t.Errorf("round-trip %v = %v, %v", want, back, err)
		}
	}
	for _, input := range []string{"", "d", "7", "7x", "7dd", "1.2.3d", "never", "d7", "7d?", "999999999999d"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("accepted invalid duration %q", input)
		}
	}
}

func TestValueKeepsDurationTypeAndLastValidValue(t *testing.T) {
	value := NewValue(30 * time.Second)
	if value.Type() != "duration" || value.String() != "30s" {
		t.Fatalf("default = %s (%s)", value, value.Type())
	}
	if err := value.Set("1.5d"); err != nil || value.String() != "36h0m0s" {
		t.Fatalf("set days: %s, %v", value, err)
	}
	if err := value.Set("bad"); err == nil || value.String() != "36h0m0s" {
		t.Fatalf("invalid value changed flag: %s, %v", value, err)
	}
}
