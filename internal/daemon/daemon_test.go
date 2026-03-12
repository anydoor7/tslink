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

func TestWritePIDCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deep", "tslink.pid")

	if err := WritePID(path); err != nil {
		t.Fatalf("WritePID() error = %v", err)
	}

	pid, err := ReadPID(path)
	if err != nil {
		t.Fatalf("ReadPID() error = %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadPID() = %d, want %d", pid, os.Getpid())
	}
}

func TestReadPIDInvalidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("not-a-number\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	_, err := ReadPID(path)
	if err == nil {
		t.Fatal("ReadPID() with invalid content should return error")
	}
}

func TestIsRunningNoPIDFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for nonexistent PID file, want false")
	}
}

func TestIsRunningInvalidPID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tslink.pid")
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	if IsRunning(path) {
		t.Fatal("IsRunning() = true for invalid PID content, want false")
	}
}

func TestRemovePIDNonexistent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.pid")
	// Should not panic
	RemovePID(path)
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
