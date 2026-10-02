package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"

	"github.com/anydoor7/tslink/internal/inspect"
	"github.com/anydoor7/tslink/internal/output"
	"github.com/anydoor7/tslink/internal/recipes"
	"github.com/anydoor7/tslink/internal/registry"
	"github.com/spf13/cobra"
)

func isRecipeLoopback(host string) bool { ip := net.ParseIP(host); return ip != nil && ip.IsLoopback() }

var appsDetectFn = recipes.Detect
var recipeAddIfMissingFn = registry.AddIfMissing

type recipeRequest struct {
	RecipeID          string  `json:"recipe_id"`
	Name              string  `json:"name,omitempty"`
	Target            string  `json:"target,omitempty"`
	Allow             string  `json:"allow,omitempty"`
	Tags              string  `json:"tags,omitempty"`
	Ephemeral         bool    `json:"ephemeral,omitempty"`
	Funnel            bool    `json:"funnel,omitempty"`
	PublicAck         bool    `json:"public_ack,omitempty"`
	FunnelTTL         *string `json:"funnel_ttl,omitempty"`
	NoAutoProvision   bool    `json:"no_auto_provision,omitempty"`
	NoDaemonInstall   bool    `json:"no_daemon_install,omitempty"`
	ControlURL        string  `json:"control_url,omitempty"`
	ForceUnsafePublic bool    `json:"force_unsafe_public,omitempty"`
}
type RecipeResult struct {
	SchemaVersion  int                 `json:"schema_version"`
	CatalogVersion int                 `json:"catalog_version"`
	Recipe         recipes.Recipe      `json:"recipe"`
	Service        inspect.ServiceView `json:"service"`
	Requested      inspect.ServiceView `json:"requested"`
	Action         string              `json:"action"`
	DryRun         bool                `json:"dry_run"`
	Applied        bool                `json:"applied"`
	Warnings       []string            `json:"warnings"`
	Next           []string            `json:"next"`
}

func recipeService(req recipeRequest) (recipes.Recipe, registry.Service, error) {
	r, ok := recipes.Lookup(req.RecipeID)
	if !ok {
		return r, registry.Service{}, output.ErrNotFound(fmt.Sprintf("recipe %q not found; run tslink apps list", req.RecipeID))
	}
	if req.ForceUnsafePublic && !req.Funnel {
		return r, registry.Service{}, output.ErrUsage("--force-unsafe-public / force_unsafe_public requires Funnel")
	}
	if r.SafetyLevel == "never_public" && req.Funnel && !req.ForceUnsafePublic {
		return r, registry.Service{}, output.ErrUsage(fmt.Sprintf("DANGER: %s is never public: %s Refusing Funnel. Override only with --force-unsafe-public (MCP: force_unsafe_public=true), which can expose control of your host or private data to anyone on the internet.", r.DisplayName, r.SafetyNote))
	}
	name := req.Name
	if name == "" {
		name = r.RecommendedName
	}
	target := req.Target
	if target == "" {
		target = r.DefaultTarget
	}
	ttl := ""
	if req.FunnelTTL != nil {
		ttl = *req.FunnelTTL
	}
	p := AddParams{Name: name, Proxy: target, Allow: req.Allow, Tags: req.Tags, Ephemeral: req.Ephemeral, Funnel: req.Funnel, Public: req.PublicAck, FunnelTTL: ttl, FunnelTTLSet: req.FunnelTTL != nil, NoAutoProvision: req.NoAutoProvision, ControlURL: req.ControlURL}
	svc, err := buildService(p)
	if err != nil {
		return r, svc, err
	}
	u, err := url.Parse(svc.Target)
	if err != nil || (u.Hostname() != "localhost" && !isRecipeLoopback(u.Hostname())) {
		return r, svc, output.ErrUsage("recipe target must be a loopback HTTP(S) service")
	}
	svc, err = resolveAddService(svc, p)
	return r, svc, err
}
func planRecipe(req recipeRequest, regPath string) (RecipeResult, registry.Service, error) {
	r, svc, err := recipeService(req)
	if err != nil {
		return RecipeResult{}, svc, err
	}
	reg, err := registry.Load(regPath)
	if err != nil {
		return RecipeResult{}, svc, err
	}
	result := RecipeResult{SchemaVersion: 1, CatalogVersion: recipes.List().CatalogVersion, Recipe: r, Service: inspect.ServiceViewFor(svc), Requested: inspect.ServiceViewFor(svc), Action: templateActionCreate, DryRun: true, Warnings: []string{r.SafetyNote, "App authentication and proxy configuration are owner actions; discovery does not verify them."}, Next: []string{"Apply with --yes after reviewing this plan; --dry-run always wins.", "Replace YOUR-TAILNET placeholders using the exact URL from tslink url " + svc.Name + " --wait=30s."}}
	if svc.Funnel {
		result.Warnings = append(result.Warnings, "PUBLIC INTERNET: everyone can reach this gateway. TSLink allow lists do not protect Funnel; verify application authentication.")
	}
	if req.ForceUnsafePublic {
		result.Warnings = append(result.Warnings, "DANGER: you overrode the never-public recipe policy. This may expose host control, code execution, GPU usage or private data to the internet.")
	}
	for _, existing := range reg.Services {
		if existing.Name == svc.Name {
			result.Action = templateActionSkipExisting
			result.Service = inspect.ServiceViewFor(existing)
			result.Warnings = append(result.Warnings, "Existing service is kept unchanged; requested recipe defaults are not applied. Inspect service and requested before proceeding.")
			break
		}
	}
	return result, svc, nil
}
func applyRecipe(ctx context.Context, req recipeRequest, regPath string, dryRun bool, errOut io.Writer) (RecipeResult, error) {
	result, svc, err := planRecipe(req, regPath)
	if err != nil || dryRun {
		return result, err
	}
	if err := ensureDirFn(); err != nil {
		return RecipeResult{}, err
	}
	if result.Action != templateActionSkipExisting {
		created, err := recipeAddIfMissingFn(regPath, svc)
		if err != nil {
			return RecipeResult{}, err
		}
		if created {
			result.Action = templateActionCreated
		} else {
			result.Action = templateActionSkipExisting
			result.Warnings = append(result.Warnings, "A concurrent registration won; its service is kept unchanged.")
		}
	}
	persisted, err := loadPersistedService(regPath, svc.Name)
	if err != nil {
		return RecipeResult{}, err
	}
	if result.Action == templateActionSkipExisting {
		// Adopt the exact stored value under the registry lock before reporting
		// reuse or attempting setup. Its creator may still be waiting on a URL
		// and compensating a tentative registration on failure.
		kept, err := addKeepIfUnchangedFn(regPath, persisted)
		if err != nil {
			return RecipeResult{}, fmt.Errorf("adopt recipe registration %q: %w", svc.Name, err)
		}
		if !kept {
			return RecipeResult{}, output.ErrConflict(fmt.Sprintf("service %q was deleted or replaced during recipe adoption; preview again before retrying", svc.Name))
		}
	}
	result.Service = inspect.ServiceViewFor(persisted)
	result.DryRun = false
	result.Applied = result.Action == templateActionCreated
	result.Next = []string{"tslink url " + svc.Name + " --wait=30s", "Apply the owner configuration below using the exact URL; registration alone does not verify app readiness."}
	if err := ensureDaemonFn(ctx, errOut, req.NoDaemonInstall); err != nil {
		return result, daemonRegistryRetainedError(err)
	}
	return result, nil
}
func detectApps(ctx context.Context, regPath string) (recipes.Detection, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return recipes.Detection{}, err
	}
	services := []recipes.RegisteredService{}
	for _, s := range reg.Services {
		if s.Type == registry.TypeProxy {
			services = append(services, recipes.RegisteredService{Name: s.Name, Target: s.Target})
		}
	}
	return appsDetectFn(ctx, services)
}
func renderRecipe(out io.Writer, result RecipeResult) {
	fmt.Fprintf(out, "%s: %s -> %s (%s)\n", result.Recipe.DisplayName, result.Service.Name, result.Service.Backend.Display, result.Action)
	fmt.Fprintf(out, "Ports: %v. %s\nWebSockets: %t; recommended health path: %s (data only).\n", result.Recipe.DefaultPorts, result.Recipe.PortNote, result.Recipe.WebSockets, result.Recipe.HealthPath)
	for _, w := range result.Warnings {
		fmt.Fprintf(out, "Warning: %s\n", w)
	}
	for _, n := range result.Recipe.Notes {
		fmt.Fprintf(out, "\nOwner configuration: %s\n%s\n", n.Text, n.Snippet)
		for _, s := range n.Sources {
			fmt.Fprintf(out, "Docs: %s (accessed %s)\n", s.URL, s.Accessed)
		}
	}
	for _, next := range result.Next {
		fmt.Fprintln(out, next)
	}
}
func recipeRequestFromCLI(cmd *cobra.Command, id, name string) recipeRequest {
	str := func(key string) string { v, _ := cmd.Flags().GetString(key); return v }
	b := func(key string) bool { v, _ := cmd.Flags().GetBool(key); return v }
	var ttl *string
	if cmd.Flags().Changed("funnel-ttl") {
		value := str("funnel-ttl")
		ttl = &value
	}
	return recipeRequest{RecipeID: id, Name: name, Target: str("proxy"), Allow: str("allow"), Tags: str("tags"), Ephemeral: b("ephemeral"), Funnel: b("funnel"), PublicAck: b("public"), FunnelTTL: ttl, NoAutoProvision: b("no-auto-provision"), NoDaemonInstall: b("no-daemon-install"), ControlURL: str("control-url"), ForceUnsafePublic: b("force-unsafe-public")}
}
func runRecipeCLI(cmd *cobra.Command, req recipeRequest, command string) error {
	dry, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")
	path, err := registryPathFn()
	if err != nil {
		return err
	}
	result, err := applyRecipe(cmd.Context(), req, path, dry || !yes, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	if jsonOutput(cmd) {
		output.Success(command, result)
	} else {
		renderRecipe(cmd.OutOrStdout(), result)
	}
	return nil
}
func init() {
	apps := &cobra.Command{Use: "apps", Short: "Discover local apps and preview app-specific sharing recipes", Args: cobra.NoArgs, RunE: runCommandGroup}
	list := &cobra.Command{Use: "list", Short: "List the versioned application recipe catalog", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c := recipes.List()
		if jsonOutput(cmd) {
			output.Success("apps list", c)
		} else {
			for _, r := range c.Recipes {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s, ports %v, safety %s\n", r.ID, r.DisplayName, r.DefaultPorts, r.SafetyLevel)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Templates are generic multi-service stacks; recipes add app configuration and safety advice. Use apps share <id> to preview.")
		}
		return nil
	}}
	detect := &cobra.Command{Use: "detect", Short: "Fingerprint listening TCP services over loopback HTTP without credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := registryPathFn()
		if err != nil {
			return err
		}
		result, err := detectApps(cmd.Context(), path)
		if err != nil {
			return err
		}
		if jsonOutput(cmd) {
			output.Success("apps detect", result)
		} else {
			for _, m := range result.Matches {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s (%s); registered: %s\n", m.RecipeID, m.Target, m.Confidence, strings.Join(m.Registered, ", "))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d loopback listeners, %d matches; complete: %t. Detection does not verify authentication or health.\n", len(result.Listeners), len(result.Matches), result.Complete)
			for _, w := range result.Warnings {
				fmt.Fprintln(cmd.ErrOrStderr(), w)
			}
		}
		return nil
	}}
	share := &cobra.Command{Use: "share <id>", Short: "Preview an app recipe; --yes registers missing services", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		return runRecipeCLI(cmd, recipeRequestFromCLI(cmd, args[0], name), "apps share")
	}}
	share.Flags().String("name", "", "Override the recommended service name")
	share.Flags().String("proxy", "", "Override the loopback HTTP(S) target (host port, not container port)")
	share.Flags().String("allow", "", "Comma-separated private HTTP identities")
	share.Flags().String("tags", "", "Comma-separated ACL tags")
	share.Flags().Bool("ephemeral", false, "Use an ephemeral node")
	share.Flags().Bool("funnel", false, "Publish on the internet; requires --public and recipe safety review")
	share.Flags().Bool("public", false, "Acknowledge public internet exposure (requires --funnel)")
	share.Flags().String("funnel-ttl", "24h", "Public lifetime: 1h, 8h, 24h, 72h, 7d, or never")
	share.Flags().Bool("no-auto-provision", false, "Disable automatic Funnel policy provisioning (requires --funnel)")
	share.Flags().String("control-url", "", "Per-service control server URL")
	share.Flags().Bool("no-daemon-install", false, "Save configuration only; do not install or start the background service")
	share.Flags().Bool("dry-run", false, "Preview without writing, even with --yes")
	share.Flags().Bool("yes", false, "Apply the reviewed recipe plan without replacing existing entries")
	share.Flags().Bool("force-unsafe-public", false, "DANGER: override never-public recipe policy; may expose host control or private data to everyone")
	apps.AddCommand(list, detect, share)
	rootCmd.AddCommand(apps)
}
