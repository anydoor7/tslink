package health

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNotifierCommandCancellation(t *testing.T) {
	if os.Getenv("TSLINK_HEALTH_CANCEL_CHILD") == "1" {
		if os.WriteFile(os.Getenv("TSLINK_HEALTH_CANCEL_READY"), []byte("ready"), 0600) != nil {
			os.Exit(2)
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	t.Setenv("TSLINK_HEALTH_CANCEL_CHILD", "1")
	ready := filepath.Join(t.TempDir(), "ready")
	t.Setenv("TSLINK_HEALTH_CANCEL_READY", ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Notify(ctx, NotifierConfig{Command: []string{os.Args[0], "-test.run=^TestNotifierCommandCancellation$"}}, Event{Kind: "app_down"})
	}()
	t.Cleanup(func() { cancel() })
	deadline := time.After(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("notifier helper never started")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "alert_command_failed" {
			t.Fatal("cancellation counted as success", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled command did not return")
	}
}

func TestDeliveryQueueBoundDedupAndShutdown(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{Command: []string{"/private/notifier"}})
	started := make(chan struct{})
	r.Send = func(ctx context.Context, _ NotifierConfig, _ Event) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	r.StartDelivery(context.Background())
	t.Cleanup(r.StopDelivery)
	r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "app"}}, now)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no worker")
	}
	// The in-flight delivery must not hold Commit or grow an unbounded queue.
	for i := 1; i <= 17; i++ {
		r.Commit(context.Background(), []Event{{Kind: "app_down", Service: "app"}}, now.Add(time.Duration(i)*5*time.Minute))
	}
	if r.State.Events[17].Delivery != "failed" || r.State.Events[0].Delivery != "pending" {
		t.Fatal(r.State.Events)
	}
	r.StopDelivery()
	for _, event := range r.State.Events {
		if event.Delivery != "failed" {
			t.Fatal("shutdown lost a reservation", event)
		}
	}
	restarted := NewRecorder(r.Path, r.Config)
	restarted.Send = func(context.Context, NotifierConfig, Event) error {
		t.Fatal("restart retried reserved delivery")
		return nil
	}
	restarted.Commit(context.Background(), []Event{{Kind: "app_down", Service: "app"}}, now.Add(17*5*time.Minute+time.Second))
	if restarted.State.Events[18].Delivery != "rate_limited" {
		t.Fatal(restarted.State.Events)
	}
}

func TestDeliveryCompletionUsesEventIDAndPersists(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{Command: []string{"/private/notifier"}})
	r.Send = func(context.Context, NotifierConfig, Event) error { return nil }
	r.StartDelivery(context.Background())
	t.Cleanup(r.StopDelivery)
	r.Commit(context.Background(), []Event{{Kind: "app_down"}}, now)
	select {
	case result := <-r.DeliveryReady():
		r.CompleteDelivery(result)
	case <-time.After(time.Second):
		t.Fatal("no completion")
	}
	if got := NewRecorder(r.Path, r.Config).State.Events[0].Delivery; got != "sent" {
		t.Fatal(got)
	}
	r.State.Events[0].Delivery = "pending"
	r.CompleteDelivery(deliveryResult{id: 999, failed: true})
	if r.State.Events[0].Delivery != "pending" {
		t.Fatal("stale ID overwrote an event")
	}
}

func TestDeliveryShutdownBoundWithUninterruptibleSend(t *testing.T) {
	r := NewRecorder(filepath.Join(t.TempDir(), StateFile), NotifierConfig{Command: []string{"/private/notifier"}})
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	r.Send = func(context.Context, NotifierConfig, Event) error { close(started); <-release; return nil }
	r.StartDelivery(context.Background())
	w := r.delivery
	r.Commit(context.Background(), []Event{{Kind: "app_down"}}, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	<-started
	done := make(chan struct{})
	go func() { r.StopDelivery(); close(done) }()
	t.Cleanup(func() { finish(); <-w.done; <-done })
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("uninterruptible send blocked shutdown")
	}
	if r.State.Events[0].Delivery != "failed" {
		t.Fatal(r.State.Events)
	}
	// The late successful return has no recorder access and cannot undo failure.
	finish()
	<-w.done
	if r.State.Events[0].Delivery != "failed" {
		t.Fatal("late return overwrote cancellation", r.State.Events)
	}
}
