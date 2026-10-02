package cmd

import (
	"bytes"
	"testing"
)

func TestPeopleCommandKeepsUnicodeIdentitiesDistinct(t *testing.T) {
	paths := peopleTestPaths(t)
	restoreInviteCommandSeams(t)
	inviteRegistryPathFn = func() (string, error) { return paths.Registry, nil }
	invitePIDPathFn = func() (string, error) { return paths.PID, nil }
	inviteRuntimeSnapshotPathFn = func() (string, error) { return paths.Snapshot, nil }
	for _, login := range []string{"kelly@example.com", "\u212aelly@example.com", "irene@example.com", "\u0130rene@example.com"} {
		c := newPeopleCmd()
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		c.SetArgs([]string{"add", login, "--apps", "photos"})
		if err := c.Execute(); err != nil {
			t.Fatal(login, err)
		}
		p, err := readPerson(paths.Registry, login)
		if err != nil || p.Login != login {
			t.Fatal("CLI changed Unicode identity bytes", login, p, err)
		}
	}
}
