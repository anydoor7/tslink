package registry

import (
	"reflect"
	"testing"
	"time"
)

func TestTentativeRenewalRestoresCreationMark(t *testing.T) {
	for _, tc := range []struct {
		name             string
		settled, adopted bool
		renewals         int
	}{
		{name: "tentative_creation", renewals: 1},
		{name: "settled_creation", settled: true, renewals: 1},
		{name: "adopted_renewal", adopted: true, renewals: 1},
		{name: "nested_renewals", renewals: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := testRegistryPath(t)
			expired := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
			created := Service{Name: "share", Type: TypeProxy, Target: "http://127.0.0.1:3000", CreatedAt: expired.Add(-time.Hour), Funnel: true, PublicAck: true, FunnelExpiresAt: &expired}
			if ok, err := AddTentative(path, created); err != nil || !ok {
				t.Fatalf("create: %v, %v", ok, err)
			}
			if tc.settled {
				if ok, err := KeepIfUnchanged(path, created); err != nil || !ok {
					t.Fatalf("keep: %v, %v", ok, err)
				}
			}
			versions := []Service{created}
			for i := 0; i < tc.renewals; i++ {
				renewed, err := MutateServiceTentative(path, created.Name, func(stored Service) (Service, error) {
					deadline := stored.FunnelExpiresAt.Add(time.Hour)
					stored.FunnelExpiresAt = &deadline
					return stored, nil
				})
				if err != nil {
					t.Fatal(err)
				}
				versions = append(versions, renewed)
			}
			if tc.adopted {
				if ok, err := KeepIfUnchanged(path, versions[len(versions)-1]); err != nil || !ok {
					t.Fatalf("adopt renewal: %v, %v", ok, err)
				}
			}
			for i := len(versions) - 1; i > 0; i-- {
				if ok, err := RestoreTentativeIfUnchanged(path, versions[i], versions[i-1]); err != nil || ok == tc.adopted {
					t.Fatalf("restore renewal %d: %v, %v; adopted=%v", i, ok, err, tc.adopted)
				}
			}
			want := created
			if tc.adopted {
				want = versions[len(versions)-1]
			}
			if got := storedService(t, path, created.Name); !reflect.DeepEqual(got, want) {
				t.Fatalf("stored service = %+v, want %+v", got, want)
			}
			wantRemoved := !tc.settled && !tc.adopted
			if removed, err := RemoveIfUnchanged(path, created); err != nil || removed != wantRemoved {
				t.Fatalf("creator rollback removed=%v, err=%v; want removed=%v", removed, err, wantRemoved)
			}
		})
	}
}
