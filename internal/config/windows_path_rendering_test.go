package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestLegacyConfigDirMoveCommandIsACmdCommand: the next step a Windows user
// is told to run is a cmd.exe command. Go's %q doubled every backslash, so
// the command named C:\\Users\\me\\... and agents received the same string.
// A Windows path cannot contain a double quote, so quoting each path is
// enough.
func TestLegacyConfigDirMoveCommandIsACmdCommand(t *testing.T) {
	err := &LegacyConfigDirError{
		Legacy:  `C:\Users\me\.config\tslink`,
		Current: `C:\Users\me\AppData\Roaming\tslink`,
	}
	next := err.NextCommands()
	want := `move "C:\Users\me\.config\tslink" "C:\Users\me\AppData\Roaming\tslink"`
	if len(next) != 1 || next[0] != want {
		t.Fatalf("next = %q, want exactly [%s]", next, want)
	}
}

// withBackslashTempRoot makes every t.TempDir below t live under a directory
// whose name contains a backslash. On macOS and Linux that is a legal file
// name character, and %q escapes it the way it escapes every separator of a
// Windows path, so the plain tests run here show what they do on Windows.
func withBackslashTempRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("every Windows path already contains backslashes; the plain tests cover it")
	}
	root := filepath.Join(t.TempDir(), `back\slash`)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	t.Setenv("GOTMPDIR", "")
}

// TestConfigMessagesNameABackslashPath runs the config tests that name a
// path in a message or a next step with such a path.
func TestConfigMessagesNameABackslashPath(t *testing.T) {
	withBackslashTempRoot(t)
	for name, test := range map[string]func(*testing.T){
		"DefaultTagFailsClosedOnAConfigItCannotRead":            TestDefaultTagFailsClosedOnAConfigItCannotRead,
		"ResolveWindowsConfigDirReportsUnreadableCandidates":    TestResolveWindowsConfigDirReportsUnreadableCandidates,
		"ResolveWindowsConfigDirRefusesLegacyOnlyWithoutMoving": TestResolveWindowsConfigDirRefusesLegacyOnlyWithoutMoving,
		"ResolveWindowsConfigDirRefusesBothDirectories":         TestResolveWindowsConfigDirRefusesBothDirectories,
	} {
		t.Run(name, test)
	}
}
