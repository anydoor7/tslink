package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/tailapi"
)

const peopleInviteTimeout = 15 * time.Second

func peopleInviteCode(err error, fallback string) string {
	if code, ok := registry.ErrorCode(err); ok {
		return code
	}
	return fallback
}

func readPerson(path, login string) (registry.Person, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return registry.Person{}, err
	}
	if !info.Mode().IsRegular() {
		return registry.Person{}, fmt.Errorf("people invite registry must be a regular file")
	}
	reg, _, err := registry.Preflight(path)
	if err != nil {
		return registry.Person{}, err
	}
	for _, p := range reg.People {
		if p.Login == login {
			return p, nil
		}
	}
	return registry.Person{}, fmt.Errorf("person not found")
}

func personInviteTarget(op registry.PersonInvite) tailapi.DeviceTarget {
	return tailapi.DeviceTarget{Service: op.App, Hostname: op.Hostname, NodeID: op.NodeID}
}

func resumePeopleInvites(ctx context.Context, path string, p registry.Person, targets map[string]tailapi.DeviceTarget, args peopleArguments, result *PeopleResult) {
	ctx, cancel := context.WithTimeout(ctx, peopleInviteTimeout)
	defer cancel()
	acquired, err := registry.TryPeopleInviteWork(path, func() error {
		for _, g := range p.Grants {
			v := resumePeopleInvite(ctx, path, p.Login, targets[g.App], args)
			result.Invites = append(result.Invites, v)
			if v.Code != "" {
				result.Complete = false
			}
		}
		return nil
	})
	if err != nil || !acquired {
		result.Complete = false
		code := "people_invite_busy"
		if err != nil {
			code = peopleInviteCode(err, "invite_state_failed")
		}
		for _, g := range p.Grants {
			result.Invites = append(result.Invites, PeopleInviteView{App: g.App, Code: code})
		}
	}
	if current, err := readPerson(path, p.Login); err == nil {
		urls := map[string]string{}
		for _, g := range result.Person.Grants {
			urls[g.App] = g.URL
		}
		result.Person = peopleView(current, urls, peopleNowFn())
		if current.Revoked {
			result.Complete = false
		}
	}
}

// nextPeopleInvite retains every terminal attempt for this app/node. Zero is
// the original schema-2 slot; later attempts get increasing local generations.
func nextPeopleInvite(p registry.Person, target tailapi.DeviceTarget) registry.PersonInvite {
	op := registry.PersonInvite{App: target.Service, Hostname: target.Hostname, NodeID: target.NodeID, State: registry.PersonInvitePending}
	for _, old := range p.Invites {
		if old.App == op.App && old.NodeID == op.NodeID && old.Attempt >= op.Attempt {
			op.Attempt = old.Attempt + 1
		}
	}
	return op
}

func resumePeopleInvite(ctx context.Context, path, login string, target tailapi.DeviceTarget, args peopleArguments) PeopleInviteView {
	v := PeopleInviteView{App: target.Service}
	p, err := readPerson(path, login)
	if err != nil {
		return peopleInviteFailure(registry.PersonInvite{App: target.Service}, err, "invite_state_failed")
	}
	op := nextPeopleInvite(p, target)
	if p.Revoked || !registry.PersonGrantActiveAt(p, target.Service, peopleNowFn()) {
		return peopleInviteFailure(op, nil, "person_grant_inactive")
	}
	if target.NodeID == "" {
		return peopleInviteFailure(op, nil, registry.CodeInviteOwnershipUnproven)
	}
	var expected *registry.PersonInvite
	var expectedGrant *registry.PersonGrant
	for _, grant := range p.Grants {
		if grant.App == target.Service {
			previous := grant
			expectedGrant = &previous
		}
	}
	for _, existing := range p.Invites {
		if existing.App == target.Service && existing.NodeID == target.NodeID && !registry.PersonInviteTerminal(existing) {
			op = existing
			previous := existing
			expected = &previous
			break
		}
	}
	save := func(next registry.PersonInvite, reset bool) error {
		err := registry.SavePersonInviteWithOptions(path, login, next, registry.PersonInviteSaveOptions{Now: peopleNowFn(), Expected: expected, ResetConfirmed: reset, ExpectedGrant: expectedGrant})
		if err == nil {
			previous := next
			expected = &previous
		}
		return err
	}
	// Replacement is bound to the old ID, so retrying the same owner request
	// can never replace its newly created successor a second time.
	replaceID := args.Replace[target.Service]
	replace := replaceID != "" && op.State == registry.PersonInviteComplete && op.ID == replaceID
	if replaceID != "" && !replace {
		retired := false
		for _, old := range p.Invites {
			if old.App == op.App && old.NodeID == op.NodeID && old.ID == replaceID && old.State == registry.PersonInviteReplaced {
				retired = true
			}
		}
		if !retired {
			return peopleInviteFailure(op, nil, "conflict")
		}
	}
	reconcileID := args.Reconcile[target.Service]
	// Explicit complete+none is a compatibility spelling of replacement. It
	// additionally requires the whole device invite list to be empty.
	replaceAbsent := op.State == registry.PersonInviteComplete && reconcileID == "none"
	if replace || replaceAbsent {
		invites, err := tailapi.ListDeviceInvitesForTarget(ctx, target)
		if err != nil {
			return peopleInviteFailure(op, err, "invite_reconciliation_failed")
		}
		for _, inv := range invites {
			if inv.ID == op.ID || replaceAbsent {
				return peopleInviteFailure(op, nil, "conflict")
			}
		}
		retired := op
		retired.State = registry.PersonInviteReplaced
		if err := save(retired, false); err != nil {
			return peopleInviteFailure(op, err, "invite_state_failed")
		}
		p.Invites = append(p.Invites, retired)
		op = nextPeopleInvite(p, target)
		expected = nil
		// Preserve the new pending attempt before POST. A crash here is resumable
		// without changing grants or forgetting the retired ID/node evidence.
		if err := save(op, false); err != nil {
			return peopleInviteFailure(op, err, "invite_state_failed")
		}
		reconcileID = ""
	}
	if reconcileID != "" && op.State != registry.PersonInviteSending && op.State != registry.PersonInviteUnknown && !(op.State == registry.PersonInviteComplete && reconcileID == op.ID) {
		return peopleInviteFailure(op, nil, "invite_reconciliation_required")
	}
	var invites []tailapi.Invite
	if op.State == registry.PersonInviteSending || op.State == registry.PersonInviteUnknown {
		op, invites, err = reconcilePeopleInvite(ctx, path, login, op, reconcileID)
		for _, inv := range invites {
			v.ReconcileIDs = append(v.ReconcileIDs, inv.ID)
		}
		if err != nil {
			failure := peopleInviteFailure(op, err, "invite_reconciliation_failed")
			failure.ReconcileIDs = v.ReconcileIDs
			return failure
		}
		if err := save(op, op.State == registry.PersonInvitePending); err != nil {
			return peopleInviteFailure(op, err, "invite_state_failed")
		}
	} else if op.State == registry.PersonInviteComplete && args.PrintLinks {
		invites, err = tailapi.ListDeviceInvitesForTarget(ctx, target)
		if err != nil {
			return peopleInviteFailure(op, err, "invite_reconciliation_failed")
		}
	}
	if op.State == registry.PersonInviteComplete {
		v.ID, v.State = op.ID, op.State
		if args.PrintLinks {
			for _, inv := range invites {
				if inv.ID == op.ID {
					v.InviteURL = inv.InviteURL
					break
				}
			}
			if v.InviteURL == "" {
				return peopleInviteFailure(op, nil, "invite_link_unavailable")
			}
		}
		return v
	}
	// Commit BEFORE POST; process death leaves sending, an unknown outcome.
	op.State = registry.PersonInviteSending
	if err := save(op, false); err != nil {
		return peopleInviteFailure(op, err, "invite_state_failed")
	}
	inv, createErr := inviteCreateDeviceFn(ctx, target, login, true, false, false)
	if createErr != nil {
		op.State = registry.PersonInvitePending
		var unknown *tailapi.DeviceInviteOutcomeUnknown
		if errors.As(createErr, &unknown) {
			op.State = registry.PersonInviteUnknown
		}
		if err := save(op, op.State == registry.PersonInvitePending); err != nil {
			return peopleInviteFailure(op, err, "invite_state_failed")
		}
		return peopleInviteFailure(op, createErr, "invite_failed")
	}
	op.State, op.ID = registry.PersonInviteComplete, inv.ID
	if err := save(op, false); err != nil {
		return PeopleInviteView{App: op.App, ID: op.ID, State: registry.PersonInviteUnknown, Code: "invite_state_failed"}
	}
	v.ID, v.State = inv.ID, op.State
	plan := invitePlan(inv, "create")
	v.RemoteSideEffectPlan = &plan
	if args.PrintLinks {
		v.InviteURL = inv.InviteURL
	}
	return v
}

func cleanupPeopleInvites(ctx context.Context, path string, result *PeopleRemoveResult, reconcile map[string]string) {
	ctx, cancel := context.WithTimeout(ctx, peopleInviteTimeout)
	defer cancel()
	p, err := readPerson(path, result.Login)
	if err != nil {
		result.Complete = false
		result.Cleanup = append(result.Cleanup, PeopleInviteView{Code: "invite_state_failed"})
		return
	}
	unfinished := false
	for _, op := range p.Invites {
		if !registry.PersonInviteTerminal(op) {
			unfinished = true
		}
	}
	if !unfinished {
		for _, op := range p.Invites {
			result.Cleanup = append(result.Cleanup, PeopleInviteView{App: op.App, ID: op.ID, State: op.State})
		}
		return
	}
	acquired, err := registry.TryPeopleInviteWork(path, func() error {
		// Re-read after obtaining the operation lock: a POST may have finished
		// between local denial and cleanup.
		current, err := readPerson(path, result.Login)
		if err != nil {
			return err
		}
		for _, op := range current.Invites {
			v := PeopleInviteView{App: op.App, ID: op.ID, State: op.State}
			if !registry.PersonInviteTerminal(op) {
				if op.State == registry.PersonInvitePending {
					op.State = registry.PersonInviteCancelled
				} else {
					if op.ID == "" {
						resolved, invites, e := reconcilePeopleInvite(ctx, path, result.Login, op, reconcile[op.App])
						for _, inv := range invites {
							v.ReconcileIDs = append(v.ReconcileIDs, inv.ID)
						}
						if e != nil {
							var gone *tailapi.PeopleInviteTargetGone
							if errors.As(e, &gone) {
								op.State = registry.PersonInviteTargetGone
							} else {
								v.Code = peopleInviteCode(e, "invite_reconciliation_failed")
							}
						} else {
							op = resolved
							// Persist association before DELETE for crash recovery.
							if op.State == registry.PersonInvitePending {
								op.State = registry.PersonInviteCancelled
							}
							if e := registry.SavePersonInvite(path, result.Login, op); e != nil {
								v.Code = peopleInviteCode(e, "invite_state_failed")
							}
						}
					}
					if v.Code == "" && op.ID != "" && !registry.PersonInviteTerminal(op) {
						state, e := tailapi.RevokePendingDeviceInvite(ctx, personInviteTarget(op), op.ID)
						if e != nil {
							v.Code = peopleInviteCode(e, "invite_cleanup_failed")
						} else {
							op.State = state
						}
					}
				}
				if v.Code == "" {
					if e := registry.SavePersonInvite(path, result.Login, op); e != nil {
						v.Code = peopleInviteCode(e, "invite_state_failed")
					}
				}
			}
			v.State, v.ID = op.State, op.ID
			if v.Code != "" {
				result.Complete = false
			}
			result.Cleanup = append(result.Cleanup, v)
		}
		return nil
	})
	if err != nil || !acquired {
		result.Complete = false
		code := "people_invite_busy"
		if err != nil {
			code = peopleInviteCode(err, "invite_state_failed")
		}
		for _, op := range p.Invites {
			if !registry.PersonInviteTerminal(op) {
				result.Cleanup = append(result.Cleanup, PeopleInviteView{App: op.App, ID: op.ID, State: op.State, Code: code})
			}
		}
	}
}

// An explicit owner-selected ID is necessary: listing a bearer invite carries
// no recipient association. "none" additionally requires an empty remote list.
func reconcilePeopleInvite(ctx context.Context, path, login string, op registry.PersonInvite, id string) (registry.PersonInvite, []tailapi.Invite, error) {
	invites, err := tailapi.ListDeviceInvitesForTarget(ctx, personInviteTarget(op))
	if err != nil {
		return op, nil, err
	}
	if id == "none" && len(invites) == 0 {
		op.State = registry.PersonInvitePending
		return op, invites, nil
	}
	for _, inv := range invites {
		if id == "" || id != inv.ID || inv.MultiUse || inv.AllowExitNode {
			continue
		}
		reg, _, err := registry.Preflight(path)
		if err != nil {
			return op, invites, err
		}
		for _, other := range reg.People {
			for _, old := range other.Invites {
				if old.ID == id && (other.Login != login || old.App != op.App || old.NodeID != op.NodeID || old.Attempt != op.Attempt) {
					return op, invites, registry.CodedError{Code: "invite_reconciliation_conflict", Message: "Invite ID already belongs to another person/app operation"}
				}
			}
		}
		op.ID, op.State = id, registry.PersonInviteComplete
		return op, invites, nil
	}
	return op, invites, registry.CodedError{Code: "invite_reconciliation_required", Message: "Verify the device invite outcome and explicitly reconcile app=id or app=none"}
}

func peopleInviteFailure(op registry.PersonInvite, err error, fallback string) PeopleInviteView {
	return PeopleInviteView{App: op.App, State: op.State, ID: op.ID, Code: peopleInviteCode(err, fallback)}
}
