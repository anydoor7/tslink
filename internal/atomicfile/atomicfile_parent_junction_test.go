package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// irregularInfo is what os.Lstat reports on Windows (Go 1.23 and later) for a
// directory junction: ModeIrregular, and neither ModeDir nor ModeSymlink.
type irregularInfo struct {
	os.FileInfo
}

func (irregularInfo) Mode() os.FileMode { return os.ModeIrregular | 0o666 }
func (irregularInfo) IsDir() bool       { return false }

// TestWriteFileInExistingDirFollowsAnIrregularParentToItsTarget is R4-8 through
// the seams, so it runs on every OS. validateExistingParent followed only
// ModeSymlink, so a parent that Lstat reports as ModeIrregular -- a Windows
// junction, e.g. a redirected Startup folder -- was judged on the link itself
// and refused as "not a directory".
//
// The cases are the pair that decides it: an irregular parent that resolves to
// a directory is written, and one that resolves to a file is still refused
// without writing anything. A fix that simply stopped checking would pass the
// first and fail the second.
func TestWriteFileInExistingDirFollowsAnIrregularParentToItsTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resolveTo func(t *testing.T, dir string) (os.FileInfo, error)
		wantErr   string
	}{
		{
			name: "resolves to a directory",
			resolveTo: func(t *testing.T, dir string) (os.FileInfo, error) {
				return os.Stat(dir)
			},
		},
		{
			name: "resolves to a file",
			resolveTo: func(t *testing.T, dir string) (os.FileInfo, error) {
				file := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(file, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return os.Stat(file)
			},
			wantErr: "is not a directory",
		},
		{
			name: "does not resolve",
			resolveTo: func(t *testing.T, dir string) (os.FileInfo, error) {
				return nil, os.ErrNotExist
			},
			wantErr: "validate parent for",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restoreAtomicFileHooks(t)
			dir := t.TempDir()
			real, err := os.Lstat(dir)
			if err != nil {
				t.Fatal(err)
			}
			origLstat, origStat := lstatFn, statFn
			lstatFn = func(path string) (os.FileInfo, error) {
				if path == dir {
					return irregularInfo{FileInfo: real}, nil
				}
				return origLstat(path)
			}
			statCalls := 0
			statFn = func(path string) (os.FileInfo, error) {
				if path == dir {
					statCalls++
					return tc.resolveTo(t, dir)
				}
				return origStat(path)
			}
			target := filepath.Join(dir, "tslink.vbs")

			err = WriteFileInExistingDir(target, []byte("script\r\n"), 0o600)
			if statCalls != 1 {
				t.Fatalf("the irregular parent was resolved %d times, want once", statCalls)
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("WriteFileInExistingDir() error = %v, want the junction followed to its directory", err)
				}
				if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "script\r\n" {
					t.Fatalf("target = %q, %v; want the written script", data, readErr)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("WriteFileInExistingDir() error = %v, want %q", err, tc.wantErr)
			}
			if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused parent still received the file (lstat err = %v)", statErr)
			}
		})
	}
}
