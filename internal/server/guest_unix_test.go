//go:build !windows

package server

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestGuestSpecialRegistryFailsClosed(t *testing.T) {
	f := newGuestFixture(t, "", false, true)
	cookies := f.login()
	raw, e := os.ReadFile(f.path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(f.path); e != nil {
		t.Fatal(e)
	}
	if e = syscall.Mkfifo(f.path, 0600); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	r, _ := f.request("GET", "/", "", cookies)
	if r.StatusCode != 401 || time.Since(start) > time.Second || f.hits.Load() != 0 {
		t.Fatal("special file blocked gate or reached backend")
	}
	if e = os.Remove(f.path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(f.path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	cookies = f.login()
	r, _ = f.request("GET", "/", "", cookies)
	if r.StatusCode != 204 {
		t.Fatal("special-file control denied")
	}
}
