package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	tsruntime "github.com/anydoor7/tslink/internal/runtime"
	"github.com/anydoor7/tslink/internal/security"
	"github.com/anydoor7/tslink/internal/tailapi"
	"github.com/spf13/cobra"
)

var peopleNowFn = time.Now

type peopleArguments struct {
	Who        string            `json:"who"`
	Apps       []string          `json:"apps,omitempty"`
	For        *string           `json:"for,omitempty"`
	Invite     bool              `json:"invite,omitempty"`
	PrintLinks bool              `json:"print_links,omitempty"`
	Reconcile  map[string]string `json:"reconcile_invites,omitempty"`
	Replace    map[string]string `json:"replace_invites,omitempty"`
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
	App                  string                         `json:"app"`
	ID                   string                         `json:"id,omitempty"`
	InviteURL            string                         `json:"invite_url,omitempty"`
	Code                 string                         `json:"code,omitempty"`
	RemoteSideEffectPlan *security.RemoteSideEffectPlan `json:"remote_side_effect_plan,omitempty"`
	State                string                         `json:"state,omitempty"`
	ReconcileIDs         []string                       `json:"reconcile_ids,omitempty"`
}

type PeopleResult struct {
	Person            PeopleView            `json:"person"`
	Invites           []PeopleInviteView    `json:"invites"`
	Complete          bool                  `json:"complete"`
	Portal            tsruntime.PortalState `json:"portal"`
	Message           string                `json:"message"`
	InviteRequirement string                `json:"invite_requirement"`
}

const peopleInviteRequirement = "Device invitations require a stored user-owned Tailscale API access token; OAuth client tokens cannot create them. People already in the tailnet need no token or invite."

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

func peopleMessage(p PeopleView, invites []PeopleInviteView, requested, printLinks bool) string {
	if p.Revoked {
		return fmt.Sprintf("Access for %s is revoked. The owner should retry tslink people remove %s to finish pending invitation cleanup; accepted network shares may remain.", p.Login, p.Login)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Install Tailscale from https://tailscale.com/download on your phone or computer. Open it, sign in as %s, and keep it connected.\n", p.Login)
	if requested {
		b.WriteString("Accept each app invitation below. Use the same account when accepting and in the Tailscale app.\n")
		for _, inv := range invites {
			if inv.InviteURL != "" && printLinks {
				fmt.Fprintf(&b, "%s: accept %s\n", inv.App, inv.InviteURL)
			} else if inv.Code != "" {
				fmt.Fprintf(&b, "%s: invitation is not ready; ask the owner for a link.\n", inv.App)
			} else {
				fmt.Fprintf(&b, "%s: invitation link hidden; ask the owner for the link.\n", inv.App)
			}
		}
	} else {
		b.WriteString("If you already belong to the owner's Tailscale network, no invitation is needed. Otherwise ask the owner for the app invitation links and accept each one.\n")
	}
	for _, g := range p.Grants {
		if !g.Active {
			fmt.Fprintf(&b, "%s: access has ended.\n", g.App)
			continue
		}
		if g.URL != "" {
			fmt.Fprintf(&b, "Open and bookmark %s for %s.\n", g.URL, g.App)
		} else {
			fmt.Fprintf(&b, "%s: ask the owner for its address (owner: tslink status --urls).\n", g.App)
		}
		if g.ExpiresAt != nil {
			fmt.Fprintf(&b, "Access ends at %s.\n", g.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}
	b.WriteString("If an app will not open, check that Tailscale is connected and you used the account above, then ask the owner for help.")
	return b.String()
}

func changePeople(ctx context.Context, paths sharePaths, args peopleArguments, update bool) (PeopleResult, error) {
	login, err := registry.NormalizePerson(args.Who)
	if err != nil {
		return PeopleResult{}, err
	}
	args.Who = login
	if args.PrintLinks && !args.Invite {
		return PeopleResult{}, output.ErrUsage("--print-links requires --invite")
	}
	if len(args.Reconcile) > 0 && (!update || !args.Invite) {
		return PeopleResult{}, output.ErrUsage("reconcile-invite requires people update --invite")
	}
	if len(args.Replace) > 0 && (!update || !args.Invite || args.Apps != nil || args.For != nil) {
		return PeopleResult{}, output.ErrUsage("replace-invite requires people update --invite without --apps or --for; grants and deadlines are preserved")
	}
	for app, id := range args.Replace {
		if err := registry.ValidateName(app); err != nil {
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
		if err := registry.ValidateName(app); err != nil {
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
	if update && args.Apps == nil && args.For == nil && !args.Invite {
		return PeopleResult{}, output.ErrUsage("update requires --apps, --for or --invite")
	}
	if args.Apps != nil && len(args.Apps) == 0 {
		return PeopleResult{}, output.ErrUsage("apps must not be empty")
	}
	now := peopleNowFn()
	var expires *time.Time
	if args.For != nil {
		expires, err = registry.ParsePersonExpiry(*args.For, now)
		if err != nil {
			return PeopleResult{}, output.ErrUsage(err.Error())
		}
	}
	urls := peopleURLs(paths)
	// Snapshot proof precedes the registry mutation: setting people_scoped
	// changes its fingerprint but does not replace the existing service node.
	// Capture the independent home address at the same point so the first
	// grant does not turn a proven portal URL into a pending guide message.
	portal := tsruntime.PortalState{State: "disabled"}
	if reg, _, err := registry.Preflight(paths.Registry); err == nil {
		pid, _ := inviteReadPIDFn(paths.PID)
		portal = readPortalView(reg, paths.Registry, inviteIsRunningFn(paths.PID), pid)
	}
	targets := map[string]tailapi.DeviceTarget{}
	if args.Invite {
		known, err := inviteDeviceTargetsForPaths(paths.Registry, paths.PID, paths.Snapshot)
		if err != nil {
			return PeopleResult{}, err
		}
		for _, target := range known {
			targets[target.Service] = target
		}
	}
	p, err := registry.ChangePerson(paths.Registry, args.Who, args.Apps, expires, args.For != nil, update)
	if err != nil {
		return PeopleResult{}, err
	}
	for app := range args.Replace {
		if !registry.PersonGrantActiveAt(p, app, now) {
			return PeopleResult{}, output.ErrConflict("replace-invite requires an active grant for " + app)
		}
	}
	result := PeopleResult{Portal: portal, Person: peopleView(p, urls, now), Invites: []PeopleInviteView{}, Complete: true, InviteRequirement: peopleInviteRequirement}
	if args.Invite {
		resumePeopleInvites(ctx, paths.Registry, p, targets, args, &result)
	}
	result.Message = peopleMessage(result.Person, result.Invites, args.Invite, args.PrintLinks)
	if result.Portal.Enabled {
		if result.Portal.URL != "" {
			result.Message += "\nYour one home address: open and bookmark " + result.Portal.URL + ". It shows the apps available to you, their health and when access ends."
		} else {
			result.Message += "\nYour home page is being prepared. Ask the owner for its address (owner: tslink status --urls). Bookmark that one address for your apps."
		}
		result.Message += " If you are outside the owner's tailnet, ask the owner to share the home node too; app invitations alone do not provide access to it."
	}
	return result, nil
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
	removed, err := registry.RemovePerson(path, login)
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
		var apps, duration string
		var invite, printLinks bool
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
			args := peopleArguments{Who: a[0], Invite: invite, PrintLinks: printLinks}
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
						return output.ErrUsage("reconcile-invite must be a unique app=id or app=none")
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
				args.For = &duration
			}
			result, err := changePeople(c.Context(), sharePaths{Registry: reg, PID: pid, Snapshot: snap}, args, update)
			if err != nil {
				return err
			}
			writePeopleResult(c.OutOrStdout(), "people "+name, result, jsonOutput(c))
			return nil
		}}
		c.Flags().StringVar(&apps, "apps", "", "Comma-separated private HTTP/file apps, or all current supported apps")
		c.Flags().StringVar(&duration, "for", "", "Grant lifetime such as 1h, 7d or never; update omission preserves deadlines")
		c.Flags().BoolVar(&invite, "invite", false, "Create or resume single-use per-app device invitations (requires a user-owned API token)")
		c.Flags().BoolVar(&printLinks, "print-links", false, "Explicitly include bearer invitation links in output and the guide")
		if update {
			c.Flags().StringArrayVar(&reconcile, "reconcile-invite", nil, "After verifying an unknown POST, associate app=id or confirm app=none; requires --invite")
			c.Flags().StringArrayVar(&replace, "replace-invite", nil, "Owner-confirmed app=recorded-id replacement after remote absence; preserves grants/deadlines; requires --invite")
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
				return output.ErrUsage("reconcile-invite must be a unique app=id or app=none")
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
	removeCmd.Flags().StringArrayVar(&removeReconcile, "reconcile-invite", nil, "After verifying an unknown POST, associate app=id or confirm app=none before cleanup")
	group.AddCommand(removeCmd)
	return group
}

func init() { rootCmd.AddCommand(newPeopleCmd()) }
