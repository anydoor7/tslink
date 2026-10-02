package registry

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
)

var requestTestNow = time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)

func requestStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(config.ConfigDirEnv, dir)
	path := filepath.Join(dir, "registry.json")
	for _, name := range []string{"photos", "finance"} {
		if _, err := Add(path, Service{Name: name, Type: TypeProxy, Target: "http://localhost:3000", Requestable: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetPortal(path, &PortalConfig{Enabled: true, Hostname: "home", Owner: "owner"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func requestCode(t *testing.T, err error, want string) {
	t.Helper()
	got, _ := ErrorCode(err)
	if got != want {
		t.Fatalf("code=%q want %q error=%v", got, want, err)
	}
}

func TestAccessRequestStoreUnknownPersonAndGuestPolicy(t *testing.T) {
	path := requestStore(t)
	r, err := SubmitAccessRequest(path, " NEW@EXAMPLE.COM ", "photos", "3d", "hello", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	approved, changed, err := DecideAccessRequest(path, r.ID, RequestApproved, "3d", "", false, duration.Policy{}, requestTestNow)
	if err != nil || !changed || approved.Who != "new@example.com" || approved.Grant.Audience != duration.TailnetMember || !approved.Grant.ExpiresAt.Equal(requestTestNow.Add(72*time.Hour)) {
		t.Fatal(approved, changed, err)
	}
	reg, _, err := Preflight(path)
	if err != nil || len(reg.People) != 1 || !reg.Services[0].PeopleScoped {
		t.Fatal(reg, err)
	}
	// A competing first invitation reserves guest classification before approval.
	r, err = SubmitAccessRequest(path, "new@example.com", "finance", "", "", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	value := "24h"
	if _, err := ChangePersonWithLifetime(path, "new@example.com", nil, true, PersonLifetimeOptions{Value: &value, Audience: duration.Guest, Now: requestTestNow}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	_, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "8d", "", false, duration.Policy{}, requestTestNow)
	requestCode(t, err, "usage_error")
	_, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "never", "", true, duration.Policy{}, requestTestNow)
	requestCode(t, err, "usage_error")
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("refused guest policy wrote store")
	}
	approved, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "1d12h", "", false, duration.Policy{}, requestTestNow)
	if err != nil || approved.Grant.Audience != duration.Guest || !approved.Grant.ExpiresAt.Equal(requestTestNow.Add(36*time.Hour)) {
		t.Fatal(approved, err)
	}
	_, err = SubmitAccessRequest(path, "new@example.com", "finance", "", "", requestTestNow)
	requestCode(t, err, "access_request_unavailable")
}

func TestAccessRequestStoreAtomicAndConcurrent(t *testing.T) {
	path := requestStore(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, duplicates := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if c, _ := ErrorCode(err); c == "access_request_duplicate" || c == "access_request_busy" {
				duplicates++
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if success != 1 || duplicates != 7 {
		t.Fatal(success, duplicates)
	}
	requests, err := ListAccessRequests(path, requestTestNow)
	if err != nil || len(requests) != 1 {
		t.Fatal(requests, err)
	}
	before, _ := os.ReadFile(path)
	old := marshalFn
	marshalFn = func(any, string, string) ([]byte, error) { return nil, errors.New("injected save failure") }
	_, changed, err := DecideAccessRequest(path, requests[0].ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow)
	marshalFn = old
	if err == nil || changed {
		t.Fatal("save failure claimed decision", changed, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("save failure partially approved")
	}
	var outcomes = make(chan string, 2)
	for _, status := range []string{RequestApproved, RequestDenied} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, changed, err := DecideAccessRequest(path, requests[0].ID, status, "8h", "", false, duration.Policy{}, requestTestNow)
			if err == nil && changed {
				outcomes <- r.Status
			} else {
				c, _ := ErrorCode(err)
				outcomes <- c
			}
		}()
	}
	wg.Wait()
	close(outcomes)
	winners, conflicts := 0, 0
	for result := range outcomes {
		if result == RequestApproved || result == RequestDenied {
			winners++
		} else if result == "access_request_decided" {
			conflicts++
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
}

func TestAccessRequestStoreValidationAndRetention(t *testing.T) {
	path := requestStore(t)
	for _, tc := range []struct{ name, who, app, value, note, code string }{
		{"unknown app", "alice", "hidden", "", "", "access_request_unavailable"},
		{"invalid identity", "bad\xff", "photos", "", "", "usage_error"},
		{"long note", "alice", "photos", "", strings.Repeat("中", 501), "usage_error"},
		{"invalid note", "alice", "photos", "", "\xff", "usage_error"},
		{"long duration", "alice", "photos", strings.Repeat("a", 129), "", "usage_error"},
		{"bad duration", "alice", "photos", "nonsense", "", "usage_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SubmitAccessRequest(path, tc.who, tc.app, tc.value, tc.note, requestTestNow)
			requestCode(t, err, tc.code)
		})
	}
	r, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ status, value, reason, code string }{
		{"oops", "8h", "", "usage_error"}, {RequestApproved, "", "", "usage_error"}, {RequestApproved, "30m", "", "usage_error"}, {RequestDenied, "", strings.Repeat("x", 501), "usage_error"},
	} {
		_, _, err := DecideAccessRequest(path, r.ID, tc.status, tc.value, tc.reason, false, duration.Policy{}, requestTestNow)
		requestCode(t, err, tc.code)
	}
	_, _, err = DecideAccessRequest(path, "no-such-id", RequestDenied, "", "", false, duration.Policy{}, requestTestNow)
	requestCode(t, err, "not_found")
	requests, err := ListAccessRequests(path, requestTestNow.Add(7*24*time.Hour))
	if err != nil || len(requests) != 1 || requests[0].Status != RequestExpired || !requests[0].DecidedAt.Equal(requestTestNow.Add(7*24*time.Hour)) {
		t.Fatal(requests, err)
	}
	_, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow.Add(7*24*time.Hour))
	requestCode(t, err, "access_request_decided")
	requests, err = ListAccessRequests(path, requestTestNow.Add(37*24*time.Hour))
	if err != nil || len(requests) != 0 {
		t.Fatal(requests, err)
	}
	requests, err = ListAccessRequests(filepath.Join(t.TempDir(), "missing.json"), requestTestNow)
	if err != nil || requests == nil || len(requests) != 0 {
		t.Fatal(requests, err)
	}
	// Already denied service requests never disclose unknown service state.
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Portal.Enabled = false
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	_, err = SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_unavailable")
}

func TestAccessRequestDurableRestart(t *testing.T) {
	if path := os.Getenv("TSLINK_REQUEST_RESTART_FILE"); path != "" {
		requests, err := ListAccessRequests(path, requestTestNow)
		if err != nil || len(requests) != 1 || requests[0].Note != "restart-note" {
			t.Fatal(requests, err)
		}
		_, _, err = DecideAccessRequest(path, requests[0].ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	path := requestStore(t)
	if _, err := SubmitAccessRequest(path, "alice", "photos", "3d", "restart-note", requestTestNow); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAccessRequestDurableRestart$")
	cmd.Env = append(os.Environ(), "TSLINK_REQUEST_RESTART_FILE="+path)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(b), err)
	}
	requests, err := ListAccessRequests(path, requestTestNow)
	if err != nil || len(requests) != 1 || requests[0].Status != RequestApproved {
		t.Fatal(requests, err)
	}
}

func TestAccessRequestStrictReader(t *testing.T) {
	path := requestStore(t)
	r, err := SubmitAccessRequest(path, "alice", "photos", "3d", "note", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"id", "who", "app", "status", "note", "reason", "date", "duplicate", "duplicate open", "pending grant", "approved missing grant", "decided missing date", "requested", "bound"} {
		t.Run(kind, func(t *testing.T) {
			copy := *reg
			bad := r
			switch kind {
			case "id":
				bad.ID = "x"
			case "who":
				bad.Who = "ALICE"
			case "app":
				bad.App = "bad app"
			case "status":
				bad.Status = "oops"
			case "note":
				bad.Note = strings.Repeat("x", 501)
			case "reason":
				bad.Reason = strings.Repeat("x", 501)
			case "date":
				bad.CreatedAt = time.Time{}
			case "pending grant":
				bad.Grant = &DurationChange{}
			case "approved missing grant":
				bad.Status = RequestApproved
				at := requestTestNow
				bad.DecidedAt = &at
			case "decided missing date":
				bad.Status = RequestDenied
			case "requested":
				bad.RequestedDuration = "bad"
			}
			copy.Requests = []AccessRequest{bad}
			if kind == "duplicate" {
				copy.Requests = append(copy.Requests, bad)
			}
			if kind == "duplicate open" {
				other := bad
				other.ID = strings.Repeat("a", 32)
				copy.Requests = append(copy.Requests, other)
			}
			if kind == "bound" {
				for i := 0; i < 1001; i++ {
					other := bad
					other.ID = fmt.Sprintf("%032x", i)
					other.Who = fmt.Sprintf("u%d", i)
					copy.Requests = append(copy.Requests, other)
				}
			}
			b, _ := json.Marshal(copy)
			if _, _, err := decodeForRuntime(b); err == nil {
				t.Fatal("strict reader accepted", kind)
			}
		})
	}
}

func TestAccessRequestSingleAppContract(t *testing.T) {
	path := requestStore(t)
	value := "8h"
	change, err := ChangePersonAppWithLifetime(path, "alice", "photos", PersonLifetimeOptions{Value: &value, Now: requestTestNow})
	if err != nil || change.Who != "alice" {
		t.Fatal(change, err)
	}
	change, err = ChangePersonAppWithLifetime(path, "alice", "finance", PersonLifetimeOptions{Now: requestTestNow})
	if err != nil || !change.ExpiresAt.Equal(requestTestNow.Add(24*time.Hour)) {
		t.Fatal(change, err)
	}
	value = "never"
	change, err = ChangePersonAppWithLifetime(path, "alice", "photos", PersonLifetimeOptions{Value: &value, AckNever: true, Now: requestTestNow})
	if err != nil || change.ExpiresAt != nil {
		t.Fatal(change, err)
	}
	if _, err := ChangePersonAppWithLifetime(path, "bob", "finance", PersonLifetimeOptions{Now: requestTestNow}); err != nil {
		t.Fatal(err)
	}
	reg, _, err := Preflight(path)
	if err != nil || len(reg.People) != 2 || reg.People[0].Login != "alice" || reg.People[1].Login != "bob" || len(reg.People[0].Grants) != 2 || reg.People[0].Grants[1].ExpiresAt != nil {
		t.Fatal("creating another person changed the first person's grants", reg, err)
	}
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = ChangePersonAppWithLifetime(path, "alice", "photos", PersonLifetimeOptions{Now: requestTestNow})
	requestCode(t, err, "conflict")
	_, err = ChangePersonAppWithLifetime(path, "bob", "missing", PersonLifetimeOptions{Now: requestTestNow})
	requestCode(t, err, "not_found")
	_, err = ChangePersonAppWithLifetime(path, "bad\xff", "photos", PersonLifetimeOptions{Now: requestTestNow})
	requestCode(t, err, "usage_error")
}

func TestAccessRequestCapacityGlobalRateAndStaleApproval(t *testing.T) {
	path := requestStore(t)
	for i := range 100 {
		if _, err := SubmitAccessRequest(path, fmt.Sprintf("u%d", i), "photos", "", "", requestTestNow); err != nil {
			t.Fatal(i, err)
		}
	}
	_, err := SubmitAccessRequest(path, "other", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_rate_limited")
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	r := reg.Requests[0]
	reg.Requests = nil
	for i := range 1000 {
		copy := r
		copy.ID = fmt.Sprintf("%032x", i)
		copy.Who = fmt.Sprintf("u%d", i)
		copy.CreatedAt = requestTestNow.Add(-2 * time.Hour)
		reg.Requests = append(reg.Requests, copy)
	}
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	_, err = SubmitAccessRequest(path, "other", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_capacity")
	reg.Requests = []AccessRequest{r}
	if err := save(path, reg); err != nil {
		t.Fatal(err)
	}
	_, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow.Add(7*24*time.Hour))
	requestCode(t, err, "access_request_decided")
	requests, err := ListAccessRequests(path, requestTestNow)
	if err != nil || requests[0].Status != RequestExpired {
		t.Fatal("stale approval did not latch expiry", requests, err)
	}
	// Bound serialized state, not just number of records. An existing large
	// service inventory cannot make a successful request write unreadable.
	reg.Requests = []AccessRequest{r}
	reg.Services[0].AllowedUsers = []string{strings.Repeat("x", 4<<20)}
	if err := save(path, reg); err == nil {
		t.Fatal("overlarge request registry committed")
	}
}

func TestAccessRequestApprovalRechecksCurrentState(t *testing.T) {
	for _, kind := range []string{"non-requestable", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			path := requestStore(t)
			r, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "revoked" {
				if _, err := RemovePerson(path, "alice"); err != nil {
					t.Fatal(err)
				}
			} else {
				reg, _, err := Preflight(path)
				if err != nil {
					t.Fatal(err)
				}
				svc := reg.Services[0]
				svc.Requestable = false
				if _, err := Add(path, svc); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(path)
			_, _, err = DecideAccessRequest(path, r.ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow)
			want := "access_request_unavailable"
			if kind == "revoked" {
				want = "conflict"
			}
			requestCode(t, err, want)
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("refused approval changed bytes")
			}
		})
	}
	path := requestStore(t)
	r, err := SubmitAccessRequest(path, "alice", "photos", "until 2030-01-03T00:00:00-08:00", "", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if r.RequestedDuration != "until 2030-01-03T08:00:00Z" {
		t.Fatal(r)
	}
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services[0].Requestable = true
	reg.Services[0].Type = TypeTCP
	if err := ValidateService(reg.Services[0]); err == nil {
		t.Fatal("raw TCP requestable admitted")
	}
}

func TestAccessRequestMaintenanceTransactions(t *testing.T) {
	path := requestStore(t)
	r, err := SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if e := r.Event(); e.At != r.CreatedAt || e.Status != RequestPending {
		t.Fatal(e)
	}
	_, err = SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_duplicate")
	decided, changed, err := DecideAccessRequest(path, r.ID, RequestDenied, "", "Later", false, duration.Policy{}, requestTestNow.Add(time.Hour))
	if err != nil || !changed || decided.Event().At != *decided.DecidedAt {
		t.Fatal(decided, changed, err)
	}
	if _, changed, err := DecideAccessRequest(path, r.ID, RequestDenied, "", "Later", false, duration.Policy{}, requestTestNow.Add(2*time.Hour)); err != nil || changed {
		t.Fatal(changed, err)
	}
	r, err = SubmitAccessRequest(path, "bob", "photos", "", "", requestTestNow)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	old := marshalFn
	t.Cleanup(func() { marshalFn = old })
	marshalFn = func(any, string, string) ([]byte, error) { return nil, errors.New("simulated serialization failure") }
	if _, err := ListAccessRequests(path, requestTestNow.Add(7*24*time.Hour)); err == nil {
		t.Fatal("failed expiry save accepted")
	}
	if _, _, err := DecideAccessRequest(path, r.ID, RequestApproved, "8h", "", false, duration.Policy{}, requestTestNow.Add(7*24*time.Hour)); err == nil {
		t.Fatal("failed expired decision save accepted")
	}
	if _, _, err := DecideAccessRequest(path, "unknown", RequestDenied, "", "", false, duration.Policy{}, requestTestNow.Add(37*24*time.Hour)); err == nil {
		t.Fatal("failed retention save accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed maintenance changed persistent bytes")
	}
	marshalFn = old
	_, _, err = DecideAccessRequest(path, "unknown", RequestDenied, "", "", false, duration.Policy{}, requestTestNow.Add(37*24*time.Hour))
	requestCode(t, err, "not_found")
	rows, err := ListAccessRequests(path, requestTestNow.Add(37*24*time.Hour))
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	value := "8h"
	if _, err := ChangePersonAppWithLifetime(path+".missing", "alice", "photos", PersonLifetimeOptions{Value: &value, Policy: duration.Policy{}, Now: requestTestNow}); err == nil {
		t.Fatal("missing person store accepted")
	}
	deadline := requestTestNow.Add(-time.Hour)
	if _, err := ChangePerson(path, "alice", []string{"photos"}, &deadline, true, false); err != nil {
		t.Fatal(err)
	}
	grant, err := ChangePersonAppWithLifetime(path, "alice", "photos", PersonLifetimeOptions{Value: &value, Policy: duration.Policy{}, Now: requestTestNow})
	if err != nil || !grant.Regranted || !grant.ExpiresAt.Equal(requestTestNow.Add(8*time.Hour)) {
		t.Fatal(grant, err)
	}
	if _, err := RemovePerson(path, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = SubmitAccessRequest(path, "alice", "photos", "", "", requestTestNow)
	requestCode(t, err, "access_request_unavailable")
	reg, _, err := Preflight(path)
	if err != nil {
		t.Fatal(err)
	}
	reg.Services = append(reg.Services, Service{Name: "broken", Type: "invalid"})
	b, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ListAccessRequests(path, requestTestNow); err == nil {
		t.Fatal("invalid service accepted for typed maintenance")
	}
}
