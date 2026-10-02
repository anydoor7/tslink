package duration

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestLifetimeGrammar(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"90m", 90 * time.Minute}, {"36h", 36 * time.Hour}, {"3d", 72 * time.Hour}, {"1w", 168 * time.Hour},
		{"1w2d3h4m5s6ms7us8ns", 9*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second + 6*time.Millisecond + 7*time.Microsecond + 8*time.Nanosecond},
		{"1d12h", 36 * time.Hour}, {"1.5h", 90 * time.Minute}, {" 24h ", 24 * time.Hour}, {"1h0m", time.Hour}, {"0.000000001s", time.Nanosecond},
	} {
		t.Run(tc.value, func(t *testing.T) {
			l, err := ParseLifetime(tc.value, now, nil)
			if err != nil || l.Never || l.Deadline == nil || !l.Deadline.Equal(now.Add(tc.want)) || l.Deadline.Location() != time.UTC {
				t.Fatalf("%+v, %v", l, err)
			}
		})
	}
	l, err := ParseLifetime("never", now, nil)
	if err != nil || !l.Never || l.Deadline != nil {
		t.Fatalf("%+v %v", l, err)
	}
	for _, value := range []string{"", "0h", "-1h", "+1h", "1h1h", "1m1h", "1h 1m", "1D", "garbage", "999999999999999999999999w", "0.0000000001s", "1e2h", "1.h", "neverx"} {
		t.Run("invalid "+value, func(t *testing.T) {
			_, err := ParseLifetime(value, now, nil)
			if err == nil || !strings.Contains(err.Error(), "valid examples:") {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestLifetimeAbsoluteDSTAndLeapDays(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ value, utc string }{
		{"until 2028-02-29", "2028-02-29T05:00:00Z"},
		{"until 2028-02-29T18:00", "2028-02-29T23:00:00Z"},
		{"until 2028-03-12T03:00", "2028-03-12T07:00:00Z"},
		{"until 2028-03-12", "2028-03-12T05:00:00Z"},
		{"until 2028-11-05T01:30:00-04:00", "2028-11-05T05:30:00Z"},
		{"until 2028-11-05T01:30:00-05:00", "2028-11-05T06:30:00Z"},
		{"until 2028-02-29T18:00:00Z", "2028-02-29T18:00:00Z"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			l, err := ParseLifetime(tc.value, now, ny)
			if err != nil || l.Deadline.Format(time.RFC3339) != tc.utc {
				t.Fatalf("%+v %v; want %s", l, err, tc.utc)
			}
		})
	}
	for _, tc := range []struct{ value, reason string }{
		{"until 2028-03-12T02:30", "nonexistent"}, {"until 2028-11-05T01:30", "ambiguous"},
		{"until 2029-02-29", "invalid"}, {"until 2028-13-01", "invalid"}, {"until 2028-02-29T18:00:00", "invalid"},
		{"until nonsense", "invalid"}, {"until 2027-12-31T00:00:00Z", "future"}, {"until 2028-01-01T00:00:00Z", "future"},
		{"until 2028-02-29T1:00:00Z", "invalid"}, {"until 2028-02-29T18:00:00,1Z", "invalid"}, {"until 2028-02-29T18:00:00+24:00", "invalid"}, {"until 2028-02-29T18:00:00+00:60", "invalid"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			_, err := ParseLifetime(tc.value, now, ny)
			if err == nil || !strings.Contains(err.Error(), tc.reason) || !strings.Contains(err.Error(), "valid examples:") {
				t.Fatalf("%v", err)
			}
		})
	}
	lh, err := time.LoadLocation("Australia/Lord_Howe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseLifetime("until 2028-04-02T01:45", now, lh); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("half-hour fold: %v", err)
	}
	start := time.Date(2028, 3, 11, 12, 0, 0, 0, ny)
	l, err := ParseLifetime("1d", start, ny)
	if err != nil || l.Deadline.Sub(start) != 24*time.Hour || l.Deadline.In(ny).Hour() != 13 {
		t.Fatalf("elapsed day across DST: %+v %v", l, err)
	}
	// Exercise the default location independently from the TZ subprocess below.
	if _, err := ParseLifetime("until 2028-02-29", now, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLifetimePolicy(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value    string
		audience Audience
		ack      bool
		max      time.Duration
		reason   string
	}{
		{"1h", Public, false, 0, ""}, {"7d", Guest, false, 0, ""}, {"7d1s", Public, false, 0, "exceeds"},
		{"59m59s", TailnetMember, false, 0, "at least 1h"}, {"never", Public, true, 0, "only"}, {"never", Guest, true, 0, "only"},
		{"never", TailnetMember, false, 0, "ack-never"}, {"never", TailnetMember, true, 0, ""},
		{"36h", Public, false, 48 * time.Hour, ""}, {"3d", Guest, false, 48 * time.Hour, "exceeds"},
		{"7d1s", TailnetMember, false, 0, ""}, {"1h", Audience("invalid"), false, 0, "unknown"}, {"1h", Public, false, time.Minute, "maximum"},
		{"until 2029-12-31", Public, false, 0, "future"}, {"1h", Public, false, -time.Hour, "maximum"},
	} {
		t.Run(string(tc.audience)+tc.value+tc.reason, func(t *testing.T) {
			l, err := (Policy{PublicMax: tc.max}).Resolve(tc.value, tc.audience, tc.ack, now, time.UTC)
			if tc.reason == "" {
				if err != nil || (!l.Never && l.Deadline == nil) {
					t.Fatalf("%+v %v", l, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("%v want %s", err, tc.reason)
			}
		})
	}
	target := now.Add(time.Hour)
	for _, l := range []Lifetime{{}, {Never: true, Deadline: &target}} {
		if err := (Policy{}).Check(l, TailnetMember, true, now); err == nil {
			t.Fatal("invalid lifetime admitted")
		}
	}
}

func TestLifetimeLocalTZSubprocess(t *testing.T) {
	if os.Getenv("TSLINK_DURATION_TZ_TEST") == "1" {
		// Go on Windows reads the OS timezone instead of TZ. In this isolated
		// child only, adapt TZ to the local-location seam. Unix exercises the
		// real TZ initialization without an adapter; production keeps OS local.
		if runtime.GOOS == "windows" {
			loc, err := time.LoadLocation(os.Getenv("TZ"))
			if err != nil {
				t.Fatal(err)
			}
			time.Local = loc
		}
		now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
		l, err := ParseLifetime("until 2030-01-02T12:00", now, nil)
		if err != nil || l.Deadline.Format(time.RFC3339) != "2030-01-02T03:00:00Z" {
			t.Fatalf("TZ not applied: %+v %v", l, err)
		}
		return
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(binary, "-test.run=^TestLifetimeLocalTZSubprocess$", "-test.v")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "TZ=") && !strings.HasPrefix(env, "TSLINK_DURATION_TZ_TEST=") {
			c.Env = append(c.Env, env)
		}
	}
	c.Env = append(c.Env, "TZ=Asia/Tokyo", "TSLINK_DURATION_TZ_TEST=1")
	out, err := c.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestLifetimeLocalTZSubprocess") {
		t.Fatalf("%s %v", out, err)
	}
}
