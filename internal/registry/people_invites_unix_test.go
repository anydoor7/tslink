//go:build !windows

package registry

import (
	"syscall"
	"testing"
)

func TestPeopleInviteLockRejectsFIFO(t *testing.T) {
	path := peopleFixture(t)
	if err := syscall.Mkfifo(path+".people-invites.lock", 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	acquired, err := TryPeopleInviteWork(path, func() error { called = true; return nil })
	if err == nil || acquired || called {
		t.Fatal("FIFO accepted as lock", acquired, err)
	}
}
