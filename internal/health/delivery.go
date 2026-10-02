package health

import (
	"context"
	"time"
)

type DeliveryResult struct {
	id     uint64
	failed bool
}

type deliveryResult = DeliveryResult

type deliveryWorker struct {
	queue   chan Event
	results chan DeliveryResult
	done    chan struct{}
	cancel  context.CancelFunc
	startID uint64
}

// StartDelivery must run before Commit, on the recorder's owning goroutine.
// Only the worker invokes the notifier. It never reads or mutates the journal.
func (r *Recorder) StartDelivery(ctx context.Context) {
	if r.delivery != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &deliveryWorker{queue: make(chan Event, 16), results: make(chan DeliveryResult, 17), done: make(chan struct{}), cancel: cancel}
	r.delivery = w
	w.startID = r.State.NextID
	send, config := r.Send, r.Config
	go func() {
		defer close(w.done)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-w.queue:
				if ctx.Err() != nil {
					return
				}
				err := send(ctx, config, event)
				result := DeliveryResult{event.ID, err != nil || ctx.Err() != nil}
				select {
				case w.results <- result:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

// DeliveryReady wakes the monitor so delivery changes can be published without
// waiting for another backend check. A nil channel disables this select case.
func (r *Recorder) DeliveryReady() <-chan DeliveryResult {
	if r.delivery == nil {
		return nil
	}
	return r.delivery.results
}

// CompleteDelivery is called only by the recorder's owner, including when a
// select receives the first completion. Event IDs survive journal truncation.
func (r *Recorder) CompleteDelivery(results ...DeliveryResult) bool {
	oldError := r.Error
	for _, result := range results {
		r.completeDelivery(result)
	}
	changed := r.dirty
	if r.save() != nil {
		r.Error = "alert_state_write_failed"
	}
	return changed || oldError != r.Error
}

func (r *Recorder) completeDelivery(result DeliveryResult) {
	for i := range r.State.Events {
		if r.State.Events[i].ID == result.id && r.State.Events[i].Delivery == "pending" {
			r.State.Events[i].Delivery = "sent"
			if result.failed {
				r.State.Events[i].Delivery = "failed"
			}
			r.dirty = true
			return
		}
	}
}

func (r *Recorder) drainDelivery() {
	for {
		select {
		case result := <-r.DeliveryReady():
			r.completeDelivery(result)
		default:
			return
		}
	}
}

func (r *Recorder) DrainDelivery() {
	r.drainDelivery()
	if r.save() != nil {
		r.Error = "alert_state_write_failed"
	}
}

// StopDelivery cancels active I/O and allows one second to join the notifier.
// Even uninterruptible process/OS I/O cannot prevent monitor shutdown. The lone
// worker only owns captured values, so a late return cannot mutate the journal.
// Reservations remain durable and are not retried.
func (r *Recorder) StopDelivery() {
	if r.delivery == nil {
		return
	}
	r.delivery.cancel()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-r.delivery.done:
	case <-timer.C:
	}
	r.drainDelivery()
	for i := range r.State.Events {
		if r.State.Events[i].ID > r.delivery.startID && r.State.Events[i].Delivery == "pending" {
			r.State.Events[i].Delivery = "failed"
			r.dirty = true
		}
	}
	if r.save() != nil {
		r.Error = "alert_state_write_failed"
	}
	r.delivery = nil
}
