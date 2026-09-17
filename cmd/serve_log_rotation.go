package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/monody0007/tslink/internal/logrotate"
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
)

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
