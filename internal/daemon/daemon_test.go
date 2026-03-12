package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWriteAndReadPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}

	if want := os.Getpid(); pid != want {
		t.Fatalf("ReadPID() = %d, want %d", pid, want)
	}
}

func TestReadPIDNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.pid")

	if _, err := ReadPID(path); err == nil {
		t.Fatal("ReadPID() error = nil, want error")
	}
}

func TestRemovePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	RemovePID(path)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("os.Stat() error = %v, want not exists", err)
	}
}

func TestIsRunningCurrentProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	if !IsRunning(path) {
		t.Fatal("IsRunning() = false, want true")
	}
}

func TestIsRunningDeadProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")

	if err := os.WriteFile(path, []byte(strconv.Itoa(99999999)+"\n"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	if IsRunning(path) {
		t.Skip("PID 99999999 appears to be running on this system")
	}
}
