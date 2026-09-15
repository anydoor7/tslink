package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestLog writes a log fixture and returns the directory holding it.
func writeTestLog(t *testing.T, name string, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write log fixture: %v", err)
	}
	return dir
}

// freezeLogsClock pins the tool's clock so the time window is deterministic.
func freezeLogsClock(t *testing.T, now time.Time) {
	t.Helper()
	old := mcpLogsNowFn
	mcpLogsNowFn = func() time.Time { return now }
	t.Cleanup(func() { mcpLogsNowFn = old })
}

func logLine(ts time.Time, level, msg string) string {
	return fmt.Sprintf("time=%s level=%s msg=%s", ts.Format(time.RFC3339Nano), level, msg)
}

// TestMCPLogsRedactsSecrets is a reverse assertion and ships with its control.
//
// The control is the first half of each case: the raw fixture line is checked
// to contain the sentinel. Without it, "the sentinel is absent from the result"
// would pass just as well if the fixture never carried it, if the level filter
// had excluded the line, or if the file had not been found — none of which is
// the property under test.
func TestMCPLogsRedactsSecrets(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		sentinel string
		raw      string
		// keep is text that must survive, so the redaction is shown to be
		// targeted rather than a blanket erasure that would trivially pass.
		keep string
	}{
		{
			name:     "tailscale auth key",
			sentinel: "tskey-auth-LOGSSENTINELAAAA",
			raw:      "auth key tskey-auth-LOGSSENTINELAAAA rejected",
			keep:     "rejected",
		},
		{
			name:     "url userinfo",
			sentinel: "hunter2",
			raw:      `dial backend target="https://admin:hunter2@backend.example.com/health"`,
			keep:     "dial backend",
		},
		{
			name:     "interactive enrollment url",
			sentinel: "https://login.tailscale.com/a/LOGSSENTINELBBBB",
			raw:      "msg=\"To authorize, visit: https://login.tailscale.com/a/LOGSSENTINELBBBB\" source=tsnet",
			keep:     "To authorize",
		},
		{
			name:     "caller login name",
			sentinel: "eve@example.com",
			raw:      "mcp control plane denied: caller not authorized login=eve@example.com",
			keep:     "caller not authorized",
		},
		{
			name:     "credential bearing query string",
			sentinel: "SUPERSECRETQUERY",
			raw:      `probe url=https://backend.example.com/cb?token=SUPERSECRETQUERY`,
			keep:     "probe",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := logLine(now.Add(-time.Minute), "WARN", "x") + " " + tc.raw
			if !strings.Contains(line, tc.sentinel) {
				t.Fatalf("control failed: fixture line %q does not contain %q, so the assertion below proves nothing", line, tc.sentinel)
			}
			dir := writeTestLog(t, "tslink.err.log", line)
			freezeLogsClock(t, now)

			result, err := collectMCPLogs(dir, mcpLogsArguments{})
			if err != nil {
				t.Fatalf("collectMCPLogs() error = %v", err)
			}
			if len(result.Lines) != 1 {
				t.Fatalf("lines = %#v, want the single fixture line back", result.Lines)
			}
			// Control for the control: the same line really did reach the
			// result, so an absent sentinel means redaction and not exclusion.
			if !strings.Contains(result.Lines[0], tc.keep) {
				t.Fatalf("returned line %q dropped %q; the line may have been excluded rather than redacted", result.Lines[0], tc.keep)
			}
			if strings.Contains(result.Lines[0], tc.sentinel) {
				t.Fatalf("returned line leaked %q: %q", tc.sentinel, result.Lines[0])
			}
			if !result.Redacted {
				t.Fatal("result does not declare itself redacted")
			}
		})
	}
}

// TestSanitizeLogLineIsNotBlanketErasure is the counterweight to the redaction
// tests: an ordinary line must come back byte-identical, or "nothing leaked"
// would be satisfied by a function that returned the empty string.
func TestSanitizeLogLineIsNotBlanketErasure(t *testing.T) {
	line := `time=2026-09-14T12:00:00Z level=INFO msg=access service=demo method=GET path=/health status=200 remote_addr=100.64.0.9:1234`
	if got := sanitizeLogLine(line); got != line {
		t.Fatalf("sanitizeLogLine() rewrote an ordinary line:\n got %q\nwant %q", got, line)
	}
}

// TestMCPLogsFiltersByLevel pins the level threshold semantics the CLI already
// has, through the tool's own entry point.
func TestMCPLogsFiltersByLevel(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.err.log",
		logLine(now.Add(-5*time.Minute), "DEBUG", "debug-line"),
		logLine(now.Add(-4*time.Minute), "INFO", "info-line"),
		logLine(now.Add(-3*time.Minute), "WARN", "warn-line"),
		logLine(now.Add(-2*time.Minute), "ERROR", "error-line"),
	)
	freezeLogsClock(t, now)

	cases := []struct {
		level string
		want  []string
	}{
		{"", []string{"debug-line", "info-line", "warn-line", "error-line"}},
		{"debug", []string{"debug-line", "info-line", "warn-line", "error-line"}},
		{"info", []string{"info-line", "warn-line", "error-line"}},
		{"warn", []string{"warn-line", "error-line"}},
		{"error", []string{"error-line"}},
	}
	for _, tc := range cases {
		t.Run("level="+tc.level, func(t *testing.T) {
			result, err := collectMCPLogs(dir, mcpLogsArguments{Level: tc.level})
			if err != nil {
				t.Fatalf("collectMCPLogs() error = %v", err)
			}
			if len(result.Lines) != len(tc.want) {
				t.Fatalf("lines = %#v, want %d entries %v", result.Lines, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(result.Lines[i], want) {
					t.Fatalf("line[%d] = %q, want it to contain %q", i, result.Lines[i], want)
				}
			}
			if result.Level != tc.level {
				t.Fatalf("result level = %q, want %q echoed back", result.Level, tc.level)
			}
		})
	}
}

// TestMCPLogsAppliesTheTimeWindow covers the recent-window default and the
// continuation-line rule, both of which decide what a caller sees.
func TestMCPLogsAppliesTheTimeWindow(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.err.log",
		logLine(now.Add(-48*time.Hour), "ERROR", "ancient"),
		logLine(now.Add(-90*time.Minute), "ERROR", "outside-default"),
		logLine(now.Add(-10*time.Minute), "ERROR", "inside-default"),
		"    continuation of the recent record",
	)
	freezeLogsClock(t, now)

	result, err := collectMCPLogs(dir, mcpLogsArguments{})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	joined := strings.Join(result.Lines, "\n")
	if strings.Contains(joined, "ancient") || strings.Contains(joined, "outside-default") {
		t.Fatalf("default window returned lines older than 1h: %q", joined)
	}
	if !strings.Contains(joined, "inside-default") {
		t.Fatalf("default window dropped a line inside it: %q", joined)
	}
	if !strings.Contains(joined, "continuation of the recent record") {
		t.Fatalf("an undated continuation line did not inherit its record's timestamp: %q", joined)
	}
	if result.Since != mcpLogsDefaultSince.String() {
		t.Fatalf("since = %q, want the default %s", result.Since, mcpLogsDefaultSince)
	}
	if !result.SinceAt.Equal(now.Add(-mcpLogsDefaultSince).UTC()) {
		t.Fatalf("since_at = %s, want %s", result.SinceAt, now.Add(-mcpLogsDefaultSince).UTC())
	}

	widened, err := collectMCPLogs(dir, mcpLogsArguments{Since: "72h"})
	if err != nil {
		t.Fatalf("collectMCPLogs(since=72h) error = %v", err)
	}
	if !strings.Contains(strings.Join(widened.Lines, "\n"), "ancient") {
		t.Fatalf("widened window still excluded the oldest line: %#v", widened.Lines)
	}
}

// TestMCPLogsKeepsUndatedFilesVisible pins the fail-toward-showing-data rule:
// a file with no parseable timestamps must not come back empty, because an
// empty answer is indistinguishable from a quiet daemon.
func TestMCPLogsKeepsUndatedFilesVisible(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.out.log", "plain stdout line one", "plain stdout line two")
	freezeLogsClock(t, now)

	result, err := collectMCPLogs(dir, mcpLogsArguments{Source: "out"})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	if len(result.Lines) != 2 {
		t.Fatalf("lines = %#v, want both undated lines", result.Lines)
	}
}

// TestMCPLogsTruncatesByBytes is the hard ceiling. It also pins which end
// survives: the newest lines, because those are the ones that explain the
// failure being diagnosed.
func TestMCPLogsTruncatesByBytes(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	filler := strings.Repeat("x", 4096)
	lines := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		lines = append(lines, logLine(now.Add(-time.Duration(200-i)*time.Second), "INFO", fmt.Sprintf("record-%03d %s", i, filler)))
	}
	dir := writeTestLog(t, "tslink.err.log", lines...)
	freezeLogsClock(t, now)

	result, err := collectMCPLogs(dir, mcpLogsArguments{Last: intPtr(200)})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	if !result.Truncated || result.TruncatedReason != mcpLogsTruncatedByBytes {
		t.Fatalf("truncated = %v reason = %q, want a byte_limit truncation", result.Truncated, result.TruncatedReason)
	}
	if result.Matched != 200 {
		t.Fatalf("matched = %d, want 200 so the caller can see how much was cut", result.Matched)
	}
	if result.Count >= 200 {
		t.Fatalf("count = %d, want fewer than the 200 matched lines", result.Count)
	}
	size := 0
	for _, line := range result.Lines {
		size += len(line) + 1
	}
	if size > mcpLogsMaxBytes {
		t.Fatalf("returned %d bytes, want at most %d", size, mcpLogsMaxBytes)
	}
	if !strings.Contains(result.Lines[len(result.Lines)-1], "record-199") {
		t.Fatalf("last returned line = %q, want the newest record kept", result.Lines[len(result.Lines)-1])
	}
	if strings.Contains(result.Lines[0], "record-000") {
		t.Fatalf("first returned line = %q, want the oldest records dropped", result.Lines[0])
	}
}

// TestMCPLogsTruncatesByLineBound is the other bound, reported under its own
// reason so a caller can tell which one to relax.
func TestMCPLogsTruncatesByLineBound(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, logLine(now.Add(-time.Duration(20-i)*time.Second), "INFO", fmt.Sprintf("record-%02d", i)))
	}
	dir := writeTestLog(t, "tslink.err.log", lines...)
	freezeLogsClock(t, now)

	result, err := collectMCPLogs(dir, mcpLogsArguments{Last: intPtr(5)})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	if result.Count != 5 || result.Matched != 20 {
		t.Fatalf("count = %d matched = %d, want 5 and 20", result.Count, result.Matched)
	}
	if !result.Truncated || result.TruncatedReason != mcpLogsTruncatedByLines {
		t.Fatalf("truncated = %v reason = %q, want a line_limit truncation", result.Truncated, result.TruncatedReason)
	}
	if !strings.Contains(result.Lines[0], "record-15") {
		t.Fatalf("first line = %q, want the last 5 records", result.Lines[0])
	}
}

// TestMCPLogsReportsCompleteAnswersAsNotTruncated is the control for both
// truncation tests: an answer that fits reports truncated false, so the field
// is not simply always true.
func TestMCPLogsReportsCompleteAnswersAsNotTruncated(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.err.log",
		logLine(now.Add(-time.Minute), "INFO", "one"),
		logLine(now.Add(-time.Second), "INFO", "two"),
	)
	freezeLogsClock(t, now)

	result, err := collectMCPLogs(dir, mcpLogsArguments{})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	if result.Truncated || result.TruncatedReason != "" {
		t.Fatalf("truncated = %v reason = %q, want a complete answer", result.Truncated, result.TruncatedReason)
	}
	if result.Count != 2 || result.Matched != 2 {
		t.Fatalf("count = %d matched = %d, want 2 and 2", result.Count, result.Matched)
	}
}

// TestMCPLogsIsReadOnly proves the tool cannot change what it reads.
func TestMCPLogsIsReadOnly(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.err.log", logLine(now.Add(-time.Minute), "INFO", "only"))
	freezeLogsClock(t, now)
	path := filepath.Join(dir, "tslink.err.log")

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	beforeEntries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}

	if _, err := collectMCPLogs(dir, mcpLogsArguments{}); err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("log file changed:\nbefore %q\nafter  %q", before, after)
	}
	afterEntries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("re-read dir: %v", err)
	}
	if len(beforeEntries) != len(afterEntries) {
		t.Fatalf("log directory gained or lost entries: %d -> %d", len(beforeEntries), len(afterEntries))
	}
}

// TestMCPLogsMissingFileIsAnEmptyAnswer keeps a daemon that has never logged
// from looking like a failure.
func TestMCPLogsMissingFileIsAnEmptyAnswer(t *testing.T) {
	freezeLogsClock(t, time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC))
	result, err := collectMCPLogs(t.TempDir(), mcpLogsArguments{})
	if err != nil {
		t.Fatalf("collectMCPLogs() error = %v", err)
	}
	if result.Count != 0 || result.Truncated {
		t.Fatalf("result = %+v, want an empty, untruncated answer", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"lines":[]`) {
		t.Fatalf("payload = %s, want an empty lines array rather than null", encoded)
	}
}

func TestResolveMCPLogsQueryRejectsBadArguments(t *testing.T) {
	cases := []struct {
		name string
		args mcpLogsArguments
	}{
		{"unknown source", mcpLogsArguments{Source: "stdout"}},
		{"zero last", mcpLogsArguments{Last: intPtr(0)}},
		{"negative last", mcpLogsArguments{Last: intPtr(-1)}},
		{"last above the ceiling", mcpLogsArguments{Last: intPtr(mcpLogsMaxLast + 1)}},
		{"unknown level", mcpLogsArguments{Level: "critical"}},
		{"unparseable since", mcpLogsArguments{Since: "yesterday"}},
		{"zero since", mcpLogsArguments{Since: "0s"}},
		{"negative since", mcpLogsArguments{Since: "-1h"}},
		{"since above the ceiling", mcpLogsArguments{Since: "169h"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveMCPLogsQuery(tc.args); err == nil {
				t.Fatal("resolveMCPLogsQuery() = nil error, want a usage refusal")
			}
		})
	}

	// The control: the defaults every rejection above is measured against.
	query, err := resolveMCPLogsQuery(mcpLogsArguments{})
	if err != nil {
		t.Fatalf("resolveMCPLogsQuery(zero) error = %v", err)
	}
	if query.Source != "err" || query.Last != mcpLogsDefaultLast || query.Since != mcpLogsDefaultSince || query.Level != "" {
		t.Fatalf("defaults = %+v, want err/%d/%s/all levels", query, mcpLogsDefaultLast, mcpLogsDefaultSince)
	}
}

// TestMCPLogsToolIsReachableOverTheProtocol drives the tool through the same
// dispatch the transports use, so a tool registered but not wired fails here.
func TestMCPLogsToolIsReachableOverTheProtocol(t *testing.T) {
	result, err := callMCPTool(context.Background(), fakeMCPActions(), "logs", json.RawMessage(`{"last":10,"level":"warn","since":"15m","source":"err"}`))
	if err != nil {
		t.Fatalf("callMCPTool(logs) error = %v", err)
	}
	if result.IsError {
		t.Fatalf("logs tool returned an error result: %+v", result.StructuredContent)
	}
	structured, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content is %T, want a JSON object", result.StructuredContent)
	}
	if structured["redacted"] != true {
		t.Fatalf("structured content = %+v, want redacted true", structured)
	}

	// An argument the schema does not declare is refused rather than dropped,
	// matching every other tool on this surface.
	if _, err := callMCPTool(context.Background(), fakeMCPActions(), "logs", json.RawMessage(`{"tail":10}`)); err == nil {
		t.Fatal("callMCPTool(logs) accepted an undeclared argument")
	}
}

// TestTailFileFilteredMatchesTailFile pins the refactor: the CLI's own reader is
// now a wrapper, and it must answer exactly what it answered before.
func TestTailFileFilteredMatchesTailFile(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	dir := writeTestLog(t, "tslink.err.log",
		logLine(now.Add(-3*time.Minute), "INFO", "one"),
		logLine(now.Add(-2*time.Minute), "WARN", "two"),
		logLine(now.Add(-time.Minute), "ERROR", "three"),
	)
	path := filepath.Join(dir, "tslink.err.log")

	for _, last := range []int{0, 1, 2, 10} {
		for _, level := range []string{"", "info", "warn", "error"} {
			wrapped, err := tailFile(path, last, level)
			if err != nil {
				t.Fatalf("tailFile(%d,%q) error = %v", last, level, err)
			}
			direct, matched, err := tailFileFiltered(path, last, level, nil)
			if err != nil {
				t.Fatalf("tailFileFiltered(%d,%q) error = %v", last, level, err)
			}
			if strings.Join(wrapped, "\n") != strings.Join(direct, "\n") {
				t.Fatalf("tailFile(%d,%q) = %#v, tailFileFiltered = %#v", last, level, wrapped, direct)
			}
			if matched < len(direct) {
				t.Fatalf("matched = %d, want at least the %d returned lines", matched, len(direct))
			}
		}
	}

	// The predicate runs before the last-N bound, not after. Filtering
	// afterwards would return zero lines here instead of one.
	keepOnlyOldest, _, err := tailFileFiltered(path, 1, "", func(line string) bool { return strings.Contains(line, "msg=one") })
	if err != nil {
		t.Fatalf("tailFileFiltered with a predicate error = %v", err)
	}
	if len(keepOnlyOldest) != 1 || !strings.Contains(keepOnlyOldest[0], "msg=one") {
		t.Fatalf("predicate result = %#v, want the oldest line even under last=1", keepOnlyOldest)
	}
}
