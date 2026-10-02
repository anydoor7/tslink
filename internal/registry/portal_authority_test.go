package registry

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/anydoor7/tslink/internal/filelock"
)

func TestPortalAuthorityCheckUsesCurrentRegistry(t *testing.T) {
	path := requestStore(t)
	lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = filelock.Unlock(lock)
		}
	}()
	denied := errors.New("current owner required")
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		done <- SetPortalAuthorized(path, &PortalConfig{Enabled: true, Hostname: "home", Owner: "previous-owner"}, func(reg *Registry) error {
			if reg.Portal.Owner != "replacement" {
				return errors.New("authorization did not see committed owner")
			}
			return denied
		})
	}()
	<-started
	// This writer commits a replacement while the other writer waits on the
	// actual file lock. Authorization must use the registry after acquisition.
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Portal.Owner = "replacement"
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := filelock.Unlock(lock); err != nil {
		t.Fatal(err)
	}
	locked = false
	if err := <-done; !errors.Is(err, denied) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refused authority mutation changed bytes", err)
	}
}
