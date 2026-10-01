package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/anydoor7/tslink/internal/logrotate"
)

// stderrLogFileName is the file every supervisor redirects the daemon's stderr
// to: launchd's StandardErrorPath, the systemd unit, and the descriptor
// internal/daemon opens for `serve --daemon`.
const stderrLogFileName = "tslink.err.log"

var (
	// stderrLogRotateFn is the seam the wiring tests replace. The real function
	// refuses unless the descriptor is the configured log file opened with
	// O_APPEND, so leaving it unstubbed in a test is also safe.
	stderrLogRotateFn = logrotate.RotateStderrLog
	// stderrLogFileFn returns the descriptor to bound. It is a function rather
	// than os.Stderr read inline so a test can hand in a file it owns.
	stderrLogFileFn = func() *os.File { return os.Stderr }
	// stderrLogRotateInterval is long because the check is a Stat and the growth
	// it guards against is measured in hundreds of kilobytes per day.
	stderrLogRotateInterval = 5 * time.Minute
	stderrLogMaxBytes       = logrotate.DefaultMaxBytes
	// stderrLogDegradedRepeatInterval is how long a decision that will not fix
	// itself stays quiet before it is stated again. It is an hour rather than
	// zero (a line every tick, i.e. this code feeding the growth it bounds) and
	// rather than never (the failure mode below).
	stderrLogDegradedRepeatInterval = time.Hour
	// chmodLogFileFn is the seam for the one-time permission narrowing below.
	chmodLogFileFn = os.Chmod
)

// supervisedLogFileMode is the mode the daemon's own log files are narrowed to
// at startup. It is the same value internal/daemon creates them with; the
// duplication is deliberate, because the two are reached on different paths and
// tying them together would suggest one of them can be skipped.
const supervisedLogFileMode os.FileMode = 0o600

// narrowSupervisedLogModes restricts the daemon's own log files to owner-only,
// once, at daemon startup.
//
// internal/daemon creates these 0600 and narrows them on open, but that only
// covers `serve --daemon`, where tslink itself opens the files. Under launchd
// and systemd the supervisor opens them and hands the daemon the descriptors:
// the plist names StandardOutPath and StandardErrorPath and sets no Umask, so
// launchd creates them with its own umask and they land 0644. The daemon never
// opens those files, so nothing in the create path can reach them -- which left
// the live log of a launchd-supervised install readable by every local user,
// while only the rotated .1 archive was narrowed.
//
// Rotated archives are narrowed alongside the live files. logrotate caps every
// archive it writes at 0600, so this only ever finds one written by a build
// that predated that cap -- but an archive is never reopened, so nothing else
// will ever reach it and it stays as wide as it was created for as long as it
// exists. Such a .1 can hold months of access lines at 0644 next to a live log
// at 0600.
//
// The narrowing is by path rather than through os.Stderr, and that is not an
// oversight: in a foreground run stderr is a terminal, and calling Chmod on
// that descriptor would change the mode of the user's tty. Only a regular file
// at the configured log path is touched.
//
// Every failure is logged and none is fatal. A daemon that refuses to start
// because it could not tighten a log file leaves the operator with no service
// and no log to diagnose it from, which is worse than the mode.
func narrowSupervisedLogModes(logDir string) {
	sources := make([]string, 0, 2*len(logSources))
	for _, name := range logSources {
		sources = append(sources, name, name+logrotate.ArchiveSuffix)
	}
	sort.Strings(sources)

	for _, name := range sources {
		path := filepath.Join(logDir, name)
		info, err := os.Stat(path)
		if err != nil {
			// Absent is the ordinary case for a log the supervisor has not
			// created yet, and for the source this deployment does not use.
			if !os.IsNotExist(err) {
				slog.Warn("could not inspect daemon log file permissions", "path", path, "error", err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if extra := info.Mode().Perm() &^ supervisedLogFileMode; extra == 0 {
			continue
		}
		if err := chmodLogFileFn(path, supervisedLogFileMode); err != nil {
			slog.Warn("could not restrict daemon log file permissions", "path", path,
				"had_mode", info.Mode().Perm().String(), "want_mode", supervisedLogFileMode.String(), "error", err)
			continue
		}
		slog.Info("restricted daemon log file permissions", "path", path,
			"had_mode", info.Mode().Perm().String(), "mode", supervisedLogFileMode.String())
	}
}

// startStderrLogRotation bounds the daemon's own stderr log for the lifetime of
// ctx and returns a channel closed when the goroutine has stopped.
//
// It lives in the daemon rather than in an external rotator because the daemon
// is the only process that holds the descriptor. An outside tool can rename the
// file (the daemon keeps writing to the renamed inode and the path goes quiet)
// or truncate it (correct here, but only because launchd happens to open with
// O_APPEND, which the outside tool cannot check for a descriptor it does not
// own). The writer can check, and does.
//
// The first decision is logged, including a refusal, and later identical
// decisions are not. One line at startup is what separates "installed and
// nothing to do" from "never installed"; a line every five minutes would be
// this function contributing to the growth it exists to stop.
//
// Two decisions are exempt from that silence, because for them it is the wrong
// default. A rotation error and a logrotate.Result marked Degraded both mean
// rotation has stopped and will not restart on its own -- the operator deleted
// the log, or something replaced it, and the daemon is still appending to the
// old inode with nothing bounding it. Announced once and never again, that
// state is indistinguishable in the log from a healthy daemon under the size
// cap. They are restated every stderrLogDegradedRepeatInterval instead.
func startStderrLogRotation(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})

	logDir, err := serveLogDirFn()
	if err != nil {
		slog.Warn("stderr log rotation disabled: cannot resolve the log directory", "error", err)
		close(done)
		return done
	}
	// Before anything else this process does with these files: a log the
	// supervisor created is still carrying the supervisor's umask.
	narrowSupervisedLogModes(logDir)

	target := filepath.Join(logDir, stderrLogFileName)

	go func() {
		defer close(done)
		announced := ""
		announcedAt := time.Time{}
		// due reports whether this decision has earned a line: a change of
		// decision always has, and a decision that will not fix itself has
		// again once the repeat interval has passed.
		due := func(reason string, degraded bool) bool {
			return reason != announced ||
				(degraded && time.Since(announcedAt) >= stderrLogDegradedRepeatInterval)
		}
		rotate := func() {
			result, err := stderrLogRotateFn(stderrLogFileFn(), target, stderrLogMaxBytes)
			switch {
			case err != nil:
				reason := "rotation returned an error: " + err.Error()
				if due(reason, true) {
					slog.Warn("stderr log rotation failed", "path", target, "error", err,
						"repeat_after", stderrLogDegradedRepeatInterval)
					announced, announcedAt = reason, time.Now()
				}
			case result.Rotated:
				slog.Info("stderr log rotated", "path", target, "archive", result.ArchivePath, "bytes", result.SizeBefore)
			case due(result.Reason, result.Degraded):
				if result.Degraded {
					slog.Warn("stderr log rotation is off and will not resume on its own", "path", target,
						"decision", result.Reason, "repeat_after", stderrLogDegradedRepeatInterval)
				} else {
					slog.Info("stderr log rotation active", "path", target,
						"max_bytes", stderrLogMaxBytes, "decision", result.Reason, "bytes", result.SizeBefore)
				}
				announced, announcedAt = result.Reason, time.Now()
			}
			if result.Rotated {
				announced, announcedAt = "", time.Time{}
			}
		}

		// Once immediately: a daemon that starts against an already-oversized
		// log should not wait out a full interval before bounding it.
		rotate()

		ticker := time.NewTicker(stderrLogRotateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rotate()
			}
		}
	}()
	return done
}
