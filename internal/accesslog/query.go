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
)

type Filter struct {
	App      string     `json:"app,omitempty"`
	Who      string     `json:"who,omitempty"`
	Since    *time.Time `json:"since,omitempty"`
	Until    *time.Time `json:"until,omitempty"`
	Decision string     `json:"decision,omitempty"`
	Limit    int        `json:"limit,omitempty"`
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
	Count   int     `json:"count"`
	Allowed int     `json:"allowed"`
	Denied  int     `json:"denied"`
	People  []Count `json:"people"`
	Apps    []Count `json:"apps"`
}
type Result struct {
	Events    []Event `json:"events"`
	Summary   Summary `json:"summary"`
	Truncated bool    `json:"truncated"`
}

func (f Filter) matches(e Event) bool {
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
			match = match || asciiLogin(e.MCP.Principal) == asciiLogin(f.Who)
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
	r := Result{Events: []Event{}, Summary: Summary{People: []Count{}, Apps: []Count{}}}
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
	// Newest segments first; bounded insertion also supports injected clock
	// rollback and multiple event producers whose timestamps arrive out of order.
	for i := len(files) - 1; i >= 0; i-- {
		entry := files[i]
		file, err := os.Open(filepath.Join(dir, entry.name))
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
			if !f.matches(e) {
				continue
			}
			r.Summary.Count++
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
		file.Close()
	}
	r.Summary.People = counts(people)
	r.Summary.Apps = counts(apps)
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
