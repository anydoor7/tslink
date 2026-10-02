package registry

import (
	"fmt"
	"time"

	"github.com/anydoor7/tslink/internal/errcode"
)

// PersonInvite is a durable operation and non-secret person/app association.
// Sending survives a crash as an unknown outcome, never as permission to POST.
// Bearer URLs and credentials must never be stored here.
type PersonInvite struct {
	App      string `json:"app"`
	Hostname string `json:"hostname"`
	NodeID   string `json:"node_id"`
	ID       string `json:"id,omitempty"`
	State    string `json:"state"`
	// Attempt zero is the original schema-2 operation. Later attempts retain
	// terminal records instead of overwriting person/app/node evidence.
	Attempt uint64 `json:"attempt,omitempty"`
}

const (
	PersonInvitePending    = "pending"
	PersonInviteSending    = "sending"
	PersonInviteUnknown    = "unknown"
	PersonInviteComplete   = "complete"
	PersonInviteRevoked    = "revoked"
	PersonInviteAccepted   = "accepted"
	PersonInviteCancelled  = "cancelled"
	PersonInviteTargetGone = "target_gone"
	PersonInviteReplaced   = "replaced"
)

func PersonInviteTerminal(op PersonInvite) bool {
	return op.State == PersonInviteRevoked || op.State == PersonInviteAccepted || op.State == PersonInviteCancelled || op.State == PersonInviteTargetGone || op.State == PersonInviteReplaced
}

func validatePersonInvites(ops []PersonInvite) error {
	seen := map[string]bool{}
	active := map[string]bool{}
	for _, op := range ops {
		if err := ValidateName(op.App); err != nil {
			return err
		}
		if err := ValidateName(op.Hostname); err != nil {
			return err
		}
		if op.NodeID == "" {
			return fmt.Errorf("invite operation requires node ID")
		}
		for _, c := range op.ID {
			if c < '0' || c > '9' {
				return fmt.Errorf("invite ID must contain ASCII digits only")
			}
		}
		switch op.State {
		case PersonInvitePending, PersonInviteSending, PersonInviteUnknown, PersonInviteComplete, PersonInviteRevoked, PersonInviteAccepted, PersonInviteCancelled, PersonInviteTargetGone, PersonInviteReplaced:
		default:
			return fmt.Errorf("unsupported invite operation state %q", op.State)
		}
		if (op.State == PersonInviteComplete || op.State == PersonInviteAccepted || op.State == PersonInviteRevoked || op.State == PersonInviteReplaced) && op.ID == "" {
			return fmt.Errorf("completed invite operation requires ID")
		}
		target := op.App + "\x00" + op.NodeID
		key := fmt.Sprintf("%s\x00%d", target, op.Attempt)
		if seen[key] {
			return fmt.Errorf("duplicate invite operation for %s", op.App)
		}
		seen[key] = true
		if !PersonInviteTerminal(op) {
			if active[target] {
				return fmt.Errorf("multiple active invite attempts for %s", op.App)
			}
			active[target] = true
		}
	}
	return nil
}

// TryPeopleInviteWork serializes remote invite work across owner processes on
// a separate lock. Local removal/expiry never waits for network I/O. A busy
// caller reports deferred work rather than racing a POST or DELETE.
func TryPeopleInviteWork(path string, fn func() error) (bool, error) {
	return tryWithLock(path+".people-invites", fn)
}

// PersonInviteSaveOptions supplies a clock and optional compare-and-swap proof.
// ResetConfirmed permits sending/unknown -> pending only after the caller has
// proved no POST was attempted or explicitly reconciled an empty remote list.
// Callers must hold TryPeopleInviteWork across remote proof and transitions.
type PersonInviteSaveOptions struct {
	Now            time.Time
	Expected       *PersonInvite
	ResetConfirmed bool
}

// SavePersonInvite retains the compatibility signature and uses wall time.
// It cannot erase an ambiguous send or overwrite a terminal attempt.
func SavePersonInvite(path, who string, op PersonInvite) error {
	return SavePersonInviteWithOptions(path, who, op, PersonInviteSaveOptions{Now: time.Now()})
}

// SavePersonInviteWithOptions changes only one attempt under the writer lock.
// Late outcomes may be recorded after revocation/expiry; new sends may not.
func SavePersonInviteWithOptions(path, who string, op PersonInvite, opts PersonInviteSaveOptions) error {
	login, err := ResolvePersonLogin(path, who)
	if err != nil {
		return err
	}
	if err := validatePersonInvites([]PersonInvite{op}); err != nil {
		return CodedError{Code: errcode.UsageError, Message: err.Error()}
	}
	if opts.Now.IsZero() {
		return CodedError{Code: errcode.UsageError, Message: "invite transition requires a clock"}
	}
	return withLock(path, func() error {
		reg, err := loadForMutation(path)
		if err != nil {
			return err
		}
		for i := range reg.People {
			p := &reg.People[i]
			if p.Login != login {
				continue
			}
			if op.State == PersonInvitePending || op.State == PersonInviteSending {
				if !PersonGrantActiveAt(*p, op.App, opts.Now) {
					return CodedError{Code: errcode.Conflict, Message: "person grant removed or expired before invite send"}
				}
			}
			found := false
			for j := range p.Invites {
				old := p.Invites[j]
				if old.App == op.App && old.NodeID == op.NodeID && old.Attempt == op.Attempt {
					if opts.Expected != nil && old != *opts.Expected {
						return CodedError{Code: errcode.Conflict, Message: "invite attempt changed before transition"}
					}
					if PersonInviteTerminal(old) && old != op {
						return CodedError{Code: errcode.Conflict, Message: "terminal invite attempt cannot be overwritten"}
					}
					if old.ID != "" && op.ID != old.ID {
						return CodedError{Code: errcode.Conflict, Message: "recorded invite ID cannot be changed"}
					}
					if old.Hostname != op.Hostname || (op.State == PersonInviteSending && old.State != PersonInvitePending && old.State != PersonInviteSending) {
						return CodedError{Code: errcode.Conflict, Message: "invite ownership or send state cannot be replaced"}
					}
					if (old.State == PersonInviteUnknown || old.State == PersonInviteSending) && op.State == PersonInvitePending && !opts.ResetConfirmed {
						return CodedError{Code: errcode.Conflict, Message: "ambiguous invite send requires explicit absence reconciliation"}
					}
					p.Invites[j] = op
					found = true
					break
				}
			}
			if !found {
				if opts.Expected != nil {
					return CodedError{Code: errcode.Conflict, Message: "expected invite attempt is missing"}
				}
				p.Invites = append(p.Invites, op)
			}
			if err := validatePersonInvites(p.Invites); err != nil {
				return CodedError{Code: errcode.Conflict, Message: err.Error()}
			}
			return save(path, reg)
		}
		return CodedError{Code: errcode.NotFound, Message: fmt.Sprintf("person not found: %s", login)}
	})
}
