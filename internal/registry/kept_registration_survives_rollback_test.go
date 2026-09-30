package registry

import (
	"errors"
	"os"
	"testing"
	"time"
)

// B6a-5 (audit X4-5). A share that created a registration removes it again
// when its wait fails. Content equality cannot tell that removal whether
// another call relies on the registration by now, so a creation is tentative
// until someone keeps it, and RemoveIfUnchanged undoes only a creation that is
// still tentative.

func tentativeFixture(t *testing.T) (path string, created Service) {
	t.Helper()
	path = testRegistryPath(t)
	svc := Service{Name: "share", Type: TypeProxy, Target: "http://127.0.0.1:3000", CreatedAt: time.Date(2035, 1, 1, 0, 0, 0, 123456789, time.UTC)}
	if made, err := AddTentative(path, svc); err != nil || !made {
		t.Fatalf("AddTentative() = %v, %v", made, err)
	}
	// The creator undoes with the service it built, as share does.
	return path, svc
}

func storedService(t *testing.T, path, name string) Service {
	t.Helper()
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, svc := range reg.Services {
		if svc.Name == name {
			return svc
		}
	}
	t.Fatalf("%q is not registered; services = %+v", name, reg.Services)
	return Service{}
}

func registeredCount(t *testing.T, path string) int {
	t.Helper()
	reg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(reg.Services)
}

// The core of the fix, with its control: the same undo removes a registration
// nobody kept and leaves one another caller kept.
func TestRemoveIfUnchangedLeavesARegistrationAnotherCallerKept(t *testing.T) {
	for _, tc := range []struct {
		name        string
		keptBefore  bool
		wantRemoved bool
	}{
		{name: "control: nobody kept it", keptBefore: false, wantRemoved: true},
		{name: "another caller kept it", keptBefore: true, wantRemoved: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, created := tentativeFixture(t)
			if tc.keptBefore {
				// The reusing caller holds the registration as it read it.
				if kept, err := KeepIfUnchanged(path, storedService(t, path, created.Name)); err != nil || !kept {
					t.Fatalf("KeepIfUnchanged() = %v, %v", kept, err)
				}
			}
			removed, err := RemoveIfUnchanged(path, created)
			if err != nil || removed != tc.wantRemoved {
				t.Fatalf("RemoveIfUnchanged() = %v, %v; want removed=%v", removed, err, tc.wantRemoved)
			}
			if got, want := registeredCount(t, path), map[bool]int{true: 0, false: 1}[tc.wantRemoved]; got != want {
				t.Fatalf("registered services = %d, want %d", got, want)
			}
			if _, err := os.Stat(tentativeMarkPath(path, created.Name)); !os.IsNotExist(err) {
				t.Fatalf("tentative mark after the undo or keep: stat err = %v, want it gone", err)
			}
		})
	}
}

// A registration that was never tentative is never undone: the undo belongs
// to the call that created it.
func TestRemoveIfUnchangedLeavesARegistrationNobodyMarkedTentative(t *testing.T) {
	path := testRegistryPath(t)
	svc := Service{Name: "plain", Type: TypeProxy, Target: "http://127.0.0.1:3000"}
	if _, err := Add(path, svc); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveIfUnchanged(path, storedService(t, path, svc.Name))
	if err != nil || removed {
		t.Fatalf("RemoveIfUnchanged() = %v, %v; want the untentative registration kept", removed, err)
	}
	if registeredCount(t, path) != 1 {
		t.Fatal("untentative registration was removed")
	}
}

// Keep settles only the registration the caller read. A changed one is
// reported, and its creator's undo still cannot match it.
func TestKeepIfUnchangedReportsAChangedRegistration(t *testing.T) {
	path, created := tentativeFixture(t)
	changed := created
	changed.Target = "http://127.0.0.1:4000"
	if _, err := Add(path, changed); err != nil {
		t.Fatal(err)
	}
	kept, err := KeepIfUnchanged(path, created)
	if err != nil || kept {
		t.Fatalf("KeepIfUnchanged(stale) = %v, %v; want false", kept, err)
	}
	if removed, err := RemoveIfUnchanged(path, created); err != nil || removed {
		t.Fatalf("RemoveIfUnchanged(stale) = %v, %v; want the changed service kept", removed, err)
	}
	if _, err := Remove(path, created.Name); err != nil {
		t.Fatal(err)
	}
	if kept, err := KeepIfUnchanged(path, created); err != nil || kept {
		t.Fatalf("KeepIfUnchanged(removed) = %v, %v; want false", kept, err)
	}
}

// A failed save leaves neither the registration nor a mark, and a mark that
// cannot be written registers nothing.
func TestAddTentativeLeavesNothingBehindWhenItFails(t *testing.T) {
	svc := Service{Name: "share", Type: TypeProxy, Target: "http://127.0.0.1:3000"}
	t.Run("registry write fails", func(t *testing.T) {
		path := testRegistryPath(t)
		old := marshalFn
		marshalFn = func(any, string, string) ([]byte, error) { return nil, errors.New("synthetic marshal failure") }
		t.Cleanup(func() { marshalFn = old })
		if made, err := AddTentative(path, svc); err == nil || made {
			t.Fatalf("AddTentative() = %v, %v; want the write failure", made, err)
		}
		marshalFn = old
		if registeredCount(t, path) != 0 {
			t.Fatal("a failed AddTentative registered the service")
		}
		if _, err := os.Stat(tentativeMarkPath(path, svc.Name)); !os.IsNotExist(err) {
			t.Fatalf("tentative mark after a failed save: stat err = %v, want none", err)
		}
	})
	t.Run("mark write fails", func(t *testing.T) {
		path := testRegistryPath(t)
		if err := os.Mkdir(tentativeMarkPath(path, svc.Name), 0o700); err != nil {
			t.Fatal(err)
		}
		if made, err := AddTentative(path, svc); err == nil || made {
			t.Fatalf("AddTentative() = %v, %v; want the mark failure", made, err)
		}
		if registeredCount(t, path) != 0 {
			t.Fatal("AddTentative registered a service it could not mark tentative")
		}
	})
}
