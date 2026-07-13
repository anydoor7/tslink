package cmd

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/config"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const publicAckRequiredError = "funnel requires explicit public acknowledgement (--public on CLI, public_ack:true in API)"

// AddResult is the JSON data for the add command.
type AddResult struct {
	Name     string               `json:"name"`
	Type     string               `json:"type"`
	Created  bool                 `json:"created"`
	URL      string               `json:"url"`
	Endpoint inspect.EndpointView `json:"endpoint"`
	Exposure inspect.ExposureView `json:"exposure"`
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
	Name       string
	Proxy      string
	Dir        string
	TCP        string
	Ephemeral  bool
	Tags       string
	Allow      string
	Funnel     bool
	Public     bool
	Domain     string
	AcmeEmail  string
	ControlURL string
}

// buildService validates parameters and constructs a registry.Service.
// For Dir type, it returns the service with Type set but Path empty —
// the caller must resolve and validate the filesystem path.
func buildService(p AddParams) (registry.Service, error) {
	if err := registry.ValidateName(p.Name); err != nil {
		return registry.Service{}, err
	}

	var allowedUsers []string
	if p.Allow != "" {
		for _, a := range strings.Split(p.Allow, ",") {
			a = strings.TrimSpace(a)
			if a != "" {
				if !strings.HasPrefix(a, "tag:") {
					a = strings.ToLower(a)
				}
				allowedUsers = append(allowedUsers, a)
			}
			if strings.HasPrefix(a, "tag:") {
				if err := registry.ValidateTag(a); err != nil {
					return registry.Service{}, err
				}
			}
		}
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
		return registry.Service{}, fmt.Errorf("exactly one of --proxy, --dir, or --tcp must be provided")
	}

	if p.Funnel && svcType != registry.TypeProxy {
		return registry.Service{}, fmt.Errorf("--funnel can only be used with --proxy")
	}
	if p.Public && !p.Funnel {
		return registry.Service{}, fmt.Errorf("--public can only be used with --funnel")
	}
	if err := registry.ValidateFunnelGuardrails(svcType, p.Funnel, allowedUsers, p.ControlURL, p.Public); err != nil {
		return registry.Service{}, err
	}
	if p.Funnel && !p.Public {
		return registry.Service{}, errors.New(publicAckRequiredError)
	}
	if p.Domain != "" || p.AcmeEmail != "" {
		return registry.Service{}, registry.FeatureUnavailableError("custom-domain/ACME runtime is not wired; --domain and --acme-email are unavailable")
	}
	if err := registry.ValidateControlURL(p.ControlURL); err != nil {
		return registry.Service{}, err
	}
	if p.TCP != "" && len(allowedUsers) > 0 {
		return registry.Service{}, fmt.Errorf("--allow is not supported for --tcp services")
	}

	var tags []string
	if p.Tags != "" {
		var err error
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
			return registry.Service{}, err
		}
		host, portStr, err := net.SplitHostPort(p.TCP)
		if err != nil {
			return registry.Service{}, fmt.Errorf("--tcp requires host:port format: %w", err)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return registry.Service{}, fmt.Errorf("invalid port: %s", portStr)
		}
		return registry.Service{
			Name: p.Name, Type: registry.TypeTCP,
			Target: net.JoinHostPort(host, portStr), Port: port,
			Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
			ControlURL: p.ControlURL,
		}, nil
	}

	if p.Proxy != "" {
		target := p.Proxy
		if !hasScheme(target) {
			target = "http://" + target
		}
		return registry.Service{
			Name: p.Name, Type: registry.TypeProxy, Target: target,
			Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
			Funnel: p.Funnel, PublicAck: p.Public,
			ControlURL: p.ControlURL,
		}, nil
	}

	// Dir mode — path validation is done in RunE (needs filesystem)
	return registry.Service{
		Name: p.Name, Type: registry.TypeFile,
		Ephemeral: p.Ephemeral, Tags: tags, AllowedUsers: allowedUsers,
		ControlURL: p.ControlURL,
	}, nil
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
			public, _ := cmd.Flags().GetBool("public")
			domainName, _ := cmd.Flags().GetString("domain")
			acmeEmail, _ := cmd.Flags().GetString("acme-email")
			controlURL, _ := cmd.Flags().GetString("control-url")

			svc, err := buildService(AddParams{
				Name:       args[0],
				Proxy:      proxyTarget,
				Dir:        dirPath,
				TCP:        tcpTarget,
				Ephemeral:  ephemeral,
				Tags:       tagsStr,
				Allow:      allowStr,
				Funnel:     funnel,
				Public:     public,
				Domain:     domainName,
				AcmeEmail:  acmeEmail,
				ControlURL: controlURL,
			})
			if err != nil {
				return err
			}

			if invalid := invalidAllowEntries(svc.AllowedUsers); len(invalid) > 0 && !jsonOutput(cmd) {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: --allow entry %q is neither a valid email address nor tag:<name>; it will likely deny rather than allow access.\n", invalid[0])
			}

			// For dir type, resolve and validate filesystem path
			if svc.Type == registry.TypeFile {
				if !filepath.IsAbs(dirPath) {
					return fmt.Errorf("file service path %q must be absolute", dirPath)
				}
				if err := registry.ValidateFileRoot(dirPath); err != nil {
					return err
				}
				svc.Path = filepath.Clean(dirPath)
			}

			if err := ensureDirFn(); err != nil {
				return err
			}

			regPath, err := registryPathFn()
			if err != nil {
				return err
			}

			created, err := registry.Add(regPath, svc)
			if err != nil {
				return err
			}

			view := inspect.ServiceViewFor(svc)
			endpoint := view.Endpoint
			url := endpoint.Display

			if jsonOutput(cmd) {
				output.Success("add", AddResult{
					Name:     svc.Name,
					Type:     svc.Type,
					Created:  created,
					URL:      url,
					Endpoint: endpoint,
					Exposure: view.Exposure,
				})
				return nil
			}

			if svc.Type == registry.TypeTCP {
				fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ TCP service %q registered (target %s, port %d)\n", svc.Name, svc.Target, svc.Port)
				fmt.Fprintln(cmd.OutOrStdout(), "TSLink HTTP allow and identity headers do not apply to raw TCP; protection is Tailscale policy plus backend auth.")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "→ ✓ Service %q registered\n", svc.Name)
				if svc.Funnel {
					fmt.Fprintf(cmd.OutOrStdout(), "URL: %s (PUBLIC via Tailscale Funnel, available after tslink serve)\n", url)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "URL: %s (available after tslink serve)\n", url)
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
	addCmd.Flags().Bool("public", false, "Acknowledge public internet exposure for --funnel (only valid with --funnel)")
	addCmd.Flags().String("domain", "", "Custom domain name for the service (proxy only, e.g., app.example.com)")
	addCmd.Flags().String("allow", "", "Comma-separated allowed identities (e.g., user@example.com,tag:admin)")
	addCmd.Flags().String("acme-email", "", "Email for Let's Encrypt ACME certificates (requires --domain)")
	addCmd.Flags().String("control-url", "", "Per-service custom control server URL (e.g., Headscale)")
	rootCmd.AddCommand(addCmd)
}
