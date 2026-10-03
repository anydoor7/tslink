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
	base := ""
	if args.PrintLink {
		base = peopleURLs(paths)[args.App]
		u, e := url.Parse(base)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return guestCreateResult{}, registry.URLNotReadyError(args.App)
		}
	}
	grant, token, e := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{Context: ctx, App: args.App, Label: args.Label, Value: args.For, PIN: args.PIN, PublicAck: args.Public, Policy: policy, Now: now})
	if e != nil {
		return guestCreateResult{}, e
	}
	r := guestCreateResult{Grant: grant, Message: fmt.Sprintf("Open the private link in your browser. Access ends %s. You do not need to install anything or create a Tailscale account.", guestExpiryDate(grant.ExpiresAt)), EdgeState: "configured; check status for Funnel availability"}
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
