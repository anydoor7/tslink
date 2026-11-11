package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type guestArguments struct {
	App       string `json:"app,omitempty"`
	ID        string `json:"id,omitempty"`
	For       string `json:"for,omitempty"`
	Label     string `json:"label,omitempty"`
	PIN       string `json:"pin,omitempty"`
	Public    bool   `json:"public,omitempty"`
	PrintLink bool   `json:"print_link,omitempty"`
}
type guestCreateResult struct {
	Grant     registry.GuestView `json:"grant"`
	Link      *string            `json:"link"`
	Message   string             `json:"message"`
	EdgeState string             `json:"edge_state"`
	// RemovedGatewayRoute reports the home gateway route this create removed
	// in the same registry write; null when nothing was removed.
	RemovedGatewayRoute *guestRemovedRoute `json:"removed_gateway_route"`
	// RequestBodyLimit states the per-request upload cap guests meet, so an
	// upload app's owner sees it before a guest's upload is refused.
	RequestBodyLimit guestBodyLimit `json:"request_body_limit"`
}

type guestBodyLimit struct {
	MaxBodyBytes int64  `json:"max_body_bytes"`
	Message      string `json:"message"`
}

type guestRemovedRoute struct {
	Port    int    `json:"port"`
	Message string `json:"message"`
}

func createGuest(paths sharePaths, args guestArguments, now time.Time) (guestCreateResult, error) {
	return createGuestContext(context.Background(), paths, args, now)
}

func createGuestContext(ctx context.Context, paths sharePaths, args guestArguments, now time.Time) (guestCreateResult, error) {
	if args.App == "" || args.For == "" {
		return guestCreateResult{}, output.ErrUsage("guest create requires app and --for")
	}
	policy, e := config.LoadLifetimePolicy()
	if e != nil {
		return guestCreateResult{}, e
	}
	// Resolve the trustworthy node authority before changing the fingerprint.
	// A create that raises the app's Funnel deadline (the first one always
	// does) changes the registry fingerprint, so a create issued before the
	// daemon republishes runtime.json lands here too. Nothing is saved, no
	// gateway route is removed, and the hint says how to wait for the URL and
	// rerun. The wait is bounded by guestURLWait and only spent while a
	// running daemon may still publish the node; an enrollment handoff or a
	// stopped daemon fails at once.
	//
	// Removing a home gateway route needs the same evidence, with or without
	// --print-link: the app's own node must be running with an exact URL, or
	// the app could end with no address. A route someone else depends on is
	// refused first, so that answer does not wait on the node.
	base := ""
	removingRoute := false
	if reg, _, err := registry.Preflight(paths.Registry); err == nil {
		for _, s := range reg.Services {
			if s.Name != args.App {
				continue
			}
			// The same authority as every other mutation of a routed app,
			// checked again under the registry lock by CreateGuest.
			if err := authorizePortalServiceMutation(ctx, reg, s); err != nil {
				return guestCreateResult{}, err
			}
			if !s.GuestGate && (s.PortalRoute != nil || s.PortalOnly) {
				if err := registry.GuestRouteRemovable(reg, s); err != nil {
					return guestCreateResult{}, err
				}
				removingRoute = true
			}
		}
	}
	if args.PrintLink || removingRoute {
		var e error
		if base, e = guestNodeURL(ctx, paths, args.App); e != nil {
			if removingRoute {
				if coded, ok := e.(registry.CodedError); ok {
					coded.Next = append(coded.Next, "the home gateway route was kept")
					e = coded
				}
			}
			return guestCreateResult{}, e
		}
	}
	removedRoute := 0
	var svc registry.Service
	grant, token, e := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{Context: ctx, App: args.App, Label: args.Label, Value: args.For, PIN: args.PIN, PublicAck: args.Public, Policy: policy, Now: now, RemovedRoute: &removedRoute, SavedService: &svc, AllowRouteRemoval: removingRoute, Authorize: func(reg *registry.Registry, s registry.Service) error {
		return authorizePortalServiceMutation(ctx, reg, s)
	}})
	if e != nil {
		return guestCreateResult{}, e
	}
	r := guestCreateResult{Grant: grant, Message: fmt.Sprintf("Open the private link in your browser. Access ends %s. You do not need to install anything or create a Tailscale account.", guestExpiryDate(grant.ExpiresAt)), EdgeState: "configured; check status for Funnel availability"}
	if limits := svc.EffectiveRequestLimits(); limits != nil && limits.MaxBodyBytes < 0 {
		r.RequestBodyLimit = guestBodyLimit{MaxBodyBytes: -1, Message: fmt.Sprintf("Uploads through %s have no body size cap (acknowledged unlimited).", args.App)}
	} else if limits != nil {
		r.RequestBodyLimit = guestBodyLimit{MaxBodyBytes: limits.MaxBodyBytes, Message: fmt.Sprintf("Uploads through %s are capped at %s per request; larger uploads are refused with HTTP 413. Raise it with: %s", args.App, registry.RequestBodyLimitText(limits.MaxBodyBytes), registry.RequestLimitCommand(svc.Name, registry.CodeRequestBodyLimit))}
	}
	if removedRoute != 0 {
		r.RemovedGatewayRoute = &guestRemovedRoute{Port: removedRoute, Message: fmt.Sprintf("Removed %s from the home gateway (port %d): guest-gated apps cannot be routed. Its own node is running at %s.", args.App, removedRoute, base)}
	}
	if grant.PINRequired {
		r.Message += " Enter the PIN I send separately."
	}
	if args.PrintLink {
		link := strings.TrimRight(base, "/") + "/guest/" + token
		r.Link = &link
		r.Message = strings.Replace(r.Message, "the private link", link, 1)
	}
	return r, nil
}

// guestURLWait bounds how long guest create --print-link waits for a running
// daemon to publish the app's exact node URL, as people add does with
// peopleRuntimeWait. A test replaces it.
var guestURLWait = 3 * time.Second

func guestNodeURL(ctx context.Context, paths sharePaths, app string) (string, error) {
	deadline := time.Now().Add(guestURLWait)
	for {
		base := peopleURLs(paths)[app]
		if u, e := url.Parse(base); e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
			return base, nil
		}
		if handoff, ok := validAuthHandoffForService(paths.PID, app); ok {
			return "", registry.CodedError{Code: registry.CodeEnrollmentRequired, Message: "Authorize this app's node before creating a printed guest link; no guest link was saved", Next: []string{"Open " + handoff.AuthURL, "tslink url " + app + " --wait=30s", "rerun this guest create"}, MessageOnly: true}
		}
		if !inviteIsRunningFn(paths.PID) || !time.Now().Before(deadline) {
			notReady := registry.URLNotReadyError(app).(registry.CodedError)
			notReady.Next = append(notReady.Next, "no guest link was saved; rerun this guest create once the URL is exact")
			return "", notReady
		}
		timer := time.NewTimer(min(50*time.Millisecond, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func guestPINInput(c *cobra.Command) (string, error) {
	var raw []byte
	var e error
	if f, ok := c.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(c.ErrOrStderr(), "PIN (6..64 digits): ")
		raw, e = term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(c.ErrOrStderr())
	} else {
		raw, e = bufio.NewReader(io.LimitReader(c.InOrStdin(), 66)).ReadBytes('\n')
		if e == io.EOF {
			e = nil
		}
	}
	if e != nil {
		return "", output.ErrUsage("cannot read PIN")
	}
	pin := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(pin) < 6 || len(pin) > 64 {
		return "", output.ErrUsage("PIN must contain 6..64 ASCII digits")
	}
	return pin, nil
}
func newGuestCmd() *cobra.Command {
	group := &cobra.Command{Use: "guest", Short: "Share one app through a time-limited browser link", Args: cobra.NoArgs, RunE: runCommandGroup}
	var a guestArguments
	var pin bool
	create := &cobra.Command{Use: "create <app>", Short: "Create an owner-only guest link; acknowledge Funnel with --public", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		paths, e := configSharePaths()
		if e != nil {
			return e
		}
		a.App = args[0]
		if pin {
			a.PIN, e = guestPINInput(c)
			if e != nil {
				return e
			}
			defer func() { a.PIN = "" }()
		}
		r, e := createGuest(paths, a, durationNowFn())
		if e != nil {
			return e
		}
		if jsonOutput(c) {
			output.WriteJSON(c.OutOrStdout(), output.NewSuccess("guest create", r))
		} else {
			fmt.Fprintf(c.OutOrStdout(), "Guest %s for %s until %s\n%s\n", r.Grant.ID, r.Grant.App, guestExpiryDate(r.Grant.ExpiresAt), r.Message)
			if r.RemovedGatewayRoute != nil {
				fmt.Fprintln(c.OutOrStdout(), r.RemovedGatewayRoute.Message)
			}
			if r.RequestBodyLimit.Message != "" {
				fmt.Fprintln(c.OutOrStdout(), r.RequestBodyLimit.Message)
			}
			if r.Link == nil {
				fmt.Fprintln(c.OutOrStdout(), "Link hidden. Tokens cannot be recovered; create with --print-link to deliver a new link.")
			}
		}
		return nil
	}}
	create.Flags().StringVar(&a.For, "for", "", "Lifetime: 1h, 8h, 24h, 3d, 7d; never refused")
	create.Flags().StringVar(&a.Label, "label", "", "Owner's label for this link")
	create.Flags().BoolVar(&pin, "pin", false, "Read a PIN from hidden terminal input or stdin; never pass it in argv")
	create.Flags().BoolVar(&a.Public, "public", false, "Acknowledge public internet reachability through Funnel with mandatory guest authentication")
	create.Flags().BoolVar(&a.PrintLink, "print-link", false, "Explicitly return the one-time bearer link and sendable message")
	group.AddCommand(create)
	for _, name := range []string{"list", "show", "revoke"} {
		leaf := &cobra.Command{Use: name, Short: "Owner-only: " + name + " guest grants without bearer tokens", Args: cobra.NoArgs}
		if name != "list" {
			leaf.Use += " <id>"
			leaf.Args = cobra.ExactArgs(1)
		}
		leaf.RunE = func(c *cobra.Command, args []string) error {
			path, e := config.RegistryPath()
			if e != nil {
				return e
			}
			var data any
			var views []registry.GuestView
			now := durationNowFn()
			switch name {
			case "list":
				list, e := registry.ListGuests(path, now)
				if e != nil {
					return e
				}
				data = map[string]any{"grants": list}
				views = list
			case "show":
				view, e := registry.ShowGuest(path, args[0], now)
				if e != nil {
					return e
				}
				data = map[string]any{"grant": view}
				views = []registry.GuestView{view}
			case "revoke":
				view, e := registry.RevokeGuest(path, args[0], now)
				if e != nil {
					return e
				}
				data = map[string]any{"grant": view}
				views = []registry.GuestView{view}
			}
			if jsonOutput(c) {
				output.WriteJSON(c.OutOrStdout(), output.NewSuccess("guest "+name, data))
			} else {
				if name == "revoke" {
					fmt.Fprintf(c.OutOrStdout(), "Revoked guest %s.\n", args[0])
				}
				writeGuestSummary(c.OutOrStdout(), views, now)
			}
			return nil
		}
		group.AddCommand(leaf)
	}
	return group
}

func guestExpiryDate(at time.Time) string {
	local := at.In(time.Local)
	return local.Format("Jan 2, 2006 at 15:04 MST (UTC-07:00)")
}

func guestExpiryRelative(at, now time.Time) string {
	d := at.Sub(now)
	if d <= 0 {
		return "expired"
	}
	value, unit := int(d.Round(time.Minute)/time.Minute), "minute"
	if d >= 24*time.Hour {
		value, unit = int(d.Round(24*time.Hour)/(24*time.Hour)), "day"
	} else if d >= time.Hour {
		value, unit = int(d.Round(time.Hour)/time.Hour), "hour"
	}
	if value < 1 {
		return "in less than a minute"
	}
	if value != 1 {
		unit += "s"
	}
	return fmt.Sprintf("in %d %s", value, unit)
}

func writeGuestSummary(out io.Writer, views []registry.GuestView, now time.Time) {
	if len(views) == 0 {
		fmt.Fprintln(out, "No guest links.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLABEL\tAPP\tEXPIRY\tSTATUS\tUSES")
	for _, view := range views {
		status := "active"
		if view.Revoked {
			status = "revoked"
		} else if view.Expired {
			status = "expired"
		}
		label := view.Label
		if label == "" {
			label = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s (%s)\t%s\t%d\n", view.ID, label, view.App, guestExpiryDate(view.ExpiresAt), guestExpiryRelative(view.ExpiresAt, now), status, view.Uses)
	}
	_ = w.Flush()
}
func configSharePaths() (sharePaths, error) {
	registryPath, e := config.RegistryPath()
	if e != nil {
		return sharePaths{}, e
	}
	pid, e := config.PIDPath()
	if e != nil {
		return sharePaths{}, e
	}
	snapshot, e := config.RuntimeSnapshotPath()
	return sharePaths{Registry: registryPath, PID: pid, Snapshot: snapshot}, e
}
func init() { rootCmd.AddCommand(newGuestCmd()) }
