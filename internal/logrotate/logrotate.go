// Package logrotate bounds the size of a log file that a supervisor opened on
// this process's behalf.
//
// The problem it solves is specific. launchd's StandardErrorPath, systemd's
// StandardError=append: and tslink's own daemonize path all hand the daemon an
// already-open file descriptor. The daemon never opened that file and cannot
// reopen it, so the two rotation moves that work for a file you own are both
// wrong here:
//
//   - rename: the descriptor follows the inode, so the daemon keeps writing to
//     the renamed file and the original path simply stops receiving anything
//     until the supervisor restarts the process.
//   - truncate on a descriptor without O_APPEND: the file offset is unchanged,
//     so the next write lands at the old offset and the file instantly reports
//     its former size again, now with a hole in front.
//
// Truncate on a descriptor *with* O_APPEND is correct, because O_APPEND makes
// the kernel seek to end-of-file as part of every write. Measured on this
// machine on 2026-09-16: a writer started with `2>>` returned to 15 bytes after
// a truncate, and the same writer started with `2>` returned to 202015 bytes.
// The production daemon's fd 2 carries the AP flag (lsof -a -p <pid> -d 2 +fg),
// as does the descriptor internal/daemon opens for `serve --daemon`.
//
// So O_APPEND is a precondition, not an assumption: RotateStderrLog verifies it
// and refuses rather than producing the sparse file above.
package logrotate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DefaultMaxBytes is the size above which the log is rotated. Steady-state disk
// use is at most twice this, the live file plus one archived generation.
const DefaultMaxBytes int64 = 8 << 20

// ArchiveSuffix names the single retained generation. One generation, not N:
// the content is an access log whose value decays within hours, and every extra
// generation is another full-size copy of a file this package exists to stop
// from being large.
const ArchiveSuffix = ".1"

// ErrCannotVerifyAppend is returned by the platform probe when a descriptor's
// open flags cannot be read. It is deliberately not treated as "assume append".
var ErrCannotVerifyAppend = errors.New("cannot read the file descriptor's open flags")

// Result describes one rotation decision. Skipped is the common outcome and
// carries the reason, so a caller that logs it says why nothing happened rather
// than staying silent.
type Result struct {
	Rotated     bool
	Reason      string
	SizeBefore  int64
	ArchivePath string
}

// RotateStderrLog copies target aside and truncates it through the open
// descriptor f, but only when all four preconditions hold:
//
//  1. f is a regular file. An interactive run writes to a terminal and a test
//     usually writes to a pipe; neither is rotatable and neither is an error.
//  2. f was opened with O_APPEND. See the package comment.
//  3. f and target are the same inode. This is what makes the call safe to
//     place on a timer inside a process whose tests also reach this code: a
//     descriptor pointing anywhere other than the configured log file is left
//     alone, so the function cannot truncate a caller's stderr, a CI log, or a
//     developer's redirect.
//  4. The file is larger than maxBytes.
//
// The archive is written to a temporary file in the same directory and renamed
// into place, so a reader never observes a half-copied generation.
//
// What it loses, stated plainly: writes that land between the copy and the
// truncate are discarded. There is no way to avoid that from inside the writing
// process without stopping every other goroutine, and the window is the time it
// takes to copy at most maxBytes.
func RotateStderrLog(f *os.File, target string, maxBytes int64) (Result, error) {
	if f == nil {
		return Result{Reason: "no descriptor"}, nil
	}
	info, err := f.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("stat the open log descriptor: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Result{Reason: "the descriptor is not a regular file"}, nil
	}

	appendMode, err := isAppendOnly(f)
	if err != nil {
		return Result{Reason: "could not verify O_APPEND: " + err.Error()}, nil
	}
	if !appendMode {
		return Result{Reason: "the descriptor was not opened with O_APPEND; truncating it would leave a sparse file that immediately reports the old size"}, nil
	}

	targetInfo, err := os.Stat(target)
	if err != nil {
		return Result{Reason: "the configured log file is unreadable: " + err.Error()}, nil
	}
	if !os.SameFile(info, targetInfo) {
		return Result{Reason: "the descriptor is not the configured log file"}, nil
	}

	size := info.Size()
	if size <= maxBytes {
		return Result{Reason: "under the size cap", SizeBefore: size}, nil
	}

	archivePath := target + ArchiveSuffix
	if err := copyPrefix(target, archivePath, size); err != nil {
		return Result{SizeBefore: size, ArchivePath: archivePath}, err
	}
	if err := f.Truncate(0); err != nil {
		return Result{SizeBefore: size, ArchivePath: archivePath}, fmt.Errorf("truncate the log through its own descriptor: %w", err)
	}
	return Result{Rotated: true, SizeBefore: size, ArchivePath: archivePath, Reason: "rotated"}, nil
}

// copyPrefix writes the first size bytes of source to destination through a
// temporary file in the same directory.
//
// The copy is bounded by the size observed before the decision rather than
// reading to EOF: the file is being appended to concurrently, and an unbounded
// copy would chase a moving end while the truncate that follows discards
// whatever it managed to catch.
func copyPrefix(source, destination string, size int64) error {
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open the log for archiving: %w", err)
	}
	defer in.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".partial-*")
	if err != nil {
		return fmt.Errorf("create the archive temporary: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("restrict the archive temporary: %w", err)
	}
	if _, err := io.Copy(temporary, io.LimitReader(in, size)); err != nil {
		cleanup()
		return fmt.Errorf("copy the log into the archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("close the archive temporary: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("publish the archive: %w", err)
	}
	return nil
}
