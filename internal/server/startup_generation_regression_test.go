package server

// Deterministic interleaving of generation registration; no node is started.

import (
	"context"
	"fmt"
	"testing"
)

func TestStartupGenerationRegistrationIsMonotonic(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		t.Run(fmt.Sprintf("registration_reversed=%v", reversed), func(t *testing.T) {
			s := &Server{}
			oldGeneration := s.syncGeneration.Add(1)
			oldContext, oldCancel := context.WithCancel(context.Background())
			defer oldCancel()
			if !reversed {
				s.installStartupGeneration(oldGeneration, oldCancel)
			}
			// A second registry/ticker reconciliation runs while the first is
			// descheduled between Add(1) and installStartupGeneration.
			newGeneration := s.syncGeneration.Add(1)
			newContext, newCancel := context.WithCancel(context.Background())
			defer newCancel()
			s.installStartupGeneration(newGeneration, newCancel)
			if reversed {
				s.installStartupGeneration(oldGeneration, oldCancel)
			}
			t.Logf("latest=%d registered=%d oldErr=%v newErr=%v", s.syncGeneration.Load(), s.startupGeneration, oldContext.Err(), newContext.Err())
			if err := s.ensureRunning(newContext); err != nil {
				t.Errorf("older reconciliation canceled the newest startup: %v", err)
			}
			if s.startupGeneration != newGeneration {
				t.Errorf("cancel ownership moved backward: registered=%d latest=%d", s.startupGeneration, newGeneration)
			}
		})
	}
}
