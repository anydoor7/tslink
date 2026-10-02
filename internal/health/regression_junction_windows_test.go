package health

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/anydoor7/tslink/internal/registry"
	"golang.org/x/sys/windows"
)

func TestWindowsJunctionIsAValidDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	junction := filepath.Join(dir, "junction")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	svc := registry.Service{Type: registry.TypeFile, Path: target}
	if got := Probe(context.Background(), svc); got != "" {
		t.Fatal("ordinary directory positive control", got)
	}
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", junction, target).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture setup failed: %v %s", err, out)
	}
	info, err := os.Stat(junction)
	if err != nil || !info.IsDir() {
		t.Fatal("junction does not resolve to directory", err)
	}
	lstat, err := os.Lstat(junction)
	if err != nil {
		t.Fatal(err)
	}
	svc.Path = junction
	got := Probe(context.Background(), svc)
	t.Logf("junction Lstat mode=%s Stat.IsDir=%v Probe=%q", lstat.Mode(), info.IsDir(), got)
	if got != "" {
		t.Errorf("registered readable directory junction rejected: %s", got)
	}
	child := filepath.Join(target, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	svc.Path = filepath.Join(junction, "child")
	if got := Probe(context.Background(), svc); got != "" {
		t.Fatalf("directory beneath a junction rejected: %s", got)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}
	if f, err := openProbeFile(junction); err == nil {
		f.Close()
		t.Fatal("junction with a missing target opened successfully")
	}
	svc.Path = junction
	if got := Probe(context.Background(), svc); got != "health_file_unavailable" {
		t.Fatalf("missing junction target health=%q", got)
	}
}

func TestWindowsProbeModeClassification(t *testing.T) {
	for _, tc := range []struct {
		name            string
		entry, resolved os.FileMode
		want            bool
	}{
		{"directory", os.ModeDir | 0755, os.ModeDir | 0755, true},
		{"regular_file", 0666, 0666, true},
		{"symlink_directory", os.ModeSymlink | 0666, os.ModeDir | 0755, true},
		{"symlink_file", os.ModeSymlink | 0666, 0666, true},
		// Go 1.26 junction Lstat on the runner: ?rw-rw-rw-.
		{"irregular_junction_directory", os.ModeIrregular | 0666, os.ModeDir | 0755, true},
		{"irregular_directory", os.ModeIrregular | os.ModeDir | 0755, os.ModeDir | 0755, true},
		{"irregular_file", os.ModeIrregular | 0666, 0666, false},
		{"unresolved_irregular", os.ModeIrregular | 0666, os.ModeIrregular | 0666, false},
		{"pipe", os.ModeNamedPipe | 0666, os.ModeNamedPipe | 0666, false},
		{"device", os.ModeDevice | 0666, os.ModeDevice | 0666, false},
		{"special_entry_directory", os.ModeNamedPipe | 0666, os.ModeDir | 0755, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validWindowsProbeModes(tc.entry, tc.resolved); got != tc.want {
				t.Fatalf("entry=%s resolved=%s valid=%v, want %v", tc.entry, tc.resolved, got, tc.want)
			}
		})
	}
}

func TestWindowsFinalProbePath(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		noDrive    bool
		err        error
		oversized  bool
	}{
		{name: "dos_path", path: `\\?\C:\fixture`},
		{name: "long_path", path: `\\?\C:\` + strings.Repeat("folder\\", 50)},
		{name: "volume_without_drive_letter", path: `\\?\Volume{fixture}\directory`, noDrive: true},
		{name: "native_error", err: windows.ERROR_ACCESS_DENIED},
		{name: "volume_error", err: windows.ERROR_PATH_NOT_FOUND},
		{name: "oversized_path", oversized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got, err := finalProbePath(42, func(handle windows.Handle, p *uint16, size, flags uint32) (uint32, error) {
				calls++
				if handle != 42 {
					t.Fatalf("resolved a different handle: %d", handle)
				}
				if tc.noDrive && flags == 0 {
					return 0, windows.ERROR_PATH_NOT_FOUND
				}
				if tc.noDrive && flags != 1 {
					t.Fatalf("volume without a drive letter needs VOLUME_NAME_GUID: %d", flags)
				}
				if tc.err != nil {
					return 0, tc.err
				}
				if tc.oversized {
					return 32768, nil
				}
				name, err := windows.UTF16FromString(tc.path)
				if err != nil {
					t.Fatal(err)
				}
				if uint32(len(name)) > size {
					return uint32(len(name)), nil
				}
				copy(unsafe.Slice(p, int(size)), name)
				return uint32(len(name) - 1), nil
			})
			wantErr := tc.err
			if tc.oversized {
				wantErr = os.ErrInvalid
			}
			if !errors.Is(err, wantErr) || got != tc.path {
				t.Fatalf("path=%q error=%v, want %q / %v", got, err, tc.path, wantErr)
			}
			if calls == 0 || calls > 2 {
				t.Fatalf("path resolution calls=%d", calls)
			}
		})
	}
}

func TestWindowsProbeHandleRejectsInvalidAndDevicePaths(t *testing.T) {
	for _, path := range []string{"invalid\x00path", filepath.Join(t.TempDir(), "missing"), "NUL"} {
		if f, err := openProbeFile(path); err == nil {
			f.Close()
			t.Fatalf("invalid/device probe path accepted: %q", path)
		}
		if f, err := openProbeHandle(path, 0, windows.FILE_FLAG_BACKUP_SEMANTICS); err == nil {
			f.Close()
			t.Fatalf("invalid/device path accepted: %q", path)
		}
	}
}
