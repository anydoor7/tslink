package credentials

import "github.com/zalando/go-keyring"

// IsolateForTesting switches this process to go-keyring's in-memory mock, so
// a test can never read, write, or delete the operator's real OS keyring
// items. go-keyring offers no way back to the real provider once the mock is
// installed. Every test binary that links go-keyring calls this from an init
// function in a _test.go file; that runs before TestMain and before any test,
// and self-exec children re-run the same binary and install it again.
// TestEveryKeyringLinkingTestBinaryInstallsIsolation enforces the rule.
// Production code must never call it.
func IsolateForTesting() {
	keyring.MockInit()
}
