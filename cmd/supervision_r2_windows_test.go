//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapWindowsStartupRegistration(t *testing.T) {
	dir := isolateBootstrap(t)
	isolateWindowsStartupInstall(t)
	path, err := windowsStartupScriptPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(windowsConfigEnvironment(dir)+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := detectSupervision(filepath.Join(dir, "tslink.pid"), true, 4242)
	if s.Manager != "windows-startup" || !s.Installed || !s.Autostart || s.RestartOnExit {
		t.Fatalf("startup=%+v", s)
	}
	if err := os.WriteFile(path, []byte(windowsConfigEnvironment(dir+"other")+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s = detectSupervision(filepath.Join(dir, "tslink.pid"), true, 4242)
	if s.Manager != "manual" || s.Autostart || s.Installed {
		t.Fatalf("foreign startup accepted: %+v", s)
	}
}
