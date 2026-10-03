package registry

import (
	"context"
	"time"

	"github.com/anydoor7/tslink/internal/mcpaudit"
)

// Called after the registry lock is released and a new expiry latch committed.
// Repeated reads and clock rollback do not produce duplicate expiry events.
func recordExpiry(path string, now time.Time, change mcpaudit.Change) error {
	return mcpaudit.RecordChange(context.Background(), path, "lifecycle", "system:expiry", now, change)
}
