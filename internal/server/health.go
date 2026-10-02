package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/anydoor7/tslink/internal/accesslog"
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
	Stale       bool // Last freshness projection announced to event clients.
}
type healthTarget struct {
	CanonicalHost string
	Service       registry.Service
	Node          *ServiceNode
	Failed        bool
}

func healthIdentity(svc registry.Service) string {
	// Sharing-policy edits must not reset an app's failure streak or create a
	// second down event. Scope the identity to the backend/probe incarnation.
	b, _ := json.Marshal(struct {
		Type, Target, Path, File string
		Health                   *registry.HealthConfig
		CreatedAt                time.Time
		PreserveHost             bool
		RequestLimits            *registry.EffectiveRequestLimits
	}{svc.Type, svc.Target, svc.Path, svc.File, svc.Health, svc.CreatedAt, svc.PreserveHost, svc.EffectiveRequestLimits()})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func healthProbeIdentity(svc registry.Service, host string) string {
	identity := healthIdentity(svc)
	if svc.PreserveHost {
		identity += ":" + host
	}
	return identity
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
		defer func() {
			r.StopDelivery()
			s.mu.RLock()
			changed := !reflect.DeepEqual(s.alerts, r.View())
			s.mu.RUnlock()
			if changed {
				s.publishHealth(r)
			}
		}()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var credentialChecked time.Time
		for {
			now := nowFn()
			var events []health.Event
			if credentialChecked.IsZero() || now.Sub(credentialChecked) >= time.Minute {
				inventory := inventoryFn(now)
				for _, slot := range []credentials.SlotView{inventory.APIKey, inventory.ClientSecret} {
					if !slot.Present || slot.Metadata == nil {
						continue
					}
					e := health.ExpiryAt(slot.Metadata.ExpiresAt, slot.Metadata.ExpiresAtSource, now, credentials.NextAPIKeyBootstrap())
					events = append(events, r.ObserveExpiry("", slot.Slot, e, now)...)
				}
				credentialChecked = now
			}
			s.healthCycle(ctx, r, now, probeFn, events...)

		wait:
			for {
				select {
				case <-ctx.Done():
					return
				case result := <-r.DeliveryReady():
					batch := []health.DeliveryResult{result}
				drain:
					for {
						select {
						case next := <-r.DeliveryReady():
							batch = append(batch, next)
						default:
							break drain
						}
					}
					if r.CompleteDelivery(batch...) {
						s.publishHealth(r)
					}
				case <-ticker.C:
					break wait
				}
			}
		}
	}()
	return done
}

func (s *Server) healthCycle(ctx context.Context, r *health.Recorder, now time.Time, probe func(context.Context, registry.Service) string, pendingEvents ...health.Event) {
	s.mu.Lock()
	if s.healthProbePool == nil {
		s.healthProbePool = newHealthReadPool()
		s.healthNodePool = newHealthReadPool()
	}
	probeSlots, nodeSlots := s.healthProbePool, s.healthNodePool
	var targets []healthTarget
	for _, node := range s.nodes {
		if health.TargetSafe(node.service) {
			targets = append(targets, healthTarget{Service: node.service, Node: node, CanonicalHost: canonicalHostFor(node.tsnetSrv, node.runtimeHost)})
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
		identity := healthProbeIdentity(target.Service, target.CanonicalHost)
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
				// Enrollment/policy failures still get backend observations.
				code, attempted := boundedHealthRead(ctx, probeSlots, target.Service.Name, timeout, "health_timeout", func(ctx context.Context) string {
					return probe(health.WithCanonicalHost(ctx, target.CanonicalHost), target.Service)
				})
				var h *health.State
				if attempted {
					value := health.Result(previous, target.Service.Type, code, now)
					h = &value
				}
				results <- result{target: target, identity: identity, health: h}
			}()
		}
		// The LocalAPI cadence belongs to the node, not health.interval. A new
		// pointer denotes a new incarnation and must be read immediately.
		if !exists || observed.Node != target.Node || observed.NodeChecked.IsZero() || now.Sub(observed.NodeChecked) >= time.Minute {
			jobs++
			go func() {
				e, attempted := boundedHealthRead(ctx, nodeSlots, target.Service.Name, 5*time.Second, nodeKeyExpiry(nil, now), func(ctx context.Context) health.Expiry {
					if target.Node != nil && target.Node.tsnetSrv != nil {
						if lc, err := target.Node.tsnetSrv.LocalClient(); err == nil && lc != nil {
							if st, err := lc.StatusWithoutPeers(ctx); err == nil {
								return nodeKeyExpiry(st, now)
							}
						}
					}
					return nodeKeyExpiry(nil, now)
				})
				var expiry *health.Expiry
				if attempted {
					expiry = &e
				}
				results <- result{target: target, identity: identity, expiry: expiry}
			}()
		}
	}
	// Gather ready results for a short bounded window. Fast observations are
	// published while slow reads continue; an immediate batch writes once.
	generation := s.events.currentGeneration()
	published := false
	for jobs > 0 {
		batch := []result{<-results}
		jobs--
		timer := time.NewTimer(50 * time.Millisecond)
	collect:
		for jobs > 0 {
			select {
			case value := <-results:
				batch = append(batch, value)
				jobs--
			case <-timer.C:
				break collect
			case <-ctx.Done():
				break collect
			}
		}
		timer.Stop()
		if ctx.Err() != nil {
			continue
		}
		changed := false
		events := pendingEvents
		pendingEvents = nil
		for _, result := range batch {
			if result.health == nil && result.expiry == nil {
				continue
			} // not attempted
			s.mu.Lock()
			name := result.target.Service.Name
			current, running := s.nodes[name]
			if result.target.Node != nil && (!running || current != result.target.Node || healthProbeIdentity(current.service, canonicalHostFor(current.tsnetSrv, current.runtimeHost)) != result.identity) {
				s.mu.Unlock()
				continue
			}
			if result.target.Failed {
				failure, failed := s.serviceFailures[name]
				if running || !failed || healthProbeIdentity(failure.Service, "") != result.identity {
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
			if result.health != nil {
				changed = changed || !reflect.DeepEqual(observed.Health, *result.health)
				observed.Health = *result.health
				events = append(events, r.ObserveHealth(name, result.identity, *result.health, now)...)
			}
			if result.expiry != nil {
				changed = changed || observed.Node != result.target.Node || !reflect.DeepEqual(observed.NodeKey, *result.expiry)
				observed.NodeKey, observed.Node, observed.NodeChecked = *result.expiry, result.target.Node, now
				events = append(events, r.ObserveExpiry(name, "node_key", *result.expiry, now)...)
			}
			s.healthStates[name] = observed
			s.mu.Unlock()
		}
		events = append(events, r.ObserveMonitor(probeSlots.saturated() || nodeSlots.saturated(), now)...)
		if changed || len(events) > 0 {
			r.Commit(ctx, events, now)
			s.publishHealth(r)
			published = true
		}
	}
	// Pool recovery can happen between cycles even when no app check is due.
	if ctx.Err() == nil {
		events := append(pendingEvents, r.ObserveMonitor(probeSlots.saturated() || nodeSlots.saturated(), now)...)
		s.mu.Lock()
		changed := !reflect.DeepEqual(s.alerts, r.View())
		accessChanged := false
		if writer, ok := s.accessWriter.(interface{ Health() accesslog.Health }); ok {
			accessChanged = !reflect.DeepEqual(s.lastAccessHealth, writer.Health())
		}
		guestChanged := !reflect.DeepEqual(s.lastGuestCounterWarnings, s.guestCounterWarningsLocked())
		retry := (s.runtimeSnapshotDirty || accessChanged || guestChanged) && !published
		aged := false
		for _, target := range targets {
			name := target.Service.Name
			observed, ok := s.healthStates[name]
			if !ok || observed.Identity != healthProbeIdentity(target.Service, target.CanonicalHost) || observed.Health.LastChecked == nil {
				continue
			}
			cfg := registry.HealthConfig{}
			if target.Service.Health != nil {
				cfg = *target.Service.Health
			}
			timeout, interval := cfg.Durations()
			stale := now.Sub(*observed.Health.LastChecked) > 2*interval+timeout
			aged = aged || stale != observed.Stale
			observed.Stale = stale
			s.healthStates[name] = observed
		}
		s.mu.Unlock()
		if r.Commit(ctx, events, now) || changed || retry {
			s.publishHealth(r)
		}
		if aged && s.events.currentGeneration() == generation {
			// Consumers age the raw observation when rebuilding the projection.
			// Path resolution may prevent a snapshot attempt from notifying them.
			// Wake once per boundary without another disk write.
			s.events.publish()
		}
	}
}

// Each pool bounds real reads, including uninterruptible calls after timeout.
// flights guards service names until the actual read exits, even after removal
// or replacement. A true value means an admitted read outlived its I/O budget.
type healthReadPool struct {
	mu      sync.Mutex
	slots   chan struct{}
	flights map[string]bool
}

func newHealthReadPool() *healthReadPool {
	return &healthReadPool{slots: make(chan struct{}, 4), flights: map[string]bool{}}
}

func (p *healthReadPool) saturated() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	stuck := 0
	for _, timedOut := range p.flights {
		if timedOut {
			stuck++
		}
	}
	return stuck == cap(p.slots)
}

// Queue admission has its own five-second bound. No slot, an existing read,
// canceled admission or queue timeout means not attempted. Only an admitted
// read can return a checked timeout; its I/O budget starts after admission.
func boundedHealthRead[T any](ctx context.Context, pool *healthReadPool, name string, timeout time.Duration, fallback T, read func(context.Context) T) (T, bool) {
	pool.mu.Lock()
	if _, busy := pool.flights[name]; busy || ctx.Err() != nil {
		pool.mu.Unlock()
		return fallback, false
	}
	pool.flights[name] = false
	pool.mu.Unlock()
	release := func() { pool.mu.Lock(); delete(pool.flights, name); pool.mu.Unlock() }
	queueTimer := time.NewTimer(5 * time.Second)
	defer queueTimer.Stop()
	if pool.saturated() {
		release()
		return fallback, false
	}
	select {
	case pool.slots <- struct{}{}:
	case <-queueTimer.C:
		release()
		return fallback, false
	case <-ctx.Done():
		release()
		return fallback, false
	}
	if ctx.Err() != nil {
		<-pool.slots
		release()
		return fallback, false
	}
	ioCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan T, 1)
	go func() {
		defer func() { <-pool.slots; release() }()
		result <- read(ioCtx)
	}()
	select {
	case value := <-result:
		return value, true
	case <-ioCtx.Done():
		pool.mu.Lock()
		if _, exists := pool.flights[name]; exists {
			pool.flights[name] = true
		}
		pool.mu.Unlock()
		return fallback, true
	}
}

func (s *Server) publishHealth(r *health.Recorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts = r.View()
	s.writeRuntimeSnapshotLocked(s.lastRegistryFingerprint, s.lastSnapshotComplete)
}
