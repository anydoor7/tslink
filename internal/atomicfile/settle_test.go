package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func notExist(path string) error {
	return &os.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
}

func ownedTemp(t *testing.T, path string) string {
	t.Helper()
	name, err := randomTempName(filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(filepath.Dir(path), name)
	if err := os.WriteFile(temp, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	return temp
}

func TestReadSettledGenuineAbsenceReturnsAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	calls := 0
	err := readSettled(path, func() error { calls++; return notExist(path) }, func(time.Duration) { t.Fatal("slept on genuine absence") })
	if !errors.Is(err, fs.ErrNotExist) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	// An unrelated or foreign temporary is not writer evidence.
	for _, name := range []string{".state.json.tmp", ".state.json.zz.tmp", ".state.json." + strings.Repeat("g", 32) + ".tmp", ".other.json." + strings.Repeat("a", 32) + ".tmp", "state.json.tmp"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(path), name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	calls = 0
	err = readSettled(path, func() error { calls++; return notExist(path) }, func(time.Duration) { t.Fatal("slept for a non-owned temporary") })
	if !errors.Is(err, fs.ErrNotExist) || calls != 1 {
		t.Fatalf("foreign temporaries: err=%v calls=%d", err, calls)
	}
}

func TestReadSettledWaitsWhileReplacementInProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	temp := ownedTemp(t, path)
	calls := 0
	var sleeps []time.Duration
	err := readSettled(path, func() error {
		calls++
		if calls < 3 {
			return notExist(path)
		}
		return nil
	}, func(d time.Duration) { sleeps = append(sleeps, d) })
	if err != nil || calls != 3 || len(sleeps) != 2 || sleeps[0] != time.Millisecond || sleeps[1] != 2*time.Millisecond {
		t.Fatalf("err=%v calls=%d sleeps=%v", err, calls, sleeps)
	}
	if !ReplacementInProgress(path) {
		t.Fatal("owned temporary not recognised")
	}
	if err := os.Remove(temp); err != nil {
		t.Fatal(err)
	}
	if ReplacementInProgress(path) {
		t.Fatal("replacement reported without a temporary")
	}
}

func TestReadSettledRereadsWhenTargetReappeared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := readSettled(path, func() error {
		calls++
		if calls == 1 {
			return notExist(path) // The replacement completed after this read.
		}
		return nil
	}, func(time.Duration) { t.Fatal("slept although the target is back") })
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestReadSettledIsBoundedWithStaleTemporary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ownedTemp(t, path) // A crashed writer's leftover.
	calls := 0
	var total time.Duration
	err := readSettled(path, func() error { calls++; return notExist(path) }, func(d time.Duration) { total += d })
	if !errors.Is(err, fs.ErrNotExist) || calls != settleAttempts || total != 255*time.Millisecond {
		t.Fatalf("err=%v calls=%d slept=%s", err, calls, total)
	}
}

func TestReadSettledReturnsOtherErrorsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ownedTemp(t, path)
	permanent := errors.New("permission denied")
	calls := 0
	err := readSettled(path, func() error { calls++; return permanent }, func(time.Duration) { t.Fatal("slept on a non-not-exist error") })
	if err != permanent || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if runtime.GOOS != "windows" {
		calls = 0
		if err := ReadSettled(path, func() error { calls++; return notExist(path) }); !errors.Is(err, fs.ErrNotExist) || calls != 1 {
			t.Fatalf("non-Windows ReadSettled must read once: err=%v calls=%d", err, calls)
		}
	}
}

func TestConvergePrivateFileVanishingDuringChmodIsMissing(t *testing.T) {
	restoreAtomicFileHooks(t)
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Lstat sees the file with a non-private mode; the name then disappears
	// before chmod, as during a Windows replacement.
	lstatFn = func(string) (os.FileInfo, error) {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		return fakeModeInfo{info, 0o644}, nil
	}
	chmodFn = func(string, os.FileMode) error { return notExist(path) }
	if err := ConvergePrivateFile(path); err != nil {
		t.Fatalf("vanished file must read as missing: %v", err)
	}
	denied := errors.New("access denied")
	chmodFn = func(string, os.FileMode) error { return denied }
	if err := ConvergePrivateFile(path); err != denied {
		t.Fatalf("other chmod errors must surface: %v", err)
	}
}

type fakeModeInfo struct {
	os.FileInfo
	mode os.FileMode
}

func (f fakeModeInfo) Mode() os.FileMode { return f.mode }
