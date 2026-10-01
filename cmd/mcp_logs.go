package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/output"
)

const (
	// mcpLogsDefaultLast and mcpLogsMaxLast bound how many lines one call
	// returns. A model asking for "the logs" wants the tail, not the file.
	mcpLogsDefaultLast = 100
	mcpLogsMaxLast     = 1000

	// mcpLogsDefaultSince is the default time window. It is a recent window
	// rather than "everything" on purpose: the daemon's log grows without
	// bound, and a tool whose default answer is the whole history spends its
	// caller's context on lines that predate the problem being diagnosed.
	mcpLogsDefaultSince = time.Hour
	mcpLogsMaxSince     = 168 * time.Hour

	// mcpLogsMaxBytes is the hard ceiling on returned log text, counted after
	// redaction. The bound is on bytes rather than lines because one access-log
	// line and one panic backtrace differ by orders of magnitude, and the line
	// count alone cannot stop a few enormous records from filling a response.
	mcpLogsMaxBytes = 256 * 1024

	mcpLogsTruncatedByBytes = "byte_limit"
	mcpLogsTruncatedByLines = "line_limit"
)

// MCPLogsResult is the logs tool payload.
type MCPLogsResult struct {
	Source string `json:"source"`
	File   string `json:"file"`
	Level  string `json:"level,omitempty"`
	// Since is the requested window as a duration; SinceAt is the resulting
	// absolute cutoff, so a caller never has to recompute it against a clock
	// that may not be the daemon's.
	Since   string    `json:"since"`
	SinceAt time.Time `json:"since_at"`
	Lines   []string  `json:"lines"`
	Count   int       `json:"count"`
	// Truncated says the answer is not the whole of what matched, and
	// TruncatedReason says which bound cut it. Both are always present rather
	// than omitted when false: a client must be able to distinguish "complete"
	// from "this field was not populated".
	Truncated       bool   `json:"truncated"`
	TruncatedReason string `json:"truncated_reason,omitempty"`
	// Matched is how many lines passed the level and time filters before any
	// bound was applied, so a caller can tell a quiet window from a truncated
	// one and decide whether to narrow the query.
	Matched int `json:"matched"`
	// Redacted is constant true. It is emitted so a consumer of these lines
	// knows they are not byte-identical to the file on disk, and does not treat
	// a "[redacted]" token as something the daemon logged.
	Redacted bool `json:"redacted"`
}

// mcpLogsEmailPattern matches a bare email address.
//
// Credential redaction is reused wholesale from doctor
// (sanitizeDoctorEvidenceValue); this pattern is the one thing doctor does not
// need and the log tool does. Doctor's evidence is assembled from local file
// and config state, while the daemon log records who called the control plane:
// mcp_controlplane.go logs the caller's Tailscale LoginName on every denial,
// and that is a tailnet member's email address. doctorUserInfoPattern only
// matches "user:password@", so a bare address would pass straight through it.
var mcpLogsEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9.\-]*[A-Za-z0-9])?\.[A-Za-z]{2,}`)

// mcpLogsTailscaleURLPattern matches any login.tailscale.com URL in a log line.
// Matching is the cheap half; whether the match is redacted is decided by
// redactTailscaleURL below.
//
// The trailing character class mirrors doctorURLPattern's, so the two rules
// agree on where a URL ends inside a quoted log field.
//
// The pattern consumes everything up to a delimiter instead of spelling out the
// URL's grammar, and accepts one or two slashes after the scheme. Both are
// reactions to the same failure: a rule shaped like "//host/..." did not match
// "https://login.tailscale.com:443/a/<token>" or "https:/login.tailscale.com/a/
// <token>" at all, so redactTailscaleURL never ran and the line was emitted with
// the capability in it. A later attempt that allowed an optional ":<digits>"
// port failed the same way on ":notaport" -- the match stopped at the host and
// the rest of the string, capability included, fell outside it.
//
// Whatever this pattern declines to consume is emitted verbatim, so the pattern
// is deliberately greedy and the judgement lives in the predicate, which fails
// closed on any host it cannot resolve to this one. doctorURLPattern carries
// the same "://" literal, so there is no second layer to catch what this one
// misses.
var mcpLogsTailscaleURLPattern = regexp.MustCompile(`(?i)https:/{1,2}login\.tailscale\.com[^\s"'<>)]*`)

// mcpLogsTailscaleCredentialPathPattern matches the login.tailscale.com paths
// that are bearer capabilities in themselves.
//
// These reach the log legitimately — internal/logging routes tsnet's
// interactive authorization URL through slog precisely so it is not lost — and
// they carry neither userinfo nor a query string, so doctor's URL rule leaves
// them intact. Whoever opens one enrolls a device or accepts an invitation, so
// the path prefix is the only thing that separates them from an ordinary
// admin-console link:
//
//	/a/<token>              interactive enrollment
//	/uinv/<token>           user invitation
//	/admin/invite/<token>   device invitation
//
// Everything else under login.tailscale.com — /admin/settings/keys,
// /admin/settings/oauth and the rest of the console — is a location, not a
// capability, and the operator reading these logs is being told where to go.
var mcpLogsTailscaleCredentialPathPattern = regexp.MustCompile(`(?i)^/(?:a|uinv|admin/invite)/.`)

// redactTailscaleURL decides one login.tailscale.com match.
//
// The predicate is deliberately the same shape as doctor's: userinfo, a query
// string, or an embedded credential token means the URL carries a secret. The
// bearer paths above are the one thing doctor has no reason to know about, so
// they are added here rather than widened into doctor's rule.
//
// This function does not rely on sanitizeDoctorEvidenceValue having run first.
// It would be correct to: sanitizeLogLine runs doctor's pass before this one,
// and that pass already replaces any URL with userinfo, a query string or a
// tskey token. But a redaction rule whose safety depends on another rule
// running first is one refactor away from being wrong, and the failure would be
// silent — a credential in the output, with every test still green.
// hostWithoutPort strips a trailing :port by asking net.SplitHostPort. An input
// SplitHostPort refuses -- a bracketless IPv6 such as "::1" ("too many colons")
// -- comes back unchanged, which then fails the exact-host comparison and
// redacts. Anyone replacing this helper with hand-rolled string splitting must
// preserve that: returning a "cleaned" host for inputs SplitHostPort rejects
// turns the comparison fail-open.
//
// It does NOT carry every malformed-port case. "login.tailscale.com:443." is
// accepted by SplitHostPort (port "443.", err nil) and yields the matching
// host, so the comparison passes; that shape is refused one layer earlier, by
// url.Parse rejecting `invalid port ":443." after host`. Do not read this
// helper as the fail-closed gate for ports -- it is only the gate for hosts
// that reach it.
//
// It strips a trailing :port, leaving bare hosts and IPv6
// literals alone.
func hostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func redactTailscaleURL(raw string) string {
	if strings.ContainsAny(raw, "?@") || doctorCredentialTokenPattern.MatchString(raw) {
		return doctorRedactedURL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		// Unparseable and login.tailscale.com-shaped: redact. An address this
		// rule cannot reason about is not one to hand to a model verbatim.
		return doctorRedactedURL
	}
	// The outer pattern now matches shapes url.Parse does not read as a host:
	// "https:/login.tailscale.com/a/<token>" parses with an empty Host and the
	// whole string in Path, which would then miss the bearer-path rule. Anything
	// whose host is not exactly this one, port aside, is a shape this predicate
	// cannot reason about, so it is redacted rather than reasoned about.
	if !strings.EqualFold(hostWithoutPort(parsed.Host), "login.tailscale.com") {
		return doctorRedactedURL
	}
	// Fragment sits beside userinfo and query for the same reason, and it is the
	// one this rule originally missed: the outer pattern's token class does not
	// exclude "#", so the whole fragment is inside the string this predicate is
	// handed, while path.Clean(parsed.Path) never sees it. A bearer shape moved
	// behind the "#" therefore passed the path rule by not being a path at all,
	// and the line was emitted verbatim. Same family as the ":443" and
	// single-slash shapes: the predicate reasons about scheme, host, userinfo,
	// query and path, and a capability carried outside those five is redacted
	// rather than reasoned about.
	//
	// That statement is about this predicate only, and is not a claim that the
	// rule as a whole is exhaustive: whether a capability reaches the predicate
	// at all is decided earlier, by how much of the line the outer pattern
	// matched. The pattern's token class stops at whitespace, quotes, angle
	// brackets and ")", so a capability separated from the host by one of those
	// never enters this function -- see AGENTS.md "Known gaps" for that half.
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return doctorRedactedURL
	}
	// Match on the cleaned path, not the raw one. url.Parse does not normalize
	// "//a/<token>" or "/x/../a/<token>", so a prefix rule applied to the raw
	// path lets a capability URL through while looking like it matched -- the
	// fail-open direction, and silent. path.Clean collapses both forms before
	// the rule sees them; it also makes "/X/../admin/invite/<token>" resolve to
	// the invite path it actually is rather than being caught by accident.
	if mcpLogsTailscaleCredentialPathPattern.MatchString(path.Clean(parsed.Path)) {
		return doctorRedactedURL
	}
	return raw
}

// sanitizeLogLine redacts one log line.
//
// Layering, outermost first: doctor's own evidence sanitizer (auth keys,
// credential-bearing URLs, userinfo), then the two classes above that doctor
// has no reason to carry — the login.tailscale.com bearer paths, which are
// redacted only when the URL actually carries a capability, and bare email
// addresses. Running doctor's pass first means the userinfo rule sees
// "user:password@host" before the email rule can consume part of it.
func sanitizeLogLine(line string) string {
	line = sanitizeDoctorEvidenceValue(line)
	line = mcpLogsTailscaleURLPattern.ReplaceAllStringFunc(line, redactTailscaleURL)
	return mcpLogsEmailPattern.ReplaceAllString(line, doctorRedactedEvidenceValue)
}

// mcpLogsArguments is the wire shape of the logs tool's arguments.
type mcpLogsArguments struct {
	Source string `json:"source,omitempty"`
	Last   *int   `json:"last,omitempty"`
	Level  string `json:"level,omitempty"`
	Since  string `json:"since,omitempty"`
}

// mcpLogsQuery is the validated form of those arguments.
type mcpLogsQuery struct {
	Source string
	Last   int
	Level  string
	Since  time.Duration
}

// resolveMCPLogsQuery validates the tool arguments. Every refusal is a usage
// error carrying the accepted values, because the model that called it can act
// on that and cannot act on a generic failure.
func resolveMCPLogsQuery(args mcpLogsArguments) (mcpLogsQuery, error) {
	query := mcpLogsQuery{Source: "err", Last: mcpLogsDefaultLast, Since: mcpLogsDefaultSince}

	if source := strings.TrimSpace(args.Source); source != "" {
		if _, ok := logSources[source]; !ok {
			return mcpLogsQuery{}, output.ErrUsage(fmt.Sprintf("invalid source %q (must be \"err\" or \"out\")", args.Source))
		}
		query.Source = source
	}
	if args.Last != nil {
		if *args.Last < 1 || *args.Last > mcpLogsMaxLast {
			return mcpLogsQuery{}, output.ErrUsage(fmt.Sprintf("invalid last %d (must be between 1 and %d)", *args.Last, mcpLogsMaxLast))
		}
		query.Last = *args.Last
	}
	if level := strings.TrimSpace(args.Level); level != "" {
		if !validLogLevel(level) {
			return mcpLogsQuery{}, output.ErrUsage(fmt.Sprintf("invalid level %q (must be debug, info, warn, or error)", args.Level))
		}
		query.Level = level
	}
	if since := strings.TrimSpace(args.Since); since != "" {
		parsed, err := parseDuration(since)
		if err != nil {
			return mcpLogsQuery{}, output.ErrUsage(fmt.Sprintf("invalid since %q: %v", args.Since, err))
		}
		if parsed <= 0 || parsed > mcpLogsMaxSince {
			return mcpLogsQuery{}, output.ErrUsage(fmt.Sprintf("invalid since %q (must be positive and at most %s)", args.Since, mcpLogsMaxSince))
		}
		query.Since = parsed
	}
	return query, nil
}

// mcpLogsNowFn is the clock the time window is measured against.
var mcpLogsNowFn = time.Now

// collectMCPLogs answers one logs tool call. It opens the log file read-only
// and writes nothing.
func collectMCPLogs(logDir string, args mcpLogsArguments) (MCPLogsResult, error) {
	query, err := resolveMCPLogsQuery(args)
	if err != nil {
		return MCPLogsResult{}, err
	}
	path, err := resolveLogFilePath(logDir, query.Source)
	if err != nil {
		return MCPLogsResult{}, err
	}

	cutoff := mcpLogsNowFn().Add(-query.Since)
	keep := newLogWindowFilter(cutoff)
	lines, matched, err := tailFileFiltered(path, query.Last, query.Level, keep)
	if err != nil {
		return MCPLogsResult{}, err
	}

	result := MCPLogsResult{
		Source:   query.Source,
		File:     path,
		Level:    query.Level,
		Since:    query.Since.String(),
		SinceAt:  cutoff.UTC(),
		Matched:  matched,
		Redacted: true,
	}
	if matched > len(lines) {
		result.Truncated = true
		result.TruncatedReason = mcpLogsTruncatedByLines
	}

	// Redact first, then measure. Measuring the raw line and emitting the
	// redacted one would let the byte ceiling drift from what is actually
	// returned, in either direction.
	redacted := make([]string, 0, len(lines))
	for _, line := range lines {
		redacted = append(redacted, sanitizeLogLine(line))
	}
	kept, droppedByBytes := boundLogBytes(redacted, mcpLogsMaxBytes)
	if droppedByBytes > 0 {
		result.Truncated = true
		// Bytes win the reason: they are the tighter bound whenever both fired,
		// and they are the one the caller can do something about by narrowing
		// level or since.
		result.TruncatedReason = mcpLogsTruncatedByBytes
	}
	// Always an array, never null, so a client's list rendering has one shape.
	if kept == nil {
		kept = []string{}
	}
	result.Lines = kept
	result.Count = len(kept)
	return result, nil
}

// boundLogBytes keeps the newest lines that fit in maxBytes and reports how
// many older ones were dropped. Dropping from the front is deliberate: when a
// log answer must be cut, the recent end is the part that explains the problem
// being diagnosed.
func boundLogBytes(lines []string, maxBytes int) ([]string, int) {
	total := 0
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		// +1 accounts for the newline a consumer re-joins these lines with, so
		// the ceiling bounds rendered text rather than raw field bytes.
		size := len(lines[i]) + 1
		if total+size > maxBytes {
			break
		}
		total += size
		start = i
	}
	return lines[start:], start
}

// newLogWindowFilter builds the per-line time-window predicate.
//
// Two behaviours are load-bearing and neither is obvious:
//
//   - A line with no parseable timestamp inherits the previous line's. slog
//     writes one record per line, but a record's own text can contain newlines
//     (a wrapped error, a stack), and those continuation lines belong to the
//     record above them.
//   - A line with no timestamp and no predecessor is kept. Dropping it would
//     make the tool return nothing at all for a file that is not slog-formatted
//     — an empty answer that looks exactly like a quiet daemon. The window is a
//     convenience for narrowing output, not a security boundary, so failing
//     toward showing data is the right direction.
func newLogWindowFilter(cutoff time.Time) func(string) bool {
	var last time.Time
	var haveLast bool
	return func(line string) bool {
		if ts, ok := extractLogTimestamp(line); ok {
			last = ts
			haveLast = true
		}
		if !haveLast {
			return true
		}
		return !last.Before(cutoff)
	}
}

// extractLogTimestamp reads the producer's own time field out of one line,
// accepting both slog handler formats for the same reason matchLevel does.
func extractLogTimestamp(line string) (time.Time, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(trimmed), &fields); err == nil {
			if raw, ok := fields["time"].(string); ok {
				return parseLogTimestamp(raw)
			}
		}
	}
	raw, ok := extractTextLogField(line, "time")
	if !ok {
		return time.Time{}, false
	}
	return parseLogTimestamp(raw)
}

func parseLogTimestamp(raw string) (time.Time, bool) {
	raw = strings.Trim(strings.TrimSpace(raw), `"`)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05"} {
		if ts, err := time.Parse(layout, raw); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

// extractTextLogField is extractTextLogLevel generalized to any key. It keeps
// the same quote-aware tokenizer so a key=value pair inside a quoted message
// cannot be mistaken for a top-level field.
func extractTextLogField(line, key string) (string, bool) {
	prefix := key + "="
	inQuote := false
	escaped := false
	tokenStart := 0
	for i := 0; i <= len(line); i++ {
		end := i == len(line)
		if !end {
			ch := line[i]
			if inQuote && escaped {
				escaped = false
			} else if inQuote && ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inQuote = !inQuote
			}
			if inQuote || ch != ' ' && ch != '\t' {
				continue
			}
		}
		if tokenStart < i {
			token := line[tokenStart:i]
			if strings.HasPrefix(token, prefix) {
				return strings.Trim(strings.TrimPrefix(token, prefix), `"`), true
			}
		}
		tokenStart = i + 1
	}
	return "", false
}
