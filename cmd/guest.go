package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
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
	grant, token, e := registry.CreateGuest(paths.Registry, registry.CreateGuestOptions{App: args.App, Label: args.Label, Value: args.For, PIN: args.PIN, PublicAck: args.Public, Policy: policy, Now: now})
	if e != nil {
		return guestCreateResult{}, e
	}
	r := guestCreateResult{Grant: grant, Message: fmt.Sprintf("Open the private link in your browser before %s. You do not need to install anything or create a Tailscale account.", grant.ExpiresAt.Format(time.RFC3339)), EdgeState: "configured; check status for Funnel availability"}
	if grant.PINRequired {
		r.Message += " Enter the PIN I send separately."
	}
	if args.PrintLink {
		link := strings.TrimRight(base, "/") + "/guest/" + token
		r.Link = &link
		r.Message = "Open " + link + " in your browser. " + r.Message
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
			fmt.Fprintf(c.OutOrStdout(), "Guest %s for %s until %s\n%s\n", r.Grant.ID, r.Grant.App, r.Grant.ExpiresAt.Format(time.RFC3339), r.Message)
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
			switch name {
			case "list":
				list, e := registry.ListGuests(path, durationNowFn())
				if e != nil {
					return e
				}
				data = map[string]any{"grants": list}
			case "show":
				view, e := registry.ShowGuest(path, args[0], durationNowFn())
				if e != nil {
					return e
				}
				data = map[string]any{"grant": view}
			case "revoke":
				view, e := registry.RevokeGuest(path, args[0], durationNowFn())
				if e != nil {
					return e
				}
				data = map[string]any{"grant": view}
			}
			if jsonOutput(c) {
				output.WriteJSON(c.OutOrStdout(), output.NewSuccess("guest "+name, data))
			} else {
				fmt.Fprintf(c.OutOrStdout(), "%+v\n", data)
			}
			return nil
		}
		group.AddCommand(leaf)
	}
	return group
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
