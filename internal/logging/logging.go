package logging

import (
	"log/slog"
	"os"
)

// Init initializes the default slog logger.
// If jsonOutput is true, uses JSON handler; otherwise uses text handler.
func Init(jsonOutput bool) {
	var handler slog.Handler
	if jsonOutput {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})
	}
	slog.SetDefault(slog.New(handler))
}
