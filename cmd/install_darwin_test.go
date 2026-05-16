//go:build darwin

package cmd

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistPath(t *testing.T) {
	old := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "/Users/testuser", nil }
	defer func() { userHomeDirFn = old }()

	got, err := plistPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join("/Users/testuser", "Library", "LaunchAgents", plistLabel+".plist")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestPlistPath_Error(t *testing.T) {
	old := userHomeDirFn
	userHomeDirFn = func() (string, error) { return "", fmt.Errorf("injected homedir error") }
	defer func() { userHomeDirFn = old }()

	_, err := plistPath()
	if err == nil {
		t.Fatal("expected error from userHomeDirFn")
	}
	if !strings.Contains(err.Error(), "injected homedir error") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPlistTemplateIncludesRestartThrottle(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:            plistLabel,
		Executable:       "/usr/local/bin/tslink",
		OutLog:           "/tmp/tslink.out.log",
		ErrLog:           "/tmp/tslink.err.log",
		ThrottleInterval: launchdThrottleInterval,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}

	plist := buf.String()
	for _, want := range []string{
		"<key>KeepAlive</key>",
		"<key>ThrottleInterval</key>",
		"<integer>30</integer>",
	} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
}

func TestPlistTemplateEscapesXMLPaths(t *testing.T) {
	var buf bytes.Buffer
	err := plistTemplate.Execute(&buf, plistData{
		Label:            plistLabel,
		Executable:       "/Applications/TSLink & Tools/<tslink>/tslink",
		OutLog:           "/tmp/tslink > out.log",
		ErrLog:           "/tmp/tslink < err.log",
		ThrottleInterval: launchdThrottleInterval,
	})
	if err != nil {
		t.Fatalf("plistTemplate.Execute() error = %v", err)
	}

	var parsed struct {
		XMLName xml.Name `xml:"plist"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("rendered plist is not well-formed XML: %v\n%s", err, buf.String())
	}
	for _, want := range []string{
		"/Applications/TSLink &amp; Tools/&lt;tslink&gt;/tslink",
		"/tmp/tslink &gt; out.log",
		"/tmp/tslink &lt; err.log",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("plist missing escaped path %q:\n%s", want, buf.String())
		}
	}
}

func TestInstallCommandBootoutThenBootstrapsLaunchAgentOnSuccess(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootstrap ok"), nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "installed and loaded in gui/501") {
		t.Fatalf("install output missing success domain: %s", out.String())
	}
}

func TestInstallCommandBootstrapsLaunchAgentAndSurfacesOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 501 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootstrap stderr"), errors.New("launchctl failed")
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/501/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/501", plistPath}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "bootstrap stderr") {
		t.Fatalf("install output did not surface launchctl output: %s", out.String())
	}
	plist, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read generated plist: %v", err)
	}
	if !strings.Contains(string(plist), "<key>ThrottleInterval</key>") {
		t.Fatalf("plist missing throttle interval:\n%s", plist)
	}
}

func TestInstallCommandFallsBackToUserDomainWhenGUIDomainMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	oldHome := userHomeDirFn
	oldExe := executablePathFn
	oldEval := evalSymlinksFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		executablePathFn = oldExe
		evalSymlinksFn = oldEval
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	executablePathFn = func() (string, error) { return "/Applications/TSLink App/tslink", nil }
	evalSymlinksFn = func(path string) (string, error) { return path, nil }
	userUIDFn = func() int { return 503 }

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		if len(args) >= 2 && args[0] == "bootstrap" && args[1] == "gui/503" {
			return []byte("Bootstrap failed: 113: Domain does not exist"), errors.New("bootstrap failed")
		}
		return []byte("user bootstrap ok"), nil
	}

	var out bytes.Buffer
	installCmd.SetOut(&out)
	err := installCmd.RunE(installCmd, nil)
	if err != nil {
		t.Fatalf("install RunE() error = %v", err)
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/503/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/503/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootstrap", "gui/503", plistPath}, "\x00"),
		strings.Join([]string{"bootstrap", "user/503", plistPath}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	for _, want := range []string{"SSH/headless", "user/503", "installed and loaded in user/503"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("install output = %q, want %q", out.String(), want)
		}
	}
}

func TestLaunchctlDomainNotFoundMatchesRealCouldNotFindDomainWording(t *testing.T) {
	if !launchctlDomainNotFound([]byte("Could not find domain for: gui/503"), errors.New("bootstrap failed")) {
		t.Fatal("expected real launchctl domain-missing wording to be matched")
	}
}

func TestUninstallCommandBootoutsLaunchAgentOnSuccess(t *testing.T) {
	home := t.TempDir()

	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 504 }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootout ok"), nil
	}

	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	wantCalls := []string{strings.Join([]string{"bootout", "gui/504/" + plistLabel}, "\x00")}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "LaunchAgent removed") {
		t.Fatalf("uninstall output missing success: %s", out.String())
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, stat error = %v", err)
	}
}

func TestUninstallCommandBootoutsLaunchAgentAndSurfacesOutput(t *testing.T) {
	home := t.TempDir()

	oldHome := userHomeDirFn
	oldUID := userUIDFn
	oldLaunchctl := launchctlCombinedOutput
	t.Cleanup(func() {
		userHomeDirFn = oldHome
		userUIDFn = oldUID
		launchctlCombinedOutput = oldLaunchctl
	})

	userHomeDirFn = func() (string, error) { return home, nil }
	userUIDFn = func() int { return 502 }

	plistPath := filepath.Join(home, "Library", "LaunchAgents", plistLabel+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(plistPath, []byte("plist"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var gotCalls []string
	launchctlCombinedOutput = func(args ...string) ([]byte, error) {
		gotCalls = append(gotCalls, strings.Join(args, "\x00"))
		return []byte("bootout stderr"), errors.New("bootout failed")
	}

	var out bytes.Buffer
	uninstallCmd.SetOut(&out)
	err := uninstallCmd.RunE(uninstallCmd, nil)
	if err != nil {
		t.Fatalf("uninstall RunE() error = %v", err)
	}

	wantCalls := []string{
		strings.Join([]string{"bootout", "gui/502/" + plistLabel}, "\x00"),
		strings.Join([]string{"bootout", "user/502/" + plistLabel}, "\x00"),
	}
	if strings.Join(gotCalls, "\n") != strings.Join(wantCalls, "\n") {
		t.Fatalf("launchctl calls = %q, want %q", gotCalls, wantCalls)
	}
	if !strings.Contains(out.String(), "bootout stderr") {
		t.Fatalf("uninstall output did not surface launchctl output: %s", out.String())
	}
	if _, err := os.Stat(plistPath); !os.IsNotExist(err) {
		t.Fatalf("plist should be removed, stat error = %v", err)
	}
}
