package cmd

import "testing"

func TestVersionDisplayString(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{name: "no commit returns version alone", version: "1.2.3", commit: "", want: "1.2.3"},
		{name: "commit is parenthesized after version", version: "1.2.3", commit: "abcdef", want: "1.2.3 (abcdef)"},
		{name: "dev fallback with no commit stays bare", version: developmentVersionUnmeasured, commit: "", want: developmentVersionUnmeasured},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionDisplayString(tc.version, tc.commit); got != tc.want {
				t.Fatalf("versionDisplayString(%q, %q) = %q, want %q", tc.version, tc.commit, got, tc.want)
			}
		})
	}
}

// TestSelfBuildIdentity exercises the three cases selfBuildIdentity's doc
// comment describes: main.go's ldflags release path, its source-build VCS
// fallback (Version already carries the module version and/or revision, so
// it must be reported as-is, not collapsed), and the one case both main.go
// and selfBuildIdentity agree means "nothing was measurable" (the bare "dev"
// literal main.go uses only when it found neither evidence source).
func TestSelfBuildIdentity(t *testing.T) {
	tests := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{
			name:    "release ldflags with commit",
			version: "v1.2.3",
			commit:  "deadbeefcafe",
			want:    "v1.2.3 (deadbeefcafe)",
		},
		{
			name:    "ldflags version without a linked commit is still measured",
			version: "v1.2.3",
			commit:  "",
			want:    "v1.2.3",
		},
		{
			name:    "commit-only ldflags (unusual, but still linked evidence)",
			version: developmentVersionUnmeasured,
			commit:  "deadbeefcafe",
			want:    developmentVersionUnmeasured + " (deadbeefcafe)",
		},
		{
			name:    "source build with VCS revision is measured, not collapsed",
			version: "0.0.0-dev+0123456789ab (2026-08-10T00:12:03Z)",
			commit:  "",
			want:    "0.0.0-dev+0123456789ab (2026-08-10T00:12:03Z)",
		},
		{
			name:    "neither ldflags nor VCS metadata: unmeasured",
			version: developmentVersionUnmeasured,
			commit:  "",
			want:    "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldVersion, oldCommit := Version, Commit
			t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
			Version, Commit = tc.version, tc.commit

			if got := selfBuildIdentity(); got != tc.want {
				t.Fatalf("selfBuildIdentity() with Version=%q Commit=%q = %q, want %q", tc.version, tc.commit, got, tc.want)
			}
		})
	}
}

// TestSelfBuildIdentityMatchesExecuteVersionFormatting is the "same helper"
// contract from a different angle: Execute()'s `tslink --version` string and
// selfBuildIdentity()'s daemon/CLI comparison value must be built from the
// identical formatting rule, so a future edit to one cannot silently diverge
// from the other and produce a false build-skew report.
func TestSelfBuildIdentityMatchesExecuteVersionFormatting(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	t.Cleanup(func() { Version, Commit = oldVersion, oldCommit })
	Version, Commit = "v2.4.6", "cafebabe1234"

	if got, want := selfBuildIdentity(), versionDisplayString(Version, Commit); got != want {
		t.Fatalf("selfBuildIdentity() = %q, want versionDisplayString(Version, Commit) = %q", got, want)
	}
}
