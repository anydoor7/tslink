package cmd

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
)

func TestHumanTextForOS(t *testing.T) {
	const decorated = "\u2192 \u2713 removed\n\u26a0 retry \u2014 \u2190 \u2500\n"
	for _, tc := range []struct {
		name, goos, input, want string
	}{
		{"windows-arrow", "windows", "\u2192 pending", "-> pending"},
		{"windows-check", "windows", "\u2713 removed", "OK removed"},
		{"windows-warning", "windows", "\u26a0 retry", "WARNING: retry"},
		{"windows-em-dash", "windows", "saved \u2014 retry", "saved - retry"},
		{"windows-left-arrow", "windows", "secret \u2190 copy", "secret <- copy"},
		{"windows-rule", "windows", "\u2500\u2500\u2500 menu", "--- menu"},
		{"windows-ascii", "windows", "Already absent\n", "Already absent\n"},
		{"darwin", "darwin", decorated, decorated},
		{"linux", "linux", decorated, decorated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanTextForOS(tc.input, tc.goos); got != tc.want {
				t.Fatalf("human text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHumanTextUsesNativeOS(t *testing.T) {
	want := "\u2192 \u2713 Startup script removed"
	if runtime.GOOS == "windows" {
		want = "-> OK Startup script removed"
	}
	if got := humanText("\u2192 \u2713 Startup script removed"); got != want {
		t.Fatalf("native human text = %q, want %q", got, want)
	}
}

func TestHumanTextPreservesFormatArguments(t *testing.T) {
	got := fmt.Sprintf(humanTextForOS("\u2192 \u2713 removed: %s", "windows"), "name-\u2192-\u2713-\u26a0")
	if want := "-> OK removed: name-\u2192-\u2713-\u26a0"; got != want {
		t.Fatalf("formatted human text = %q, want %q", got, want)
	}
}

func TestCodepageSafeHelp(t *testing.T) {
	const description = "\u2192 \u2713 help\n"
	for _, goos := range []string{"windows", "darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			command := &cobra.Command{Use: "example", Long: description}
			var out bytes.Buffer
			command.SetOut(&out)
			codepageSafeHelp(goos, func(cmd *cobra.Command, _ []string) {
				fmt.Fprint(cmd.OutOrStdout(), cmd.Long)
			})(command, nil)
			want := description
			if goos == "windows" {
				want = "-> OK help\n"
			}
			if got := out.String(); got != want {
				t.Fatalf("help = %q, want %q", got, want)
			}
			if command.Long != description {
				t.Fatal("help rendering changed command metadata used by JSON")
			}
			if command.OutOrStdout() != &out {
				t.Fatal("help rendering did not restore the command writer")
			}
		})
	}
}

func TestCodepageSafeHelpPreservesInheritedWriter(t *testing.T) {
	parent := &cobra.Command{Use: "parent"}
	child := &cobra.Command{Use: "child", Long: "\u2192 help"}
	parent.AddCommand(child)
	var first, second bytes.Buffer
	parent.SetOut(&first)
	codepageSafeHelp("windows", parent.HelpFunc())(child, nil)
	parent.SetOut(&second)
	if child.OutOrStdout() != &second {
		t.Fatal("help rendering pinned an inherited writer")
	}
}

func TestAllCommandWindowsHelpASCII(t *testing.T) {
	defaultHelp := (&cobra.Command{}).HelpFunc()
	count := 0
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		count++
		t.Run(command.CommandPath(), func(t *testing.T) {
			var out bytes.Buffer
			renderCommand := *command
			renderCommand.SetOut(&out)
			codepageSafeHelp("windows", defaultHelp)(&renderCommand, nil)
			if out.Len() == 0 {
				t.Fatal("Windows help is empty")
			}
			for _, r := range out.String() {
				if r > unicode.MaxASCII {
					t.Fatalf("Windows help contains non-ASCII rune %U:\n%s", r, out.String())
				}
			}
		})
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(rootCmd)
	t.Logf("checked Windows help for %d commands", count)
}
