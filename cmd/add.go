package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// AddResult is the JSON data for the add command.
type AddResult struct {
	Name            string                `json:"name"`
	Type            string                `json:"type"`
	Created         bool                  `json:"created"`
	FunnelExpiresAt *time.Time            `json:"funnel_expires_at,omitempty"`
	FunnelRearmed   bool                  `json:"funnel_rearmed"`
	URL             *string               `json:"url"`
	URLPending      bool                  `json:"url_pending"`
	Endpoint        inspect.EndpointView  `json:"endpoint"`
	Exposure        inspect.ExposureView  `json:"exposure"`
	Warnings        []inspect.WarningView `json:"warnings,omitempty"`
	DaemonRunning   bool                  `json:"daemon_running"`
	AuthURL         string                `json:"auth_url,omitempty"`
	Next            []string              `json:"next,omitempty"`
}

type AddDryRunResult struct {
	DryRun  bool             `json:"dry_run"`
	Service registry.Service `json:"service"`
}

func hasScheme(target string) bool {
	return strings.Contains(target, "://")
}

func parseTags(tagsStr string) ([]string, error) {
	var tags []string
	for _, t := range strings.Split(tagsStr, ",") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if err := registry.ValidateTag(t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func isValidAllowEmail(entry string) bool {
	addr, err := mail.ParseAddress(entry)
	return err == nil && addr.Address == entry && strings.Contains(entry, "@")
}

func invalidAllowEntries(allowedUsers []string) []string {
	var invalid []string
	for _, entry := range allowedUsers {
		if strings.HasPrefix(entry, "tag:") || isValidAllowEmail(entry) {
			continue
		}
		invalid = append(invalid, entry)
	}
	return invalid
}

// AddParams holds parsed flags for the add command.
type AddParams struct {
	Name            string
	Proxy           string
	Dir             string
	TCP             string
	Ephemeral       bool
	Tags            string
	Allow           string
	Funnel          bool
	FunnelTTL       string
	FunnelTTLSet    bool
	Now             time.Time
	Public          bool
	NoAutoProvision bool
	NoDaemonInstall bool
	Domain          string
	AcmeEmail       string
	ControlURL      string
}

// parseAllowedUsers normalizes a comma-separated allow list into the stored
// AllowedUsers form. Email principals are lowercased; tag: principals keep
// their case and are validated as ACL tags. It is the single implementation
// behind `tslink add --allow` and the MCP add/share allow parameters, so the
// two surfaces cannot drift in what they persist.
func parseAllowedUsers(allow string) ([]string, error) {
	if allow == "" {
		return nil, nil
	}
	var allowedUsers []string
	for _, a := range strings.Split(allow, ",") {
		a = strings.TrimSpace(a)
		if a != "" {
			if !strings.HasPrefix(a, "tag:") {
				a = strings.ToLower(a)
			}
			allowedUsers = append(allowedUsers, a)
		}
		if strings.HasPrefix(a, "tag:") {
			if err := registry.ValidateTag(a); err != nil {
				return nil, err
			}
		}
	}
	return allowedUsers, nil
}

// funnelOptionRequiresFunnel rejects a Funnel-only option that was supplied
// without Funnel itself. Both option names are passed in so the CLI keeps its
// flag spelling while the MCP tools keep their JSON parameter spelling.
func funnelOptionRequiresFunnel(option, funnelOption string, set, funnel bool) error {
	if set && !funnel {
		return output.ErrUsage(fmt.Sprintf("%s can only be used with %s", option, funnelOption))
	}
	return nil
}

// resolveFunnelExpiry turns a Funnel TTL selection into a stored deadline.
// A nil deadline with a nil error means "never". Callers must have already
// rejected a TTL supplied without Funnel; see funnelOptionRequiresFunnel.
func resolveFunnelExpiry(funnel bool, ttl string, ttlSet bool, now time.Time) (*time.Time, error) {
	if !funnel {
		return nil, nil
	}
	if ttl == "" {
		if ttlSet {
			return nil, output.ErrUsage("funnel TTL must be one of: 1h, 8h, 24h, 72h, 7d, never")
		}
		ttl = "24h"
	}
	duration, never, err := registry.ParseFunnelTTL(ttl)
	if err != nil {
		return nil, output.ErrUsage(err.Error())
	}
	if never {
		return nil, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	expiresAt := now.UTC().Add(duration)
	return &expiresAt, nil
}

// buildService validates parameters and constructs a registry.Service.
// For Dir type, it returns the service with Type set but Path empty —
// the caller must resolve and validate the filesystem path.
func buildService(p AddParams) (registry.Service, error) {
	if err := registry.ValidateName(p.Name); err != nil {
		return registry.Service{}, err
	}

	allowedUsers, err := parseAllowedUsers(p.Allow)
	if err != nil {
		return registry.Service{}, err
	}

	modes := 0
	svcType := ""
	if p.Proxy != "" {
		modes++
		svcType = registry.TypeProxy
	}
	if p.Dir != "" {
		modes++
		svcType = registry.TypeFile
	}
	if p.TCP != "" {
		modes++
		svcType = registry.TypeTCP
	}
	if modes != 1 {
		return registry.Service{}, registry.ServiceTypeAmbiguousError()
	}

	if err := funnelOptionRequiresFunnel("--public", "--funnel", p.Public, p.Funnel); err != nil {
		return registry.Service{}, err
	}
	if err := funnelOptionRequiresFunnel("--no-auto-provision", "--funnel", p.NoAutoProvision, p.Funnel); err != nil {
		return registry.Service{}, err
	}
	if err := funnelOptionRequiresFunnel("--funnel-ttl", "--funnel", p.FunnelTTLSet, p.Funnel); err != nil {
		return registry.Service{}, err
	}
	if err := registry.ValidateFunnelGuardrails(svcType, p.Funnel, allowedUsers, p.ControlURL, p.Public); err != nil {
		return registry.Service{}, err
	}
	if p.Domain != "" || p.AcmeEmail != "" {
		return registry.Service{}, registry.FeatureUnavailableError("custom-domain/ACME runtime is not wired; --domain and --acme-email are unavailable")
	}
	if err := registry.ValidateControlURL(p.ControlURL); err != nil {
		return registry.Service{}, output.ErrUsage(err.Error())
	}
	if p.TCP != "" && len(allowedUsers) > 0 {
		return registry.Service{}, registry.AllowUnsupportedTCPError()
	}

	var tags []string
	if p.Tags != "" {
		tags, err = parseTags(p.Tags)
		if err != nil {
			return registry.Service{}, err
		}
	}
	// If no tags specified, use default
	if len(tags) == 0 {
		tags = []string{config.GetDefaultTag()}
	}
	if p.TCP != "" {
		if err := registry.ValidateTCPTarget(p.TCP); err != nil {
			return registry.Service{}, output.ErrUsage(err.Error())
		}
		host, portStr, err := net.SplitHostPort(p.TCP)
		if err != nil {
			return registry.Service{}, output.ErrUsage(fmt.Sprintf("--tcp requires host:port format: %v", err))
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return registry.Service{}, output.ErrUsage(fmt.Sprintf("invalid port: %s", portStr))
		}
		return registry.Service{
			Name: p.Name, Type: registry.TypeTCP,
			Target: net.JoinHostPort(host, portStr), Port: port,
			Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
			ControlURL: p.ControlURL,
		}, nil
	}

	funnelExpiresAt, err := resolveFunnelExpiry(p.Funnel, p.FunnelTTL, p.FunnelTTLSet, p.Now)
	if err != nil {
		return registry.Service{}, err
	}

	if p.Proxy != "" {
		target := p.Proxy
		if !hasScheme(target) {
			target = "http://" + target
		}
		return registry.Service{
			Name: p.Name, Type: registry.TypeProxy, Target: target,
			Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
			Funnel: p.Funnel, PublicAck: p.Public, NoAutoProvision: p.NoAutoProvision,
			FunnelExpiresAt: funnelExpiresAt,
			ControlURL:      p.ControlURL,
		}, nil
	}

	// Dir mode — path validation is done in RunE (needs filesystem)
	return registry.Service{
		Name: p.Name, Type: registry.TypeFile,
		Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
		ControlURL: p.ControlURL,
	}, nil
}

func addWarnings(svc registry.Service, base []inspect.WarningView) []inspect.WarningView {
	warnings := append([]inspect.WarningView(nil), base...)
	for _, invalid := range invalidAllowEntries(svc.AllowedUsers) {
		warnings = append(warnings, inspect.WarningView{
			Code:     "invalid_allow_entry",
			Severity: "warning",
			Message:  fmt.Sprintf("--allow entry %q is neither a valid email address nor tag:<name>; it will likely deny rather than allow access.", invalid),
			Source:   "cmd.add",
		})
	}
	return warnings
}

func buildAddResult(ctx context.Context, svc registry.Service, created bool, pidPath, regPath, snapshotPath string, wait time.Duration) (AddResult, error) {
	view := inspect.ServiceViewFor(registry.EffectiveServiceAt(svc, time.Now()))
	result := AddResult{
		Name:            svc.Name,
		Type:            svc.Type,
		Created:         created,
		FunnelExpiresAt: cloneTimePointer(svc.FunnelExpiresAt),
		URLPending:      true,
		Endpoint:        view.Endpoint,
		Exposure:        view.Exposure,
		Warnings:        addWarnings(svc, view.Warnings),
	}
	result.Endpoint.Display = ""
	result.Endpoint.Host = ""
	result.DaemonRunning = isRunningFn(pidPath)
	if !result.DaemonRunning {
		result.Next = []string{"tslink install"}
		result.Warnings = append(result.Warnings, inspect.WarningView{Code: "daemon_not_running", Severity: "error", Source: "cmd.add", Message: "Configuration saved only; run 'tslink install' to make this service reachable."})
		return result, nil
	}
	resolution, handoff, err := resolveAddEndpoint(ctx, pidPath, regPath, snapshotPath, svc.Name, wait)
	if err != nil {
		if code, ok := registry.ErrorCode(err); ok && code == registry.CodeURLNotReady && wait <= 0 {
			return result, nil
		}
		return AddResult{}, err
	}
	if handoff != "" {
		result.AuthURL = handoff
		result.Next = []string{"open the auth_url to authorize this node", "tslink url " + svc.Name + " --wait=30s"}
		return result, nil
	}
	result.URL = &resolution.Result.URL
	result.URLPending = false
	result.Endpoint = resolution.Endpoint
	return result, nil
}

func resolveAddEndpoint(ctx context.Context, pidPath, regPath, snapshotPath, name string, wait time.Duration) (serviceURLResolution, string, error) {
	deadline := time.Now().Add(wait)
	for {
		resolution, err := resolveServiceEndpointOnce(pidPath, regPath, snapshotPath, name)
		if err == nil {
			return resolution, "", nil
		}
		if code, ok := registry.ErrorCode(err); !ok || (code != registry.CodeURLNotReady && code != registry.CodeEnrollmentRequired) {
			return resolution, "", err
		}
		if handoff, ok := validAuthHandoffForService(pidPath, name); ok {
			return resolution, handoff.AuthURL, nil
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return resolution, "", err
		}
		timer := time.NewTimer(min(urlPollInterval, time.Until(deadline)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return resolution, "", ctx.Err()
		case <-timer.C:
		}
	}
}

// resolveAddService completes a service already built from p by buildService
// and runs the canonical registry admission gate. Dir services get their root
// resolved here because that step needs the filesystem, which buildService
// deliberately does not touch. Both `tslink add` and the MCP add tool call it,
// so neither can reach registry.AddWithOutcome on a service the other would
// have rejected.
func resolveAddService(svc registry.Service, p AddParams) (registry.Service, error) {
	if svc.Type == registry.TypeFile {
		if !filepath.IsAbs(p.Dir) {
			return registry.Service{}, registry.PathMustBeAbsoluteError(p.Dir)
		}
		if err := registry.ValidateFileRoot(p.Dir); err != nil {
			return registry.Service{}, err
		}
		svc.Path = filepath.Clean(p.Dir)
	}
	if err := registry.ValidateService(svc); err != nil {
		if _, coded := registry.ErrorCode(err); coded {
			return registry.Service{}, err
		}
		return registry.Service{}, output.ErrUsage(err.Error())
	}
	return svc, nil
}

// executeAdd writes an admitted service to the registry and builds the shared
// AddResult. It is the single write path behind `tslink add` and the MCP add
// tool. The persisted service is returned alongside the result because the
// human CLI rendering reports fields (TCP target and port) the result does not
// carry.
func executeAdd(ctx context.Context, svc registry.Service, regPath, pidPath, snapshotPath string, preserveFunnelExpiry bool, wait time.Duration, afterPersist ...func() error) (AddResult, registry.Service, error) {
	outcome, err := registry.AddWithOutcome(regPath, svc, registry.AddOptions{
		PreserveFunnelExpiry: preserveFunnelExpiry,
	})
	if err != nil {
		return AddResult{}, registry.Service{}, err
	}
	persisted, err := loadPersistedService(regPath, svc.Name)
	if err != nil {
		return AddResult{}, registry.Service{}, err
	}
	for _, setup := range afterPersist {
		if err := setup(); err != nil {
			return AddResult{}, persisted, daemonRegistryRetainedError(err)
		}
	}
	result, err := buildAddResult(ctx, persisted, outcome.Created, pidPath, regPath, snapshotPath, wait)
	if err != nil {
		return AddResult{}, registry.Service{}, err
	}
	result.FunnelRearmed = outcome.RearmedExpiredFunnel
	return result, persisted, nil
}

func loadPersistedService(regPath, name string) (registry.Service, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return registry.Service{}, err
	}
	for _, svc := range reg.Services {
		if svc.Name == name {
			return svc, nil
		}
	}
	return registry.Service{}, fmt.Errorf("persisted service not found after successful add: %s", name)
}

func init() {
	addCmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Register a local service or file directory",
		Long: `Register a local service or file directory to expose on the Tailscale network.

Examples:
  tslink add myapp --proxy localhost:3000         Expose a web service
  tslink add docs --dir ~/Documents               Expose a file directory
  tslink add mydb --tcp localhost:5432             Expose raw TCP (e.g., database)
  tslink add myapp --proxy :3000 --ephemeral      Ephemeral node (removed on disconnect)
  tslink add myapp --proxy :3000 --tags tag:web    Tag the node in the tailnet
  tslink add myapp --proxy :3000 --funnel --public Expose publicly via Tailscale Funnel`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			proxyTarget, _ := cmd.Flags().GetString("proxy")
			dirPath, _ := cmd.Flags().GetString("dir")
			tcpTarget, _ := cmd.Flags().GetString("tcp")
			ephemeral, _ := cmd.Flags().GetBool("ephemeral")
			tagsStr, _ := cmd.Flags().GetString("tags")
			allowStr, _ := cmd.Flags().GetString("allow")
			funnel, _ := cmd.Flags().GetBool("funnel")
			funnelTTL, _ := cmd.Flags().GetString("funnel-ttl")
			public, _ := cmd.Flags().GetBool("public")
			noAutoProvision, _ := cmd.Flags().GetBool("no-auto-provision")
			domainName, _ := cmd.Flags().GetString("domain")
			acmeEmail, _ := cmd.Flags().GetString("acme-email")
			controlURL, _ := cmd.Flags().GetString("control-url")
			wait, _ := cmd.Flags().GetDuration("wait")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			params := AddParams{
				Name:            args[0],
				Proxy:           proxyTarget,
				Dir:             dirPath,
				TCP:             tcpTarget,
				Ephemeral:       ephemeral,
				Tags:            tagsStr,
				Allow:           allowStr,
				Funnel:          funnel,
				FunnelTTL:       funnelTTL,
				FunnelTTLSet:    cmd.Flags().Changed("funnel-ttl"),
				Public:          public,
				NoAutoProvision: noAutoProvision,
				Domain:          domainName,
				AcmeEmail:       acmeEmail,
				ControlURL:      controlURL,
			}
			svc, err := buildService(params)
			if err != nil {
				return err
			}

			warnings := addWarnings(svc, nil)
			if !jsonOutput(cmd) {
				for _, warning := range warnings {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", warning.Message)
				}
			}

			// Dry-run and actual registration share the canonical admission
			// gate in resolveAddService, which also resolves the dir root.
			// Only the registry lock/write is skipped below for dry-run.
			svc, err = resolveAddService(svc, params)
			if err != nil {
				return err
			}

			if dryRun {
				// Preview the same compatibility rule as AddWithOptions: an
				// existing entry without funnel_expires_at is legacy never unless
				// the operator explicitly supplies --funnel-ttl.
				if !cmd.Flags().Changed("funnel-ttl") {
					regPath, err := registryPathFn()
					if err != nil {
						return err
					}
					reg, err := registry.Load(regPath)
					if err != nil {
						return err
					}
					for _, existing := range reg.Services {
						if existing.Name == svc.Name {
							if !svc.Funnel || existing.FunnelExpiresAt == nil || existing.FunnelExpiresAt.After(time.Now()) {
								svc.FunnelExpiresAt = existing.FunnelExpiresAt
							}
							break
						}
					}
				}
				if jsonOutput(cmd) {
					output.Success("add", AddDryRunResult{DryRun: true, Service: svc})
					return nil
				}
				encoded, err := json.MarshalIndent(svc, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
				return nil
			}

			if err := ensureDirFn(); err != nil {
				return err
			}
			noDaemonInstall, _ := cmd.Flags().GetBool("no-daemon-install")

			regPath, err := registryPathFn()
			if err != nil {
				return err
			}

			pidPath, err := config.PIDPath()
			if err != nil {
				return err
			}
			snapshotPath, err := config.RuntimeSnapshotPath()
			if err != nil {
				return err
			}
			result, persisted, err := executeAdd(cmd.Context(), svc, regPath, pidPath, snapshotPath, !cmd.Flags().Changed("funnel-ttl"), wait, func() error {
				return ensureDaemonFn(cmd.Context(), cmd.ErrOrStderr(), noDaemonInstall)
			})
			if err != nil {
				return err
			}
			svc = persisted

			if jsonOutput(cmd) {
				output.Success("add", result)
				return nil
			}
			if !result.DaemonRunning {
				if result.FunnelRearmed {
					fmt.Fprintln(cmd.OutOrStdout(), "→ re-armed expired Funnel (24h)")
				}
				if svc.Funnel {
					fmt.Fprintln(cmd.OutOrStdout(), "Configured exposure: PUBLIC via Tailscale Funnel (inactive until started).")
				}
				if svc.Type == registry.TypeTCP {
					fmt.Fprintln(cmd.OutOrStdout(), "TCP configuration: TSLink HTTP allow and identity headers do not apply to raw TCP; protection is Tailscale policy plus backend auth.")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Service %q saved to configuration; not running or reachable.\nNext: tslink install\n", svc.Name)
				return nil
			}
			if result.AuthURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Service %q is waiting for Tailscale authorization.\nOpen: %s\nThen: tslink url %s --wait=30s\n", svc.Name, result.AuthURL, svc.Name)
				return nil
			}

			if svc.Type == registry.TypeTCP {
				fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ TCP service %q registered (target %s, port %d)\n", svc.Name, svc.Target, svc.Port)
				fmt.Fprintln(cmd.OutOrStdout(), "TSLink HTTP allow and identity headers do not apply to raw TCP; protection is Tailscale policy plus backend auth.")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ Service %q registered\n", svc.Name)
				if result.FunnelRearmed {
					fmt.Fprintln(cmd.OutOrStdout(), "→ re-armed expired Funnel (24h)")
				}
				if result.URLPending {
					if svc.Funnel {
						fmt.Fprintf(cmd.OutOrStdout(), "URL: pending (PUBLIC via Tailscale Funnel; run: tslink url %s --wait=30s)\n", svc.Name)
					} else {
						fmt.Fprintf(cmd.OutOrStdout(), "URL: pending (run: tslink url %s --wait=30s)\n", svc.Name)
					}
				} else if svc.Funnel {
					fmt.Fprintf(cmd.OutOrStdout(), "URL: %s (PUBLIC via Tailscale Funnel)\n", *result.URL)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "URL: %s\n", *result.URL)
				}
			}
			return nil
		},
	}

	addCmd.Flags().String("proxy", "", "Proxy target in host:port or URL form")
	addCmd.Flags().String("dir", "", "Directory to expose")
	addCmd.Flags().String("tcp", "", "TCP proxy target in host:port form")
	addCmd.Flags().Bool("ephemeral", false, "Register as ephemeral node (removed on disconnect)")
	addCmd.Flags().String("tags", "", "Comma-separated ACL tags (e.g., tag:web,tag:internal)")
	addCmd.Flags().Bool("funnel", false, "Expose publicly via Tailscale Funnel (proxy only, requires --public)")
	addCmd.Flags().String("funnel-ttl", "24h", "Public Funnel lifetime: 1h, 8h, 24h, 72h, 7d, or never")
	addCmd.Flags().Bool("public", false, "Acknowledge public internet exposure for --funnel (only valid with --funnel)")
	addCmd.Flags().Bool("no-auto-provision", false, "Disable automatic Funnel policy provisioning (only valid with --funnel)")
	addCmd.Flags().String("domain", "", "[UNAVAILABLE] Reserved: custom-domain runtime TLS is unavailable; rejected with feature_unavailable")
	addCmd.Flags().String("allow", "", "Comma-separated allowed identities (e.g., user@example.com,tag:admin)")
	addCmd.Flags().String("acme-email", "", "[UNAVAILABLE] Reserved: ACME runtime TLS is unavailable; rejected with feature_unavailable")
	addCmd.Flags().String("control-url", "", "Per-service custom control server URL (e.g., Headscale)")
	addCmd.Flags().Duration("wait", defaultURLWait, "Wait for an exact runtime URL or enrollment URL (default 30s; 0 disables waiting)")
	addCmd.Flags().Lookup("wait").NoOptDefVal = defaultURLWait.String()
	addCmd.Flags().Bool("dry-run", false, "Validate and print the service JSON without writing registry.json")
	addCmd.Flags().Bool("no-daemon-install", false, "Save configuration only; do not install or start the background service")
	rootCmd.AddCommand(addCmd)
}
