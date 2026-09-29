package server

import (
	"slices"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
)

// The production schedule, in wall time: the next 30 s tick, then doubling up
// to 15 minutes. The expected durations are the requirement, not a restatement
// of the constants, and the daily figure is what a service blocked for good
// costs at the cap.
func TestPolicyRetryBackoffProductionScheduleCapsAtFifteenMinutes(t *testing.T) {
	const tick = 30 * time.Second
	capTicks := policyRetryWaitCapTicks(tick)
	var got []time.Duration
	for streak := 1; streak <= 8; streak++ {
		got = append(got, time.Duration(policyRetryWaitTicks(streak, capTicks))*tick)
	}
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	if !slices.Equal(got, want) {
		t.Fatalf("retry waits = %v, want %v", got, want)
	}
	if perDay := int(24 * time.Hour / (time.Duration(capTicks) * tick)); perDay != 96 {
		t.Fatalf("policy requests per day at the cap = %d, want 96", perDay)
	}
}

// The cap holds on the running ticker too: with a cap of 4 ticks the waits run
// 1, 2, 4, 4, 4.
func TestPolicyRetryBackoffStopsDoublingAtTheCap(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := config.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, []registry.Service{publicPolicyService("http://localhost:3000")})
	oldMax := policyRetryMaxWait
	policyRetryMaxWait = 4 * time.Millisecond // runLifecycleTicks ticks every millisecond
	t.Cleanup(func() { policyRetryMaxWait = oldMax })
	r := &policyTickRecorder{}
	s := newPolicyBlockedServer(t, r)

	runLifecycleTicks(t, s, r, 19, nil)

	if got, want := r.callTicks(19), []int32{0, 1, 3, 7, 11, 15, 19}; !slices.Equal(got, want) {
		t.Fatalf("policy requests on ticks %v, want %v", got, want)
	}
}

// A sync that leaves no policy failure ends the streak, so a later failure
// starts again from a retry on the next tick.
func TestPolicyRetryBackoffResetsAfterASyncWithoutPolicyFailure(t *testing.T) {
	var b policyRetryBackoff
	for i := 0; i < 4; i++ {
		b.record(true, "registry-a")
	}
	if b.tick(30) {
		t.Fatal("retry due one tick after the fourth consecutive blocked sync, want a wait of 8 ticks")
	}
	b.record(false, "registry-a")
	for i := 0; i < 40; i++ {
		if b.tick(30) {
			t.Fatalf("retry due on tick %d after a sync without policy failure", i+1)
		}
	}
	b.record(true, "registry-a")
	if !b.tick(30) {
		t.Fatal("first retry after a new policy failure is not on the next tick")
	}
}
