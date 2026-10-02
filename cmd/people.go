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
	Who        string   `json:"who"`
	Apps       []string `json:"apps,omitempty"`
	For        *string  `json:"for,omitempty"`
	Invite     bool     `json:"invite,omitempty"`
	PrintLinks bool     `json:"print_links,omitempty"`
}

type PeopleGrantView struct {
	registry.PersonGrant
	Active bool   `json:"active"`
	URL    string `json:"url,omitempty"`
}

type PeopleView struct {
	Login   string            `json:"login"`
	Revoked bool              `json:"revoked"`
	Grants  []PeopleGrantView `json:"grants"`
}

type PeopleInviteView struct {
	App                  string                         `json:"app"`
	ID                   string                         `json:"id,omitempty"`
	InviteURL            string                         `json:"invite_url,omitempty"`
	Code                 string                         `json:"code,omitempty"`
	RemoteSideEffectPlan *security.RemoteSideEffectPlan `json:"remote_side_effect_plan,omitempty"`
}

type PeopleResult struct {
	Person            PeopleView         `json:"person"`
	Invites           []PeopleInviteView `json:"invites"`
	Complete          bool               `json:"complete"`
	Message           string             `json:"message"`
	InviteRequirement string             `json:"invite_requirement"`
}

const peopleInviteRequirement = "Device invitations require a stored user-owned Tailscale API access token; OAuth client tokens cannot create them. People already in the tailnet need no token or invite."

func peopleURLs(paths sharePaths) map[string]string {
	urls := map[string]string{}
	snapshot, err := tsruntime.Load(paths.Snapshot)
	if err != nil || !inviteIsRunningFn(paths.PID) {
		return urls
	}
	expected := tsruntime.ExpectedRuntime{CurrentRegistryFingerprint: currentRegistryFingerprint(paths.Registry)}
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
	v := PeopleView{Login: p.Login, Revoked: p.Revoked, Grants: []PeopleGrantView{}}
	for _, g := range p.Grants {
		v.Grants = append(v.Grants, PeopleGrantView{PersonGrant: g, Active: registry.PersonGrantActiveAt(p, g.App, now), URL: urls[g.App]})
	}
	return v
}

func peopleMessage(p PeopleView, invites []PeopleInviteView, requested, printLinks bool) string {
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
	if args.PrintLinks && !args.Invite {
		return PeopleResult{}, output.ErrUsage("--print-links requires --invite")
	}
	if !update && len(args.Apps) == 0 {
		return PeopleResult{}, output.ErrUsage("--apps is required; use a comma-separated app list or all")
	}
	if update && args.Apps == nil && args.For == nil {
		return PeopleResult{}, output.ErrUsage("update requires --apps or --for")
	}
	if args.Apps != nil && len(args.Apps) == 0 {
		return PeopleResult{}, output.ErrUsage("apps must not be empty")
	}
	now := peopleNowFn()
	var expires *time.Time
	var err error
	if args.For != nil {
		expires, err = registry.ParsePersonExpiry(*args.For, now)
		if err != nil {
			return PeopleResult{}, output.ErrUsage(err.Error())
		}
	}
	urls := peopleURLs(paths)
	// Snapshot proof precedes the registry mutation: setting people_scoped
	// changes its fingerprint but does not replace the existing service node.
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
	result := PeopleResult{Person: peopleView(p, urls, now), Invites: []PeopleInviteView{}, Complete: true, InviteRequirement: peopleInviteRequirement}
	if args.Invite {
		for _, g := range p.Grants {
			// Link mode asks Tailscale to send no email; no elevated exit-node
			// or multi-use invitation can be requested through this surface.
			invite, err := inviteCreateDeviceFn(ctx, targets[g.App], p.Login, true, false, false)
			v := PeopleInviteView{App: g.App, Code: "invite_failed"}
			if err != nil {
				result.Complete = false
				if code, ok := registry.ErrorCode(err); ok {
					v.Code = code
				}
			} else {
				v.Code = ""
				v.ID = invite.ID
				plan := invitePlan(invite, "create")
				v.RemoteSideEffectPlan = &plan
				if args.PrintLinks {
					v.InviteURL = invite.InviteURL
				}
			}
			result.Invites = append(result.Invites, v)
		}
	}
	result.Message = peopleMessage(result.Person, result.Invites, args.Invite, args.PrintLinks)
	return result, nil
}

type PeopleListResult struct {
	People []PeopleView `json:"people"`
}
type PeopleRemoveResult struct {
	Login   string `json:"login"`
	Removed bool   `json:"removed"`
	Revoked bool   `json:"revoked"`
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
	urls, now := peopleURLs(paths), peopleNowFn()
	for _, p := range reg.People {
		result.People = append(result.People, peopleView(p, urls, now))
	}
	return result, nil
}

func removePeople(path, who string) (PeopleRemoveResult, error) {
	login, err := registry.NormalizePerson(who)
	if err != nil {
		return PeopleRemoveResult{}, err
	}
	removed, err := registry.RemovePerson(path, login)
	return PeopleRemoveResult{Login: login, Removed: removed, Revoked: true}, err
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
			fmt.Fprintln(out, "Local access saved; some invitations failed. Inspect the per-app errors and retry invitations with tslink invite device.")
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
		}
		if len(d.People) == 0 {
			fmt.Fprintln(out, "No people configured.")
		}
	case PeopleRemoveResult:
		fmt.Fprintf(out, "Revoked %s across all private HTTP and file services. Accepted network shares may remain; TCP and public Funnel are outside person enforcement.\n", d.Login)
	}
}

func newPeopleCmd() *cobra.Command {
	group := &cobra.Command{Use: "people", Short: "Share private apps with people, with optional expiry", Args: cobra.NoArgs, RunE: runCommandGroup}
	for _, update := range []bool{false, true} {
		var apps, duration string
		var invite, printLinks bool
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
		c.Flags().BoolVar(&invite, "invite", false, "Create one single-use device invite per app and bundle a guide (requires a user-owned API token)")
		c.Flags().BoolVar(&printLinks, "print-links", false, "Explicitly include bearer invitation links in output and the guide")
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
	group.AddCommand(&cobra.Command{Use: "remove <login-or-email>", Short: "Revoke a person everywhere in private HTTP/file services", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
		path, err := inviteRegistryPathFn()
		if err != nil {
			return err
		}
		result, err := removePeople(path, a[0])
		if err != nil {
			return err
		}
		writePeopleResult(c.OutOrStdout(), "people remove", result, jsonOutput(c))
		return nil
	}})
	return group
}

func init() { rootCmd.AddCommand(newPeopleCmd()) }
