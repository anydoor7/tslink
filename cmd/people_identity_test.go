package cmd

import (
	"bytes"
	"os"
	"testing"
)

func TestPeopleCommandRejectsUnicodeIdentity(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	for _, login := range []string{"\u212aelly@example.com", "\u0130rene@example.com"} {
		before, _ := os.ReadFile(paths.Registry)
		c := newPeopleCmd()
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		c.SetArgs([]string{"add", login, "--apps", "photos"})
		if err := c.Execute(); err == nil {
			t.Errorf("CLI accepted unsupported identity %q", login)
		}
		after, _ := os.ReadFile(paths.Registry)
		if !bytes.Equal(before, after) {
			t.Errorf("CLI mutated registry for unsupported login %q", login)
		}
	}
}
