package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/health"
	"github.com/anydoor7/tslink/internal/registry"
	"tailscale.com/ipn/ipnstate"
)

var healthTickInterval = 10 * time.Second
var healthProbeFn = health.Probe
var healthCredentialInventoryFn = func(now time.Time) credentials.Inventory {
	values, err := credentials.StoredSlotValues()
	if err != nil {
		return credentials.Inventory{MetadataError: err}
	}
	return credentials.DescribeSlots(values, now, false)
}

type serviceHealth struct {
	Identity string
	Health   health.State
	NodeKey  health.Expiry
}
type healthTarget struct {
	Service registry.Service
	Node    *ServiceNode
	Failed  bool
}

func healthIdentity(svc registry.Service) string {
	// Sharing-policy edits must not reset an app's failure streak or create a
	// second down event. Scope the identity to the backend/probe incarnation.
	b, _ := json.Marshal(struct {
		Type, Target, Path, File string
		Health                   *registry.HealthConfig
		CreatedAt                time.Time
	}{svc.Type, svc.Target, svc.Path, svc.File, svc.Health, svc.CreatedAt})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func nodeKeyExpiry(st *ipnstate.Status, now time.Time) health.Expiry {
	var expiry *time.Time
	if st != nil && st.Self != nil {
		expiry = st.Self.KeyExpiry
	}
	return health.ExpiryAt(expiry, "localclient", now, []string{"tslink doctor --json", "Open https://login.tailscale.com/admin/machines and reauthenticate this service node or review its key expiry setting"})
}

// All seams are captured before spawning. Run joins this goroutine before it
// closes nodes; each cycle also joins its bounded probe workers.
func (s *Server) startHealthMonitor(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	nowFn, probeFn, inventoryFn := serverNowFn, healthProbeFn, healthCredentialInventoryFn
	interval := healthTickInterval
	c, configErr := health.LoadNotifier(filepath.Join(s.cfgDir, health.ConfigFile))
	r := health.NewRecorder(filepath.Join(s.cfgDir, health.StateFile), c)
	if configErr != nil {
		r.Error = configErr.Error()
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var credentialChecked time.Time
		for {
			now := nowFn()
			s.healthCycle(ctx, r, now, probeFn)
			if credentialChecked.IsZero() || now.Sub(credentialChecked) >= time.Minute {
				inventory := inventoryFn(now)
				var events []health.Event
				for _, slot := range []credentials.SlotView{inventory.APIKey, inventory.ClientSecret} {
					if !slot.Present || slot.Metadata == nil {
						continue
					}
					e := health.ExpiryAt(slot.Metadata.ExpiresAt, slot.Metadata.ExpiresAtSource, now, credentials.NextAPIKeyBootstrap())
					events = append(events, r.ObserveExpiry("", slot.Slot, e, now)...)
				}
				r.Commit(ctx, events, now)
				credentialChecked = now
				s.publishHealth(r)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func (s *Server) healthCycle(ctx context.Context, r *health.Recorder, now time.Time, probe func(context.Context, registry.Service) string) {
	s.mu.RLock()
	var targets []healthTarget
	for _, node := range s.nodes {
		targets = append(targets, healthTarget{Service: node.service, Node: node})
	}
	for name, failure := range s.serviceFailures {
		if _, ok := s.nodes[name]; !ok {
			targets = append(targets, healthTarget{Service: failure.Service, Failed: true})
		}
	}
	s.mu.RUnlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].Service.Name < targets[j].Service.Name })
	type result struct {
		target   healthTarget
		identity string
		health   health.State
		expiry   health.Expiry
	}
	results := make([]result, len(targets))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, target := range targets {
		identity := healthIdentity(target.Service)
		previous := r.Previous(target.Service.Name, identity)
		cfg := registry.HealthConfig{}
		if target.Service.Health != nil {
			cfg = *target.Service.Health
		}
		_, interval := cfg.Durations()
		s.mu.RLock()
		_, observed := s.healthStates[target.Service.Name]
		s.mu.RUnlock()
		if observed && previous.LastChecked != nil && now.Sub(*previous.LastChecked) < interval {
			continue
		}
		wg.Add(1)
		go func(i int, target healthTarget, previous health.State, identity string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			// A node enrollment/policy failure says nothing about the backend
			// app. Keep app health separate from RuntimeState and probe it too.
			code := probe(ctx, target.Service)
			h := health.Result(previous, target.Service.Type, code, now)
			e := nodeKeyExpiry(nil, now)
			if target.Node != nil && target.Node.tsnetSrv != nil {
				if lc, err := target.Node.tsnetSrv.LocalClient(); err == nil && lc != nil {
					pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					st, err := lc.StatusWithoutPeers(pollCtx)
					cancel()
					if err == nil {
						e = nodeKeyExpiry(st, now)
					}
				}
			}
			results[i] = result{target, identity, h, e}
		}(i, target, previous, identity)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	var events []health.Event
	s.mu.Lock()
	if s.healthStates == nil {
		s.healthStates = map[string]serviceHealth{}
	}
	for _, result := range results {
		if result.identity == "" {
			continue
		}
		name := result.target.Service.Name
		current, running := s.nodes[name]
		if result.target.Node != nil && (!running || current != result.target.Node || healthIdentity(current.service) != result.identity) {
			continue
		}
		if result.target.Failed {
			failure, failed := s.serviceFailures[name]
			if running || !failed || healthIdentity(failure.Service) != result.identity {
				continue
			}
		}
		s.healthStates[name] = serviceHealth{result.identity, result.health, result.expiry}
		events = append(events, r.ObserveHealth(name, result.identity, result.health, now)...)
		events = append(events, r.ObserveExpiry(name, "node_key", result.expiry, now)...)
	}
	s.mu.Unlock()
	r.Commit(ctx, events, now)
	s.publishHealth(r)
}

func (s *Server) publishHealth(r *health.Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = r.View()
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
}
