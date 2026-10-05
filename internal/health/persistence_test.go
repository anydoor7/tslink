package health

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/testwait"
)

func TestRecorderCoalescesDeliveryAndSkipsUnchangedWrites(t *testing.T) {
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{Command: []string{"/private/notifier"}})
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	started, release := make(chan struct{}), make(chan struct{})
	first := true
	r.Send = func(ctx context.Context, _ NotifierConfig, _ Event) error {
		if first {
			first = false
			close(started)
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.StartDelivery(context.Background())
	t.Cleanup(r.StopDelivery)
	writes := 0
	write := r.WriteFile
	r.WriteFile = func(path string, data []byte) error { writes++; return write(path, data) }
	r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "app"}}, now)
	<-started
	if writes != 1 {
		t.Errorf("async reservation wrote %d times; want one", writes)
	}
	for i := 1; i < 17; i++ {
		r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "app"}}, now.Add(time.Duration(i)*5*time.Minute))
	}
	close(release)
	testwait.Until(t, "all 17 delivery completions ready", func() bool { return len(r.DeliveryReady()) == 17 })
	writes = 0
	r.DrainDelivery()
	if writes != 1 {
		t.Errorf("17 ready completions wrote %d journals; want one", writes)
	}
	for _, e := range NewRecorder(r.Path, r.Config).State.Events {
		if e.Delivery != "sent" {
			t.Error("completion not durable", e)
		}
	}
	writes = 0
	r.Commit(context.Background(), nil, now)
	if writes != 0 {
		t.Errorf("unchanged journal wrote %d times", writes)
	}
}

func TestRecorderRetainsDirtyUntilWriteSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateFile)
	r := NewRecorder(path, NotifierConfig{})
	// The real atomic writer rejects a directory at the file path.
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	h := Result(Unchecked("proxy"), "proxy", "", now)
	writes := 0
	write := r.WriteFile
	r.WriteFile = func(path string, data []byte) error { writes++; return write(path, data) }
	for i := 0; i < 2; i++ {
		events := r.ObserveHealth("app", "backend", h, now)
		if !r.Commit(context.Background(), events, now) || r.Error != "alert_state_write_failed" || writes != i+1 {
			t.Fatal("failed journal did not remain dirty", r.Error, writes)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if !r.Commit(context.Background(), r.ObserveHealth("app", "backend", h, now), now) {
		t.Error("identical observation did not retry the pending journal")
	}
	disk := NewRecorder(path, r.Config)
	if disk.Error != "" || disk.Previous("app", "backend").State != Healthy || writes != 3 {
		t.Fatal("journal recovery did not persist the observation", disk.Error, disk.State, writes)
	}
	if r.Commit(context.Background(), nil, now) || writes != 3 {
		t.Error("unchanged journal kept writing after successful recovery", writes)
	}
}
