package logging

import (
	"io"
	"log/slog"
	"os"
)

// Init initializes the default slog logger.
// If jsonOutput is true, uses JSON handler; otherwise uses text handler.
func Init(jsonOutput bool) {
	InitTo(os.Stderr, jsonOutput)
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
