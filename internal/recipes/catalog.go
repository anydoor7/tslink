// Package recipes contains app advice and credential-free loopback discovery.
// HealthPath supplies recipe registration defaults; monitoring belongs to health.
package recipes

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

//go:embed catalog.json
var data embed.FS

type Source struct {
	URL      string `json:"url"`
	Accessed string `json:"accessed"`
}
type Note struct {
	Text    string   `json:"text"`
	Snippet string   `json:"snippet"`
	Format  string   `json:"format"`
	Sources []Source `json:"sources"`
}
type Fingerprint struct {
	Path       string            `json:"path"`
	Kind       string            `json:"kind"`
	Value      string            `json:"value,omitempty"`
	Header     string            `json:"header,omitempty"`
	JSONMatch  map[string]string `json:"json_match,omitempty"`
	Confidence string            `json:"confidence"`
}
type Recipe struct {
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name"`
	DefaultPorts    []int         `json:"default_ports"`
	DefaultTarget   string        `json:"default_target"`
	PreserveHost    bool          `json:"preserve_host"`
	PortNote        string        `json:"port_note"`
	RecommendedName string        `json:"recommended_name"`
	WebSockets      bool          `json:"websockets"`
	HealthPath      string        `json:"health_path"`
	Fingerprints    []Fingerprint `json:"fingerprints"`
	SafetyLevel     string        `json:"safety_level"`
	SafetyNote      string        `json:"safety_note"`
	Notes           []Note        `json:"notes"`
}
type Catalog struct {
	SchemaVersion  int      `json:"schema_version"`
	CatalogVersion int      `json:"catalog_version"`
	Recipes        []Recipe `json:"recipes"`
}

// List decodes a fresh copy so callers cannot mutate the built-in catalog.
func List() Catalog {
	raw, _ := data.ReadFile("catalog.json")
	return decodeCatalog(raw)
}
func decodeCatalog(raw []byte) Catalog {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		panic(err)
	}
	return c
}
func Lookup(id string) (Recipe, bool) {
	for _, r := range List().Recipes {
		if r.ID == id {
			return r, true
		}
	}
	return Recipe{}, false
}

// Validate guards the data contract consumed by the CLI, MCP and health package.
func (c Catalog) Validate() error {
	if c.SchemaVersion != 1 || c.CatalogVersion < 1 {
		return fmt.Errorf("invalid catalog version")
	}
	ids := map[string]bool{}
	for _, r := range c.Recipes {
		if r.ID == "" || ids[r.ID] || r.DisplayName == "" || r.RecommendedName == "" {
			return fmt.Errorf("invalid recipe identity: %q", r.ID)
		}
		ids[r.ID] = true
		if len(r.DefaultPorts) == 0 || len(r.Fingerprints) == 0 || len(r.Notes) == 0 {
			return fmt.Errorf("recipe %s lacks ports, fingerprints or notes", r.ID)
		}
		for _, p := range r.DefaultPorts {
			if p < 1 || p > 65535 {
				return fmt.Errorf("invalid port for %s", r.ID)
			}
		}
		u, err := url.Parse(r.DefaultTarget)
		if err != nil || u.Scheme != "http" || !isLoopbackHost(u.Hostname()) || u.Port() == "" {
			return fmt.Errorf("invalid default target for %s", r.ID)
		}
		if !safePath(r.HealthPath) {
			return fmt.Errorf("invalid health path for %s", r.ID)
		}
		if r.SafetyLevel != "never_public" && r.SafetyLevel != "app_login" {
			return fmt.Errorf("invalid safety level for %s", r.ID)
		}
		if r.SafetyNote == "" {
			return fmt.Errorf("missing safety note for %s", r.ID)
		}
		for _, f := range r.Fingerprints {
			if !safePath(f.Path) || (f.Confidence != "medium" && f.Confidence != "high") {
				return fmt.Errorf("invalid fingerprint for %s", r.ID)
			}
			switch f.Kind {
			case "title":
				if f.Value == "" && r.ID != "generic-web" {
					return fmt.Errorf("empty title for %s", r.ID)
				}
			case "body":
				if f.Value == "" {
					return fmt.Errorf("empty body marker for %s", r.ID)
				}
			case "header":
				if f.Header == "" {
					return fmt.Errorf("empty header for %s", r.ID)
				}
			case "json":
				if len(f.JSONMatch) == 0 {
					return fmt.Errorf("empty JSON marker for %s", r.ID)
				}
			default:
				return fmt.Errorf("unknown fingerprint kind for %s", r.ID)
			}
		}
		for _, n := range r.Notes {
			if n.Text == "" || n.Snippet == "" || len(n.Sources) == 0 {
				return fmt.Errorf("missing app advice for %s", r.ID)
			}
			for _, s := range n.Sources {
				if !strings.HasPrefix(s.URL, "https://") || s.Accessed == "" {
					return fmt.Errorf("missing documentation provenance for %s", r.ID)
				}
			}
		}
	}
	return nil
}
func safePath(path string) bool {
	u, err := url.Parse(path)
	return err == nil && strings.HasPrefix(path, "/") && !strings.HasPrefix(path, "//") && u.Host == "" && u.RawQuery == "" && u.Fragment == ""
}
