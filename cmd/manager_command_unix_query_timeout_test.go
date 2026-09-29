//go:build darwin || linux

package cmd

import (
	"context"
	"fmt"
)

// managerQueryTimeoutError is the error runBoundedManagerCommand returns when a
// read-only query runs out its budget, so a fixture can stand in for one slow
// answer from an otherwise healthy service manager.
func managerQueryTimeoutError(manager string) error {
	return fmt.Errorf("%s command exceeded %s: %w", manager, managerQueryTimeout, context.DeadlineExceeded)
}

// errManualDaemonConflict stands in for installDaemonArtifactConflictFn, the
// "stop the manual daemon" refusal install gives when the running daemon is
// classified as not owned by the service manager.
var errManualDaemonConflict = fmt.Errorf("fixture: running daemon classified as not owned (manual daemon conflict)")
