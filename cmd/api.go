package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

// APIRequest is a single JSON command read from stdin.
type APIRequest struct {
	Action string `json:"action"`
	// add fields
	Name   string `json:"name,omitempty"`
	Type   string `json:"type,omitempty"`
	Target string `json:"target,omitempty"`
	Path   string `json:"path,omitempty"`
	Port   int    `json:"port,omitempty"`
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

// apiHandler holds paths so the logic is unit-testable without touching real config.
type apiHandler struct {
	regPath string
	pidPath string
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
	if err := registry.ValidateName(req.Name); err != nil {
		writeResponse(out, APIResponse{OK: false, Error: err.Error()})
		return
	}

	switch req.Type {
	case registry.TypeProxy:
		target := req.Target
		if target == "" {
			writeResponse(out, APIResponse{OK: false, Error: "target is required for proxy type"})
			return
		}
		if !hasScheme(target) {
			target = "http://" + target
		}
		if _, err := registry.Add(h.regPath, registry.Service{
			Name:   req.Name,
			Type:   registry.TypeProxy,
			Target: target,
		}); err != nil {
			writeResponse(out, APIResponse{OK: false, Error: err.Error()})
			return
		}
		writeResponse(out, APIResponse{
			OK:      true,
			Message: "service added",
			URL:     fmt.Sprintf("https://%s.<tailnet>.ts.net", req.Name),
		})

	case registry.TypeFile:
		dirPath := req.Path
		if dirPath == "" {
			writeResponse(out, APIResponse{OK: false, Error: "path is required for file type"})
			return
		}
		absPath, err := filepath.Abs(dirPath)
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
		if _, err := registry.Add(h.regPath, registry.Service{
			Name: req.Name,
			Type: registry.TypeFile,
			Path: absPath,
		}); err != nil {
			writeResponse(out, APIResponse{OK: false, Error: err.Error()})
			return
		}
		writeResponse(out, APIResponse{
			OK:      true,
			Message: "service added",
			URL:     fmt.Sprintf("https://%s.<tailnet>.ts.net", req.Name),
		})

	case registry.TypeTCP:
		target := req.Target
		if target == "" {
			writeResponse(out, APIResponse{OK: false, Error: "target is required for tcp type"})
			return
		}
		host, portStr, err := net.SplitHostPort(target)
		if err != nil {
			writeResponse(out, APIResponse{OK: false, Error: fmt.Sprintf("tcp requires host:port format: %v", err)})
			return
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			writeResponse(out, APIResponse{OK: false, Error: fmt.Sprintf("invalid port: %s", portStr)})
			return
		}
		joinedTarget := net.JoinHostPort(host, portStr)
		if _, err := registry.Add(h.regPath, registry.Service{
			Name:   req.Name,
			Type:   registry.TypeTCP,
			Target: joinedTarget,
			Port:   port,
		}); err != nil {
			writeResponse(out, APIResponse{OK: false, Error: err.Error()})
			return
		}
		writeResponse(out, APIResponse{
			OK:      true,
			Message: "service added",
			URL:     fmt.Sprintf("https://%s.<tailnet>.ts.net", req.Name),
		})

	default:
		msg := "type must be one of: proxy, file, tcp"
		if req.Type == "" {
			msg = "type is required"
		}
		writeResponse(out, APIResponse{OK: false, Error: msg})
	}
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
  {"action":"add","name":"myapp","type":"proxy","target":"localhost:3000"}
  {"action":"add","name":"docs","type":"file","path":"/path/to/dir"}
  {"action":"add","name":"mydb","type":"tcp","target":"localhost:5432"}
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
				var req APIRequest
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					writeResponse(out, APIResponse{OK: false, Error: fmt.Sprintf("invalid JSON: %v", err)})
					continue
				}
				h.handle(req, out)
			}

			return scanner.Err()
		},
	}

	rootCmd.AddCommand(apiCmd)
}
