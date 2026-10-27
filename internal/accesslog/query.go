package accesslog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/mcpaudit"
)

type Filter struct {
	// AllowedApps is an authority filter applied before counts and truncation.
	// nil is unrestricted; an empty non-nil list authorizes no records.
	AllowedApps []string   `json:"-"`
	App         string     `json:"app,omitempty"`
	Who         string     `json:"who,omitempty"`
	Since       *time.Time `json:"since,omitempty"`
	Until       *time.Time `json:"until,omitempty"`
	Decision    string     `json:"decision,omitempty"`
	Limit       int        `json:"limit,omitempty"`
}

func (f Filter) Validate() error {
	if f.Limit < 0 || f.Limit > 10000 {
		return fmt.Errorf("limit must be 1..10000 (0 uses 100)")
	}
	if f.Decision != "" && f.Decision != "allowed" && f.Decision != "denied" {
		return fmt.Errorf("decision must be allowed or denied")
	}
	if f.Since != nil && f.Until != nil && f.Since.After(*f.Until) {
		return fmt.Errorf("since must not follow until")
	}
	return nil
}

type Count struct {
	Key      string     `json:"key"`
	Count    int        `json:"count"`
	Allowed  int        `json:"allowed"`
	Denied   int        `json:"denied"`
	LastSeen *time.Time `json:"last_seen"`
}
type Summary struct {
	Traffic TrafficSummary `json:"traffic"`
	Reasons []Count        `json:"reasons,omitempty"`
	Count   int            `json:"count"`
	Allowed int            `json:"allowed"`
	Denied  int            `json:"denied"`
	People  []Count        `json:"people"`
	Apps    []Count        `json:"apps"`
}

// TrafficSummary counts retained, matching traffic independently of audit
// receipts and the returned-event limit. It does not consult current routes.
type TrafficSummary struct {
	HTTP       HTTPTraffic         `json:"http"`
	PeopleApps []SubjectAppTraffic `json:"people_apps"`
	Apps       []AppTraffic        `json:"apps"`
	TCP        TCPTraffic          `json:"tcp"`
}

type HTTPTraffic struct {
	Requests      int        `json:"requests"`
	Allowed       int        `json:"allowed"`
	Denied        int        `json:"denied"`
	BytesIn       int64      `json:"bytes_in"`
	BytesOut      int64      `json:"bytes_out"`
	LastRequestAt *time.Time `json:"last_request_at"`
	LastAllowedAt *time.Time `json:"last_allowed_at"`
}

type SubjectAppTraffic struct {
	SubjectKind string `json:"subject_kind"` // human, tagged, public, unknown
	Login       string `json:"login,omitempty"`
	Subject     string `json:"subject,omitempty"` // non-human bucket, never an inferred login
	App         string `json:"app"`
	HTTPTraffic
}

type AppTraffic struct {
	App string `json:"app"`
	HTTPTraffic
}

type TCPTraffic struct {
	Connections int   `json:"connections"` // opens, excluding gateway gate drops
	Allowed     int   `json:"allowed"`
	Denied      int   `json:"denied"`
	Closed      int   `json:"closed"`
	BytesIn     int64 `json:"bytes_in"` // close events only
	BytesOut    int64 `json:"bytes_out"`
	GateDrops   int   `json:"gate_drops"` // recorded, rate-limited diagnostics
}

type subjectAppKey struct {
	kind, login, app string
}

func trafficKey(e Event) subjectAppKey {
	k := subjectAppKey{kind: "unknown", app: e.App}
	login := asciiLogin(e.Identity.Login)
	switch {
	case len(e.Identity.Tags) > 0:
		k.kind = "tagged"
	case login == "public":
		k.kind = "public"
	case login != "" && login != "[redacted]":
		k.kind, k.login = "human", login
	}
	return k
}

func (h *HTTPTraffic) add(e Event) {
	h.Requests++
	h.BytesIn += e.BytesIn
	h.BytesOut += e.BytesOut
	if h.LastRequestAt == nil || e.Time.After(*h.LastRequestAt) {
		at := e.Time
		h.LastRequestAt = &at
	}
	if e.Decision == "denied" {
		h.Denied++
	} else {
		h.Allowed++
		if h.LastAllowedAt == nil || e.Time.After(*h.LastAllowedAt) {
			at := e.Time
			h.LastAllowedAt = &at
		}
	}
}

func (t *TrafficSummary) add(e Event, people map[subjectAppKey]*SubjectAppTraffic, apps map[string]*AppTraffic) {
	switch e.Kind {
	case "http":
		t.HTTP.add(e)
		key := trafficKey(e)
		if people[key] == nil {
			p := &SubjectAppTraffic{SubjectKind: key.kind, Login: key.login, App: key.app}
			if key.kind != "human" {
				p.Subject = key.kind
			}
			people[key] = p
		}
		people[key].HTTPTraffic.add(e)
		if apps[e.App] == nil {
			apps[e.App] = &AppTraffic{App: e.App}
		}
		apps[e.App].HTTPTraffic.add(e)
	case "tcp_open":
		if e.Reason == "conn_gate_drop" {
			t.TCP.GateDrops++
			return
		}
		t.TCP.Connections++
		if e.Decision == "denied" {
			t.TCP.Denied++
		} else {
			t.TCP.Allowed++
		}
	case "tcp_close":
		t.TCP.Closed++
		t.TCP.BytesIn += e.BytesIn
		t.TCP.BytesOut += e.BytesOut
	}
}

type Result struct {
	Events    []Event `json:"events"`
	Summary   Summary `json:"summary"`
	Truncated bool    `json:"truncated"`
}

func (f Filter) matches(e Event) bool {
	if f.AllowedApps != nil {
		apps := []string{e.App}
		if e.MCP != nil {
			apps = e.MCP.Apps
		}
		if len(apps) == 0 {
			return false
		}
		for _, app := range apps {
			if !slices.Contains(f.AllowedApps, app) {
				return false
			}
		}
	}
	if f.App != "" && e.App != f.App && (e.MCP == nil || !slices.Contains(e.MCP.Apps, f.App)) {
		return false
	}
	if f.Decision != "" && e.Decision != f.Decision {
		return false
	}
	if f.Since != nil && e.Time.Before(*f.Since) {
		return false
	}
	if f.Until != nil && e.Time.After(*f.Until) {
		return false
	}
	if f.Who != "" {
		match := asciiLogin(e.Identity.Login) == asciiLogin(f.Who) || e.Identity.Node == f.Who
		if e.MCP != nil {
			principalMatch := e.MCP.Principal == f.Who
			if !strings.HasPrefix(e.MCP.Principal, "tag:") {
				principalMatch = asciiLogin(e.MCP.Principal) == asciiLogin(f.Who)
			}
			match = match || principalMatch
			if e.MCP.Identity != nil {
				match = match || asciiLogin(e.MCP.Identity.Login) == asciiLogin(f.Who) || e.MCP.Identity.Node == f.Who
			}
		}
		for _, tag := range e.Identity.Tags {
			match = match || tag == f.Who
		}
		if !match {
			return false
		}
	}
	return true
}
func addCount(m map[string]*Count, key string, e Event) {
	if m[key] == nil {
		m[key] = &Count{Key: key}
	}
	c := m[key]
	c.Count++
	if e.Decision == "denied" {
		c.Denied++
	} else {
		c.Allowed++
		if c.LastSeen == nil || e.Time.After(*c.LastSeen) {
			t := e.Time
			c.LastSeen = &t
		}
	}
}
func counts(m map[string]*Count) []Count {
	out := []Count{}
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Query opens files read-only and takes a length snapshot per segment. An
// in-progress final append is ignored, and a complete malformed line is an
// explicit error, never a silently empty answer. Summaries cover all matching
// events, independently of the returned-event limit.
func Query(configDir string, f Filter) (Result, error) {
	r := Result{Events: []Event{}, Summary: Summary{People: []Count{}, Apps: []Count{}, Traffic: TrafficSummary{PeopleApps: []SubjectAppTraffic{}, Apps: []AppTraffic{}}}}
	if err := f.Validate(); err != nil {
		return r, err
	}
	limit := f.Limit
	if limit == 0 {
		limit = 100
	}
	dir := filepath.Join(configDir, "access-log")
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return r, fmt.Errorf("unsafe access log directory")
	}
	files, err := segments(dir)
	if err != nil {
		return r, err
	}
	people := map[string]*Count{}
	apps := map[string]*Count{}
	reasons := map[string]*Count{}
	trafficPeople := map[subjectAppKey]*SubjectAppTraffic{}
	trafficApps := map[string]*AppTraffic{}
	consume := func(e Event) {
		if !f.matches(e) {
			return
		}
		if f.AllowedApps != nil && e.MCP != nil {
			m := *e.MCP
			m.Capabilities.Apps = nil
			for _, app := range e.MCP.Capabilities.Apps {
				if slices.Contains(f.AllowedApps, app) {
					m.Capabilities.Apps = append(m.Capabilities.Apps, app)
				}
			}
			e.MCP = &m
		}
		r.Summary.Count++
		r.Summary.Traffic.add(e, trafficPeople, trafficApps)
		if e.Decision == "denied" {
			r.Summary.Denied++
		} else {
			r.Summary.Allowed++
		}
		who := e.Identity.Login
		if e.MCP != nil {
			who = e.MCP.Principal
		}
		if who == "" {
			who = e.Identity.Node
		}
		if who == "" {
			who = "unknown"
		}
		addCount(people, who, e)
		if e.Reason != "" {
			addCount(reasons, e.Reason, e)
		}
		if e.MCP != nil {
			seen := map[string]bool{}
			for _, app := range e.MCP.Apps {
				if !seen[app] {
					addCount(apps, app, e)
					seen[app] = true
				}
			}
		} else {
			addCount(apps, e.App, e)
		}
		at := sort.Search(len(r.Events), func(i int) bool { return !r.Events[i].Time.After(e.Time) })
		if at < limit {
			r.Events = append(r.Events, Event{})
			copy(r.Events[at+1:], r.Events[at:])
			r.Events[at] = e
			if len(r.Events) > limit {
				r.Events = r.Events[:limit]
			}
		}
	}
	// Newest segments first; bounded insertion also supports injected clock
	// rollback and multiple event producers whose timestamps arrive out of order.
	for i := len(files) - 1; i >= 0; i-- {
		entry := files[i]
		file, err := atomicfile.OpenSharedRead(filepath.Join(dir, entry.name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return r, err
		}
		reader := bufio.NewReaderSize(io.LimitReader(file, entry.size), maxRecordBytes)
		for {
			line, readErr := reader.ReadSlice('\n')
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				file.Close()
				return r, readErr
			}
			var e Event
			if err := json.Unmarshal(line, &e); err != nil {
				file.Close()
				return r, fmt.Errorf("invalid access log record")
			}
			if e.SchemaVersion != SchemaVersion {
				file.Close()
				return r, fmt.Errorf("unsupported access log schema")
			}
			consume(e)
		}
		file.Close()
	}
	entries, err := (mcpaudit.Journal{Path: filepath.Join(configDir, "mcp-audit.json")}).Read()
	if err != nil {
		return r, err
	}
	for _, entry := range entries {
		consume(fromReceipt(entry))
	}
	r.Summary.People = counts(people)
	r.Summary.Apps = counts(apps)
	for _, p := range trafficPeople {
		r.Summary.Traffic.PeopleApps = append(r.Summary.Traffic.PeopleApps, *p)
	}
	sort.Slice(r.Summary.Traffic.PeopleApps, func(i, j int) bool {
		a, b := r.Summary.Traffic.PeopleApps[i], r.Summary.Traffic.PeopleApps[j]
		if a.SubjectKind != b.SubjectKind {
			return a.SubjectKind < b.SubjectKind
		}
		if a.Login != b.Login {
			return a.Login < b.Login
		}
		return a.App < b.App
	})
	for _, a := range trafficApps {
		r.Summary.Traffic.Apps = append(r.Summary.Traffic.Apps, *a)
	}
	sort.Slice(r.Summary.Traffic.Apps, func(i, j int) bool { return r.Summary.Traffic.Apps[i].App < r.Summary.Traffic.Apps[j].App })
	if len(reasons) > 0 {
		r.Summary.Reasons = counts(reasons)
	}
	r.Truncated = r.Summary.Count > len(r.Events)
	return r, nil
}

func asciiLogin(s string) string {
	b := []byte(strings.Trim(s, " \t\r\n\v\f"))
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
