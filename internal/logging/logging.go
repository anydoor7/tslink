package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/config"
)

// Init initializes the default slog logger.
// If jsonOutput is true, uses JSON handler; otherwise uses text handler.
func Init(jsonOutput bool) {
	var destination io.Writer = os.Stderr
	// Foreground services managed by systemd and Windows Startup otherwise
	// have no file sink for the CLI/MCP logs reader. Keep stderr available to
	// the supervisor while also writing the private log file it reads.
	if os.Getenv("TSLINK_MANAGED_LOGS") == "1" {
		dir, err := config.LogDir()
		if err == nil {
			err = atomicfile.EnsurePrivateDir(dir)
		}
		if err == nil {
			destination = io.MultiWriter(os.Stderr, managedLogWriter{filepath.Join(dir, "tslink.err.log")})
		} else {
			fmt.Fprintf(os.Stderr, "cannot initialize managed log file: %v\n", err)
		}
	}
	InitTo(destination, jsonOutput)
}

// Open for each record so rotation/replacement and Windows file cleanup do
// not depend on a process-lifetime handle. Each append retains private mode.
type managedLogWriter struct{ path string }

func (w managedLogWriter) Write(data []byte) (int, error) {
	if err := atomicfile.ConvergePrivateFile(w.path); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, atomicfile.PrivateFileMode)
	if err != nil {
		return 0, err
	}
	n, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return n, writeErr
	}
	return n, closeErr
}

// InitTo initializes slog to a specific writer. Tests use this to generate
// fixtures through the same producer path that the shipped process uses.
func InitTo(w io.Writer, jsonOutput bool) {
	var handler slog.Handler
	if jsonOutput {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{})
	} else {
		handler = slog.NewTextHandler(w, &slog.HandlerOptions{})
	}
	slog.SetDefault(slog.New(handler))
}

// TSNetUserLogf routes tsnet's user-facing messages through TSLink's logger.
// In particular, this keeps interactive authentication URLs out of the
// timestamped stdlib logger that tsnet falls back to when Server.UserLogf is
// nil.
func TSNetUserLogf(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...), "source", "tsnet")
}
