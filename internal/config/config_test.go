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

func TestNodesDir(t *testing.T) {
	dir, err := NodesDir()
	if err != nil {
		t.Fatalf("NodesDir() error = %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("tslink", "nodes")) {
		t.Fatalf("NodesDir() = %q, want suffix %q", dir, filepath.Join("tslink", "nodes"))
	}
}

func TestAuthKeyPath(t *testing.T) {
	path, err := AuthKeyPath()
	if err != nil {
		t.Fatalf("AuthKeyPath() error = %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("tslink", "authkey")) {
		t.Fatalf("AuthKeyPath() = %q, want suffix %q", path, filepath.Join("tslink", "authkey"))
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
	nodesDir, _ := NodesDir()
	authKeyPath, _ := AuthKeyPath()
	logDir, _ := LogDir()

	for _, p := range []string{regPath, pidPath, nodesDir, authKeyPath, logDir} {
		if !strings.HasPrefix(p, dir) {
			t.Fatalf("path %q does not start with Dir() %q", p, dir)
		}
	}
}

func TestEnsureDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

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

	nodesDir, _ := NodesDir()
	info, err = os.Stat(nodesDir)
	if err != nil {
		t.Fatalf("nodes dir does not exist after EnsureDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("nodes dir is not a directory")
	}
}
