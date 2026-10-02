package registry

import "fmt"

// PersonInvite is a durable operation and non-secret person/app association.
// Sending survives a crash as an unknown outcome, never as permission to POST.
// Bearer URLs and credentials must never be stored here.
type PersonInvite struct {
	App      string `json:"app"`
	Hostname string `json:"hostname"`
	NodeID   string `json:"node_id"`
	ID       string `json:"id,omitempty"`
	State    string `json:"state"`
}

const (
	PersonInvitePending   = "pending"
	PersonInviteSending   = "sending"
	PersonInviteUnknown   = "unknown"
	PersonInviteComplete  = "complete"
	PersonInviteRevoked   = "revoked"
	PersonInviteAccepted  = "accepted"
	PersonInviteCancelled = "cancelled"
)

func PersonInviteTerminal(op PersonInvite) bool {
	return op.State == PersonInviteRevoked || op.State == PersonInviteAccepted || op.State == PersonInviteCancelled
}

func validatePersonInvites(ops []PersonInvite) error {
	seen := map[string]bool{}
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
		case PersonInvitePending, PersonInviteSending, PersonInviteUnknown, PersonInviteComplete, PersonInviteRevoked, PersonInviteAccepted, PersonInviteCancelled:
		default:
			return fmt.Errorf("unsupported invite operation state %q", op.State)
		}
		if (op.State == PersonInviteComplete || op.State == PersonInviteAccepted || op.State == PersonInviteRevoked) && op.ID == "" {
			return fmt.Errorf("completed invite operation requires ID")
		}
		key := op.App + "\x00" + op.NodeID
		if seen[key] {
			return fmt.Errorf("duplicate invite operation for %s", op.App)
		}
		seen[key] = true
	}
	return nil
}

// TryPeopleInviteWork serializes remote invite work across owner processes on
// a separate lock. Local removal/expiry never waits for network I/O. A busy
// caller reports deferred work rather than racing a POST or DELETE.
func TryPeopleInviteWork(path string, fn func() error) (bool, error) {
	return tryWithLock(path+".people-invites", fn)
}

// SavePersonInvite changes only the operation under the registry writer lock.
// Results may be recorded after revocation; new sends require an active grant.
func SavePersonInvite(path, who string, op PersonInvite) error {
	login, err := NormalizePerson(who)
	if err != nil {
		return err
	}
	if err := validatePersonInvites([]PersonInvite{op}); err != nil {
		return err
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
				granted := false
				for _, g := range p.Grants {
					if g.App == op.App {
						granted = true
					}
				}
				if p.Revoked || !granted {
					return fmt.Errorf("person grant removed before invite send")
				}
			}
			found := false
			for j := range p.Invites {
				if p.Invites[j].App == op.App && p.Invites[j].NodeID == op.NodeID {
					p.Invites[j] = op
					found = true
					break
				}
			}
			if !found {
				p.Invites = append(p.Invites, op)
			}
			return save(path, reg)
		}
		return fmt.Errorf("person not found: %s", login)
	})
}
