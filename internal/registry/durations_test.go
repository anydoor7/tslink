package registry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
)

func durationStore(t *testing.T) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.json")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, name := range []string{"photos", "finance", "public"} {
		svc := Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000"}
		if name == "public" {
			deadline := now.Add(8 * time.Hour)
			svc.Funnel, svc.PublicAck, svc.FunnelExpiresAt = true, true, &deadline
		}
		if _, err := Add(path, svc); err != nil {
			t.Fatal(err)
		}
	}
	deadline := now.Add(24 * time.Hour)
	if _, err := ChangePerson(path, "alice", []string{"photos", "finance"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	return path, now
}

func TestExtendDurationAtomicPolicyAndRegrant(t *testing.T) {
	for _, tc := range []struct {
		name, who, value    string
		old                 time.Duration
		latch, regrant, ack bool
		reason              string
	}{
		{"shorten", "alice", "1h", 24 * time.Hour, false, false, false, ""},
		{"extend", "alice", "36h", 24 * time.Hour, false, false, false, ""},
		{"absolute", "alice", "until 2030-01-01T03:00:00Z", 24 * time.Hour, false, false, false, ""},
		{"member never ack", "alice", "never", time.Hour, false, false, true, ""},
		{"member never unacked", "alice", "never", time.Hour, false, false, false, "ack-never"},
		{"expired person", "alice", "1h", -time.Second, false, false, false, "already expired"},
		{"boundary person", "alice", "1h", 0, false, false, false, "already expired"},
		{"latched person", "alice", "1h", time.Hour, true, false, false, "already expired"},
		{"regrant person", "alice", "1h", -time.Hour, true, true, false, ""},
		{"public shorten", "", "1h", 8 * time.Hour, false, false, false, ""},
		{"public extend", "", "7d", 8 * time.Hour, false, false, false, ""},
		{"public over max", "", "7d1s", 8 * time.Hour, false, false, false, "exceeds"},
		{"public never", "", "never", 8 * time.Hour, false, false, true, "only"},
		{"past until", "", "until 2029-01-01", 8 * time.Hour, false, false, false, "future"},
		{"below min", "", "59m", 8 * time.Hour, false, false, false, "at least 1h"},
		{"expired Funnel", "", "1h", -time.Second, false, false, false, "already expired"},
		{"regrant Funnel", "", "1h", -time.Hour, false, true, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, now := durationStore(t)
			reg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			old := now.Add(tc.old)
			if tc.who != "" {
				reg.People[0].Grants[1].ExpiresAt = &old
				reg.People[0].Grants[1].Expired = tc.latch
			} else {
				reg.Services[2].FunnelExpiresAt = &old
				if tc.old <= 0 {
					reg.Services[2].Funnel = false
				}
			}
			if err := save(path, reg); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			service := "public"
			if tc.who != "" {
				service = "photos"
			}
			result, err := ExtendDuration(path, ExtendOptions{Service: service, Who: tc.who, Value: tc.value, Regrant: tc.regrant, AckNever: tc.ack, Now: now})
			if tc.reason != "" {
				if err == nil || !strings.Contains(err.Error(), tc.reason) {
					t.Fatalf("%v want %s", err, tc.reason)
				}
				after, _ := os.ReadFile(path)
				if string(before) != string(after) {
					t.Fatal("refused operation wrote registry")
				}
				return
			}
			if err != nil || !result.PreviousExpiresAt.Equal(old) || !result.ChangedAt.Equal(now) || result.Regranted != (tc.old <= 0 || tc.latch) {
				t.Fatalf("%+v %v", result, err)
			}
			reg, err = Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.who != "" {
				if reg.People[0].Grants[1].Expired || !sameDurationDeadline(reg.People[0].Grants[1].ExpiresAt, result.ExpiresAt) || !reg.People[0].Grants[0].ExpiresAt.Equal(now.Add(24*time.Hour)) {
					t.Fatalf("scope/latch: %+v", reg.People)
				}
			} else if !reg.Services[2].Funnel || !sameDurationDeadline(reg.Services[2].FunnelExpiresAt, result.ExpiresAt) {
				t.Fatal("Funnel not rearmed/saved")
			}
		})
	}
}

func sameDurationDeadline(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func TestExtendDurationFailureAndConcurrentWriters(t *testing.T) {
	path, now := durationStore(t)
	for _, tc := range []ExtendOptions{
		{Service: "bad name"}, {Service: "missing"}, {Service: "photos"}, {Service: "public", Who: "missing"},
		{Service: "photos", Who: "bad login"}, {Service: "public", Who: "alice"},
	} {
		tc.Now, tc.Value = now, "1h"
		if _, err := ExtendDuration(path, tc); err == nil {
			t.Fatal("invalid target admitted", tc)
		}
	}
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtendDuration(path, ExtendOptions{Service: "photos", Who: "alice", Value: "1h", Regrant: true, Now: now}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("revoked revived: %v", err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy permanent Funnel can become finite; no storage migration is needed.
	reg.Services[2].FunnelExpiresAt = nil
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	if result, err := ExtendDuration(path, ExtendOptions{Service: "public", Value: "1h", Now: now}); err != nil || result.PreviousExpiresAt != nil {
		t.Fatalf("%+v %v", result, err)
	}
	path, now = durationStore(t)
	var wg sync.WaitGroup
	failures := make(chan error, 2)
	for _, tc := range []ExtendOptions{{Service: "photos", Who: "alice", Value: "36h", Now: now}, {Service: "public", Value: "3d", Now: now}} {
		wg.Go(func() { _, err := ExtendDuration(path, tc); failures <- err })
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	reg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reg.People[0].Grants[1].ExpiresAt.Equal(now.Add(36*time.Hour)) || !reg.Services[2].FunnelExpiresAt.Equal(now.Add(72*time.Hour)) {
		t.Fatal("concurrent update lost")
	}
	for _, body := range []string{`{`, `{"schema_version":2,"services":[],"future":true}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ExtendDuration(path, ExtendOptions{Service: "public", Value: "1h", Now: now}); err == nil {
			t.Fatal("malformed registry admitted")
		}
		after, _ := os.ReadFile(path)
		if string(after) != body {
			t.Fatal("malformed input overwritten")
		}
	}
	if _, err := ExtendDuration(filepath.Join(path, "child"), ExtendOptions{Service: "public", Value: "1h", Now: now}); err == nil {
		t.Fatal("non-directory parent admitted")
	}
}

func TestPeopleUnifiedLifetimesAndGuestPolicy(t *testing.T) {
	path, now := durationStore(t)
	for _, value := range []string{"90m", "36h", "3d", "1w", "until 2030-01-02T12:00:00Z"} {
		p, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Now: now, Audience: duration.TailnetMember})
		if err != nil || p.Grants[0].ExpiresAt == nil {
			t.Fatalf("%+v %v", p, err)
		}
	}
	value := "never"
	if _, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Now: now, Audience: duration.TailnetMember}); err == nil || !strings.Contains(err.Error(), "ack-never") {
		t.Fatal(err)
	}
	if _, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Now: now, Audience: duration.TailnetMember, AckNever: true}); err != nil {
		t.Fatal(err)
	}
	value = "7d1s"
	if _, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Now: now, Audience: duration.Guest}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal(err)
	}
	p, err := ChangePersonWithLifetime(path, "bob", []string{"photos"}, false, PersonLifetimeOptions{Now: now, Audience: duration.TailnetMember})
	if err != nil || !p.Grants[0].ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("default: %+v %v", p, err)
	}
	p, err = ChangePersonWithLifetime(path, "bob", []string{"photos", "finance"}, true, PersonLifetimeOptions{Now: now.Add(time.Hour), Audience: duration.TailnetMember})
	if err != nil || !p.Grants[0].ExpiresAt.Equal(now.Add(25*time.Hour)) || !p.Grants[1].ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("new/retained: %+v %v", p, err)
	}
	p, err = ChangePersonWithLifetime(path, "bob", []string{"all"}, true, PersonLifetimeOptions{Now: now, Audience: duration.TailnetMember})
	if err != nil || len(p.Grants) != 2 {
		t.Fatalf("all: %+v %v", p, err)
	}
	// An invite-only update must preserve existing deadlines even under a tighter cap.
	p, err = ChangePersonWithLifetime(path, "bob", nil, true, PersonLifetimeOptions{Now: now, Audience: duration.TailnetMember, Policy: duration.Policy{PublicMax: time.Hour}})
	if err != nil || !p.Grants[1].ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("preserve: %+v %v", p, err)
	}
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.People[0].Invites = []PersonInvite{{App: "photos", Hostname: "photos", NodeID: "n1", State: PersonInvitePending}}
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	value = "never"
	if _, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &value, Now: now, Audience: duration.TailnetMember, AckNever: true}); err == nil || !strings.Contains(err.Error(), "only") {
		t.Fatal(err)
	}
	if _, err := ExtendDuration(path, ExtendOptions{Service: "photos", Who: "alice", Value: "7d1s", Now: now}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatal(err)
	}
}

func TestFirstGuestInviteChecksRetainedLifetimes(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		apps        []string
		reason      string
	}{
		{"permanent", "never", nil, "never is allowed only"},
		{"overlong", "8d", nil, "exceeds"},
		{"below min", "1h", nil, "at least 1h"},
		{"finite control", "36h", nil, ""},
		{"selected control", "36h", []string{"photos"}, ""},
		{"all control", "36h", []string{"all"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, now := durationStore(t)
			if _, err := ChangePersonWithLifetime(path, "alice", nil, true, PersonLifetimeOptions{Value: &tc.value, Now: now, Audience: duration.TailnetMember, AckNever: true}); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			_, err := ChangePersonWithLifetime(path, "alice", tc.apps, true, PersonLifetimeOptions{Now: now.Add(time.Minute), Audience: duration.Guest})
			if tc.reason != "" {
				if err == nil || !strings.Contains(err.Error(), "first guest invitation") || !strings.Contains(err.Error(), tc.reason) {
					t.Fatal(err)
				}
				after, _ := os.ReadFile(path)
				if string(before) != string(after) {
					t.Fatal("failed guest transition wrote registry")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDurationStoreUnusualStateAndSaveFailure(t *testing.T) {
	path, now := durationStore(t)
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services[0].Type, reg.Services[0].Target, reg.Services[0].PeopleScoped = TypeTCP, "localhost:3000", false
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtendDuration(path, ExtendOptions{Service: "photos", Who: "alice", Value: "1h", Now: now}); err == nil || !strings.Contains(err.Error(), "private HTTP/file") {
		t.Fatal(err)
	}
	path, now = durationStore(t)
	reg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services[2].Funnel = false
	old := now.Add(-time.Hour)
	reg.Services[2].FunnelExpiresAt = &old
	reg.Services[2].PeopleScoped = true
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtendDuration(path, ExtendOptions{Service: "public", Value: "1h", Now: now, Regrant: true}); err == nil || !strings.Contains(err.Error(), "person-scoped") {
		t.Fatal(err)
	}
	path, now = durationStore(t)
	original := marshalFn
	t.Cleanup(func() { marshalFn = original })
	failure := errors.New("duration write interrupted")
	marshalFn = func(any, string, string) ([]byte, error) { return nil, failure }
	before, _ := os.ReadFile(path)
	result, err := ExtendDuration(path, ExtendOptions{Service: "public", Value: "1h", Now: now})
	if !errors.Is(err, failure) || result.Service != "" {
		t.Fatalf("failed save: %+v %v", result, err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed save changed registry")
	}
	marshalFn = original
	// Cover default lifetime for new apps selected through all.
	if _, err := ChangePersonWithLifetime(path, "alice", []string{"photos"}, true, PersonLifetimeOptions{Now: now, Audience: duration.TailnetMember}); err != nil {
		t.Fatal(err)
	}
	p, err := ChangePersonWithLifetime(path, "alice", []string{"all"}, true, PersonLifetimeOptions{Now: now, Audience: duration.TailnetMember})
	if err != nil || len(p.Grants) != 2 || !p.Grants[0].ExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("all new grant: %+v %v", p, err)
	}
}
