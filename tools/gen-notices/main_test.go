package main

import (
	"strings"
	"testing"
)

func TestSupportedReleaseTargetsCoverReleaseMatrix(t *testing.T) {
	got := map[string]bool{}
	for _, target := range supportedReleaseTargets {
		got[target.goos+"/"+target.goarch] = true
	}
	for _, want := range []string{
		"darwin/amd64",
		"darwin/arm64",
		"linux/amd64",
		"linux/arm64",
		"windows/amd64",
		"windows/arm64",
	} {
		if !got[want] {
			t.Fatalf("supportedReleaseTargets missing %s", want)
		}
	}
	if len(got) != 6 {
		t.Fatalf("supportedReleaseTargets = %v, want exactly 6 target pairs", got)
	}
}

func TestLinkedModulesForTargetsUnionsAndDedupesExactPathVersion(t *testing.T) {
	targets := []releaseTarget{
		{goos: "darwin", goarch: "amd64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "amd64"},
	}
	got, err := linkedModulesForTargets(targets, func(target releaseTarget) ([]module, error) {
		common := module{path: "example.com/common", version: "v1.0.0", dir: "/mods/common"}
		switch target.goos {
		case "darwin":
			return []module{common, {path: "example.com/darwin", version: "v1.0.0", dir: "/mods/darwin"}}, nil
		case "linux":
			return []module{common, {path: "example.com/linux", version: "v1.0.0", dir: "/mods/linux"}}, nil
		case "windows":
			return []module{
				common,
				{path: "example.com/windows", version: "v1.0.0", dir: "/mods/windows"},
				{path: "example.com/common", version: "v1.1.0", dir: "/mods/common-v1.1.0"},
			}, nil
		default:
			t.Fatalf("unexpected target %s/%s", target.goos, target.goarch)
			return nil, nil
		}
	})
	if err != nil {
		t.Fatalf("linkedModulesForTargets() error = %v", err)
	}
	var keys []string
	for _, mod := range got {
		keys = append(keys, mod.path+"@"+mod.version)
	}
	want := []string{
		"example.com/common@v1.0.0",
		"example.com/common@v1.1.0",
		"example.com/darwin@v1.0.0",
		"example.com/linux@v1.0.0",
		"example.com/windows@v1.0.0",
	}
	if strings.Join(keys, "\n") != strings.Join(want, "\n") {
		t.Fatalf("union keys:\n%s\nwant:\n%s", strings.Join(keys, "\n"), strings.Join(want, "\n"))
	}
}

func TestLinkedModulesIncludesSupportedTargetSpecificModules(t *testing.T) {
	mods, err := linkedModules()
	if err != nil {
		t.Fatalf("linkedModules() error = %v", err)
	}
	seen := map[string]bool{}
	for _, mod := range mods {
		seen[mod.path] = true
	}
	for _, want := range []string{
		"github.com/godbus/dbus/v5",
		"github.com/danieljoos/wincred",
		"golang.zx2c4.com/wireguard/windows",
	} {
		if !seen[want] {
			t.Fatalf("target-union module %s missing from linked module union", want)
		}
	}
}

func TestClassifyLicenseKnownFamilies(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"apache", "Apache License\nVersion 2.0, January 2004", "Apache-2.0"},
		{"mit", "Permission is hereby granted, free of charge, to any person obtaining a copy", "MIT"},
		{"isc", "Permission to use, copy, modify, and distribute this software for any purpose with or without fee", "ISC"},
		{"bsd3", "Redistribution and use in source and binary forms are permitted. Neither the name of the author nor the names of its contributors may be used.", "BSD-3-Clause"},
		{"bsd2", "Redistribution and use in source and binary forms are permitted provided that the following conditions are met.", "BSD-2-Clause"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyLicense([]noticeFile{{name: "LICENSE", text: tc.text}})
			if err != nil {
				t.Fatalf("classifyLicense() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("classifyLicense() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClassifyLicenseFailsClosed(t *testing.T) {
	if got, err := classifyLicense([]noticeFile{{name: "LICENSE", text: "custom terms"}}); err == nil {
		t.Fatalf("classifyLicense() = %q, want unknown-license error", got)
	}
}

func TestRenderContainsNoticePayloadAndNoPendingMarker(t *testing.T) {
	got := string(render([]module{{
		path:    "example.com/mod",
		version: "v1.2.3",
		dir:     "/tmp/mod",
		license: "MIT",
		files: []noticeFile{{
			name: "LICENSE",
			text: "Permission is hereby granted, free of charge\nCopyright Example",
		}},
	}}))
	for _, want := range []string{"example.com/mod", "v1.2.3", "MIT", "Permission is hereby granted", "Copyright Example"} {
		if !strings.Contains(got, want) {
			t.Fatalf("render() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "review pending") {
		t.Fatalf("render() contains review pending marker:\n%s", got)
	}
}
