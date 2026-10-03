package cmd

import (
	"context"
	"fmt"
	"github.com/anydoor7/tslink/internal/mcpaudit"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/duration"
	"github.com/anydoor7/tslink/internal/mcpscope"
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
	return listRequestsContext(context.Background(), path, now)
}

func requestOwnerAuthorization(ctx context.Context) func(*registry.Registry) error {
	_, scoped := mcpscope.FromContext(ctx)
	if _, remote := server.MCPCallerFromContext(ctx); !remote && !scoped {
		return nil
	}
	return func(reg *registry.Registry) error {
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return err
		}
		if session, ok := mcpscope.FromContext(ctx); ok && !session.Scope.ToolAllowed("requests_list") {
			return mcpscope.Denied{}
		}
		return requireRequestOwnerInRegistry(ctx, reg)
	}
}

// The callback receives the very registry that the decision will update.
// Re-resolve its app here, not from a preflight snapshot supplied by a caller.
func requestDecisionAuthorization(ctx context.Context, args requestDecisionArguments, approve bool, now time.Time) func(*registry.Registry) error {
	return func(reg *registry.Registry) error {
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return err
		}
		if err := requireRequestOwnerInRegistry(ctx, reg); err != nil {
			return err
		}
		session, scoped := mcpscope.FromContext(ctx)
		if !scoped {
			return nil
		}
		tool := "requests_deny"
		if approve {
			tool = "requests_approve"
		}
		for _, request := range reg.Requests {
			if request.ID != args.ID {
				continue
			}
			if err := session.Authorize(tool, []string{request.App}, now); err != nil {
				return err
			}
			if approve {
				lifetime, err := duration.ParseLifetime(args.For, now, time.Local)
				if err != nil {
					return output.ErrUsage(err.Error())
				}
				return session.CheckLifetime(lifetime, now)
			}
			return nil
		}
		return session.Authorize(tool, nil, now)
	}
}

// Every remote person writer uses this guard on resolved pre-mutation state.
// Scope permission cannot authorize changing owner/admin grants or tombstones.
func peopleMutationAuthorization(ctx context.Context) func(*registry.Registry, string) error {
	return func(reg *registry.Registry, login string) error {
		if reg.Portal != nil {
			protected := login == reg.Portal.Owner
			for _, admin := range reg.Portal.Admins {
				protected = protected || login == admin
			}
			if protected {
				if session, scoped := mcpscope.FromContext(ctx); scoped && session.Scope.Role != "owner" {
					return mcpscope.Denied{}
				}
				return requireRequestOwnerInRegistry(ctx, reg)
			}
		}
		return nil
	}
}

func listRequestsContext(ctx context.Context, path string, now time.Time) (RequestListResult, error) {
	requests, err := registry.ListAccessRequestsAuthorized(path, now, requestOwnerAuthorization(ctx))
	return RequestListResult{Requests: requests}, err
}

func decideRequest(path string, args requestDecisionArguments, approve bool, now time.Time) (RequestDecisionResult, error) {
	return decideRequestContext(context.Background(), path, args, approve, now)
}

func decideRequestContext(ctx context.Context, path string, args requestDecisionArguments, approve bool, now time.Time) (RequestDecisionResult, error) {
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
	r, changed, err := registry.DecideAccessRequestAuthorized(path, args.ID, status, args.For, args.Reason, args.AckNever, policy, now, requestDecisionAuthorization(ctx, args, approve, now), ctx)
	if err == nil && changed {
		requestDecidedFn(r.Event())
		change := mcpaudit.Change{Action: "request_" + r.Status, App: r.App, Subject: r.Who, ID: r.ID}
		if r.Grant != nil {
			change.ExpiresAt, change.PreviousExpiresAt = r.Grant.ExpiresAt, r.Grant.PreviousExpiresAt
		}
		err = mcpaudit.RecordChange(ctx, path, mcpaudit.Surface(ctx), mcpLocalActor(), now, change)
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
