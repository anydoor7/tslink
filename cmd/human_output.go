package cmd

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var windowsHumanGlyphs = strings.NewReplacer(
	"\u2192", "->",
	"\u2713", "OK",
	"\u26a0", "WARNING:",
	"\u2014", "-",
	"\u2190", "<-",
	"\u2500", "-",
)

// HumanText adapts human-only output from the program entry point. It must not
// be applied to JSON payloads or command metadata.
func HumanText(text string) string {
	return humanText(text)
}

// humanText adapts decorative text before formatting. Arguments and JSON
// payloads retain their original contents. Windows always uses ASCII markers
// so both legacy consoles and redirected OEM-codepage readers can display them.
func humanText(text string) string {
	return humanTextForOS(text, runtime.GOOS)
}

func humanTextForOS(text, goos string) string {
	if goos != "windows" {
		return text
	}
	return windowsHumanGlyphs.Replace(text)
}

// Render help separately so command metadata (also used by manifest --json)
// stays unchanged. Cobra inherits this help function for child commands.
func codepageSafeHelp(goos string, help func(*cobra.Command, []string)) func(*cobra.Command, []string) {
	if goos != "windows" {
		return help
	}
	return func(cmd *cobra.Command, args []string) {
		out := cmd.OutOrStdout()
		var rendered bytes.Buffer
		// Render through a copy: SetOut on the original would replace a nil
		// inherited writer with an explicit one on subsequent help calls.
		renderCommand := *cmd
		renderCommand.SetOut(&rendered)
		help(&renderCommand, args)
		fmt.Fprint(out, humanTextForOS(rendered.String(), goos))
	}
}

func init() {
	rootCmd.SetHelpFunc(codepageSafeHelp(runtime.GOOS, rootCmd.HelpFunc()))
}
