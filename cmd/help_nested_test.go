package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// helpTargets returns every visible command below the root, depth first. The
// generated help and completion commands are not part of TSLink's own surface.
func helpTargets(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, child := range c.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			out = append(out, child)
			walk(child)
		}
	}
	walk(root)
	return out
}

// TestCompiledEveryCommandHelpIsItsOwn runs the real binary for each command
// the CLI registers, nested ones included, in the three spellings a user can
// type: `<cmd> --help`, `<cmd> -h` and `help <cmd>`. Each must print that
// command's own usage on stdout, exit 0 and write nothing to stderr. A help
// route that falls back to the root help (which every command's output would
// otherwise be mistaken for) fails the comparison against the root text.
func TestCompiledEveryCommandHelpIsItsOwn(t *testing.T) {
	configDir := t.TempDir()
	rootHelp, rootErr, rootCode := runCompiledTSLinkWithConfigDir(t, configDir, "", "--help")
	if rootCode != 0 || rootErr != "" || !strings.Contains(rootHelp, "Available Commands:") {
		t.Fatalf("root help: exit=%d stderr=%q stdout=%q", rootCode, rootErr, rootHelp)
	}

	targets := helpTargets(rootCmd)
	nested := 0
	for _, c := range targets {
		if strings.Count(c.CommandPath(), " ") >= 2 {
			nested++
		}
	}
	// The brief for this test is nested commands; a tree walk that found none
	// would pass vacuously.
	if len(targets) < 40 || nested < 15 {
		t.Fatalf("command walk found %d commands, %d nested; the tree walk is broken", len(targets), nested)
	}

	for _, c := range targets {
		path := c.CommandPath()
		words := strings.Fields(strings.TrimPrefix(path, "tslink"))
		var localFlags []string
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || f.Name == "help" || f.Name == "json" {
				return
			}
			localFlags = append(localFlags, "--"+f.Name)
		})
		var children []string
		for _, child := range c.Commands() {
			if !child.Hidden && child.Name() != "help" && child.Name() != "completion" {
				children = append(children, child.Name())
			}
		}

		spellings := map[string][]string{
			"--help": append(append([]string{}, words...), "--help"),
			"-h":     append(append([]string{}, words...), "-h"),
			"help":   append([]string{"help"}, words...),
		}
		for spelling, args := range spellings {
			t.Run(strings.ReplaceAll(path, " ", "_")+"/"+spelling, func(t *testing.T) {
				stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, configDir, "", args...)
				if code != 0 || stderr != "" {
					t.Fatalf("tslink %s: exit=%d stderr=%q, want exit 0 and empty stderr", strings.Join(args, " "), code, stderr)
				}
				if stdout == rootHelp {
					t.Fatalf("tslink %s printed the root help instead of its own", strings.Join(args, " "))
				}
				if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(path) + `( |$)`).MatchString(stdout) {
					t.Errorf("tslink %s: no usage line for %q in:\n%s", strings.Join(args, " "), path, stdout)
				}
				for _, flag := range localFlags {
					if !strings.Contains(stdout, flag) {
						t.Errorf("tslink %s does not document its flag %s:\n%s", strings.Join(args, " "), flag, stdout)
					}
				}
				for _, child := range children {
					if !strings.Contains(stdout, "  "+child+" ") {
						t.Errorf("tslink %s does not list its subcommand %q:\n%s", strings.Join(args, " "), child, stdout)
					}
				}
			})
		}
	}
}

// mcpHelpToolNames extracts the tool list `tslink mcp --help` prints: the
// indented, comma-separated block that follows the "exposes N tools" sentence.
func mcpHelpToolNames(t *testing.T, help string) (count int, names []string) {
	t.Helper()
	match := regexp.MustCompile(`exposes (\d+) tools`).FindStringSubmatch(help)
	if match == nil {
		t.Fatalf("mcp help does not say how many tools the server exposes:\n%s", help)
	}
	fmt.Sscan(match[1], &count)
	start := strings.Index(help, "session):\n\n")
	if start < 0 {
		t.Fatalf("mcp help has no tool list block:\n%s", help)
	}
	block := help[start+len("session):\n\n"):]
	if end := strings.Index(block, "\n\n"); end >= 0 {
		block = block[:end]
	}
	names = strings.FieldsFunc(block, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' })
	return count, names
}

// TestCompiledMCPHelpListsTheRegisteredTools keeps `tslink mcp --help` from
// naming a hand-kept subset again: the list must equal mcpToolDefinitions,
// which is also what tools/list serves an owner session.
func TestCompiledMCPHelpListsTheRegisteredTools(t *testing.T) {
	want := mcpRegisteredToolNames()
	if len(want) < 40 {
		t.Fatalf("registry holds %d tools; expected the full owner set", len(want))
	}
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedWant)

	for _, args := range [][]string{{"mcp", "--help"}, {"mcp", "-h"}, {"help", "mcp"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			stdout, stderr, code := runCompiledTSLinkWithConfigDir(t, t.TempDir(), "", args...)
			if code != 0 || stderr != "" {
				t.Fatalf("tslink %s: exit=%d stderr=%q", strings.Join(args, " "), code, stderr)
			}
			count, got := mcpHelpToolNames(t, stdout)
			if count != len(want) {
				t.Errorf("help says %d tools, registry has %d", count, len(want))
			}
			sortedGot := append([]string(nil), got...)
			sort.Strings(sortedGot)
			if strings.Join(sortedGot, ",") != strings.Join(sortedWant, ",") {
				t.Errorf("help lists %v, registry has %v", sortedGot, sortedWant)
			}
		})
	}
}

// TestMCPClientsDocStatesTheOwnerToolCount pins the count docs/mcp-clients.md
// gives for an owner session to the registry.
func TestMCPClientsDocStatesTheOwnerToolCount(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the source tree")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(file)), "docs", "mcp-clients.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(raw)), " ")
	want := fmt.Sprintf("An owner session lists %d tools", len(mcpToolDefinitions))
	if !strings.Contains(doc, want) {
		t.Errorf("docs/mcp-clients.md does not say %q; update it when the registry changes", want)
	}
}
