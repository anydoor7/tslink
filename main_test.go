package main

import (
	"errors"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
)

func TestResolveBuildVersion(t *testing.T) {
	vcsInfo := func(moduleVersion string, modified bool) *debug.BuildInfo {
		modifiedValue := "false"
		if modified {
			modifiedValue = "true"
		}
		return &debug.BuildInfo{
			Main: debug.Module{Version: moduleVersion},
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				{Key: "vcs.time", Value: "2026-08-10T00:12:03Z"},
				{Key: "vcs.modified", Value: modifiedValue},
			},
		}
	}

	tests := []struct {
		name          string
		linkedVersion string
		linkedCommit  string
		info          *debug.BuildInfo
		infoOK        bool
		wantVersion   string
		wantCommit    string
	}{
		{
			name:          "release ldflags take precedence",
			linkedVersion: "1.2.3",
			linkedCommit:  "deadbeef",
			info:          vcsInfo("(devel)", true),
			infoOK:        true,
			wantVersion:   "1.2.3",
			wantCommit:    "deadbeef",
		},
		{
			name:          "commit-only ldflags remain unchanged",
			linkedVersion: developmentVersion,
			linkedCommit:  "deadbeef",
			info:          vcsInfo("(devel)", true),
			infoOK:        true,
			wantVersion:   developmentVersion,
			wantCommit:    "deadbeef",
		},
		{
			name:          "source build uses clean VCS metadata",
			linkedVersion: developmentVersion,
			info:          vcsInfo("(devel)", false),
			infoOK:        true,
			wantVersion:   "0.0.0-dev+0123456789ab (2026-08-10T00:12:03Z)",
		},
		{
			name:          "source build marks dirty worktree",
			linkedVersion: developmentVersion,
			info:          vcsInfo("(devel)", true),
			infoOK:        true,
			wantVersion:   "0.0.0-dev+0123456789ab (2026-08-10T00:12:03Z, dirty)",
		},
		{
			name:          "module version remains visible with VCS metadata",
			linkedVersion: developmentVersion,
			info:          vcsInfo("v1.2.3", false),
			infoOK:        true,
			wantVersion:   "v1.2.3 (0123456789ab, 2026-08-10T00:12:03Z)",
		},
		{
			name:          "Go pseudo-version does not duplicate revision or dirty marker",
			linkedVersion: developmentVersion,
			info:          vcsInfo("v0.0.0-20260810001203-0123456789ab+dirty", true),
			infoOK:        true,
			wantVersion:   "v0.0.0-20260810001203-0123456789ab+dirty (2026-08-10T00:12:03Z)",
		},
		{
			name:          "module version works without VCS settings",
			linkedVersion: developmentVersion,
			info:          &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}},
			infoOK:        true,
			wantVersion:   "v1.2.3",
		},
		{
			name:          "missing VCS metadata falls back to dev",
			linkedVersion: developmentVersion,
			info:          &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			infoOK:        true,
			wantVersion:   developmentVersion,
		},
		{
			name:          "missing build info falls back to dev",
			linkedVersion: developmentVersion,
			infoOK:        false,
			wantVersion:   developmentVersion,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotVersion, gotCommit := resolveBuildVersion(tc.linkedVersion, tc.linkedCommit, tc.info, tc.infoOK)
			if gotVersion != tc.wantVersion || gotCommit != tc.wantCommit {
				t.Fatalf("resolveBuildVersion() = (%q, %q), want (%q, %q)", gotVersion, gotCommit, tc.wantVersion, tc.wantCommit)
			}
		})
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit("deadbeef"); got != "deadbeef" {
		t.Fatalf("shortCommit(short) = %q, want deadbeef", got)
	}
	if got := shortCommit("0123456789abcdef"); got != "0123456789ab" {
		t.Fatalf("shortCommit(long) = %q, want 0123456789ab", got)
	}
}

func TestFormatHumanErrorPrintsNextLinesFromEnvelopeMetadata(t *testing.T) {
	err := registry.CodedError{
		Code:        registry.CodeInviteAPIUnauthorized,
		Message:     "create user invite was rejected by Tailscale as unauthenticated (HTTP 401)",
		Next:        credentials.NextAPIKeyBootstrap(),
		MessageOnly: true,
	}
	got := formatHumanError(err)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if lines[0] != "Error: "+err.Message {
		t.Fatalf("first line = %q, want Error: message", lines[0])
	}
	if len(lines) != 1+len(err.Next) {
		t.Fatalf("lines = %d, want error line plus %d next lines:\n%s", len(lines), len(err.Next), got)
	}
	for i, step := range err.Next {
		if lines[i+1] != "Next: "+step {
			t.Fatalf("line %d = %q, want %q", i+1, lines[i+1], "Next: "+step)
		}
	}
	if !strings.Contains(got, credentials.KeysPageURL) {
		t.Fatalf("human error output lacks the Keys page URL:\n%s", got)
	}
	if !reflect.DeepEqual(output.NextCommandsForError(err), err.Next) {
		t.Fatal("human next diverged from envelope next")
	}
	if plain := formatHumanError(errors.New("boom")); plain != "Error: boom\n" {
		t.Fatalf("plain error output = %q, want only the Error line", plain)
	}
}
