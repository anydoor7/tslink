package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/anydoor7/tslink/internal/server"
	"github.com/spf13/cobra"
)

type requestDecisionArguments struct {
	ID       string `json:"id"`
	For      string `json:"for,omitempty"`
	Reason   string `json:"reason,omitempty"`
	AckNever bool   `json:"ack_never,omitempty"`
}
type RequestListResult struct {
	Requests []registry.AccessRequest `json:"requests"`
}
type RequestDecisionResult struct {
	Request registry.AccessRequest `json:"request"`
	Changed bool                   `json:"changed"`
}

// F4 integration hook, after a committed decision, never on an idempotent retry.
var requestDecidedFn = func(registry.RequestEvent) {}

func requireRequestOwner(ctx context.Context, path string) error {
	_, remote := server.MCPCallerFromContext(ctx)
	if !remote {
		return nil
	}
	reg, _, err := registry.PortalPreflight(path)
	if err != nil {
		return registry.CodedError{Code: "access_request_owner_required", Message: "access requests require the portal owner; local owner CLI/MCP is also available"}
	}
	return requireRequestOwnerInRegistry(ctx, reg)
}

// Authority mutations use the same owner decision against the locked registry.
func requireRequestOwnerInRegistry(ctx context.Context, reg *registry.Registry) error {
	caller, remote := server.MCPCallerFromContext(ctx)
	if !remote {
		return nil
	}
	login, e := registry.NormalizePerson(caller.Login)
	if e != nil || len(caller.Tags) > 0 || reg == nil || reg.Portal == nil || reg.Portal.Owner == "" || login != reg.Portal.Owner {
		return registry.CodedError{Code: "access_request_owner_required", Message: "access requests require the portal owner; local owner CLI/MCP is also available"}
	}
	for _, p := range reg.People {
		if p.Login == login && p.Revoked {
			return registry.CodedError{Code: "access_request_owner_required", Message: "access requests require the portal owner"}
		}
	}
	return nil
}

func listRequests(path string, now time.Time) (RequestListResult, error) {
	requests, err := registry.ListAccessRequests(path, now)
	return RequestListResult{Requests: requests}, err
}

func decideRequest(path string, args requestDecisionArguments, approve bool, now time.Time) (RequestDecisionResult, error) {
	status := registry.RequestDenied
	policy := duration.Policy{}
	var err error
	if approve {
		status = registry.RequestApproved
		policy, err = config.LoadLifetimePolicy()
		if err != nil {
			return RequestDecisionResult{}, err
		}
	}
	r, changed, err := registry.DecideAccessRequest(path, args.ID, status, args.For, args.Reason, args.AckNever, policy, now)
	if err == nil && changed {
		requestDecidedFn(r.Event())
	}
	return RequestDecisionResult{Request: r, Changed: changed}, err
}

// Untrusted notes/reasons/logins must never control a terminal. JSON keeps the
// original text as escaped JSON; this human renderer filters all controls,
// including ESC/C1, and directional formatting characters.
func requestTerminalText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

func writeRequestResult(out io.Writer, command string, data any, isJSON bool) {
	if isJSON {
		output.WriteJSON(out, output.NewSuccess(command, data))
		return
	}
	requests := []registry.AccessRequest{}
	switch v := data.(type) {
	case RequestListResult:
		requests = v.Requests
	case RequestDecisionResult:
		requests = append(requests, v.Request)
	}
	if len(requests) == 0 {
		fmt.Fprintln(out, "No access requests.")
		return
	}
	for _, r := range requests {
		fmt.Fprintf(out, "%s  %s  %s  %s\n", requestTerminalText(r.ID), requestTerminalText(r.Who), requestTerminalText(r.App), requestTerminalText(r.Status))
		if r.RequestedDuration != "" {
			fmt.Fprintln(out, "  Requested time:", requestTerminalText(r.RequestedDuration))
		}
		if r.Note != "" {
			fmt.Fprintln(out, "  Visitor note (untrusted):", requestTerminalText(r.Note))
		}
		if r.Reason != "" {
			fmt.Fprintln(out, "  Reason:", requestTerminalText(r.Reason))
		}
		if r.Grant != nil && r.Grant.ExpiresAt != nil {
			fmt.Fprintln(out, "  Access ends:", r.Grant.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}
}

func newRequestsCmd() *cobra.Command {
	group := &cobra.Command{Use: "requests", Short: "Review and decide access requests as the owner", Args: cobra.NoArgs, RunE: runCommandGroup}
	group.AddCommand(&cobra.Command{Use: "list", Short: "List the durable request inbox", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		path, err := inviteRegistryPathFn()
		if err != nil {
			return err
		}
		result, err := listRequests(path, peopleNowFn())
		if err != nil {
			return err
		}
		writeRequestResult(c.OutOrStdout(), "requests list", result, jsonOutput(c))
		return nil
	}})
	for _, approve := range []bool{true, false} {
		var args requestDecisionArguments
		name := "deny"
		if approve {
			name = "approve"
		}
		c := &cobra.Command{Use: name + " <id>", Short: "Decide one request; identical retries replay the original result", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, a []string) error {
			path, err := inviteRegistryPathFn()
			if err != nil {
				return err
			}
			args.ID = a[0]
			result, err := decideRequest(path, args, approve, peopleNowFn())
			if err != nil {
				return err
			}
			writeRequestResult(c.OutOrStdout(), "requests "+name, result, jsonOutput(c))
			return nil
		}}
		if approve {
			c.Flags().StringVar(&args.For, "for", "", "Owner-chosen lifetime; presets "+duration.Suggestions)
			c.Flags().BoolVar(&args.AckNever, "ack-never", false, "Acknowledge permanent member access; guests remain finite")
		} else {
			c.Flags().StringVar(&args.Reason, "reason", "", "Optional reason (500 characters), shown to the visitor")
		}
		group.AddCommand(c)
	}
	return group
}

func init() { rootCmd.AddCommand(newRequestsCmd()) }
