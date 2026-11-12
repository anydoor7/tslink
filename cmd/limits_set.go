package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/anydoor7/tslink/internal/config"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// limitsSetArguments is the wire shape of the limits_set tool and the parsed
// form of `tslink limits set`. A nil field keeps the stored value; an empty
// string returns that limit to its default.
type limitsSetArguments struct {
	App           string  `json:"app"`
	MaxBody       *string `json:"max_body,omitempty"`
	UnlimitedAck  *bool   `json:"unlimited_ack,omitempty"`
	HeaderTimeout *string `json:"header_timeout,omitempty"`
	ReadTimeout   *string `json:"read_timeout,omitempty"`
	IdleTimeout   *string `json:"idle_timeout,omitempty"`
}

func (a limitsSetArguments) update() registry.RequestLimitsUpdate {
	return registry.RequestLimitsUpdate{MaxBody: a.MaxBody, UnlimitedAck: a.UnlimitedAck, HeaderTimeout: a.HeaderTimeout, ReadTimeout: a.ReadTimeout, IdleTimeout: a.IdleTimeout}
}

type LimitsSetResult struct {
	App string `json:"app"`
	// Changed is false when the stored limits already matched; nothing was written.
	Changed       bool                             `json:"changed"`
	RequestLimits *registry.EffectiveRequestLimits `json:"request_limits"`
	// NodeRestarting is true when the change was written: the running daemon
	// restarts the app's node to apply it. The command does not wait for it.
	NodeRestarting bool   `json:"node_restarting"`
	Message        string `json:"message,omitempty"`
}

// limitsRestartMessage says what a written change does to people using the
// app: the registry write returns at once and the node restarts after it.
func limitsRestartMessage(app string) string {
	return fmt.Sprintf("%s's node is restarting to apply the new limits: open connections and uploads in flight end, and guests get 401 until they reopen their link (and enter its PIN again). Check `tslink url %s --wait` (or `tslink status`) before telling anyone to retry.", app, app)
}

// setLimitsContext is shared by the CLI and the limits_set tool. Like health
// set it changes one field group in place, so it works on guest-gated apps
// (whose sticky gate refuses add) and keeps every other setting. A routed or
// public app requires the current portal owner for remote MCP callers, checked inside
// the registry write transaction.
func setLimitsContext(ctx context.Context, regPath string, args limitsSetArguments) (LimitsSetResult, error) {
	u := args.update()
	if u.Empty() {
		return LimitsSetResult{}, output.ErrUsage("nothing to change: pass a request-limit option")
	}
	svc, changed, err := registry.SetRequestLimitsContext(ctx, regPath, args.App, u, func(reg *registry.Registry, svc registry.Service) error {
		return authorizePortalServiceMutation(ctx, reg, svc)
	})
	var coded registry.CodedError
	if errors.As(err, &coded) && coded.Code == registry.CodeInvalidRequestLimits {
		coded.Next = []string{"tslink limits set --help"}
		return LimitsSetResult{}, coded
	}
	if err != nil {
		return LimitsSetResult{}, err
	}
	result := LimitsSetResult{App: svc.Name, Changed: changed, RequestLimits: svc.EffectiveRequestLimits()}
	if changed {
		result.NodeRestarting, result.Message = true, limitsRestartMessage(svc.Name)
	}
	return result, nil
}

func formatLimitsSet(out io.Writer, r LimitsSetResult) {
	verb := "updated"
	if !r.Changed {
		verb = "unchanged"
	}
	l := r.RequestLimits
	if l == nil {
		fmt.Fprintf(out, humanText("✓ %s request limits %s.\n"), r.App, verb)
	} else {
		fmt.Fprintf(out, humanText("✓ %s request limits %s: body %s, header timeout %s, read timeout %s, idle timeout %s\n"), r.App, verb, registry.RequestBodyLimitText(l.MaxBodyBytes), l.HeaderTimeout, l.ReadTimeout, l.IdleTimeout)
	}
	if r.Message != "" {
		fmt.Fprintln(out, r.Message)
	}
}

func init() {
	group := &cobra.Command{Use: "limits", Short: "Manage app HTTP request limits", Args: cobra.NoArgs, RunE: runCommandGroup}
	set := &cobra.Command{
		Use:   "set <app>",
		Short: "Change only an app's HTTP request limits, keeping everything else",
		Long: "Change only the HTTP request limits of a registered proxy or file app in one registry transaction. " +
			"Target, access, routes, health check, preserve-host, the guest gate and the enrolled node are kept. " +
			"Unset options keep their stored value; an empty value returns that limit to its default " +
			"(32MiB body, 10s header timeout, 30s read timeout, 60s idle timeout). unlimited needs --ack-unlimited-request-body. " +
			"The app's node restarts to apply new limits: open connections and uploads in flight end, and guests must reopen their link (and enter its PIN again). " +
			"Set limits before sending a guest link. Use this instead of re-running add, which replaces the whole service and is refused on apps with guest links.",
		Example: "  tslink limits set photos --max-request-body 4GiB\n  tslink limits set photos --request-read-timeout 2m\n  tslink limits set photos --max-request-body ''",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			args := limitsSetArguments{App: a[0]}
			flags := cmd.Flags()
			str := func(name string) *string {
				if !flags.Changed(name) {
					return nil
				}
				v, _ := flags.GetString(name)
				return &v
			}
			args.MaxBody, args.HeaderTimeout, args.ReadTimeout, args.IdleTimeout = str("max-request-body"), str("request-header-timeout"), str("request-read-timeout"), str("idle-timeout")
			if flags.Changed("ack-unlimited-request-body") {
				v, _ := flags.GetBool("ack-unlimited-request-body")
				args.UnlimitedAck = &v
			}
			path, err := config.RegistryPath()
			if err != nil {
				return err
			}
			result, err := setLimitsContext(cmd.Context(), path, args)
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("limits set", result)
			} else {
				formatLimitsSet(cmd.OutOrStdout(), result)
			}
			return nil
		},
	}
	set.Flags().String("max-request-body", "", "Maximum request body: bytes, B, KiB/MiB/GiB/TiB, KB/MB/GB/TB or unlimited (default 32MiB)")
	set.Flags().Bool("ack-unlimited-request-body", false, "Acknowledge --max-request-body unlimited")
	set.Flags().String("request-header-timeout", "", "Time allowed for request headers (default 10s)")
	set.Flags().String("request-read-timeout", "", "Request body inactivity window (default 30s)")
	set.Flags().String("idle-timeout", "", "Keep-alive idle timeout (default 60s)")
	group.AddCommand(set)
	rootCmd.AddCommand(group)

	mcpToolHints["limits_set"] = mcpHints(false, false, true, false)
	mcpToolDefinitions = append(mcpToolDefinitions, mcpToolDefinition{
		Name: "limits_set",
		Description: "Change only one app's HTTP request limits in a single registry transaction. Keeps target, access, routes, health, preserve_host, the guest gate and the enrolled node; works on apps with guest links, where add is refused. The app's node restarts to apply new limits: open connections and in-flight uploads end and guests must reopen their link. The call returns before the restart; a written change reports node_restarting true, so check url or status before telling anyone to retry. " +
			"Omitted fields keep their stored value; an empty string returns that limit to its default (32MiB body, 10s header, 30s read, 60s idle). max_body unlimited requires unlimited_ack true. " +
			"HTTP MCP requires the current portal owner for a routed app, checked in the write transaction. Owner scope only." + mcpPublicOwnerNote,
		InputSchema: objectSchema(map[string]any{
			"app":            map[string]any{"type": "string", "minLength": 1},
			"max_body":       map[string]any{"type": "string", "description": "Size such as 4GiB, or unlimited; empty means the 32MiB default."},
			"unlimited_ack":  map[string]any{"type": "boolean", "description": "Acknowledge max_body unlimited."},
			"header_timeout": map[string]any{"type": "string", "description": "Duration; empty means 10s."},
			"read_timeout":   map[string]any{"type": "string", "description": "Body inactivity duration; empty means 30s."},
			"idle_timeout":   map[string]any{"type": "string", "description": "Keep-alive idle duration; empty means 60s."},
		}, "app"),
		OutputSchema: objectSchema(map[string]any{
			"app":             map[string]any{"type": "string"},
			"changed":         map[string]any{"type": "boolean"},
			"request_limits":  map[string]any{"type": []string{"object", "null"}},
			"node_restarting": map[string]any{"type": "boolean", "description": "True when the change was written: the app's node restarts, guests must reopen their link; check url or status before asking anyone to retry."},
			"message":         map[string]any{"type": "string"},
		}, "app", "changed", "request_limits", "node_restarting"),
	})
}
