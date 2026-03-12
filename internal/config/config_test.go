package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}

	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "tslink")
	if dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
}

func TestRegistryPath(t *testing.T) {
	path, err := RegistryPath()
	if err != nil {
		t.Fatalf("RegistryPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "registry.json") {
		t.Fatalf("RegistryPath() = %q, want suffix registry.json", path)
	}
}

func TestPIDPath(t *testing.T) {
	path, err := PIDPath()
	if err != nil {
		t.Fatalf("PIDPath() error = %v", err)
	}
	if !strings.HasSuffix(path, "tslink.pid") {
		t.Fatalf("PIDPath() = %q, want suffix tslink.pid", path)
	}
}

func TestTsnetStateDir(t *testing.T) {
	dir, err := TsnetStateDir()
	if err != nil {
		t.Fatalf("TsnetStateDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, "tsnet-state") {
		t.Fatalf("TsnetStateDir() = %q, want suffix tsnet-state", dir)
	}
}

func TestLogDir(t *testing.T) {
	dir, err := LogDir()
	if err != nil {
		t.Fatalf("LogDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, "logs") {
		t.Fatalf("LogDir() = %q, want suffix logs", dir)
	}
}

func TestAllPathsSharePrefix(t *testing.T) {
	dir, _ := Dir()
	regPath, _ := RegistryPath()
	pidPath, _ := PIDPath()
	stateDir, _ := TsnetStateDir()
	logDir, _ := LogDir()

	for _, p := range []string{regPath, pidPath, stateDir, logDir} {
		if !strings.HasPrefix(p, dir) {
			t.Fatalf("path %q does not start with Dir() %q", p, dir)
		}
	}
}

func TestEnsureDir(t *testing.T) {
	if err := EnsureDir(); err != nil {
		t.Fatalf("EnsureDir() error = %v", err)
	}

	dir, _ := Dir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("config dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("config dir is not a directory")
	}

	logDir, _ := LogDir()
	info, err = os.Stat(logDir)
	if err != nil {
		t.Fatalf("log dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("log dir is not a directory")
	}
}
