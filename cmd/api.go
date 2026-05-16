package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// APIRequest is a single JSON command read from stdin.
type APIRequest struct {
	Action string `json:"action"`
	// add fields
	Name      string   `json:"name,omitempty"`
	Type      string   `json:"type,omitempty"`
	Target    string   `json:"target,omitempty"`
	Path      string   `json:"path,omitempty"`
	Port      int      `json:"port,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Allow     []string `json:"allow,omitempty"`
	Ephemeral bool     `json:"ephemeral,omitempty"`
	Funnel    bool     `json:"funnel,omitempty"`
}

// APIResponse is written back to stdout for each request.
type APIResponse struct {
	OK       bool               `json:"ok"`
	Error    string             `json:"error,omitempty"`
	Message  string             `json:"message,omitempty"`
	URL      string             `json:"url,omitempty"`
	Services []registry.Service `json:"services,omitempty"`
	Running  bool               `json:"running,omitempty"`
	Count    int                `json:"count,omitempty"`
}

func writeResponse(out io.Writer, resp APIResponse) {
	data, _ := json.Marshal(resp)
	fmt.Fprintf(out, "%s\n", data)
}

func decodeAPIRequest(line string) (APIRequest, error) {
	var req APIRequest
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return APIRequest{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return APIRequest{}, fmt.Errorf("multiple JSON values in one request")
	}
	return req, nil
}

// apiHandler holds paths so the logic is unit-testable without touching real config.
type apiHandler struct {
	regPath string
	pidPath string
}

func (h *apiHandler) handleLine(line string, out io.Writer) {
	req, err := decodeAPIRequest(line)
	if err != nil {
		writeResponse(out, APIResponse{OK: false, Error: fmt.Sprintf("invalid JSON: %v", err)})
		return
	}
	h.handle(req, out)
}

func (h *apiHandler) handle(req APIRequest, out io.Writer) {
	switch req.Action {
	case "list":
		h.handleList(out)
	case "add":
		h.handleAdd(req, out)
	case "remove":
		h.handleRemove(req, out)
	case "status":
		h.handleStatus(out)
	default:
		writeResponse(out, APIResponse{
			OK:    false,
			Error: fmt.Sprintf("unknown action: %s", req.Action),
		})
	}
}

func (h *apiHandler) handleList(out io.Writer) {
	reg, err := registry.Load(h.regPath)
	if err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeResponse(out, APIResponse{OK: true, Services: reg.Services})
}

func (h *apiHandler) handleAdd(req APIRequest, out io.Writer) {
	if req.Name == "" {
		writeResponse(out, APIResponse{OK: false, Error: "name is required"})
		return
	}
	params, err := addParamsFromAPIRequest(req)
	if err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}

	svc, err := buildService(params)
	if err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}
	if svc.Type == registry.TypeFile {
		absPath, err := filepath.Abs(params.Dir)
		if err != nil {
			writeResponse(out, APIResponse{OK: false, Error: err.Error()})
			return
		}
		info, err := os.Stat(absPath)
		if err != nil {
			writeResponse(out, APIResponse{OK: false, Error: err.Error()})
			return
		}
		if !info.IsDir() {
			writeResponse(out, APIResponse{OK: false, Error: fmt.Sprintf("not a directory: %s", absPath)})
			return
		}
		svc.Path = absPath
	}
	if _, err := registry.Add(h.regPath, svc); err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeResponse(out, APIResponse{
		OK:      true,
		Message: "service added",
		URL:     fmt.Sprintf("https://%s.<tailnet>.ts.net", req.Name),
	})
}

func addParamsFromAPIRequest(req APIRequest) (AddParams, error) {
	if req.Port != 0 {
		return AddParams{}, fmt.Errorf("port is derived from target host:port and is not supported as a separate field")
	}
	if req.Funnel && req.Type != registry.TypeProxy {
		return AddParams{}, fmt.Errorf("funnel is supported only for proxy type")
	}
	if len(req.Allow) > 0 && req.Type == registry.TypeTCP {
		return AddParams{}, fmt.Errorf("allow is not supported for tcp type")
	}

	params := AddParams{
		Name:      req.Name,
		Ephemeral: req.Ephemeral,
		Tags:      strings.Join(req.Tags, ","),
		Allow:     strings.Join(req.Allow, ","),
		Funnel:    req.Funnel,
	}

	switch req.Type {
	case registry.TypeProxy:
		if req.Target == "" {
			return AddParams{}, fmt.Errorf("target is required for proxy type")
		}
		if req.Path != "" {
			return AddParams{}, fmt.Errorf("path is not supported for proxy type")
		}
		params.Proxy = req.Target
	case registry.TypeFile:
		if req.Path == "" {
			return AddParams{}, fmt.Errorf("path is required for file type")
		}
		if req.Target != "" {
			return AddParams{}, fmt.Errorf("target is not supported for file type")
		}
		params.Dir = req.Path
	case registry.TypeTCP:
		if req.Target == "" {
			return AddParams{}, fmt.Errorf("target is required for tcp type")
		}
		if req.Path != "" {
			return AddParams{}, fmt.Errorf("path is not supported for tcp type")
		}
		params.TCP = req.Target
	case "":
		return AddParams{}, fmt.Errorf("type is required")
	default:
		return AddParams{}, fmt.Errorf("type must be one of: proxy, file, tcp")
	}

	return params, nil
}

func (h *apiHandler) handleRemove(req APIRequest, out io.Writer) {
	if req.Name == "" {
		writeResponse(out, APIResponse{OK: false, Error: "name is required"})
		return
	}
	if _, err := registry.Remove(h.regPath, req.Name); err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}
	writeResponse(out, APIResponse{OK: true, Message: "service removed"})
}

func (h *apiHandler) handleStatus(out io.Writer) {
	running := daemon.IsRunning(h.pidPath)
	count := 0
	if reg, err := registry.Load(h.regPath); err == nil {
		count = len(reg.Services)
	}
	writeResponse(out, APIResponse{OK: true, Running: running, Count: count})
}

func init() {
	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "JSON-over-stdin/stdout interface for programmatic service management",
		Long: `Read JSON commands from stdin (one per line) and write JSON responses to stdout.

Supported actions:
  {"action":"list"}
  {"action":"add","name":"myapp","type":"proxy","target":"localhost:3000","tags":["tag:tsmain"],"allow":["user@example.com"],"ephemeral":false,"funnel":false}
  {"action":"add","name":"docs","type":"file","path":"/path/to/dir","tags":["tag:docs"]}
  {"action":"add","name":"mydb","type":"tcp","target":"localhost:5432","tags":["tag:db"]}
  {"action":"remove","name":"myapp"}
  {"action":"status"}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ensureDirFn(); err != nil {
				return err
			}
			regPath, err := registryPathFn()
			if err != nil {
				return err
			}
			pidPath, err := pidPathFn()
			if err != nil {
				return err
			}

			h := &apiHandler{regPath: regPath, pidPath: pidPath}
			out := cmd.OutOrStdout()
			scanner := bufio.NewScanner(os.Stdin)

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				h.handleLine(line, out)
			}

			return scanner.Err()
		},
	}

	rootCmd.AddCommand(apiCmd)
}
