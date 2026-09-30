package registry

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestConfigDirRefusalsNameABackslashPath runs the config-directory refusal
// tests with every t.TempDir under a directory whose name contains a
// backslash. On macOS and Linux that is a legal file name character, and %q
// escapes it the way it escapes every separator of a Windows path, so these
// runs show what the plain tests do on Windows.
func TestConfigDirRefusalsNameABackslashPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("every Windows path already contains backslashes; the plain tests cover it")
	}
	root := filepath.Join(t.TempDir(), `back\slash`)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	t.Setenv("GOTMPDIR", "")
	for name, test := range map[string]func(*testing.T){
		"FileRootRefusesTSLinkConfigDirectory":                 TestFileRootRefusesTSLinkConfigDirectory,
		"FileRootConfigDirCheckSeesThroughCase":                TestFileRootConfigDirCheckSeesThroughCase,
		"SingleFileShareInsideConfigDirIsRefused":              TestSingleFileShareInsideConfigDirIsRefused,
		"HomeRootAcceptedWhenConfigDirIsElsewhere":             TestHomeRootAcceptedWhenConfigDirIsElsewhere,
		"SingleFileShareNotYetCreatedInsideConfigDirIsRefused": TestSingleFileShareNotYetCreatedInsideConfigDirIsRefused,
	} {
		t.Run(name, test)
	}
}
