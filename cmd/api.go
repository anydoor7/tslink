package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/daemon"
	"github.com/monody0007/tslink/internal/inspect"
	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const apiMaxRecordBytes = 1024 * 1024

// APIRequest is a single JSON command read from stdin.
type APIRequest struct {
	Action string `json:"action"`
	// add fields
	Name          string   `json:"name,omitempty"`
	Type          string   `json:"type,omitempty"`
	Target        string   `json:"target,omitempty"`
	Path          string   `json:"path,omitempty"`
	Port          int      `json:"port,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Allow         []string `json:"allow,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	Funnel        bool     `json:"funnel,omitempty"`
	PublicAck     bool     `json:"public_ack,omitempty"`
	ControlURL    string   `json:"control_url,omitempty"`
	URLs          bool     `json:"urls,omitempty"`
	ProbeExternal bool     `json:"probe_external,omitempty"`
}

type apiListData struct {
	Services []inspect.ServiceView `json:"services"`
	Count    int                   `json:"count"`
}

type apiAddData struct {
	Message  string                `json:"message"`
	URL      string                `json:"url"`
	Endpoint inspect.EndpointView  `json:"endpoint"`
	Exposure inspect.ExposureView  `json:"exposure"`
	Warnings []inspect.WarningView `json:"warnings,omitempty"`
}

type apiStatusData struct {
	Running bool `json:"running"`
	Count   int  `json:"count"`
}

type apiStatusURLsData struct {
	StatusURLs StatusURLsResult `json:"status_urls"`
}

type apiDoctorData struct {
	Doctor DoctorResult `json:"doctor"`
}

type apiAccessExplainData struct {
	AccessExplain AccessExplainResult `json:"access_explain"`
}

type apiTemplateListData struct {
	TemplateList TemplateListResult `json:"template_list"`
}

type apiTemplatePlanData struct {
	TemplatePlan TemplateApplyResult `json:"template_plan"`
}

type apiTemplateApplyData struct {
	TemplateApply TemplateApplyResult `json:"template_apply"`
}

func writeAPISuccess(out io.Writer, data any) output.Result {
	result := output.NewSuccess("", data)
	output.WriteJSON(out, result)
	return result
}

func writeAPIError(out io.Writer, err error) output.Result {
	result := output.NewFailureForError("", err)
	output.WriteJSON(out, result)
	return result
}

func writeAPIUsageError(out io.Writer, msg string) output.Result {
	return writeAPIError(out, output.ErrUsage(msg))
}

func writeAPINotFoundError(out io.Writer, msg string) output.Result {
	return writeAPIError(out, output.ErrNotFound(msg))
}

func writeAPICommandError(out io.Writer, err error) output.Result {
	if _, ok := registry.ErrorCode(err); ok {
		return writeAPIError(out, err)
	}
	return writeAPIError(out, output.ErrUsage(err.Error()))
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
	regPath             string
	pidPath             string
	runtimeSnapshotPath string
}

func (h *apiHandler) handleLine(line string, out io.Writer) output.Result {
	req, err := decodeAPIRequest(line)
	if err != nil {
		return writeAPIUsageError(out, fmt.Sprintf("invalid JSON: %v", err))
	}
	return h.handle(req, out)
}

func (h *apiHandler) handle(req APIRequest, out io.Writer) output.Result {
	switch req.Action {
	case "list":
		return h.handleList(out)
	case "add":
		return h.handleAdd(req, out)
	case "remove":
		return h.handleRemove(req, out)
	case "status":
		return h.handleStatus(req, out)
	case "doctor":
		return h.handleDoctor(req, out)
	case "access_explain":
		return h.handleAccessExplain(req, out)
	case "template_list":
		return h.handleTemplateList(out)
	case "template_plan":
		return h.handleTemplatePlan(req, out)
	case "template_apply":
		return h.handleTemplateApply(req, out)
	default:
		return writeAPIUsageError(out, fmt.Sprintf("unknown action: %s", req.Action))
	}
}

func (h *apiHandler) handleList(out io.Writer) output.Result {
	reg, err := registry.Load(h.regPath)
	if err != nil {
		return writeAPIError(out, err)
	}
	return writeAPISuccess(out, apiListData{Services: inspect.ServiceViews(reg.Services), Count: len(reg.Services)})
}

func (h *apiHandler) handleAdd(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	params, err := addParamsFromAPIRequest(req)
	if err != nil {
		return writeAPICommandError(out, err)
	}

	svc, err := buildService(params)
	if err != nil {
		return writeAPICommandError(out, err)
	}
	if svc.Type == registry.TypeFile {
		if !filepath.IsAbs(params.Dir) {
			return writeAPICommandError(out, fmt.Errorf("file service path %q must be absolute", params.Dir))
		}
		if err := registry.ValidateFileRoot(params.Dir); err != nil {
			return writeAPICommandError(out, err)
		}
		svc.Path = filepath.Clean(params.Dir)
	}
	if _, err := registry.Add(h.regPath, svc); err != nil {
		return writeAPICommandError(out, err)
	}
	view := inspect.ServiceViewFor(svc)
	endpoint := view.Endpoint
	return writeAPISuccess(out, apiAddData{
		Message:  "service added",
		URL:      endpoint.Display,
		Endpoint: endpoint,
		Exposure: view.Exposure,
		Warnings: view.Warnings,
	})
}

func addParamsFromAPIRequest(req APIRequest) (AddParams, error) {
	if req.Port != 0 {
		return AddParams{}, fmt.Errorf("port is derived from target host:port and is not supported as a separate field")
	}
	if req.Funnel && req.Type != registry.TypeProxy {
		return AddParams{}, registry.FunnelTypeConflictError(req.Type)
	}
	if req.PublicAck && !req.Funnel {
		return AddParams{}, fmt.Errorf("public_ack is supported only when funnel is true")
	}
	if req.Funnel && !req.PublicAck {
		return AddParams{}, registry.FunnelPublicAckError()
	}
	if len(req.Allow) > 0 && req.Type == registry.TypeTCP {
		return AddParams{}, fmt.Errorf("allow is not supported for tcp type")
	}

	params := AddParams{
		Name:       req.Name,
		Ephemeral:  req.Ephemeral,
		Tags:       strings.Join(req.Tags, ","),
		Allow:      strings.Join(req.Allow, ","),
		Funnel:     req.Funnel,
		Public:     req.PublicAck,
		ControlURL: req.ControlURL,
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

func (h *apiHandler) handleRemove(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	removed, err := registry.Remove(h.regPath, req.Name)
	if err != nil {
		return writeAPIError(out, err)
	}
	return writeAPISuccess(out, RemoveResult{Name: req.Name, Removed: removed})
}

func (h *apiHandler) handleStatus(req APIRequest, out io.Writer) output.Result {
	if req.URLs {
		return h.handleStatusURLs(out)
	}
	running := daemon.IsRunning(h.pidPath)
	reg, err := registry.Load(h.regPath)
	if err != nil {
		return writeAPIError(out, err)
	}
	return writeAPISuccess(out, apiStatusData{Running: running, Count: len(reg.Services)})
}

func (h *apiHandler) handleStatusURLs(out io.Writer) output.Result {
	snapshotPath := h.runtimeSnapshotPath
	if snapshotPath == "" {
		var err error
		snapshotPath, err = statusRuntimeSnapshotPathFn()
		if err != nil {
			return writeAPIError(out, err)
		}
	}
	result, err := getStatusURLs(h.pidPath, h.regPath, snapshotPath)
	if err != nil {
		return writeAPIError(out, err)
	}
	return writeAPISuccess(out, apiStatusURLsData{StatusURLs: result})
}

func (h *apiHandler) handleDoctor(req APIRequest, out io.Writer) output.Result {
	result := buildDoctorResult(doctorOptions{
		ProbeExternal:       req.ProbeExternal,
		RegistryPath:        h.regPath,
		PIDPath:             h.pidPath,
		RuntimeSnapshotPath: h.runtimeSnapshotPath,
	})
	return writeAPISuccess(out, apiDoctorData{Doctor: result})
}

func (h *apiHandler) handleAccessExplain(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		return writeAPIError(out, err)
	}
	for _, svc := range reg.Services {
		if svc.Name != req.Name {
			continue
		}
		result := buildAccessExplainResult(svc)
		return writeAPISuccess(out, apiAccessExplainData{AccessExplain: result})
	}
	return writeAPINotFoundError(out, fmt.Sprintf("service not found: %s", req.Name))
}

func (h *apiHandler) handleTemplateList(out io.Writer) output.Result {
	result := listTemplatesResult()
	return writeAPISuccess(out, apiTemplateListData{TemplateList: result})
}

func (h *apiHandler) handleTemplatePlan(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	result, err := applyTemplate(req.Name, h.regPath, true)
	if err != nil {
		return writeAPICommandError(out, err)
	}
	return writeAPISuccess(out, apiTemplatePlanData{TemplatePlan: result})
}

func (h *apiHandler) handleTemplateApply(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	result, err := applyTemplate(req.Name, h.regPath, false)
	if err != nil {
		return writeAPICommandError(out, err)
	}
	return writeAPISuccess(out, apiTemplateApplyData{TemplateApply: result})
}

func init() {
	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "JSON-over-stdin/stdout interface for programmatic service management",
		Long: `Read JSON commands from stdin (one per line) and write JSON responses to stdout.
Blank lines are ignored. Each record is limited to 1048576 bytes. Recoverable
record failures still emit one JSON response in input order; after EOF the
process exits nonzero if any response failed. Fatal scanner/framing errors emit
one failure envelope and terminate the stream.

Supported actions:
  {"action":"list"}
  {"action":"add","name":"myapp","type":"proxy","target":"localhost:3000","tags":["tag:tsmain"],"allow":["user@example.com"],"ephemeral":false,"funnel":false,"public_ack":false,"control_url":"https://headscale.example.com"}
  {"action":"add","name":"docs","type":"file","path":"/path/to/dir","tags":["tag:docs"]}
  {"action":"add","name":"mydb","type":"tcp","target":"localhost:5432","tags":["tag:db"]}
  {"action":"remove","name":"myapp"}
  {"action":"status"}
  {"action":"status","urls":true}
  {"action":"doctor","probe_external":false}
  {"action":"access_explain","name":"myapp"}
  {"action":"template_list"}
  {"action":"template_plan","name":"personal-harness"}
  {"action":"template_apply","name":"personal-harness"}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if err := ensureDirFn(); err != nil {
				result := writeAPIError(out, err)
				return output.SilentExit(result.Code)
			}
			regPath, err := registryPathFn()
			if err != nil {
				result := writeAPIError(out, err)
				return output.SilentExit(result.Code)
			}
			pidPath, err := pidPathFn()
			if err != nil {
				result := writeAPIError(out, err)
				return output.SilentExit(result.Code)
			}

			h := &apiHandler{regPath: regPath, pidPath: pidPath}
			scanner := bufio.NewScanner(os.Stdin)
			scanner.Buffer(make([]byte, 0, 64*1024), apiMaxRecordBytes+1)

			firstFailureCode := output.ExitSuccess
			for scanner.Scan() {
				rawLine := scanner.Text()
				if len(rawLine) > apiMaxRecordBytes {
					result := output.NewFailure("", output.ExitUsage, fmt.Sprintf("record exceeds maximum size of %d bytes", apiMaxRecordBytes))
					output.WriteJSON(out, result)
					if firstFailureCode == output.ExitSuccess {
						firstFailureCode = result.Code
					}
					continue
				}
				line := strings.TrimSpace(rawLine)
				if line == "" {
					continue
				}
				result := h.handleLine(line, out)
				if !result.OK && firstFailureCode == output.ExitSuccess {
					firstFailureCode = result.Code
				}
			}

			if err := scanner.Err(); err != nil {
				code := output.ExitError
				msg := fmt.Sprintf("read JSONL input: %v", err)
				if errors.Is(err, bufio.ErrTooLong) {
					code = output.ExitUsage
					msg = fmt.Sprintf("record exceeds maximum size of %d bytes", apiMaxRecordBytes)
				}
				output.WriteJSON(out, output.NewFailure("", code, msg))
				return output.SilentExit(code)
			}
			if firstFailureCode != output.ExitSuccess {
				return output.SilentExit(firstFailureCode)
			}
			return nil
		},
	}

	rootCmd.AddCommand(apiCmd)
}
