// Command gen-notices generates THIRD_PARTY_NOTICES.md from the exact set of
// modules linked into the tslink binary. It runs `go list -deps` over the main
// package and records every dependency module with its resolved version, so the
// inventory is traceable to the real linked module graph rather than a
// hand-maintained list.
//
//	go run ./tools/gen-notices          # (re)write THIRD_PARTY_NOTICES.md
//	go run ./tools/gen-notices -check   # exit nonzero if the committed file is stale
//
// License columns are a reviewed best-effort mapping for the modules tslink
// links. Modules not covered by the reviewed map are emitted as
// "review pending" so the remaining legal-review scope is explicit.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const outputFile = "THIRD_PARTY_NOTICES.md"

func main() {
	check := flag.Bool("check", false, "verify the committed file is up to date instead of writing it")
	flag.Parse()

	generated, err := generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-notices:", err)
		os.Exit(1)
	}

	if *check {
		existing, err := os.ReadFile(outputFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gen-notices: cannot read", outputFile, "-", err)
			os.Exit(1)
		}
		if !bytes.Equal(normalize(existing), normalize(generated)) {
			fmt.Fprintf(os.Stderr, "gen-notices: %s is stale; run `go run ./tools/gen-notices`\n", outputFile)
			os.Exit(1)
		}
		fmt.Printf("gen-notices: %s is up to date\n", outputFile)
		return
	}

	if err := os.WriteFile(outputFile, generated, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-notices: write failed:", err)
		os.Exit(1)
	}
	fmt.Printf("gen-notices: wrote %s\n", outputFile)
}

// module is one linked dependency module.
type module struct {
	path    string
	version string
}

// generate lists the linked module graph and renders the notice inventory.
func generate() ([]byte, error) {
	// `go list -deps` over the main package walks only what is actually
	// compiled into the binary, i.e. the exact linked main-module graph.
	out, err := exec.Command("go", "list", "-deps",
		"-f", "{{with .Module}}{{.Path}}\t{{.Version}}{{end}}", ".").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps: %w", err)
	}

	seen := map[string]module{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		path := parts[0]
		// Skip the main module itself; it is covered by the project LICENSE.
		if path == "github.com/monody0007/tslink" {
			continue
		}
		ver := ""
		if len(parts) == 2 {
			ver = parts[1]
		}
		seen[path] = module{path: path, version: ver}
	}

	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].path < mods[j].path })

	var b strings.Builder
	b.WriteString("# Third-Party Notices\n\n")
	b.WriteString("TSLink is distributed under the Apache License 2.0 (see `LICENSE` and\n")
	b.WriteString("`NOTICE`). This file inventories the third-party modules linked into the\n")
	b.WriteString("`tslink` binary. It is generated from the linked module graph with\n")
	b.WriteString("`go run ./tools/gen-notices`; do not edit it by hand.\n\n")
	b.WriteString("License entries marked `review pending` still require legal review before a\n")
	b.WriteString("public release; full license texts are available in each module's source\n")
	b.WriteString("repository.\n\n")
	fmt.Fprintf(&b, "Linked third-party modules: %d\n\n", len(mods))
	b.WriteString("| Module | Version | License |\n")
	b.WriteString("|---|---|---|\n")
	for _, m := range mods {
		ver := m.version
		if ver == "" {
			ver = "(local)"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", m.path, ver, licenseFor(m.path))
	}
	b.WriteString("\n")
	return []byte(b.String()), nil
}

// licenseFor returns the reviewed SPDX license for a module path, or
// "review pending" when the module is outside the reviewed set.
func licenseFor(path string) string {
	switch {
	case path == "tailscale.com" || strings.HasPrefix(path, "tailscale.com/"),
		strings.HasPrefix(path, "github.com/tailscale/"):
		return "BSD-3-Clause"
	case path == "github.com/spf13/cobra",
		strings.HasPrefix(path, "github.com/prometheus/"),
		path == "github.com/inconshreveable/mousetrap",
		path == "github.com/munnerz/goautoneg":
		return "Apache-2.0"
	case path == "github.com/spf13/pflag",
		path == "github.com/fsnotify/fsnotify",
		strings.HasPrefix(path, "golang.org/x/"),
		path == "google.golang.org/protobuf",
		path == "github.com/google/go-cmp",
		path == "github.com/beorn7/perks",
		path == "github.com/cespare/xxhash/v2",
		strings.HasPrefix(path, "go4.org/"),
		strings.HasPrefix(path, "go.yaml.in/"):
		return "BSD-3-Clause"
	case path == "github.com/zalando/go-keyring",
		path == "github.com/danieljoos/wincred",
		path == "github.com/godbus/dbus/v5",
		path == "github.com/klauspost/compress",
		path == "github.com/x448/float16",
		path == "github.com/fxamacker/cbor/v2":
		return "MIT"
	default:
		return "review pending"
	}
}

// normalize strips trailing whitespace differences so -check compares content,
// not accidental editor reformatting.
func normalize(b []byte) []byte {
	return bytes.TrimRight(b, "\n \t")
}
