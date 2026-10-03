package server

import (
	"github.com/anydoor7/tslink/internal/atomicfile"
	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/registry"
)

// Counter status uses generic messages; raw I/O errors belong in daemon logs.
func (s *Server) guestCounterWarningsLocked() map[string]inspect.WarningView {
	warnings := make(map[string]inspect.WarningView)
	for name, node := range s.nodes {
		gate, ok := node.handlerCloser.(*guestGate)
		if !ok {
			continue
		}
		err := registry.GuestCounterError(gate.path)
		if err == nil {
			continue
		}
		message := "Guest counters remain pending after a persistence failure; they will be retried."
		if atomicfile.IsPublished(err) {
			message = "Guest counters were published, but directory sync failed; durability is unconfirmed. The published batch will not be applied again."
		}
		warnings[name] = inspect.WarningView{Code: inspect.WarningCodeGuestCounters, Severity: "warning", Source: "guest", Message: message}
	}
	return warnings
}
