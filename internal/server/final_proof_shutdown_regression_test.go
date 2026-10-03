package server

import (
	"context"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/testenv"
)

func TestTryNodeStateGateSkipsContentionAndCancellation(t *testing.T) {
	s, err := New("fake-key", "")
	if err != nil {
		t.Fatal(err)
	}
	s.reconcileGate <- struct{}{} // startup owns the gate
	called := false
	acquired, err := s.TryWithNodeStateLock(context.Background(), func() error { called = true; return nil })
	if err != nil || acquired || called {
		t.Fatalf("busy gate: acquired=%v err=%v called=%v", acquired, err, called)
	}
	<-s.reconcileGate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	acquired, err = s.TryWithNodeStateLock(ctx, func() error { called = true; return nil })
	if err != nil || acquired || called {
		t.Fatalf("canceled gate: acquired=%v err=%v called=%v", acquired, err, called)
	}
	acquired, err = s.TryWithNodeStateLock(context.Background(), func() error { called = true; return nil })
	if err != nil || !acquired || !called || len(s.reconcileGate) != 0 {
		t.Fatalf("uncontended gate: acquired=%v err=%v called=%v gateHeld=%v", acquired, err, called, len(s.reconcileGate) != 0)
	}
}

// Exercise the real Run/ticker/shutdown join with the same nonblocking final
// registry proof as lifecycle. No tsnet node or service manager is started.
func TestRunShutdownDoesNotWaitForBusyFinalProof(t *testing.T) {
	for _, held := range []bool{false, true} {
		name := "uncontended_control"
		if held {
			name = "contended_registry"
		}
		t.Run(name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			if err := config.EnsureDir(); err != nil {
				t.Fatal(err)
			}
			writeRegistry(t, nil)
			regPath, err := config.RegistryPath()
			if err != nil {
				t.Fatal(err)
			}
			s, err := New("fake-key", "")
			if err != nil {
				t.Fatal(err)
			}
			oldInterval := lifecycleTickerInterval
			lifecycleTickerInterval = 5 * time.Millisecond
			defer func() { lifecycleTickerInterval = oldInterval }()
			writerHeld := make(chan struct{})
			release := make(chan struct{})
			writerDone := make(chan error, 1)
			if held {
				go func() {
					writerDone <- registry.WithLockedFileState(regPath, func(*registry.Registry, registry.RegistryFileState) error {
						close(writerHeld)
						<-release
						return nil
					})
				}()
				<-writerHeld
			}
			atFinal := make(chan struct{})
			initial := true
			s.SetLifecycleReconcileFn(func(ctx context.Context, _ time.Time) (bool, error) {
				if initial {
					initial = false
					return false, nil
				}
				select {
				case <-atFinal:
				default:
					close(atFinal)
				}
				_, err := s.TryWithNodeStateLock(ctx, func() error {
					_, err := registry.TryWithLockedFileState(regPath, func(*registry.Registry, registry.RegistryFileState) error { return nil })
					return err
				})
				return false, err
			})
			ready := make(chan struct{})
			s.SetReadyFunc(func() error { close(ready); return nil })
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- s.Run(ctx) }()
			select {
			case <-ready:
			case <-time.After(5 * time.Second):
				t.Fatal("server never ready")
			}
			select {
			case <-atFinal:
			case <-time.After(5 * time.Second):
				t.Fatal("ticker never reached final proof")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("Run shutdown waited for registry writer")
			}
			if len(s.reconcileGate) != 0 {
				t.Error("final proof retained startup gate during shutdown")
			}
			if held {
				close(release)
				if err := <-writerDone; err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
