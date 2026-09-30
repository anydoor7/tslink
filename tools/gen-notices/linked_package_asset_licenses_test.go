package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedPackageAndAssetLicensesAreBundled(t *testing.T) {
	mods, err := linkedModules()
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := os.ReadFile(filepath.Join(root, outputFile))
	if err != nil {
		t.Fatal(err)
	}
	var xxhash []byte
	for _, mod := range mods {
		if mod.path == "github.com/klauspost/compress" {
			xxhash, err = os.ReadFile(filepath.Join(mod.dir, "zstd", "internal", "xxhash", "LICENSE.txt"))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(xxhash) == 0 {
		t.Fatal("positive control: linked xxhash source/license missing")
	}
	for name, data := range map[string][]byte{"generated": generated, "bundled": bundled} {
		if !bytes.Contains(data, bytes.TrimSpace(xxhash)) {
			t.Errorf("%s notices omit linked xxhash MIT license", name)
		}
		for _, want := range []string{"SIL OPEN FONT LICENSE Version 1.1", "The Inter Project Authors", "Copyright (c) Facebook, Inc. and its affiliates.", "Copyright (c) 2018 Jed Watson"} {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s notices omit embedded asset license %q", name, want)
			}
		}
	}
}

func TestLinkedNoticesFollowUsedPackageAndSourceHeaders(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"LICENSE":          "root notice",
		"used/LICENSE-MIT": "nested license for linked code",
		"used/linked.go":   "// Copyright Linked Contributor\n// Licensed under custom per-file terms.\n\npackage used\n",
		"unused/LICENSE":   "not linked notice",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mod := module{dir: root, packageDirs: []string{filepath.Join(root, "used")}, sourceFiles: []string{filepath.Join(root, "used", "linked.go")}}
	files, err := linkedNoticeFiles(mod, []noticeFile{{name: "LICENSE", text: "root notice"}})
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, file := range files {
		all.WriteString(file.name + "\n" + file.text + "\n")
	}
	for _, want := range []string{"used/LICENSE-MIT", "nested license for linked code", "Copyright Linked Contributor", "custom per-file terms"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("linked notice omitted %q: %s", want, all.String())
		}
	}
	if strings.Contains(all.String(), "not linked notice") {
		t.Fatal("collected an unrelated package's license")
	}
}

func TestEmbeddedAssetsRequireReviewedBytesAndLicensePayload(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "vendor.js")
	if err := os.WriteFile(path, []byte("vendor"), 0600); err != nil {
		t.Fatal(err)
	}
	mod := module{path: "example.com/asset-control", dir: root, embedFiles: []string{path}}
	key := mod.path + "/vendor.js"
	if _, err := embeddedNotices(mod); err == nil || !strings.Contains(err.Error(), "unreviewed embedded asset") {
		t.Fatalf("unknown vendor asset: %v", err)
	}
	t.Cleanup(func() { delete(embeddedAssetNotices, key) })
	embeddedAssetNotices[key] = assetNotice{sha256: fmt.Sprintf("%x", sha256.Sum256([]byte("vendor"))), name: "vendor license", text: "reviewed notice"}
	files, err := embeddedNotices(mod)
	if err != nil || len(files) != 1 || files[0].text != "reviewed notice" {
		t.Fatalf("positive reviewed control: files=%v err=%v", files, err)
	}
	asset := embeddedAssetNotices[key]
	asset.text = ""
	embeddedAssetNotices[key] = asset
	if _, err := embeddedNotices(mod); err == nil || !strings.Contains(err.Error(), "missing embedded asset license payload") {
		t.Fatalf("missing payload: %v", err)
	}
	asset.text = "reviewed notice"
	embeddedAssetNotices[key] = asset
	if err := os.WriteFile(path, []byte("changed vendor"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := embeddedNotices(mod); err == nil || !strings.Contains(err.Error(), "embedded asset changed") {
		t.Fatalf("changed asset: %v", err)
	}
}
