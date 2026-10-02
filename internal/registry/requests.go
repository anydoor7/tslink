package registry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anydoor7/tslink/internal/duration"
)

const (
	RequestPending         = "pending"
	RequestApproved        = "approved"
	RequestDenied          = "denied"
	RequestExpired         = "expired"
	MaxAccessRequests      = 1000
	RequestPendingLifetime = 7 * 24 * time.Hour
	RequestRetention       = 30 * 24 * time.Hour
	RequestNoteLimit       = 500 // Unicode characters, stored as untrusted text.
)

type AccessRequest struct {
	ID                string          `json:"id"`
	Who               string          `json:"who"`
	App               string          `json:"app"`
	RequestedDuration string          `json:"requested_duration,omitempty"`
	Note              string          `json:"note,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	Status            string          `json:"status"`
	DecidedAt         *time.Time      `json:"decided_at,omitempty"`
	Reason            string          `json:"reason,omitempty"`
	ApprovedFor       string          `json:"approved_for,omitempty"`
	AckNever          bool            `json:"ack_never,omitempty"`
	Grant             *DurationChange `json:"grant,omitempty"`
}

// RequestEvent deliberately excludes visitor notes and owner reasons. Those
// fields are untrusted and may contain secrets; neither alerts nor SSE push them.
type RequestEvent struct {
	ID                string    `json:"id"`
	Who               string    `json:"who"`
	App               string    `json:"app"`
	RequestedDuration string    `json:"requested_duration,omitempty"`
	Status            string    `json:"status"`
	At                time.Time `json:"at"`
}

func (r AccessRequest) Event() RequestEvent {
	at := r.CreatedAt
	if r.DecidedAt != nil {
		at = *r.DecidedAt
	}
	return RequestEvent{r.ID, r.Who, r.App, r.RequestedDuration, r.Status, at}
}

func requestError(code, message string) error { return CodedError{Code: code, Message: message} }

func requestTextValid(s string) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= RequestNoteLimit
}

func validateAccessRequests(requests []AccessRequest) error {
	if len(requests) > MaxAccessRequests {
		return fmt.Errorf("too many access requests")
	}
	ids, open := map[string]bool{}, map[string]bool{}
	for _, r := range requests {
		id, err := hex.DecodeString(r.ID)
		login, le := NormalizePerson(r.Who)
		if err != nil || len(id) != 16 || r.ID != hex.EncodeToString(id) || ids[r.ID] || le != nil || login != r.Who ||
			ValidateName(r.App) != nil || !requestTextValid(r.Note) || !requestTextValid(r.Reason) || len(r.RequestedDuration) > 128 || len(r.ApprovedFor) > 128 || r.CreatedAt.IsZero() {
			return fmt.Errorf("invalid access request")
		}
		ids[r.ID] = true
		if r.RequestedDuration != "" {
			if _, err := duration.ParseLifetime(r.RequestedDuration, r.CreatedAt, time.Local); err != nil {
				return fmt.Errorf("invalid requested duration")
			}
		}
		switch r.Status {
		case RequestPending:
			key := r.Who + "\x00" + r.App
			if open[key] || r.DecidedAt != nil || r.Grant != nil || r.ApprovedFor != "" || r.Reason != "" {
				return fmt.Errorf("invalid pending access request")
			}
			open[key] = true
		case RequestApproved:
			if r.DecidedAt == nil || r.Grant == nil || r.ApprovedFor == "" || r.Grant.Who != r.Who || r.Grant.Service != r.App {
				return fmt.Errorf("invalid approved access request")
			}
		case RequestDenied, RequestExpired:
			if r.DecidedAt == nil || r.Grant != nil || r.ApprovedFor != "" {
				return fmt.Errorf("invalid decided access request")
			}
		default:
			return fmt.Errorf("invalid access request status")
		}
	}
	return nil
}

// Reuse F5's no-follow, nonblocking, 4 MiB reader for every new request I/O.
// A malformed service must not be dropped during a typed mutation.
func loadRequestRegistry(path string) (*Registry, error) {
	reg, issues, err := PortalPreflight(path)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 {
		return nil, requestError("usage_error", "registry has invalid services; repair it before changing requests")
	}
	return reg, nil
}

func pruneAccessRequests(reg *Registry, now time.Time) {
	kept := make([]AccessRequest, 0, len(reg.Requests))
	for _, r := range reg.Requests {
		if r.Status == RequestPending && !now.Before(r.CreatedAt.Add(RequestPendingLifetime)) {
			r.Status = RequestExpired
			at := r.CreatedAt.Add(RequestPendingLifetime).UTC()
			r.DecidedAt = &at
		}
		if r.DecidedAt == nil || now.Before(r.DecidedAt.Add(RequestRetention)) {
			kept = append(kept, r)
		}
	}
	reg.Requests = kept
}

// ListAccessRequests persists expiry/retention lazily, without waiting behind
// a stalled writer. Changed projections require a successful durable save.
func ListAccessRequests(path string, now time.Time) ([]AccessRequest, error) {
	return ListAccessRequestsAuthorized(path, now, nil)
}

// ListAccessRequestsAuthorized reads and maintains the inbox using current
// authority under the write lock. Trusted local reads retain the fast path.
func ListAccessRequestsAuthorized(path string, now time.Time, authorize func(*Registry) error) ([]AccessRequest, error) {
	if authorize != nil {
		var result []AccessRequest
		locked, err := tryWithLock(path, func() error {
			reg, err := loadRequestRegistry(path)
			if err != nil {
				return err
			}
			if err := authorize(reg); err != nil {
				return err
			}
			before := append([]AccessRequest{}, reg.Requests...)
			pruneAccessRequests(reg, now)
			if !reflect.DeepEqual(before, reg.Requests) {
				if err := save(path, reg); err != nil {
					return err
				}
			}
			result = reg.Requests
			return nil
		})
		if err != nil {
			return nil, err
		}
		if !locked {
			return nil, requestError("access_request_busy", "request inbox is busy; try again")
		}
		return result, nil
	}
	reg, err := loadRequestRegistry(path)
	if os.IsNotExist(err) {
		return []AccessRequest{}, nil
	}
	if err != nil {
		return nil, err
	}
	before := append([]AccessRequest{}, reg.Requests...)
	pruneAccessRequests(reg, now)
	if !reflect.DeepEqual(before, reg.Requests) {
		locked, err := tryWithLock(path, func() error {
			current, err := loadRequestRegistry(path)
			if err != nil {
				return err
			}
			pruneAccessRequests(current, now)
			return save(path, current)
		})
		if err != nil {
			return nil, err
		}
		if !locked {
			return nil, requestError("access_request_busy", "request inbox is busy; try again")
		}
	}
	return reg.Requests, nil
}

// SubmitAccessRequest accepts only a WhoIs-resolved human from the portal.
// The HTTP boundary proves membership; this transaction rechecks service and
// person policy under the shared registry lock. Limits survive daemon restarts.
func SubmitAccessRequest(path, who, app, requested, note string, now time.Time) (result AccessRequest, err error) {
	login, e := NormalizePerson(who)
	if e != nil {
		return result, e
	}
	if !requestTextValid(note) || len(requested) > 128 {
		return result, requestError("usage_error", "note is limited to 500 characters; duration to 128 bytes")
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		lifetime, err := duration.ParseLifetime(requested, now, time.Local)
		if err != nil {
			return result, requestError("usage_error", err.Error())
		}
		if strings.HasPrefix(requested, "until ") {
			requested = "until " + lifetime.Deadline.UTC().Format(time.RFC3339Nano)
		}
	}
	locked, err := tryWithLock(path, func() error {
		reg, err := loadRequestRegistry(path)
		if err != nil {
			return err
		}
		if reg.Portal == nil || !reg.Portal.Enabled {
			return requestError("access_request_unavailable", "requests unavailable")
		}
		found := false
		for _, s := range reg.Services {
			if s.Name == app && s.Requestable && PeopleServiceSupported(s) {
				found = true
			}
		}
		if !found {
			return requestError("access_request_unavailable", "app is not requestable")
		}
		for _, p := range reg.People {
			if p.Login == login && (p.Revoked || PersonIsGuest(p)) {
				return requestError("access_request_unavailable", "requests unavailable for this account")
			}
		}
		pruneAccessRequests(reg, now)
		recent, global := 0, 0
		for _, r := range reg.Requests {
			if r.Who == login && r.App == app && r.Status == RequestPending {
				return requestError("access_request_duplicate", "a request for this app is already waiting")
			}
			if !r.CreatedAt.Before(now.Add(-time.Hour)) {
				global++
				if r.Who == login {
					recent++
				}
			}
		}
		if recent >= 5 || global >= 100 {
			return requestError("access_request_rate_limited", "too many requests; try again in an hour")
		}
		if len(reg.Requests) >= MaxAccessRequests {
			return requestError("access_request_capacity", "request inbox is full; ask the owner for help")
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return requestError("access_request_unavailable", "request ID unavailable")
		}
		result = AccessRequest{ID: hex.EncodeToString(id[:]), Who: login, App: app, RequestedDuration: requested, Note: note, CreatedAt: now.UTC(), Status: RequestPending}
		reg.Requests = append(reg.Requests, result)
		return save(path, reg)
	})
	if err == nil && !locked {
		err = requestError("access_request_busy", "request inbox is busy; try again")
	}
	if err != nil {
		return AccessRequest{}, err
	}
	return result, nil
}

// DecideAccessRequest commits the F1 grant and request decision together.
// Exact retries replay the original result. Different decisions/durations
// conflict, including stale approval after denial or expiry.
func DecideAccessRequest(path, id, status, value, reason string, ackNever bool, policy duration.Policy, now time.Time) (result AccessRequest, changed bool, err error) {
	return DecideAccessRequestAuthorized(path, id, status, value, reason, ackNever, policy, now, nil)
}

// DecideAccessRequestAuthorized checks authority before expiry, retry lookup,
// grants or decisions, against the same locked state that will be committed.
func DecideAccessRequestAuthorized(path, id, status, value, reason string, ackNever bool, policy duration.Policy, now time.Time, authorize func(*Registry) error) (result AccessRequest, changed bool, err error) {
	if status != RequestApproved && status != RequestDenied {
		return result, false, requestError("usage_error", "decision must be approved or denied")
	}
	if !requestTextValid(reason) || len(value) > 128 || (status == RequestApproved && value == "") {
		return result, false, requestError("usage_error", "approve requires --for; reason is limited to 500 characters")
	}
	err = withLock(path, func() error {
		reg, err := loadRequestRegistry(path)
		if err != nil {
			return err
		}
		if authorize != nil {
			if err := authorize(reg); err != nil {
				return err
			}
		}
		before := append([]AccessRequest{}, reg.Requests...)
		pruneAccessRequests(reg, now)
		for i, r := range reg.Requests {
			if r.ID != id {
				continue
			}
			if r.Status != RequestPending {
				if !reflect.DeepEqual(before, reg.Requests) {
					if err := save(path, reg); err != nil {
						return err
					}
				}
				if r.Status == status && ((status == RequestApproved && r.ApprovedFor == value && r.AckNever == ackNever) || (status == RequestDenied && r.Reason == reason)) {
					result = r
					return nil
				}
				return requestError("access_request_decided", "request already decided")
			}
			if status == RequestApproved {
				eligible := false
				for _, svc := range reg.Services {
					if svc.Name == r.App && svc.Requestable && PeopleServiceSupported(svc) {
						eligible = true
					}
				}
				if !eligible {
					return requestError("access_request_unavailable", "app is no longer requestable")
				}
				change, err := grantPersonApp(reg, r.Who, r.App, PersonLifetimeOptions{Value: &value, Policy: policy, Now: now, AckNever: ackNever})
				if err != nil {
					return err
				}
				r.Grant, r.ApprovedFor, r.AckNever = &change, value, ackNever
			}
			at := now.UTC()
			r.Status, r.DecidedAt, r.Reason = status, &at, reason
			reg.Requests[i] = r
			if err := save(path, reg); err != nil {
				return err
			}
			result, changed = r, true
			return nil
		}
		if !reflect.DeepEqual(before, reg.Requests) {
			if err := save(path, reg); err != nil {
				return err
			}
		}
		return requestError("not_found", "access request not found")
	})
	if err != nil {
		return AccessRequest{}, false, err
	}
	return result, changed, nil
}
