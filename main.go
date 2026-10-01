package main

import (
	"os"
	"runtime/debug"
	"strings"

	"github.com/anydoor7/tslink/cmd"
	"github.com/anydoor7/tslink/internal/logging"
	"github.com/anydoor7/tslink/internal/output"
)

const (
	developmentVersion = "dev"
	developmentSemver  = "0.0.0-dev"
	shortCommitLength  = 12
)

// version and commit are set via ldflags at build time
// (e.g., -X main.version=v1.2.3 -X main.commit=<sha>). commit is empty for
// source builds; release builds embed the exact VCS SHA.
var (
	version = developmentVersion
	commit  = ""
)

func main() {
	buildInfo, ok := debug.ReadBuildInfo()
	version, commit = resolveBuildVersion(version, commit, buildInfo, ok)

	logging.Init(false)
	cmd.Version = version
	cmd.Commit = commit
	err := cmd.Execute()
	if err == nil {
		return
	}

	code := output.ExitCode(err)

	if output.IsSilent(err) {
		os.Exit(code)
	}

	if cmd.WasJSONRequested() {
		output.FailureForError(cmd.LastCommandName(), err)
	} else {
		// Print error in human-readable format (Cobra's SilenceErrors is on)
		os.Stderr.WriteString(formatHumanError(err))
	}

	os.Exit(code)
}

// formatHumanError renders the same recovery list the JSON envelope carries in
// error.next, one "Next:" line per step, so a human sees the Keys page URL and
// bootstrap commands instead of only "Error: ...".
func formatHumanError(err error) string {
	var b strings.Builder
	b.WriteString("Error: " + err.Error() + "\n")
	for _, next := range output.NextCommandsForError(err) {
		b.WriteString("Next: " + next + "\n")
	}
	return b.String()
}

// resolveBuildVersion preserves linker-injected release metadata, then falls
// back to the VCS metadata embedded by the Go toolchain. Build-info metadata is
// folded into the version string so cmd's existing release rendering remains
// unchanged and source builds still produce one self-contained line.
func resolveBuildVersion(linkedVersion, linkedCommit string, info *debug.BuildInfo, ok bool) (string, string) {
	if linkedVersion != developmentVersion || linkedCommit != "" {
		return linkedVersion, linkedCommit
	}
	if !ok || info == nil {
		return developmentVersion, ""
	}

	resolvedVersion := info.Main.Version
	if resolvedVersion == "" || resolvedVersion == "(devel)" {
		resolvedVersion = developmentVersion
	}

	var revision, buildTime string
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = shortCommit(setting.Value)
		case "vcs.time":
			buildTime = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	details := make([]string, 0, 3)
	if revision != "" {
		if resolvedVersion == developmentVersion {
			resolvedVersion = developmentSemver + "+" + revision
		} else if !strings.Contains(resolvedVersion, revision) {
			details = append(details, revision)
		}
	}
	if buildTime != "" {
		details = append(details, buildTime)
	}
	if modified && !strings.HasSuffix(resolvedVersion, "+dirty") {
		details = append(details, "dirty")
	}
	if len(details) > 0 {
		resolvedVersion += " (" + strings.Join(details, ", ") + ")"
	}

	return resolvedVersion, ""
}

func shortCommit(revision string) string {
	if len(revision) <= shortCommitLength {
		return revision
	}
	return revision[:shortCommitLength]
}
