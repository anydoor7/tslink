package release_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const releaseVerifyDocsPassLine = "[PASS]    licence files and project documents present in every archive"

// TestReleaseVerifyScriptFailsWithoutArchives pins R4-12. With an empty dist
// directory the licence/document loop had nothing to check: bash 5 printed
// its PASS line anyway, and bash 3.2 (macOS /bin/bash) died on the unbound
// empty array before the summary. Zero archives must be a BLOCKED gate on
// both. The complete-archive case is the control that proves the PASS line
// can be matched at all; the missing-file case keeps the loop honest.
func TestReleaseVerifyScriptFailsWithoutArchives(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripts/release-verify.sh is a maintainer-only bash script for macOS and Linux hosts")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if version, err := exec.Command(bash, "--version").Output(); err == nil {
		t.Logf("bash: %s", strings.SplitN(string(version), "\n", 2)[0])
	}
	script := filepath.Join(repoRoot(t), "scripts", "release-verify.sh")
	required := []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md", "COMMERCIAL.md"}

	for _, tc := range []struct {
		name     string
		archive  []string
		want     []string
		wantNone []string
	}{
		{name: "no archives",
			want:     []string{"[BLOCKED] no archives to check for licence files and project documents", "Summary:", "RESULT: NOT VERIFIED"},
			wantNone: []string{releaseVerifyDocsPassLine, "unbound variable"}},
		{name: "one complete archive", archive: required,
			want: []string{releaseVerifyDocsPassLine, "Summary:"}},
		{name: "archive missing NOTICE", archive: []string{"LICENSE", "THIRD_PARTY_NOTICES.md", "COMMERCIAL.md"},
			want:     []string{"missing NOTICE in tslink_0.0.0_linux_arm64.tar.gz", "[BLOCKED] a licence file or project document is missing from some archive", "Summary:"},
			wantNone: []string{releaseVerifyDocsPassLine}},
		{name: "archive missing COMMERCIAL.md", archive: []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"},
			want:     []string{"missing COMMERCIAL.md in tslink_0.0.0_linux_arm64.tar.gz", "[BLOCKED] a licence file or project document is missing from some archive", "Summary:"},
			wantNone: []string{releaseVerifyDocsPassLine}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := t.TempDir()
			if tc.archive != nil {
				writeReleaseArchive(t, filepath.Join(dist, "tslink_0.0.0_linux_arm64.tar.gz"), tc.archive)
			}
			cmd := exec.Command(bash, script, "--dist", dist)
			// No goreleaser, cosign, syft or gh on this PATH: Phase 1 blocks
			// without running any of them, and the run stays hermetic.
			cmd.Env = append(os.Environ(), "PATH=/usr/bin:/bin")
			cmd.Dir = t.TempDir()
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("release-verify.sh exited 0 for an incomplete dist:\n%s", out)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, forbidden := range tc.wantNone {
				if strings.Contains(string(out), forbidden) {
					t.Errorf("output contains %q:\n%s", forbidden, out)
				}
			}
		})
	}
}

func writeReleaseArchive(t *testing.T, path string, files []string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, name := range append([]string{"tslink"}, files...) {
		body := []byte("fixture\n")
		if err := tw.WriteHeader(&tar.Header{Name: "tslink_0.0.0_linux_arm64/" + name, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
