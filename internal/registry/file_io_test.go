package registry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestRegistrySharingErrorClassifier(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"sharing", syscall.Errno(32), true},
		{"denied", syscall.Errno(5), true},
		{"path", &os.PathError{Op: "open", Path: "registry.json", Err: syscall.Errno(32)}, true},
		{"link", &os.LinkError{Op: "rename", Old: "temp", New: "registry.json", Err: syscall.Errno(5)}, true},
		{"wrapped", fmt.Errorf("write registry: %w", syscall.Errno(32)), true},
		{"missing", syscall.Errno(2), false},
		{"disk full", syscall.Errno(112), false},
		{"generic permission", os.ErrPermission, false},
		{"message only", errors.New("The process cannot access the file because it is being used by another process."), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isWindowsSharingError(tc.err); got != tc.want {
				t.Fatalf("classifier(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRegistrySharingRetryPolicy(t *testing.T) {
	sharing, denied := syscall.Errno(32), syscall.Errno(5)
	permanent := errors.New("disk error")
	for _, tc := range []struct {
		name       string
		sequence   []error
		wantCalls  int
		wantSleeps []time.Duration
		wantErr    error
	}{
		{"success", []error{nil}, 1, nil, nil},
		{"transient", []error{sharing, denied, nil}, 3, []time.Duration{time.Millisecond, 2 * time.Millisecond}, nil},
		{"other error", []error{permanent}, 1, nil, permanent},
		{"stop on other error", []error{sharing, permanent, nil}, 2, []time.Duration{time.Millisecond}, permanent},
		{"exhaustion", []error{denied}, 7, []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond, 16 * time.Millisecond, 32 * time.Millisecond}, denied},
		{"last attempt succeeds", []error{sharing, sharing, sharing, sharing, sharing, sharing, nil}, 7, []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 8 * time.Millisecond, 16 * time.Millisecond, 32 * time.Millisecond}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var sleeps []time.Duration
			err := retryRegistrySharingViolation(func() error {
				index := min(calls, len(tc.sequence)-1)
				calls++
				if calls > 8 {
					t.Fatal("unbounded retries")
				}
				return tc.sequence[index]
			}, func(d time.Duration) { sleeps = append(sleeps, d) })
			if err != tc.wantErr || calls != tc.wantCalls || len(sleeps) != len(tc.wantSleeps) {
				t.Fatalf("error=%v calls=%d sleeps=%v, want %v/%d/%v", err, calls, sleeps, tc.wantErr, tc.wantCalls, tc.wantSleeps)
			}
			for i, want := range tc.wantSleeps {
				if sleeps[i] != want {
					t.Fatalf("sleep %d = %v, want %v", i, sleeps[i], want)
				}
			}
		})
	}
}

func TestRegistryReadSharingPolicyCoversEveryLoader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	data := []byte(`{"schema_version":1,"services":[]}`)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	old := readRegistryFile
	t.Cleanup(func() { readRegistryFile = old })
	for _, tc := range []struct {
		name string
		load func() error
	}{
		{"Load", func() error { _, err := Load(path); return err }},
		{"LoadForRuntime", func() error { _, _, err := LoadForRuntime(path); return err }},
		{"LoadWithFileState", func() error { _, _, err := LoadWithFileState(path); return err }},
		{"LoadForDiagnostics", func() error { _, _, err := LoadForDiagnostics(path); return err }},
		{"Preflight", func() error { _, _, err := Preflight(path); return err }},
		{"loadForMutation", func() error { _, err := loadForMutation(path); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			readRegistryFile = func(string) ([]byte, error) {
				calls++
				if calls == 1 {
					return nil, &os.PathError{Op: "open", Path: path, Err: syscall.Errno(32)}
				}
				if calls == 2 {
					return nil, &os.PathError{Op: "open", Path: path, Err: syscall.Errno(5)}
				}
				return data, nil
			}
			err := tc.load()
			if runtime.GOOS == "windows" {
				if err != nil || calls != 3 {
					t.Fatalf("transient read: calls=%d err=%v", calls, err)
				}
			} else if !errors.Is(err, syscall.Errno(32)) || calls != 1 {
				t.Fatalf("non-Windows error changed: calls=%d err=%v", calls, err)
			}
			permanent := errors.New("unreadable registry")
			calls = 0
			readRegistryFile = func(string) ([]byte, error) { calls++; return nil, permanent }
			if err := tc.load(); err != permanent || calls != 1 {
				t.Fatalf("permanent read: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestRegistryReadSharingFileErrors(t *testing.T) {
	if _, err := readRegistryFileOnce(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing file: %v", err)
	}
	if _, err := readRegistryFileOnce("invalid\x00path"); err == nil {
		t.Fatal("NUL path accepted")
	}
	if _, err := readRegistryFileOnce(t.TempDir()); err == nil {
		t.Fatal("directory read accepted")
	}
}
