package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	releaseNotesFlag = "--release-notes"
	refNameExpr      = "${{ github.ref_name }}"
	runnerTempPrefix = "${{ runner.temp }}/"
)

// The release notes step is the one that writes the notes file GoReleaser
// publishes as the release body.
func releaseNotesIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "release notes", func(s publishStep) bool {
		return s.Env["RELEASE_NOTES"] != ""
	})
}

func checkoutIndex(t *testing.T, steps []publishStep) int {
	return stepIndex(t, steps, "checkout", func(s publishStep) bool {
		return strings.HasPrefix(s.Uses, "actions/checkout@")
	})
}

// TestReleaseNotesComeFromChangelog pins the wiring: the curated notes are
// extracted from the checked-out tree for every tag, before GoReleaser, into
// a file outside the checkout and dist/, and GoReleaser publishes that file
// instead of generating a commit list.
func TestReleaseNotesComeFromChangelog(t *testing.T) {
	steps := publishSteps(t)
	checkout, notes, release := checkoutIndex(t, steps), releaseNotesIndex(t, steps), goreleaserIndex(t, steps)
	if !(checkout < notes && notes < release) {
		t.Fatalf("publish step order: checkout %d, release notes %d, GoReleaser %d; want checkout < notes < GoReleaser", checkout, notes, release)
	}

	step := steps[notes]
	if step.If != "" {
		t.Errorf("release notes step runs only if %q; every tag, pre-releases included, needs its CHANGELOG.md section", step.If)
	}
	if step.Env["TAG"] != refNameExpr {
		t.Errorf("release notes step env TAG = %q, want %q", step.Env["TAG"], refNameExpr)
	}
	if strings.Contains(step.Run, "${{") {
		t.Errorf("release notes script interpolates an expression; pass values through env:\n%s", step.Run)
	}
	if !strings.Contains(step.Run, "CHANGELOG.md") {
		t.Errorf("release notes script does not read CHANGELOG.md:\n%s", step.Run)
	}

	path := step.Env["RELEASE_NOTES"]
	if !strings.HasPrefix(path, runnerTempPrefix) {
		t.Errorf("release notes file %q, want it under %s: --clean wipes dist/ and the checkout must stay clean", path, runnerTempPrefix)
	}
	args := steps[release].With["args"]
	if strings.Count(args, releaseNotesFlag) != 1 || !strings.Contains(args+" ", releaseNotesFlag+" "+path+" ") {
		t.Errorf("GoReleaser args %q, want %s %s exactly once", args, releaseNotesFlag, path)
	}
}

const changelogFixture = `# Changelog

Notes before the first version are not part of any release.

## [Unreleased]

- Not released yet.

## [1.2.0] - 2026-01-02

First line of 1.2.0.

### Added

- Feature A
  continued.


- Feature B.

## [1.2.0-rc.1] - 2025-12-20
- Candidate notes.
## [1.1.0] - 2025-11-01

` + "  \t" + `

## [1.0.1] - 2025-10-15

- First copy.

## [1.0.1] - 2025-10-16

- Second copy.

## [1.0.0] - 2025-10-01

- Last section, read to the end of the file.
`

// runReleaseNotesStep runs the extraction script in dir for tag and returns
// the notes it wrote, or its output and error.
func runReleaseNotesStep(t *testing.T, script, dir, tag string) (notes, out []byte, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release-notes.md")
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TAG=" + tag, "RELEASE_NOTES=" + path}
	if out, err = cmd.CombinedOutput(); err != nil {
		return nil, out, err
	}
	notes, err = os.ReadFile(path)
	return notes, out, err
}

// TestChangelogVersionsHaveReleaseNotes runs the step for every version
// heading in the repository's CHANGELOG.md. A v* tag cannot be moved, so a
// section that is empty or duplicated must fail here, before anyone tags.
func TestChangelogVersionsHaveReleaseNotes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the ubuntu publish step with bash; covered on Linux and macOS")
	}
	steps := publishSteps(t)
	script := steps[releaseNotesIndex(t, steps)].Run
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	versions := 0
	for _, line := range strings.Split(string(body), "\n") {
		heading, ok := strings.CutPrefix(line, "## [")
		version, _, closed := strings.Cut(heading, "]")
		if !ok || !closed || version == "Unreleased" {
			continue
		}
		versions++
		if _, out, err := runReleaseNotesStep(t, script, root, "v"+version); err != nil {
			t.Errorf("CHANGELOG.md ## [%s] cannot become release notes: %v\n%s", version, err, out)
		}
	}
	if versions == 0 {
		t.Fatal("CHANGELOG.md has no version sections")
	}
}

// TestReleaseNotesStepExecutes runs the checked-in extraction script against
// a fixture CHANGELOG.md, with LF and with CRLF line endings. It must write
// exactly the version's section, without carriage returns or blank lines at
// either end, and fail, naming the problem, when the section is missing, empty
// or ambiguous.
func TestReleaseNotesStepExecutes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executes the ubuntu publish step with bash; covered on Linux and macOS")
	}
	steps := publishSteps(t)
	script := steps[releaseNotesIndex(t, steps)].Run
	for _, eol := range []struct{ name, sep string }{{"LF", "\n"}, {"CRLF", "\r\n"}} {
		fixture := strings.ReplaceAll(changelogFixture, "\n", eol.sep)
		for _, tc := range []struct {
			tag     string
			want    string
			wantErr string
		}{
			{tag: "v1.2.0", want: "First line of 1.2.0.\n\n### Added\n\n- Feature A\n  continued.\n\n\n- Feature B.\n"},
			{tag: "v1.2.0-rc.1", want: "- Candidate notes.\n"},
			{tag: "v1.0.0", want: "- Last section, read to the end of the file.\n"},
			{tag: "v9.9.9", wantErr: "0 sections headed ## [9.9.9]"},
			{tag: "v1.3.0-rc.1", wantErr: "0 sections headed ## [1.3.0-rc.1]"},
			{tag: "v1.1.0", wantErr: "## [1.1.0] is empty"},
			{tag: "v1.0.1", wantErr: "2 sections headed ## [1.0.1]"},
		} {
			t.Run(eol.name+"/"+tc.tag, func(t *testing.T) {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(fixture), 0o644); err != nil {
					t.Fatal(err)
				}
				got, out, err := runReleaseNotesStep(t, script, dir, tc.tag)
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(string(out), "::error::") || !strings.Contains(string(out), tc.wantErr) {
						t.Fatalf("tag %s: want a failure naming %q, got err=%v\n%s", tc.tag, tc.wantErr, err, out)
					}
					return
				}
				if err != nil {
					t.Fatalf("tag %s rejected: %v\n%s", tc.tag, err, out)
				}
				if string(got) != tc.want {
					t.Fatalf("tag %s notes:\n%q\nwant:\n%q", tc.tag, got, tc.want)
				}
			})
		}
	}
}
