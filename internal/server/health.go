package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
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
	Identity    string
	Health      health.State
	NodeKey     health.Expiry
	Node        *ServiceNode
	NodeChecked time.Time
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
// closes nodes. Uninterruptible I/O retains a bounded worker slot, not the monitor.
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
		r.StartDelivery(ctx)
		defer func() { r.StopDelivery(); s.publishHealth(r) }()
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
		wait:
			for {
				select {
				case <-ctx.Done():
					return
				case result := <-r.DeliveryReady():
					r.CompleteDelivery(result)
					s.publishHealth(r)
				case <-ticker.C:
					break wait
				}
			}
		}
	}()
	return done
}

func (s *Server) healthCycle(ctx context.Context, r *health.Recorder, now time.Time, probe func(context.Context, registry.Service) string) {
	s.mu.Lock()
	if s.healthProbeSlots == nil {
		s.healthProbeSlots = make(chan struct{}, 4)
		s.healthNodeSlots = make(chan struct{}, 4)
	}
	probeSlots, nodeSlots := s.healthProbeSlots, s.healthNodeSlots
	var targets []healthTarget
	for _, node := range s.nodes {
		if health.TargetSafe(node.service) {
			targets = append(targets, healthTarget{Service: node.service, Node: node})
		}
	}
	for name, failure := range s.serviceFailures {
		if _, ok := s.nodes[name]; !ok && health.TargetSafe(failure.Service) &&
			(failure.Error == nil || failure.Error.Code != registry.CodePathExposesConfigDir) {
			targets = append(targets, healthTarget{Service: failure.Service, Failed: true})
		}
	}
	s.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].Service.Name < targets[j].Service.Name })
	type result struct {
		target   healthTarget
		identity string
		health   *health.State
		expiry   *health.Expiry
	}
	results := make(chan result, 2*len(targets))
	jobs := 0
	for _, target := range targets {
		identity := healthIdentity(target.Service)
		previous := r.Previous(target.Service.Name, identity)
		cfg := registry.HealthConfig{}
		if target.Service.Health != nil {
			cfg = *target.Service.Health
		}
		timeout, interval := cfg.Durations()
		if registry.ValidateHealthConfig(target.Service.Type, target.Service.Health) != nil {
			timeout = 5 * time.Second
		}
		s.mu.RLock()
		observed, exists := s.healthStates[target.Service.Name]
		s.mu.RUnlock()
		if !exists || previous.LastChecked == nil || now.Sub(*previous.LastChecked) >= interval {
			jobs++
			go func() {
				probeCtx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				// Enrollment/policy failures still get backend observations.
				code := boundedHealthRead(probeCtx, probeSlots, "health_timeout", func(ctx context.Context) string { return probe(ctx, target.Service) })
				h := health.Result(previous, target.Service.Type, code, now)
				results <- result{target: target, identity: identity, health: &h}
			}()
		}
		// The LocalAPI cadence belongs to the node, not health.interval. A new
		// pointer denotes a new incarnation and must be read immediately.
		if !exists || observed.Node != target.Node || observed.NodeChecked.IsZero() || now.Sub(observed.NodeChecked) >= time.Minute {
			jobs++
			go func() {
				pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				e := boundedHealthRead(pollCtx, nodeSlots, nodeKeyExpiry(nil, now), func(ctx context.Context) health.Expiry {
					if target.Node != nil && target.Node.tsnetSrv != nil {
						if lc, err := target.Node.tsnetSrv.LocalClient(); err == nil && lc != nil {
							if st, err := lc.StatusWithoutPeers(ctx); err == nil {
								return nodeKeyExpiry(st, now)
							}
						}
					}
					return nodeKeyExpiry(nil, now)
				})
				results <- result{target: target, identity: identity, expiry: &e}
			}()
		}
	}
	published := false
	for range jobs {
		result := <-results // bounded wrappers finish even if their I/O is stuck
		if ctx.Err() != nil {
			continue
		}
		s.mu.Lock()
		name := result.target.Service.Name
		current, running := s.nodes[name]
		if result.target.Node != nil && (!running || current != result.target.Node || healthIdentity(current.service) != result.identity) {
			s.mu.Unlock()
			continue
		}
		if result.target.Failed {
			failure, failed := s.serviceFailures[name]
			if running || !failed || healthIdentity(failure.Service) != result.identity {
				s.mu.Unlock()
				continue
			}
		}
		if s.healthStates == nil {
			s.healthStates = map[string]serviceHealth{}
		}
		observed := s.healthStates[name]
		if observed.Identity != result.identity {
			observed = serviceHealth{Identity: result.identity, Health: health.Unchecked(result.target.Service.Type)}
		}
		var events []health.Event
		if result.health != nil {
			observed.Health = *result.health
			events = append(events, r.ObserveHealth(name, result.identity, *result.health, now)...)
		}
		if result.expiry != nil {
			observed.NodeKey, observed.Node, observed.NodeChecked = *result.expiry, result.target.Node, now
			events = append(events, r.ObserveExpiry(name, "node_key", *result.expiry, now)...)
		}
		s.healthStates[name] = observed
		s.mu.Unlock()
		r.Commit(ctx, events, now)
		s.publishHealth(r)
		published = true
	}
	if !published && ctx.Err() == nil {
		r.Commit(ctx, nil, now)
		s.publishHealth(r)
	}
}

// A stuck kernel call cannot be canceled by Go. Its slot stays occupied across
// cycles, bounding abandoned I/O to four calls per pool, while the wrapper and
// monitor can finish. Workers only return values; they never publish late data.
func boundedHealthRead[T any](ctx context.Context, slots chan struct{}, fallback T, read func(context.Context) T) T {
	if ctx.Err() != nil {
		return fallback
	}
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return fallback
	}
	if ctx.Err() != nil {
		<-slots
		return fallback
	}
	result := make(chan T, 1)
	go func() {
		defer func() { <-slots }()
		result <- read(ctx)
	}()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		return fallback
	}
}

func (s *Server) publishHealth(r *health.Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = r.View()
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
}
