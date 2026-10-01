package server

import "github.com/anydoor7/tslink/internal/credentials"

// Runs before TestMain: no test in this binary may reach the operator's real
// OS keyring. TestEveryKeyringLinkingTestBinaryInstallsIsolation enforces it.
func init() { credentials.IsolateForTesting() }
