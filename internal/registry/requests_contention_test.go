package registry

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/filelock"
)

func TestAccessRequestExpiryMaintenanceContention(t *testing.T) {
	if path := os.Getenv("TSLINK_EXPIRY_RESTART_STORE"); path != "" {
		reg, _, err := Preflight(path)
		if err != nil || len(reg.Requests) != 1 || reg.Requests[0].Status != RequestExpired {
			t.Fatal(reg, err)
		}
		_, changed, err := DecideAccessRequest(path, reg.Requests[0].ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow.Add(time.Hour))
		requestCode(t, err, "access_request_decided")
		if changed {
			t.Fatal("expired decision changed after restart and rollback")
		}
		return
	}
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "available_writer", true: "busy_writer"}[busy], func(t *testing.T) {
			path := requestStore(t)
			request, err := SubmitAccessRequest(path, "alice", "photos", "3d", "", requestTestNow)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := os.OpenFile(path+".lock", os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			if busy {
				if err := filelock.Lock(lock); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = filelock.Unlock(lock) })
				rows, err := ListAccessRequests(path, requestTestNow.Add(time.Hour))
				if err != nil || len(rows) != 1 || rows[0].Status != RequestPending {
					t.Fatal("unchanged read under contention", rows, err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			rows, err := ListAccessRequests(path, requestTestNow.Add(7*24*time.Hour))
			if time.Since(start) > time.Second {
				t.Fatal("maintenance read waited for writer")
			}
			if busy {
				requestCode(t, err, "access_request_busy")
				if rows != nil {
					t.Fatal("uncommitted expiry reported", rows)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil || !bytes.Equal(before, after) {
					t.Fatal("busy maintenance modified registry", readErr)
				}
				if err := filelock.Unlock(lock); err != nil {
					t.Fatal(err)
				}
				rows, err = ListAccessRequests(path, requestTestNow.Add(7*24*time.Hour))
			}
			if err != nil || len(rows) != 1 || rows[0].Status != RequestExpired {
				t.Fatal("committed expiry", rows, err)
			}
			_, changed, err := DecideAccessRequest(path, request.ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow.Add(time.Hour))
			requestCode(t, err, "access_request_decided")
			if changed {
				t.Fatal("clock rollback changed expired decision")
			}
			child := exec.Command(os.Args[0], "-test.run=^TestAccessRequestExpiryMaintenanceContention$")
			child.Env = append(os.Environ(), "TSLINK_EXPIRY_RESTART_STORE="+path)
			if output, err := child.CombinedOutput(); err != nil {
				t.Fatalf("restart: %v\n%s", err, output)
			}
		})
	}
}
