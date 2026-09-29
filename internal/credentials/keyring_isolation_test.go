package credentials

// Runs before TestMain: no test in this binary may reach the operator's real
// OS keyring. TestEveryKeyringLinkingTestBinaryInstallsIsolation enforces it.
func init() { IsolateForTesting() }
