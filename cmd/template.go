package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const (
	templateActionCreate       = "create"
	templateActionCreated      = "created"
	templateActionSkipExisting = "skip_existing"
	templateTagPolicy          = "Built-ins intentionally use the uniform tag tag:tslink for private template services."
)

var templateAddIfMissingFn = registry.AddIfMissing

type templateServiceSpec struct {
	Summary string
	Params  AddParams
}

type serviceTemplate struct {
	Name        string
	Summary     string
	Description string
	Services    []templateServiceSpec
}

type TemplateSummary struct {
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	ServiceCount int    `json:"service_count"`
}

type TemplateListResult struct {
	SchemaVersion string            `json:"schema_version"`
	Templates     []TemplateSummary `json:"templates"`
	Count         int               `json:"count"`
}

type TemplateShowResult struct {
	SchemaVersion string                `json:"schema_version"`
	Name          string                `json:"name"`
	Summary       string                `json:"summary"`
	Description   string                `json:"description"`
	Services      []inspect.ServiceView `json:"services"`
	Count         int                   `json:"count"`
}

type TemplatePlanItem struct {
	Name    string              `json:"name"`
	Action  string              `json:"action"`
	Message string              `json:"message,omitempty"`
	Service inspect.ServiceView `json:"service"`
}

type TemplateApplyResult struct {
	SchemaVersion string             `json:"schema_version"`
	Name          string             `json:"name"`
	Summary       string             `json:"summary"`
	DryRun        bool               `json:"dry_run"`
	Applied       bool               `json:"applied"`
	Services      []TemplatePlanItem `json:"services"`
	Created       int                `json:"created"`
	Skipped       int                `json:"skipped"`
}

func builtinTemplates() []serviceTemplate {
	return []serviceTemplate{
		{
			Name:        "personal-harness",
			Summary:     "Local personal harness web and API endpoints",
			Description: "Small private harness services on localhost ports in the 8787 range. " + templateTagPolicy,
			Services: []templateServiceSpec{
				{
					Summary: "Harness web interface",
					Params:  AddParams{Name: "harness-web", Proxy: "localhost:8787", Tags: "tag:tslink"},
				},
				{
					Summary: "Harness API endpoint",
					Params:  AddParams{Name: "harness-api", Proxy: "localhost:8788", Tags: "tag:tslink"},
				},
			},
		},
		{
			Name:        "dev-suite",
			Summary:     "Local development web, API, and database endpoints",
			Description: "Common private development stack with HTTP app endpoints and raw TCP database routing. " + templateTagPolicy,
			Services: []templateServiceSpec{
				{
					Summary: "Development web frontend",
					Params:  AddParams{Name: "dev-web", Proxy: "localhost:3000", Tags: "tag:tslink"},
				},
				{
					Summary: "Development API backend",
					Params:  AddParams{Name: "dev-api", Proxy: "localhost:8000", Tags: "tag:tslink"},
				},
				{
					Summary: "Local PostgreSQL TCP endpoint",
					Params:  AddParams{Name: "dev-postgres", TCP: "localhost:5432", Tags: "tag:tslink"},
				},
			},
		},
		{
			Name:        "local-ai-suite",
			Summary:     "Local AI model and chat endpoints",
			Description: "Private localhost AI endpoints such as Ollama and Open WebUI. " + templateTagPolicy,
			Services: []templateServiceSpec{
				{
					Summary: "Ollama HTTP API",
					Params:  AddParams{Name: "ollama", Proxy: "localhost:11434", Tags: "tag:tslink"},
				},
				{
					Summary: "Open WebUI web interface",
					Params:  AddParams{Name: "open-webui", Proxy: "localhost:8080", Tags: "tag:tslink"},
				},
			},
		},
	}
}

func templateByName(name string) (serviceTemplate, bool) {
	for _, tmpl := range builtinTemplates() {
		if tmpl.Name == name {
			return tmpl, true
		}
	}
	return serviceTemplate{}, false
}

type templateNotFoundError struct {
	name string
}

func (e templateNotFoundError) Error() string {
	return fmt.Sprintf("template %q not found", e.name)
}

func (e templateNotFoundError) Unwrap() error {
	return output.ErrNotFound(e.Error())
}

func (e templateNotFoundError) StableCode() string {
	return output.StableErrorCode(output.ExitNotFound)
}

func (e templateNotFoundError) NextCommands() []string {
	return []string{"tslink template list --json"}
}

func templateNotFound(name string) error {
	return templateNotFoundError{name: name}
}

func templateSummaries() []TemplateSummary {
	templates := builtinTemplates()
	summaries := make([]TemplateSummary, 0, len(templates))
	for _, tmpl := range templates {
		summaries = append(summaries, TemplateSummary{
			Name:         tmpl.Name,
			Summary:      tmpl.Summary,
			ServiceCount: len(tmpl.Services),
		})
	}
	return summaries
}

func listTemplatesResult() TemplateListResult {
	summaries := templateSummaries()
	return TemplateListResult{
		SchemaVersion: inspect.SchemaVersion,
		Templates:     summaries,
		Count:         len(summaries),
	}
}

func buildTemplateServices(tmpl serviceTemplate) ([]registry.Service, error) {
	services := make([]registry.Service, 0, len(tmpl.Services))
	for _, spec := range tmpl.Services {
		svc, err := buildService(spec.Params)
		if err != nil {
			return nil, fmt.Errorf("build template %q service %q: %w", tmpl.Name, spec.Params.Name, err)
		}
		if err := registry.ValidateService(svc); err != nil {
			return nil, fmt.Errorf("validate template %q service %q: %w", tmpl.Name, svc.Name, err)
		}
		services = append(services, svc)
	}
	return services, nil
}

func showTemplateResult(name string) (TemplateShowResult, error) {
	tmpl, ok := templateByName(name)
	if !ok {
		return TemplateShowResult{}, templateNotFound(name)
	}
	services, err := buildTemplateServices(tmpl)
	if err != nil {
		return TemplateShowResult{}, err
	}
	return TemplateShowResult{
		SchemaVersion: inspect.SchemaVersion,
		Name:          tmpl.Name,
		Summary:       tmpl.Summary,
		Description:   tmpl.Description,
		Services:      inspect.ServiceViews(services),
		Count:         len(services),
	}, nil
}

func planTemplateApply(name string, reg *registry.Registry, dryRun bool) (TemplateApplyResult, []registry.Service, error) {
	tmpl, ok := templateByName(name)
	if !ok {
		return TemplateApplyResult{}, nil, templateNotFound(name)
	}
	services, err := buildTemplateServices(tmpl)
	if err != nil {
		return TemplateApplyResult{}, nil, err
	}

	existing := map[string]bool{}
	if reg != nil {
		for _, svc := range reg.Services {
			existing[svc.Name] = true
		}
	}

	result := TemplateApplyResult{
		SchemaVersion: inspect.SchemaVersion,
		Name:          tmpl.Name,
		Summary:       tmpl.Summary,
		DryRun:        dryRun,
		Applied:       false,
		Services:      make([]TemplatePlanItem, 0, len(services)),
	}

	for _, svc := range services {
		item := TemplatePlanItem{
			Name:    svc.Name,
			Action:  templateActionCreate,
			Service: inspect.ServiceViewFor(svc),
		}
		if existing[svc.Name] {
			item.Action = templateActionSkipExisting
			item.Message = "service already exists; leaving existing registry entry unchanged"
			result.Skipped++
		} else {
			result.Created++
		}
		result.Services = append(result.Services, item)
	}

	return result, services, nil
}

func applyTemplate(name, regPath string, dryRun bool) (TemplateApplyResult, error) {
	reg, err := registry.Load(regPath)
	if err != nil {
		return TemplateApplyResult{}, err
	}
	result, services, err := planTemplateApply(name, reg, dryRun)
	if err != nil {
		return TemplateApplyResult{}, err
	}
	if dryRun {
		return result, nil
	}

	for i, svc := range services {
		if result.Services[i].Action == templateActionSkipExisting {
			continue
		}
		created, err := templateAddIfMissingFn(regPath, svc)
		if err != nil {
			return TemplateApplyResult{}, err
		}
		if created {
			result.Services[i].Action = templateActionCreated
			continue
		}
		result.Services[i].Action = templateActionSkipExisting
		result.Services[i].Message = "service already exists; leaving existing registry entry unchanged"
		result.Created--
		result.Skipped++
	}
	result.Applied = true
	return result, nil
}

func renderTemplateList(out io.Writer, result TemplateListResult) {
	fmt.Fprintln(out, "Available templates:")
	for _, tmpl := range result.Templates {
		fmt.Fprintf(out, "  %s (%d services) - %s\n", tmpl.Name, tmpl.ServiceCount, tmpl.Summary)
	}
}

func renderTemplateShow(out io.Writer, result TemplateShowResult) {
	fmt.Fprintf(out, "%s\n", result.Name)
	fmt.Fprintf(out, "%s\n\n", result.Description)
	for _, svc := range result.Services {
		fmt.Fprintf(out, "  %s: %s -> %s\n", svc.Name, svc.Type, svc.Backend.Display)
	}
}

func renderTemplateApply(out io.Writer, result TemplateApplyResult) {
	mode := "dry run"
	if result.Applied {
		mode = "applied"
	}
	fmt.Fprintf(out, "Template %q %s\n", result.Name, mode)
	for _, item := range result.Services {
		message := item.Message
		if message != "" {
			message = " - " + message
		}
		fmt.Fprintf(out, "  %s %s%s\n", item.Action, item.Name, message)
	}
	if result.DryRun {
		fmt.Fprintln(out, "No registry changes written. Re-run with --yes to apply.")
		return
	}
	fmt.Fprintf(out, "Created %d, skipped %d.\n", result.Created, result.Skipped)
}

func init() {
	templateCmd := &cobra.Command{
		Use:   "template",
		Short: "Preview and apply built-in personal service templates",
		Long: strings.TrimSpace(`Preview and apply built-in personal service templates.

Templates only write TSLink registry entries. They do not install, start,
probe, or manage third-party applications.

Built-ins intentionally use the uniform tag tag:tslink for private template
services; change tags after apply if your tailnet policy uses another tag.`),
		Args: cobra.NoArgs,
		RunE: runCommandGroup,
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List built-in templates",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			result := listTemplatesResult()
			if jsonOutput(cmd) {
				output.Success("template list", result)
				return nil
			}
			renderTemplateList(cmd.OutOrStdout(), result)
			return nil
		},
	}

	showCmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a built-in template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := showTemplateResult(args[0])
			if err != nil {
				return err
			}
			if jsonOutput(cmd) {
				output.Success("template show", result)
				return nil
			}
			renderTemplateShow(cmd.OutOrStdout(), result)
			return nil
		},
	}

	applyCmd := &cobra.Command{
		Use:   "apply <name>",
		Short: "Preview or apply a built-in template",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRunFlag, _ := cmd.Flags().GetBool("dry-run")
			yes, _ := cmd.Flags().GetBool("yes")
			dryRun := dryRunFlag || !yes

			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			if !dryRun {
				// Validate the template before installing a background service.
				if _, err := applyTemplate(args[0], regPath, true); err != nil {
					return err
				}
				if err := ensureDirFn(); err != nil {
					return err
				}
			}

			result, err := applyTemplate(args[0], regPath, dryRun)
			if err != nil {
				return err
			}
			if !dryRun {
				noInstall, _ := cmd.Flags().GetBool("no-daemon-install")
				if err := ensureDaemonFn(cmd.Context(), cmd.ErrOrStderr(), noInstall); err != nil {
					return daemonConfigurationSavedError(err)
				}
			}
			if jsonOutput(cmd) {
				output.Success("template apply", result)
				return nil
			}
			renderTemplateApply(cmd.OutOrStdout(), result)
			return nil
		},
	}
	applyCmd.Flags().Bool("dry-run", false, "Preview the template plan without writing the registry")
	applyCmd.Flags().Bool("yes", false, "Write missing template services to the registry")
	applyCmd.Flags().Bool("no-daemon-install", false, "Apply configuration only; do not install or start the background service")

	templateCmd.AddCommand(listCmd, showCmd, applyCmd)
	rootCmd.AddCommand(templateCmd)
}
