package server

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
)

func TestDrainAccessJoinsDelayedWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		store, err := accesslog.New(dir, accesslog.Options{}, func() time.Time { return accessTestTime })
		if err != nil {
			t.Fatal(err)
		}
		defer func() { store.Close(); <-store.Done() }()
		// A slow worker is allowed to take longer than the former cleanup bound.
		// Virtual time proves the join without making the test depend on disk speed.
		if !store.RecordResolved(accesslog.Event{App: "photos", Kind: "http", Decision: "allowed"}, func() accesslog.Identity {
			time.Sleep(6 * time.Second)
			return accesslog.Identity{Login: "alice"}
		}) {
			t.Fatal("control event was not queued")
		}
		start := time.Now()
		drainAccess(t, store)
		if elapsed := time.Since(start); elapsed != 6*time.Second {
			t.Fatalf("drain returned after %s, want the full 6s worker delay", elapsed)
		}
		result, err := accesslog.Query(dir, accesslog.Filter{})
		if err != nil || len(result.Events) != 1 || result.Events[0].Identity.Login != "alice" {
			t.Fatalf("drain did not persist the delayed event: %+v, %v", result, err)
		}
	})
}
