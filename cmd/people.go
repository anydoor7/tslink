package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/security"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var peopleNowFn = time.Now
var peopleMembershipFn = tailapi.ResolveMembership
var peopleRuntimeFn = readPeopleAccessRuntime
var peopleRuntimeWait = 3 * time.Second

type peopleArguments struct {
	EmailInvite   bool              `json:"-"`
	MultiUse      bool              `json:"-"`
	AllowExitNode bool              `json:"-"`
	Now           time.Time         `json:"-"`
	Who           string            `json:"who"`
	Apps          []string          `json:"apps,omitempty"`
	For           *string           `json:"for,omitempty"`
	Until         *string           `json:"until,omitempty"`
	AckNever      bool              `json:"ack_never,omitempty"`
	Invite        bool              `json:"invite,omitempty"`
	PrintLinks    bool              `json:"print_links,omitempty"`
	QR            bool              `json:"qr,omitempty"`
	QRInvite      string            `json:"qr_invite,omitempty"`
	Reconcile     map[string]string `json:"reconcile_invites,omitempty"`
	Replace       map[string]string `json:"replace_invites,omitempty"`
}

type PeopleGrantView struct {
	registry.PersonGrant
	Active bool   `json:"active"`
	URL    string `json:"url,omitempty"`
}

type PeopleView struct {
	Login   string                  `json:"login"`
	Revoked bool                    `json:"revoked"`
	Grants  []PeopleGrantView       `json:"grants"`
	Invites []registry.PersonInvite `json:"invites,omitempty"`
}

type PeopleInviteView struct {
	TargetKind           string                         `json:"target_kind,omitempty"`
	App                  string                         `json:"app"`
	ID                   string                         `json:"id,omitempty"`
	InviteURL            string                         `json:"invite_url,omitempty"`
	Code                 string                         `json:"code,omitempty"`
	RemoteSideEffectPlan *security.RemoteSideEffectPlan `json:"remote_side_effect_plan,omitempty"`
	State                string                         `json:"state,omitempty"`
	ReconcileIDs         []string                       `json:"reconcile_ids,omitempty"`
}

type PeopleAppAccess struct {
	App         string `json:"app"`
	AccessState string `json:"access_state"`
	URL         string `json:"url,omitempty"`
	Message     string `json:"message"`
}

type PeopleResult struct {
	Access            []PeopleAppAccess     `json:"access"`
	Membership        tailapi.Membership    `json:"membership,omitempty"`
	Person            PeopleView            `json:"person"`
	Invites           []PeopleInviteView    `json:"invites"`
	Complete          bool                  `json:"complete"`
	Portal            tsruntime.PortalState `json:"portal"`
	Message           string                `json:"message"`
	InviteRequirement string                `json:"invite_requirement"`
	QRPayload         string                `json:"qr_payload,omitempty"`
	QRTerminal        bool                  `json:"-"`
	QRWarning         string                `json:"qr_warning,omitempty"`
	Warnings          []string              `json:"warnings,omitempty"`
	Guide             []string              `json:"guide"`
	GuideZH           []string              `json:"guide_zh"`
}

const peopleInviteRequirement = "Device invitations require a stored user-owned Tailscale API access token; OAuth client tokens cannot create them. Membership verification also requires access to the network user directory. Verified members need no device invitation."

func peopleURLs(paths sharePaths) map[string]string {
	reg, issues, err := registry.Preflight(paths.Registry)
	if err != nil {
		return map[string]string{}
	}
	return peopleURLsFromRegistry(paths, reg, issues)
}

func peopleURLsFromRegistry(paths sharePaths, reg *registry.Registry, issues []registry.ServiceIssue) map[string]string {
	urls := map[string]string{}
	snapshot, err := tsruntime.Load(paths.Snapshot)
	if err != nil || !inviteIsRunningFn(paths.PID) {
		return urls
	}
	fingerprint, err := tsruntime.RegistryFingerprint(reg, issues)
	if err != nil {
		return urls
	}
	expected := tsruntime.ExpectedRuntime{CurrentRegistryFingerprint: fingerprint}
	if pid, err := inviteReadPIDFn(paths.PID); err == nil {
		expected.DaemonPID = pid
	} else {
		return urls
	}
	if lower, err := invitePIDModTimeFn(paths.PID); err == nil {
		expected.DaemonStartedAtLowerBound = lower
	} else {
		return urls
	}
	if !tsruntime.Classify(snapshot, nil, expected).Exact {
		return urls
	}
	for _, svc := range snapshot.Services {
		if svc.RuntimeState == tsruntime.ServiceRuntimeRunning && svc.Endpoint.State == "exact" && svc.Endpoint.Kind == "https" {
			urls[svc.Name] = svc.Endpoint.Display
		}
	}
	return urls
}

func peopleView(p registry.Person, urls map[string]string, now time.Time) PeopleView {
	v := PeopleView{Login: p.Login, Revoked: p.Revoked, Grants: []PeopleGrantView{}, Invites: p.Invites}
	for _, g := range p.Grants {
		v.Grants = append(v.Grants, PeopleGrantView{PersonGrant: g, Active: registry.PersonGrantActiveAt(p, g.App, now), URL: urls[g.App]})
	}
	return v
}

type grantAccessIntent struct {
	Arguments       peopleArguments
	Update          bool
	PreserveRevoked bool
	Tool            string
	Commit          func(registry.PersonLifetimeOptions) (registry.Person, error)
}

func changePeople(ctx context.Context, paths sharePaths, args peopleArguments, update bool) (PeopleResult, error) {
	return grantAccess(ctx, paths, grantAccessIntent{Arguments: args, Update: update})
}

// grantAccess is the one business action: validate, classify, commit local
// authority, then complete the selected transport without holding its lock.
func grantAccess(ctx context.Context, paths sharePaths, intent grantAccessIntent) (PeopleResult, error) {
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return PeopleResult{}, err
	}
	args, update := intent.Arguments, intent.Update
	if args.QRInvite != "" && (!args.QR || !args.PrintLinks) {
		return PeopleResult{}, output.ErrUsage("--qr-invite requires --qr or --qr-png and --print-links; the QR is a credential")
	}
	login, err := registry.NormalizePerson(args.Who)
	if err != nil {
		return PeopleResult{}, err
	}
	args.Who = login
	if len(args.Reconcile) > 0 && !update {
		return PeopleResult{}, output.ErrUsage("reconcile-invite requires people update")
	}
	if len(args.Replace) > 0 && (!update || args.Apps != nil || args.For != nil || args.Until != nil || args.AckNever) {
		return PeopleResult{}, output.ErrUsage("replace-invite requires people update without --apps or --for; grants and deadlines are preserved")
	}
	for app, id := range args.Replace {
		if err := registry.ValidateName(app); err != nil && app != gatewayInviteKey {
			return PeopleResult{}, output.ErrUsage(err.Error())
		}
		if err := tailapi.ValidateInviteID(id); err != nil {
			return PeopleResult{}, err
		}
		if _, ok := args.Reconcile[app]; ok {
			return PeopleResult{}, output.ErrUsage("cannot replace and reconcile the same app")
		}
	}
	for app, id := range args.Reconcile {
		if err := registry.ValidateName(app); err != nil && app != gatewayInviteKey {
			return PeopleResult{}, output.ErrUsage(err.Error())
		}
		if id != "none" {
			if err := tailapi.ValidateInviteID(id); err != nil {
				return PeopleResult{}, err
			}
		}
	}
	if !update && len(args.Apps) == 0 {
		return PeopleResult{}, output.ErrUsage("--apps is required; use a comma-separated app list or all")
	}
	if args.Apps != nil && len(args.Apps) == 0 {
		return PeopleResult{}, output.ErrUsage("apps must not be empty")
	}
	now := args.Now
	if now.IsZero() {
		now = peopleNowFn()
	}
	value, err := lifetimeArgument(args.For, args.Until)
	if err != nil {
		return PeopleResult{}, err
	}
	policy, err := config.LoadLifetimePolicy()
	if err != nil {
		return PeopleResult{}, err
	}
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil {
		return PeopleResult{}, err
	}
	if err := peopleMutationAuthorization(ctx)(reg, login); err != nil {
		return PeopleResult{}, err
	}
	if update {
		found := false
		for _, p := range reg.People {
			found = found || p.Login == login && !p.Revoked
		}
		if !found {
			return PeopleResult{}, output.ErrNotFound("person not found: " + login)
		}
	}
	apps := args.Apps
	if apps == nil {
		for _, person := range reg.People {
			if person.Login == login {
				for _, g := range person.Grants {
					apps = append(apps, g.App)
				}
			}
		}
	}
	if len(apps) == 1 && apps[0] == "all" {
		apps = nil
		for _, svc := range reg.Services {
			if registry.PeopleServiceSupported(svc) && !svc.Funnel {
				apps = append(apps, svc.Name)
			}
		}
	}
	if len(apps) == 0 {
		return PeopleResult{}, output.ErrUsage("apps must include at least one private HTTP or file service")
	}
	global, err := config.LoadGlobalConfig()
	if err != nil {
		return PeopleResult{}, err
	}
	controlURL := global.ControlURL
	appControlURLs := map[string]string{}
	for _, app := range apps {
		found := false
		for _, svc := range reg.Services {
			if svc.Name != app {
				continue
			}
			found = true
			appControlURLs[app] = svc.ControlURL
			if !registry.PeopleServiceSupported(svc) || svc.Funnel || svc.GuestGate {
				return PeopleResult{}, registry.CodedError{Code: "people_service_unsupported", Message: app + " cannot apply per-person private app access (TCP and public/browser access use their existing settings)"}
			}
			if svc.PortalOnly && !registry.GatewayEnabled(reg) {
				return PeopleResult{}, registry.CodedError{Code: "endpoint_unavailable", Message: app + " is unavailable while its home is off"}
			}
			if svc.ControlURL != "" && svc.ControlURL != global.ControlURL {
				// A mixed/custom control plane has no Tailscale directory proof.
				controlURL = svc.ControlURL
			}
		}
		if !found {
			return PeopleResult{}, output.ErrNotFound("service not found: " + app)
		}
	}
	if value != nil {
		if _, err := policy.Resolve(*value, duration.TailnetMember, args.AckNever, now, time.Local); err != nil {
			return PeopleResult{}, output.ErrUsage(err.Error())
		}
	}
	// Reject unauthorized callers before a directory read; repeat all checks
	// under the writer lock because authority can change while the API runs.
	tool := intent.Tool
	if tool == "" {
		tool = "people_add"
		if update {
			tool = "people_update"
		}
	}
	if session, ok := mcpscope.FromContext(ctx); ok {
		if err := session.Authorize(tool, apps, peopleNowFn()); err != nil {
			return PeopleResult{}, err
		}
		known := false
		for _, p := range reg.People {
			known = known || p.Login == login
		}
		if !known && session.Scope.Role != "owner" {
			return PeopleResult{}, registry.CodedError{Code: "mcp_person_owner_required", Message: "The owner must add the person first"}
		}
	}
	membership, err := peopleMembershipFn(ctx, login, controlURL)
	if err == nil && membership != tailapi.MembershipMember && membership != tailapi.MembershipGuest {
		err = registry.CodedError{Code: "membership_unknown", Message: "Could not verify " + login + "'s network membership; access setup is incomplete"}
	}
	if err != nil {
		result := PeopleResult{Person: PeopleView{Login: login, Grants: []PeopleGrantView{}}, Complete: false, Invites: []PeopleInviteView{}, Access: []PeopleAppAccess{}, Message: err.Error()}
		for _, app := range apps {
			result.Access = append(result.Access, PeopleAppAccess{App: app, AccessState: "incomplete", Message: result.Message})
		}
		return result, accessSetupError(result, err)
	}
	audience := duration.TailnetMember
	if membership == tailapi.MembershipGuest {
		audience = duration.Guest
	}
	authorize := func(current *registry.Registry, who string) error {
		if err := peopleMutationAuthorization(ctx)(current, who); err != nil {
			return err
		}
		needsOwner := membership != tailapi.MembershipMember
		known := false
		for _, p := range current.People {
			known = known || p.Login == who
		}
		needsOwner = needsOwner || !known
		for _, svc := range current.Services {
			for _, app := range apps {
				if svc.Name == app && svc.ControlURL != appControlURLs[app] {
					return output.ErrConflict("network settings changed while checking membership; retry the same command")
				}
				if svc.Name == app && registry.GatewayEnabled(current) && svc.PortalRoute == nil {
					needsOwner = true
				}
			}
		}
		if session, ok := mcpscope.FromContext(ctx); ok {
			tool := intent.Tool
			if tool == "" {
				tool = "people_add"
				if update {
					tool = "people_update"
				}
			}
			if err := session.Authorize(tool, apps, peopleNowFn()); err != nil {
				return err
			}
			if needsOwner && session.Scope.Role != "owner" {
				return mcpscope.Denied{}
			}
		}
		if needsOwner {
			return requireRequestOwnerInRegistry(ctx, current)
		}
		return nil
	}
	if err := authorize(reg, login); err != nil {
		return PeopleResult{}, err
	}
	var inherited []registry.PersonGrant
	options := registry.PersonLifetimeOptions{Context: ctx, Value: value, Policy: policy, Audience: audience, AckNever: args.AckNever, Now: now, Authorize: authorize, PrepareAccess: true, PreserveRevoked: intent.PreserveRevoked, Tool: intent.Tool, InheritedGrants: &inherited}
	var p registry.Person
	if intent.Commit != nil {
		p, err = intent.Commit(options)
	} else {
		p, err = registry.ChangePersonWithLifetime(paths.Registry, login, args.Apps, update, options)
	}
	if err != nil {
		return PeopleResult{}, err
	}
	for _, grant := range inherited {
		if err := mcpaudit.RecordChange(ctx, paths.Registry, mcpaudit.Surface(ctx), mcpaudit.LocalActor(), now,
			mcpaudit.Change{Action: "people_inherit_legacy", App: grant.App, Subject: login, ExpiresAt: grant.ExpiresAt}); err != nil {
			return PeopleResult{}, err
		}
	}
	for app := range args.Replace {
		if app != gatewayInviteKey && !registry.PersonGrantActiveAt(p, app, now) {
			return PeopleResult{}, output.ErrConflict("replace-invite requires an active grant for " + app)
		}
	}
	portal, urls, targets := peopleRuntimeFn(ctx, paths, apps)
	result := PeopleResult{Portal: portal, Person: peopleView(p, urls, now), Invites: []PeopleInviteView{}, Access: []PeopleAppAccess{}, Complete: true, Membership: membership, InviteRequirement: peopleInviteRequirement}
	// Only this action's apps are transport targets; inherited legacy grants
	// retain their old paths and must not acquire unsolicited invitations.
	requested := p
	requested.Grants = nil
	for _, g := range p.Grants {
		for _, app := range apps {
			if g.App == app {
				requested.Grants = append(requested.Grants, g)
				break
			}
		}
	}
	if membership == tailapi.MembershipGuest {
		resumePeopleInvites(ctx, paths.Registry, requested, targets, args, &result)
	}
	if current, readErr := readPerson(paths.Registry, login); readErr == nil {
		p = current
	}
	for _, app := range apps {
		state, message := "enabled", fmt.Sprintf("%s has access to %s. Open it with Tailscale connected.", login, app)
		if urls[app] == "" {
			state, message, result.Complete = "runtime_pending", app+" is saved; its access address is not ready. Retry this command after the daemon is ready.", false
		}
		if membership == tailapi.MembershipGuest {
			for _, inv := range result.Invites {
				if inv.App != app && !(inv.TargetKind == registry.InviteTargetGateway && targets[app].Kind == registry.InviteTargetGateway) {
					continue
				}
				switch {
				case inv.Code != "":
					state, message = "incomplete", fmt.Sprintf("%s is saved, but sharing with %s is not complete (%s).", app, login, inv.Code)
				case inv.State == registry.PersonInviteAccepted:
					message = fmt.Sprintf("%s already accepted the invitation. Access to %s is enabled.", login, app)
				default:
					if state != "runtime_pending" {
						state = "awaiting_acceptance"
					}
					message = fmt.Sprintf("%s is outside your network. %s is shared with them; they can open it after accepting the invitation.", login, app)
				}
			}
		}
		if !registry.PersonGrantActiveAt(p, app, now) {
			state, message, result.Complete = "inactive", "Access to "+app+" has ended; an explicit lifetime change is required.", false
		}
		result.Access = append(result.Access, PeopleAppAccess{App: app, URL: urls[app], AccessState: state, Message: message})
		result.Message += message + "\n"
		if urls[app] != "" {
			result.Message += urls[app] + "\n"
		}
	}
	for _, inv := range result.Invites {
		if inv.Code != "" || inv.State == registry.PersonInviteAccepted {
			continue
		}
		if args.PrintLinks && inv.InviteURL != "" {
			result.Message += inviteLabel(inv) + ": accept " + inv.InviteURL + "\n"
		} else if args.EmailInvite {
			result.Message += inviteLabel(inv) + ": Tailscale was asked to email the invitation.\n"
		} else {
			result.Message += inviteLabel(inv) + ": invitation prepared; link hidden (use --print-links to forward it).\n"
		}
	}
	if err := preparePeopleQR(&result, args); err != nil {
		return result, err
	}
	if !result.Complete {
		return result, accessSetupError(result, nil)
	}
	return result, nil
}

func accessSetupError(data any, cause error) error {
	message, code := "Access setup is incomplete; retry the same command after resolving the reported state.", "access_setup_incomplete"
	if result, ok := data.(PeopleResult); ok && result.Message != "" {
		message = result.Message
	}
	if cause != nil {
		message = cause.Error()
		if c, ok := registry.ErrorCode(cause); ok {
			code = c
		}
	}
	return accessIncompleteError{CodedError: registry.CodedError{Code: code, Message: message, MessageOnly: true}, data: data}
}

type accessIncompleteError struct {
	registry.CodedError
	data any
}

func (e accessIncompleteError) ErrorData() any { return e.data }

func readPeopleAccessRuntime(ctx context.Context, paths sharePaths, apps []string) (tsruntime.PortalState, map[string]string, map[string]tailapi.DeviceTarget) {
	deadline := time.NewTimer(peopleRuntimeWait)
	defer deadline.Stop()
	for {
		urls, targets := map[string]string{}, map[string]tailapi.DeviceTarget{}
		portal := tsruntime.PortalState{State: "disabled"}
		if reg, issues, err := registry.Preflight(paths.Registry); err == nil {
			urls = peopleURLsFromRegistry(paths, reg, issues)
			pid, _ := inviteReadPIDFn(paths.PID)
			portal = readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
			if known, err := inviteDeviceTargetsForPaths(paths.Registry, paths.PID, paths.Snapshot); err == nil {
				for _, target := range known {
					targets[target.Service] = target
				}
			}
			gatewayPeopleTargets(reg, portal, paths.Registry, targets)
			if registry.GatewayEnabled(reg) {
				for _, svc := range reg.Services {
					if svc.PortalRoute != nil {
						delete(urls, svc.Name)
					}
				}
				if portal.Gateway != nil {
					for _, route := range portal.Gateway.Routes {
						if route.State == "running" && route.URL != "" {
							urls[route.App] = route.URL
						}
					}
				}
			}
		}
		ready := true
		for _, app := range apps {
			ready = ready && urls[app] != "" && targets[app].NodeID != ""
		}
		if ready || !inviteIsRunningFn(paths.PID) {
			return portal, urls, targets
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return portal, urls, targets
		case <-deadline.C:
			timer.Stop()
			return portal, urls, targets
		case <-timer.C:
		}
	}
}

type PeopleListResult struct {
	People []PeopleView `json:"people"`
}
type PeopleRemoveResult struct {
	Login    string             `json:"login"`
	Removed  bool               `json:"removed"`
	Revoked  bool               `json:"revoked"`
	Complete bool               `json:"complete"`
	Cleanup  []PeopleInviteView `json:"cleanup"`
}

func listPeople(paths sharePaths) (PeopleListResult, error) {
	result := PeopleListResult{People: []PeopleView{}}
	// Preflight is genuinely read-only, including file permissions.
	reg, issues, err := registry.Preflight(paths.Registry)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if len(issues) > 0 {
		return result, issues[0]
	}
	urls, now := peopleURLsFromRegistry(paths, reg, issues), peopleNowFn()
	for _, p := range reg.People {
		result.People = append(result.People, peopleView(p, urls, now))
	}
	return result, nil
}

func removePeople(path, who string) (PeopleRemoveResult, error) {
	return removePeopleContext(context.Background(), path, who)
}

func removePeopleContext(ctx context.Context, path, who string, reconcile ...map[string]string) (PeopleRemoveResult, error) {
	login, err := registry.ResolvePersonLogin(path, who)
	if err != nil {
		return PeopleRemoveResult{}, err
	}
	removed, err := registry.RemovePersonAuthorizedContext(peopleMutationContext(ctx), path, login, peopleMutationAuthorization(ctx))
	if err != nil {
		return PeopleRemoveResult{}, err
	}
	result := PeopleRemoveResult{Login: login, Removed: removed, Revoked: true, Complete: true, Cleanup: []PeopleInviteView{}}
	var resolved map[string]string
	if len(reconcile) > 0 {
		resolved = reconcile[0]
	}
	cleanupPeopleInvites(ctx, path, &result, resolved)
	return result, nil
}

func writePeopleResult(out io.Writer, command string, data any, isJSON bool) {
	if isJSON {
		output.WriteJSON(out, output.NewSuccess(command, data))
		return
	}
	switch d := data.(type) {
	case PeopleResult:
		fmt.Fprintln(out, d.Message)
		if d.QRPayload != "" {
			if d.QRWarning != "" {
				fmt.Fprintln(out, d.QRWarning)
			}
			if d.QRTerminal {
				fmt.Fprintln(out, "Scan on your phone (use a light terminal background):")
				qr, err := terminalQR(d.QRPayload)
				if err == nil {
					fmt.Fprint(out, qr)
				}
			}
			fmt.Fprint(out, formatPhoneGuide(d.Guide), formatPhoneGuide(d.GuideZH))
		}
		fmt.Fprintln(out, d.InviteRequirement)
		if !d.Complete {
			for _, inv := range d.Invites {
				if inv.Code != "" {
					fmt.Fprintf(out, "Invitation for %s: %s\n", inv.App, inv.Code)
				}
			}
			fmt.Fprintln(out, "Local access saved; invitation work remains. Retry with tslink people update <login> --invite. Unknown POST outcomes require listing and explicit --reconcile-invite app=id (or app=none after verifying absence); never blindly create another invitation.")
		}
	case PeopleListResult:
		for _, p := range d.People {
			fmt.Fprintf(out, "%s (revoked: %t)\n", p.Login, p.Revoked)
			for _, g := range p.Grants {
				deadline := "never"
				if g.ExpiresAt != nil {
					deadline = g.ExpiresAt.UTC().Format(time.RFC3339)
				}
				fmt.Fprintf(out, "  %s active=%t expires=%s\n", g.App, g.Active, deadline)
			}
			for _, op := range p.Invites {
				fmt.Fprintf(out, "  invite %s id=%s state=%s\n", op.App, op.ID, op.State)
			}
		}
		if len(d.People) == 0 {
			fmt.Fprintln(out, "No people configured.")
		}
	case PeopleRemoveResult:
		fmt.Fprintf(out, "Revoked %s across all private HTTP and file services. Accepted network shares may remain; TCP and public Funnel are outside person enforcement.\n", d.Login)
		for _, inv := range d.Cleanup {
			fmt.Fprintf(out, "Invitation cleanup for %s (%s): state=%s code=%s\n", inv.App, inv.ID, inv.State, inv.Code)
		}
		if !d.Complete {
			fmt.Fprintln(out, "Local denial is saved. Remote cleanup is deferred or incomplete; restore the user-owned API token, reconcile unknown outcomes, and retry people remove.")
		}
	}
}

func newPeopleCmd() *cobra.Command {
	group := &cobra.Command{Use: "people", Short: "Share private apps with people, with optional expiry", Args: cobra.NoArgs, RunE: runCommandGroup}
	for _, update := range []bool{false, true} {
		var apps, lifetime, until, qrFile, qrInvite string
		var invite, printLinks, ackNever, qr bool
		var reconcile, replace []string
		name := "add"
		if update {
			name = "update"
		}
		c := &cobra.Command{Use: name + " <login-or-email>", Short: "Set a person's apps and expiry", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
			reg, pid, snap, err := invitePaths()
			if err != nil {
				return err
			}
			args := peopleArguments{Who: a[0], Invite: invite, PrintLinks: printLinks, AckNever: ackNever, QR: qr || qrFile != "", QRInvite: qrInvite}
			if c.Flags().Changed("qr-png") && qrFile == "" {
				return output.ErrUsage("--qr-png requires a filename")
			}
			if len(replace) > 0 {
				args.Replace = map[string]string{}
				for _, value := range replace {
					app, id, ok := strings.Cut(value, "=")
					if !ok || app == "" || id == "" || args.Replace[app] != "" {
						return output.ErrUsage("replace-invite must be a unique app=recorded-id")
					}
					args.Replace[app] = id
				}
			}
			if len(reconcile) > 0 {
				args.Reconcile = map[string]string{}
				for _, value := range reconcile {
					app, id, ok := strings.Cut(value, "=")
					if !ok || app == "" || id == "" || args.Reconcile[app] != "" {
						return output.ErrUsage("reconcile-invite must be a unique app=id or app=none (use @gateway for the gateway)")
					}
					args.Reconcile[app] = id
				}
			}
			if c.Flags().Changed("apps") {
				args.Apps = strings.Split(apps, ",")
				for i := range args.Apps {
					args.Apps[i] = strings.TrimSpace(args.Apps[i])
				}
			}
			if c.Flags().Changed("for") {
				args.For = &lifetime
			}
			if c.Flags().Changed("until") {
				args.Until = &until
			}
			result, err := changePeople(c.Context(), sharePaths{Registry: reg, PID: pid, Snapshot: snap}, args, update)
			if err != nil {
				return err
			}
			if qrFile != "" {
				if err := qrPNG(result.QRPayload, qrFile); err != nil {
					return err
				}
			}
			result.QRTerminal = qr
			writePeopleResult(c.OutOrStdout(), "people "+name, result, jsonOutput(c))
			return nil
		}}
		c.Flags().StringVar(&apps, "apps", "", "Comma-separated private HTTP/file apps, or all current supported apps")
		c.Flags().StringVar(&lifetime, "for", "", "Grant lifetime; presets "+duration.Suggestions+"; relative, 'until <date/time>', or never with --ack-never; default 24h, update omission preserves deadlines")
		c.Flags().StringVar(&until, "until", "", "Absolute grant deadline: RFC3339, YYYY-MM-DD or YYYY-MM-DDTHH:MM (local without offset); conflicts with --for")
		c.Flags().BoolVar(&ackNever, "ack-never", false, "Acknowledge permanent access for a member or device-invited guest")
		c.Flags().BoolVar(&invite, "invite", false, "Compatibility option; necessary invitations are always prepared")
		_ = c.Flags().MarkHidden("invite")
		c.Flags().BoolVar(&printLinks, "print-links", false, "Explicitly include bearer invitation links in output and the guide")
		c.Flags().BoolVar(&qr, "qr", false, "Render the exact portal URL (or first app when portal disabled) as a terminal QR")
		c.Flags().StringVar(&qrFile, "qr-png", "", "Write a private QR PNG to an existing directory; JSON contains payload text only")
		c.Flags().StringVar(&qrInvite, "qr-invite", "", "Encode this app's bearer invitation instead; requires --print-links and --qr or --qr-png; QR is a credential")
		if update {
			c.Flags().StringArrayVar(&reconcile, "reconcile-invite", nil, "After verifying an unknown POST, associate app=id or confirm app=none; @gateway selects the gateway")
			c.Flags().StringArrayVar(&replace, "replace-invite", nil, "Owner-confirmed app=recorded-id (or @gateway=recorded-id) replacement after remote absence; preserves grants/deadlines")
		}
		group.AddCommand(c)
	}
	group.AddCommand(&cobra.Command{Use: "list", Short: "List people, grants and expiry", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		reg, pid, snap, err := invitePaths()
		if err != nil {
			return err
		}
		result, err := listPeople(sharePaths{Registry: reg, PID: pid, Snapshot: snap})
		if err != nil {
			return err
		}
		writePeopleResult(c.OutOrStdout(), "people list", result, jsonOutput(c))
		return nil
	}})
	var removeReconcile []string
	removeCmd := &cobra.Command{Use: "remove <login-or-email>", Short: "Revoke a person locally, then clean up pending device invitations", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		path, err := inviteRegistryPathFn()
		if err != nil {
			return err
		}
		resolved := map[string]string{}
		for _, value := range removeReconcile {
			app, id, ok := strings.Cut(value, "=")
			if !ok || app == "" || id == "" || resolved[app] != "" {
				return output.ErrUsage("reconcile-invite must be a unique app=id or app=none (use @gateway for the gateway)")
			}
			resolved[app] = id
		}
		result, err := removePeopleContext(c.Context(), path, a[0], resolved)
		if err != nil {
			return err
		}
		writePeopleResult(c.OutOrStdout(), "people remove", result, jsonOutput(c))
		return nil
	}}
	removeCmd.Flags().StringArrayVar(&removeReconcile, "reconcile-invite", nil, "After verifying an unknown POST, associate app=id or confirm app=none; @gateway selects the gateway before cleanup")
	group.AddCommand(removeCmd)
	return group
}

func init() { rootCmd.AddCommand(newPeopleCmd()) }

// currentGatewayAppURL uses the same exact-instance portal projection as status.
func currentGatewayAppURL(paths sharePaths, app string) string {
	reg, _, err := registry.Preflight(paths.Registry)
	if err != nil || !registry.GatewayEnabled(reg) {
		return ""
	}
	pid, _ := inviteReadPIDFn(paths.PID)
	portal := readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
	if portal.Gateway != nil {
		for _, route := range portal.Gateway.Routes {
			if route.App == app && route.State == "running" {
				return route.URL
			}
		}
	}
	return ""
}
