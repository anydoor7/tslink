package registry

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/filelock"
	"github.com/anydoor7/tslink/internal/testwait"
)

func TestPersonAuthorityUsesLockedPreMutationState(t *testing.T) {
	for _, operation := range []string{"add", "update", "remove", "extend", "grant", "approve", "deny"} {
		t.Run(operation, func(t *testing.T) {
			path := requestStore(t)
			if operation != "add" {
				if _, err := ChangePerson(path, "owner", []string{"photos"}, nil, false, false); err != nil {
					t.Fatal(err)
				}
			}
			r, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if err := filelock.Lock(lock); err != nil {
				t.Fatal(err)
			}
			locked := true
			defer func() {
				if locked {
					_ = filelock.Unlock(lock)
				}
			}()
			denied := errors.New("locked current owner required")
			authorize := func(reg *Registry) error {
				if reg.Portal.Owner != "replacement" || len(reg.Portal.Admins) != 1 || reg.Portal.Admins[0] != "owner" {
					return errors.New("authorization did not read replacement owner/admins")
				}
				for _, p := range reg.People {
					if p.Login == "owner" && p.Revoked {
						return denied
					}
				}
				return errors.New("authorization did not see pre-mutation tombstone")
			}
			personAuthorize := func(reg *Registry, login string) error {
				if login != "owner" {
					return errors.New("authorization did not use canonical target")
				}
				return authorize(reg)
			}
			value := "8h"
			done, started := make(chan error, 1), make(chan struct{})
			go func() {
				close(started)
				var err error
				switch operation {
				case "add", "update":
					_, err = ChangePersonWithLifetime(path, " OwNeR ", []string{"photos"}, operation == "update", PersonLifetimeOptions{Value: &value, Now: requestTestNow, Authorize: personAuthorize})
				case "remove":
					_, err = RemovePersonAuthorized(path, " OwNeR ", personAuthorize)
				case "extend":
					_, err = ExtendDuration(path, ExtendOptions{Service: "photos", Who: " OwNeR ", Value: value, Now: requestTestNow, Authorize: personAuthorize})
				case "grant":
					_, err = ChangePersonAppWithLifetime(path, " OwNeR ", "photos", PersonLifetimeOptions{Value: &value, Now: requestTestNow, Authorize: personAuthorize})
				case "approve", "deny":
					status := RequestApproved
					if operation == "deny" {
						status = RequestDenied
					}
					_, _, err = DecideAccessRequestAuthorized(path, r.ID, status, value, "", false, duration.Policy{}, requestTestNow, authorize)
				}
				done <- err
			}()
			<-started
			reg, _, err := Preflight(path)
			if err != nil {
				t.Fatal(err)
			}
			reg.Portal.Owner, reg.Portal.Admins = "replacement", []string{"owner"}
			reg.People = []Person{{Login: "owner", Revoked: true, Grants: []PersonGrant{}}}
			if err := save(path, reg); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := filelock.Unlock(lock); err != nil {
				t.Fatal(err)
			}
			locked = false
			if err := testwait.Recv(t, done, "registry writer finished after lock release"); !errors.Is(err, denied) {
				t.Fatal("locked pre-mutation authorization did not refuse", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused writer changed registry", err)
			}
		})
	}
}

func TestAuthorizedRequestListFailurePaths(t *testing.T) {
	for _, failure := range []string{"none", "authority", "busy", "save", "missing", "corrupt"} {
		t.Run(failure, func(t *testing.T) {
			path := requestStore(t)
			if _, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected list failure")
			called := false
			authorize := func(reg *Registry) error {
				called = true
				if reg.Portal.Owner != "owner" || reg.Requests[0].Status != RequestPending {
					return errors.New("list authorization did not see pre-maintenance state")
				}
				if failure == "authority" {
					return injected
				}
				return nil
			}
			switch failure {
			case "busy":
				lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				if err := filelock.Lock(lock); err != nil {
					t.Fatal(err)
				}
				defer filelock.Unlock(lock)
			case "save":
				old := marshalFn
				marshalFn = func(any, string, string) ([]byte, error) { return nil, injected }
				defer func() { marshalFn = old }()
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				before = []byte(`{"version":`)
				if err := os.WriteFile(path, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := ListAccessRequestsAuthorized(path, requestTestNow.Add(RequestPendingLifetime), authorize)
			if failure == "none" {
				if err != nil || !called || len(rows) != 1 || rows[0].Status != RequestExpired {
					t.Fatal(rows, called, err)
				}
				rows, err = ListAccessRequestsAuthorized(path, requestTestNow.Add(-time.Hour), func(*Registry) error { return nil })
				if err != nil || len(rows) != 1 || rows[0].Status != RequestExpired {
					t.Fatal("expiry was not latched durably", rows, err)
				}
				return
			}
			if err == nil || rows != nil {
				t.Fatal("failed list returned request rows", rows, err)
			}
			if failure == "authority" || failure == "save" {
				if !called || !errors.Is(err, injected) {
					t.Fatal("wrong failure path", called, err)
				}
			} else if failure == "busy" {
				requestCode(t, err, "access_request_busy")
				if called {
					t.Fatal("authorization ran without the lock")
				}
			}
			if failure != "missing" {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("failed list changed registry bytes", err)
				}
			}
		})
	}
}
