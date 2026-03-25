package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

// absFunc resolves a path to an absolute form. Defaults to filepath.Abs;
// overridden in tests to exercise the error branch on platforms (e.g. macOS)
// where filepath.Abs never fails.
var absFunc = filepath.Abs

// Handler serves the admin REST API and embedded dashboard.
type Handler struct {
	regPath   string
	pidPath   string
	startTime time.Time
	mux       *http.ServeMux
}

// APIResponse is the standard JSON envelope returned by all API endpoints.
type APIResponse struct {
	OK      bool        `json:"ok"`
	Data    any `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
}

// New creates a Handler wired to the given registry and PID file paths.
func New(regPath, pidPath string) *Handler {
	h := &Handler{
		regPath:   regPath,
		pidPath:   pidPath,
		startTime: time.Now().UTC(),
	}
	h.mux = http.NewServeMux()
	h.registerRoutes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("GET /api/services", h.handleListServices)
	h.mux.HandleFunc("POST /api/services", h.handleAddService)
	h.mux.HandleFunc("DELETE /api/services/{name}", h.handleRemoveService)
	h.mux.HandleFunc("GET /api/status", h.handleStatus)
	h.mux.HandleFunc("GET /", h.handleDashboard)
}

// writeJSON writes an APIResponse as JSON with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, resp APIResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleListServices handles GET /api/services.
func (h *Handler) handleListServices(w http.ResponseWriter, r *http.Request) {
	reg, err := registry.Load(h.regPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{OK: true, Data: reg.Services})
}

// addRequest is the JSON body for POST /api/services.
type addRequest struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

// handleAddService handles POST /api/services.
func (h *Handler) handleAddService(w http.ResponseWriter, r *http.Request) {
	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	if err := registry.ValidateName(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: err.Error()})
		return
	}

	svc := registry.Service{
		Name: req.Name,
		Type: req.Type,
	}

	switch req.Type {
	case registry.TypeProxy:
		target := req.Target
		if target == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "target is required for proxy service"})
			return
		}
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			target = "http://" + target
		}
		svc.Target = target

	case registry.TypeFile:
		path := req.Target
		if path == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "target (directory path) is required for file service"})
			return
		}
		abs, err := absFunc(path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "cannot resolve path: " + err.Error()})
			return
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: fmt.Sprintf("directory does not exist: %s", abs)})
			return
		}
		svc.Path = abs

	case registry.TypeTCP:
		target := req.Target
		if target == "" {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "target (host:port) is required for tcp service"})
			return
		}
		parts := strings.SplitN(target, ":", 2)
		if len(parts) != 2 {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "tcp target must be in host:port format"})
			return
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 {
			writeJSON(w, http.StatusBadRequest, APIResponse{Error: "invalid port in tcp target"})
			return
		}
		svc.Target = target
		svc.Port = port

	default:
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: fmt.Sprintf("unknown service type: %q", req.Type)})
		return
	}

	if _, err := registry.Add(h.regPath, svc); err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, APIResponse{OK: true, Message: "service added", Data: svc})
}

// handleRemoveService handles DELETE /api/services/{name}.
func (h *Handler) handleRemoveService(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, APIResponse{Error: "service name is required"})
		return
	}

	removed, err := registry.Remove(h.regPath, name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Error: err.Error()})
		return
	}

	if removed {
		writeJSON(w, http.StatusOK, APIResponse{OK: true, Message: "service removed"})
	} else {
		writeJSON(w, http.StatusOK, APIResponse{OK: true, Message: "service not registered, nothing to remove"})
	}
}

// statusData is the payload returned by GET /api/status.
type statusData struct {
	Running      bool   `json:"running"`
	ServiceCount int    `json:"service_count"`
	Uptime       string `json:"uptime,omitempty"`
}

// handleStatus handles GET /api/status.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	reg, err := registry.Load(h.regPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, APIResponse{Error: err.Error()})
		return
	}

	running := false
	if h.pidPath != "" {
		if _, err := os.Stat(h.pidPath); err == nil {
			running = true
		}
	}

	uptime := time.Since(h.startTime).Round(time.Second).String()

	writeJSON(w, http.StatusOK, APIResponse{
		OK: true,
		Data: statusData{
			Running:      running,
			ServiceCount: len(reg.Services),
			Uptime:       uptime,
		},
	})
}

// handleDashboard serves the embedded HTML dashboard for GET /.
func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(dashboardHTML))
}
