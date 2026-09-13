// Command gen-notices generates THIRD_PARTY_NOTICES.md from the union of modules
// linked into every supported release target. It runs `go list -deps` over
// darwin/linux/windows × amd64/arm64 with CGO disabled, reads license/notice
// files from the resolved module source directories, and fails closed when a
// module has no notice payload or an unknown license classification.
//
//	go run ./tools/gen-notices          # (re)write THIRD_PARTY_NOTICES.md
//	go run ./tools/gen-notices -check   # exit nonzero if the committed file is stale
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

type module struct {
	path    string
	version string
	dir     string
	license string
	files   []noticeFile
}

type noticeFile struct {
	name string
	text string
}

type releaseTarget struct {
	goos   string
	goarch string
}

var supportedReleaseTargets = []releaseTarget{
	{goos: "darwin", goarch: "amd64"},
	{goos: "darwin", goarch: "arm64"},
	{goos: "linux", goarch: "amd64"},
	{goos: "linux", goarch: "arm64"},
	{goos: "windows", goarch: "amd64"},
	{goos: "windows", goarch: "arm64"},
}

func generate() ([]byte, error) {
	mods, err := linkedModules()
	if err != nil {
		return nil, err
	}
	for i := range mods {
		files, err := noticeFiles(mods[i].dir)
		if err != nil {
			return nil, fmt.Errorf("%s@%s: %w", mods[i].path, mods[i].version, err)
		}
		license, err := classifyLicense(files)
		if err != nil {
			return nil, fmt.Errorf("%s@%s: %w", mods[i].path, mods[i].version, err)
		}
		mods[i].license = license
		mods[i].files = files
	}
	return render(mods), nil
}

func linkedModules() ([]module, error) {
	return linkedModulesForTargets(supportedReleaseTargets, goListModulesForTarget)
}

func linkedModulesForTargets(targets []releaseTarget, list func(releaseTarget) ([]module, error)) ([]module, error) {
	seen := map[string]module{}
	for _, target := range targets {
		mods, err := list(target)
		if err != nil {
			return nil, err
		}
		for _, mod := range mods {
			key := mod.path + "\t" + mod.version
			if _, ok := seen[key]; !ok {
				seen[key] = mod
			}
		}
	}

	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].path != mods[j].path {
			return mods[i].path < mods[j].path
		}
		return mods[i].version < mods[j].version
	})
	return mods, nil
}

func goListModulesForTarget(target releaseTarget) ([]module, error) {
	root, err := repoRoot()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "list", "-deps",
		"-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}", ".")
	cmd.Dir = root
	cmd.Env = envWith(os.Environ(), map[string]string{
		"GOOS":        target.goos,
		"GOARCH":      target.goarch,
		"CGO_ENABLED": "0",
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps for %s/%s CGO_ENABLED=0: %w\n%s", target.goos, target.goarch, err, strings.TrimSpace(stderr.String()))
	}
	return parseGoListModules(out)
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate repo root from %s", dir)
		}
		dir = parent
	}
}

func parseGoListModules(out []byte) ([]module, error) {
	seen := map[string]module{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			return nil, fmt.Errorf("unexpected go list module line %q", line)
		}
		path, version, dir := parts[0], parts[1], parts[2]
		if path == "github.com/monody0007/tslink" {
			continue
		}
		seen[path+"\t"+version] = module{path: path, version: version, dir: dir}
	}

	mods := make([]module, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].path != mods[j].path {
			return mods[i].path < mods[j].path
		}
		return mods[i].version < mods[j].version
	})
	return mods, nil
}

func envWith(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, overridden := overrides[key]; overridden {
			continue
		}
		out = append(out, entry)
	}
	for key, value := range overrides {
		out = append(out, key+"="+value)
	}
	sort.Strings(out)
	return out
}

func noticeFiles(dir string) ([]noticeFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module dir: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		if isNoticeFileName(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no LICENSE/COPYING/NOTICE file found")
	}
	files := make([]noticeFile, 0, len(names))
	for _, name := range names {
		text, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		trimmed := strings.TrimRight(string(text), "\n")
		if strings.TrimSpace(trimmed) == "" {
			return nil, fmt.Errorf("%s is empty", name)
		}
		files = append(files, noticeFile{name: name, text: trimmed})
	}
	return files, nil
}

func isNoticeFileName(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, "_test.go") {
		return false
	}
	return lower == "license" ||
		strings.HasPrefix(lower, "license.") ||
		lower == "copying" ||
		strings.HasPrefix(lower, "copying.") ||
		lower == "notice" ||
		strings.HasPrefix(lower, "notice.")
}

func classifyLicense(files []noticeFile) (string, error) {
	var combined strings.Builder
	for _, file := range files {
		if strings.HasPrefix(strings.ToLower(file.name), "notice") {
			continue
		}
		combined.WriteString(file.text)
		combined.WriteByte('\n')
	}
	text := strings.ToLower(combined.String())
	flat := strings.Join(strings.Fields(text), " ")
	switch {
	case strings.Contains(flat, "apache license") && strings.Contains(flat, "version 2.0"):
		return "Apache-2.0", nil
	case strings.Contains(flat, "permission is hereby granted, free of charge"):
		return "MIT", nil
	case strings.Contains(flat, "permission to use, copy, modify, and distribute this software for any purpose with or without fee"):
		return "ISC", nil
	case strings.Contains(flat, "redistribution and use in source and binary forms"):
		if strings.Contains(flat, "neither the name") || strings.Contains(flat, "nor the names of its contributors") {
			return "BSD-3-Clause", nil
		}
		return "BSD-2-Clause", nil
	default:
		return "", fmt.Errorf("unknown license classification")
	}
}

func render(mods []module) []byte {
	var b strings.Builder
	b.WriteString("# Third-Party Notices\n\n")
	b.WriteString("TSLink licensing is described in `LICENSE`; third-party rights are unchanged.\n")
	b.WriteString("See `NOTICE`. This generated file inventories the union of third-party modules\n")
	b.WriteString("linked into the supported CGO-disabled release targets (darwin/linux/windows\n")
	b.WriteString("× amd64/arm64) and includes the license/notice payloads found in the exact\n")
	b.WriteString("module source directories resolved by `go list -deps`. It is a\n")
	b.WriteString("mechanical inventory, not legal advice. Do not edit it by hand; run\n")
	b.WriteString("`go run ./tools/gen-notices` instead.\n\n")
	fmt.Fprintf(&b, "Linked third-party modules: %d\n\n", len(mods))
	b.WriteString("| Module | Version | License | Included files |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, m := range mods {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", m.path, m.version, m.license, fileNames(m.files))
	}
	b.WriteString("\n## Included License And Notice Text\n\n")
	for _, m := range mods {
		fmt.Fprintf(&b, "### `%s` %s\n\n", m.path, m.version)
		fmt.Fprintf(&b, "- License: %s\n", m.license)
		fmt.Fprintf(&b, "- Evidence source: `%s@%s` from the resolved Go module graph\n", m.path, m.version)
		fmt.Fprintf(&b, "- Included files: %s\n\n", fileNames(m.files))
		for _, file := range m.files {
			fmt.Fprintf(&b, "#### %s\n\n", file.name)
			b.WriteString("~~~~text\n")
			b.WriteString(file.text)
			b.WriteString("\n~~~~\n\n")
		}
	}
	return []byte(b.String())
}

func fileNames(files []noticeFile) string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, "`"+file.name+"`")
	}
	return strings.Join(names, ", ")
}

func normalize(b []byte) []byte {
	return bytes.TrimRight(b, "\n \t")
}
