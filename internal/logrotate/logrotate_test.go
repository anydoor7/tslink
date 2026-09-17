package logrotate

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// openSupervisedLog reproduces what a supervisor hands the daemon: a log file
// already open for writing, with O_APPEND, that the daemon did not open itself.
func openSupervisedLog(t *testing.T, path string, content []byte) *os.File {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open %s O_APPEND: %v", path, err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

func sizeOf(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return string(sum[:])
}

// TestRotateArchivesAndTruncatesThroughTheDescriptor is the positive case, and
// the assertion that matters is the last one: after the truncate, a write
// through the same descriptor lands at offset 0. That is the whole reason
// O_APPEND is a precondition, and checking only the post-truncate size would
// pass on a descriptor that is about to produce a sparse file.
func TestRotateArchivesAndTruncatesThroughTheDescriptor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	content := bytes.Repeat([]byte("access line\n"), 2000)
	handle := openSupervisedLog(t, target, content)

	result, err := RotateStderrLog(handle, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if !result.Rotated {
		t.Fatalf("Rotated = false (%s); want a rotation of a %d byte file over a 1024 byte cap", result.Reason, len(content))
	}
	if result.SizeBefore != int64(len(content)) {
		t.Fatalf("SizeBefore = %d, want %d", result.SizeBefore, len(content))
	}

	archived, err := os.ReadFile(target + ArchiveSuffix)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !bytes.Equal(archived, content) {
		t.Fatalf("archive holds %d bytes, want the original %d", len(archived), len(content))
	}
	if info, err := os.Stat(target + ArchiveSuffix); err != nil {
		t.Fatalf("stat archive: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode = %v, want 0600", info.Mode().Perm())
	}
	if got := sizeOf(t, target); got != 0 {
		t.Fatalf("live log size after rotation = %d, want 0", got)
	}

	if _, err := handle.Write([]byte("after\n")); err != nil {
		t.Fatalf("write after rotation: %v", err)
	}
	if got := sizeOf(t, target); got != int64(len("after\n")) {
		t.Fatalf("live log size after the next write = %d, want %d; the descriptor resumed at its old offset and left a hole",
			got, len("after\n"))
	}
	// No partial archive was left behind.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".partial-") {
			t.Fatalf("a temporary archive survived: %s", entry.Name())
		}
	}
}

// TestRotateRefusesADescriptorWithoutAppend is the measured failure mode turned
// into a refusal. Without this guard the function would truncate, the offset
// would stay where it was, and the next write would restore the old size with a
// hole in front, which reads on disk as "rotation did nothing".
func TestRotateRefusesADescriptorWithoutAppend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("isAppendOnly has no implementation off unix; the refusing path is covered by TestRotateRefusesWhenAppendCannotBeVerified")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	content := bytes.Repeat([]byte("access line\n"), 2000)
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// O_WRONLY without O_APPEND: the shape `2>file` produces.
	handle, err := os.OpenFile(target, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open without append: %v", err)
	}
	defer handle.Close()
	// Advance the offset the way a running daemon would have.
	if _, err := handle.Seek(0, 2); err != nil {
		t.Fatalf("seek to end: %v", err)
	}

	before := digestOf(t, target)
	result, err := RotateStderrLog(handle, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if result.Rotated {
		t.Fatal("a descriptor without O_APPEND was rotated; the next write would have produced a sparse file")
	}
	if !strings.Contains(result.Reason, "O_APPEND") {
		t.Fatalf("Reason = %q, want it to name O_APPEND", result.Reason)
	}
	if digestOf(t, target) != before {
		t.Fatal("the refused path still modified the log")
	}
	if _, err := os.Stat(target + ArchiveSuffix); !os.IsNotExist(err) {
		t.Fatalf("the refused path still wrote an archive (stat err = %v)", err)
	}
}

// TestRotateLeavesADescriptorThatIsNotTheConfiguredLogAlone is the guard that
// makes this safe to run on a timer inside a process whose own tests execute
// the same code. Without it, any oversized O_APPEND stderr -- a CI log, a
// developer's `tslink serve 2>>notes.txt` -- would be truncated.
func TestRotateLeavesADescriptorThatIsNotTheConfiguredLogAlone(t *testing.T) {
	dir := t.TempDir()
	elsewhere := filepath.Join(dir, "somebody-elses.log")
	target := filepath.Join(dir, "tslink.err.log")
	content := bytes.Repeat([]byte("not ours\n"), 2000)
	handle := openSupervisedLog(t, elsewhere, content)
	if err := os.WriteFile(target, bytes.Repeat([]byte("ours\n"), 2000), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	elsewhereBefore, targetBefore := digestOf(t, elsewhere), digestOf(t, target)
	result, err := RotateStderrLog(handle, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if result.Rotated {
		t.Fatal("rotated a descriptor that is not the configured log file")
	}
	if digestOf(t, elsewhere) != elsewhereBefore {
		t.Fatal("the unrelated file the descriptor points at was modified")
	}
	if digestOf(t, target) != targetBefore {
		t.Fatal("the configured log file was modified even though nothing writes to it through this descriptor")
	}
}

// TestRotateSkipsUnderTheCap keeps the cap from being decorative.
func TestRotateSkipsUnderTheCap(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	content := []byte("small\n")
	handle := openSupervisedLog(t, target, content)

	result, err := RotateStderrLog(handle, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if result.Rotated {
		t.Fatal("rotated a file under the cap")
	}
	if result.SizeBefore != int64(len(content)) {
		t.Fatalf("SizeBefore = %d, want %d", result.SizeBefore, len(content))
	}
	if _, err := os.Stat(target + ArchiveSuffix); !os.IsNotExist(err) {
		t.Fatalf("an archive was written for a file under the cap (stat err = %v)", err)
	}
}

// TestRotateIgnoresANonRegularDescriptor covers the interactive and piped runs,
// which are the common case in a terminal and in `go test`.
//
// It asserts the reason, not just "did not rotate". A pipe also has no
// O_APPEND, so deleting the regular-file guard left this test green while the
// refusal came from an entirely different check -- caught by mutation, and the
// reason is the only thing that tells the two apart.
func TestRotateIgnoresANonRegularDescriptor(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	if err := os.WriteFile(target, bytes.Repeat([]byte("x"), 4096), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := digestOf(t, target)

	result, err := RotateStderrLog(writer, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if result.Rotated {
		t.Fatal("rotated through a pipe")
	}
	if !strings.Contains(result.Reason, "not a regular file") {
		t.Fatalf("Reason = %q, want the non-regular-file refusal; a pipe is also refused for having no O_APPEND, "+
			"so any weaker assertion here passes with the regular-file guard deleted", result.Reason)
	}
	if digestOf(t, target) != before {
		t.Fatal("a pipe descriptor still caused the configured log to be modified")
	}
}

// TestCopyPrefixStopsAtTheSizeItWasGiven pins the bound on the archive copy.
//
// It exercises copyPrefix directly rather than through RotateStderrLog,
// because the end-to-end shape cannot express the claim: RotateStderrLog takes
// its own Stat, so a test has no way to add bytes between that Stat and the
// copy, and an assertion of "archive == SizeBefore" is then satisfied by an
// unbounded copy too. That version of this test survived the mutation that
// removed the bound.
//
// The bound is load bearing under load: the file is appended to while rotation
// runs, and the truncate that follows discards whatever arrived, so an
// unbounded copy would both chase a moving end and archive bytes the caller is
// about to delete.
func TestCopyPrefixStopsAtTheSizeItWasGiven(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.log")
	destination := filepath.Join(dir, "source.log"+ArchiveSuffix)
	if err := os.WriteFile(source, bytes.Repeat([]byte("a"), 4096), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := copyPrefix(source, destination, 1500); err != nil {
		t.Fatalf("copyPrefix: %v", err)
	}
	archived, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(archived) != 1500 {
		t.Fatalf("archive holds %d bytes, want exactly the 1500 it was told to copy", len(archived))
	}
	if !bytes.Equal(archived, bytes.Repeat([]byte("a"), 1500)) {
		t.Fatal("archive content is not the leading prefix of the source")
	}
}

// TestRotateArchivesExactlyWhatItMeasured is the end-to-end companion: whatever
// the bound turns out to be, the archive and the reported decision must agree.
func TestRotateArchivesExactlyWhatItMeasured(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tslink.err.log")
	handle := openSupervisedLog(t, target, bytes.Repeat([]byte("a"), 4096))

	result, err := RotateStderrLog(handle, target, 1024)
	if err != nil {
		t.Fatalf("RotateStderrLog: %v", err)
	}
	if !result.Rotated {
		t.Fatalf("Rotated = false (%s)", result.Reason)
	}
	archived, err := os.ReadFile(target + ArchiveSuffix)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if int64(len(archived)) != result.SizeBefore {
		t.Fatalf("archive holds %d bytes but the decision was taken on %d", len(archived), result.SizeBefore)
	}
}
