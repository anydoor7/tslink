package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monody0007/tslink/internal/output"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/spf13/cobra"
)

const apiMaxRecordBytes = 1024 * 1024

const (
	apiActionManifest      = "manifest"
	apiActionList          = "list"
	apiActionAdd           = "add"
	apiActionRemove        = "remove"
	apiActionStatus        = "status"
	apiActionDoctor        = "doctor"
	apiActionAccessExplain = "access_explain"
	apiActionTemplateList  = "template_list"
	apiActionTemplatePlan  = "template_plan"
	apiActionTemplateApply = "template_apply"
	apiActionInviteUser    = "invite_user"
	apiActionInviteDevice  = "invite_device"
	apiActionInviteList    = "invite_list"
	apiActionInviteRevoke  = "invite_revoke"
	apiActionInviteResend  = "invite_resend"
)

var apiActions = []string{
	apiActionManifest,
	apiActionList,
	apiActionAdd,
	apiActionRemove,
	apiActionStatus,
	apiActionDoctor,
	apiActionAccessExplain,
	apiActionTemplateList,
	apiActionTemplatePlan,
	apiActionTemplateApply,
	apiActionInviteUser,
	apiActionInviteDevice,
	apiActionInviteList,
	apiActionInviteRevoke,
	apiActionInviteResend,
}

func apiActionNames() []string {
	return append([]string(nil), apiActions...)
}

// APIRequest is a single JSON command read from stdin.
type APIRequest struct {
	Action string `json:"action"`
	// add fields
	Name            string   `json:"name,omitempty"`
	Type            string   `json:"type,omitempty"`
	Target          string   `json:"target,omitempty"`
	Path            string   `json:"path,omitempty"`
	Port            int      `json:"port,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Allow           []string `json:"allow,omitempty"`
	Ephemeral       bool     `json:"ephemeral,omitempty"`
	Funnel          bool     `json:"funnel,omitempty"`
	FunnelTTL       string   `json:"funnel_ttl,omitempty"`
	PublicAck       bool     `json:"public_ack,omitempty"`
	NoAutoProvision bool     `json:"no_auto_provision,omitempty"`
	ControlURL      string   `json:"control_url,omitempty"`
	URLs            bool     `json:"urls,omitempty"`
	ProbeExternal   bool     `json:"probe_external,omitempty"`
	IfMissing       bool     `json:"if_missing,omitempty"`
	// invite fields
	Email         string `json:"email,omitempty"`
	Role          string `json:"role,omitempty"`
	Service       string `json:"service,omitempty"`
	Kind          string `json:"kind,omitempty"`
	InviteID      string `json:"invite_id,omitempty"`
	PrintLink     bool   `json:"print_link,omitempty"`
	ShowURLs      bool   `json:"show_urls,omitempty"`
	MultiUse      bool   `json:"multi_use,omitempty"`
	AllowExitNode bool   `json:"allow_exit_node,omitempty"`
}

// These compatibility shims are still instantiated by the standalone
// doctor/access command implementations. Custom marshaling keeps their wire
// data flat without changing files owned by another parallel lane.
type apiDoctorData struct {
	Doctor DoctorResult
}

func (d apiDoctorData) MarshalJSON() ([]byte, error) { return json.Marshal(d.Doctor) }

type apiAccessExplainData struct {
	AccessExplain AccessExplainResult
}

func (d apiAccessExplainData) MarshalJSON() ([]byte, error) { return json.Marshal(d.AccessExplain) }

func writeAPISuccess(out io.Writer, command string, data any) output.Result {
	result := output.NewSuccess(command, data)
	output.WriteJSON(out, result)
	return result
}

func writeAPIError(out io.Writer, command string, err error) output.Result {
	result := output.NewFailureForError(command, err)
	output.WriteJSON(out, result)
	return result
}

func writeAPIUsageError(out io.Writer, msg string) output.Result {
	return writeAPIError(out, "api", output.ErrUsage(msg))
}

type apiUnknownActionErrorData struct {
	ValidActions []string `json:"valid_actions"`
}

func writeAPIUnknownActionError(out io.Writer, action string) output.Result {
	result := output.NewFailureForError("api", output.ErrUsage(fmt.Sprintf("unknown action: %s", action)))
	result.Error.Data = apiUnknownActionErrorData{ValidActions: apiActionNames()}
	output.WriteJSON(out, result)
	return result
}

func writeAPINotFoundError(out io.Writer, msg string) output.Result {
	return writeAPIError(out, "api", output.ErrNotFound(msg))
}

func writeAPICommandError(out io.Writer, command string, err error) output.Result {
	if _, ok := registry.ErrorCode(err); ok {
		return writeAPIError(out, command, err)
	}
	return writeAPIError(out, command, output.ErrUsage(err.Error()))
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
	authHandoffPath     string
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
	case apiActionManifest:
		return writeAPISuccess(out, apiActionManifest, Manifest())
	case apiActionList:
		return h.handleList(out)
	case apiActionAdd:
		return h.handleAdd(req, out)
	case apiActionRemove:
		return h.handleRemove(req, out)
	case apiActionStatus:
		return h.handleStatus(req, out)
	case apiActionDoctor:
		return h.handleDoctor(req, out)
	case apiActionAccessExplain:
		return h.handleAccessExplain(req, out)
	case apiActionTemplateList:
		return h.handleTemplateList(out)
	case apiActionTemplatePlan:
		return h.handleTemplatePlan(req, out)
	case apiActionTemplateApply:
		return h.handleTemplateApply(req, out)
	case apiActionInviteUser:
		return h.handleInviteUser(req, out)
	case apiActionInviteDevice:
		return h.handleInviteDevice(req, out)
	case apiActionInviteList:
		return h.handleInviteList(req, out)
	case apiActionInviteRevoke:
		return h.handleInviteRevoke(req, out)
	case apiActionInviteResend:
		return h.handleInviteResend(req, out)
	default:
		return writeAPIUnknownActionError(out, req.Action)
	}
}

func (h *apiHandler) handleList(out io.Writer) output.Result {
	result, err := loadListResultForPaths(h.regPath, h.pidPath, h.runtimeSnapshotPath, listOptions{})
	if err != nil {
		return writeAPIError(out, apiActionList, err)
	}
	return writeAPISuccess(out, apiActionList, result)
}

func (h *apiHandler) handleAdd(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	params, err := addParamsFromAPIRequest(req)
	if err != nil {
		return writeAPICommandError(out, apiActionAdd, err)
	}

	svc, err := buildService(params)
	if err != nil {
		return writeAPICommandError(out, apiActionAdd, err)
	}
	if svc.Type == registry.TypeFile {
		if !filepath.IsAbs(params.Dir) {
			return writeAPICommandError(out, apiActionAdd, registry.PathMustBeAbsoluteError(params.Dir))
		}
		if err := registry.ValidateFileRoot(params.Dir); err != nil {
			return writeAPICommandError(out, apiActionAdd, err)
		}
		svc.Path = filepath.Clean(params.Dir)
	}
	var created bool
	if req.IfMissing {
		created, err = registry.AddIfMissing(h.regPath, svc)
	} else {
		created, err = registry.AddWithOptions(h.regPath, svc, registry.AddOptions{
			PreserveFunnelExpiry: req.FunnelTTL == "",
		})
	}
	if err != nil {
		return writeAPICommandError(out, apiActionAdd, err)
	}
	if req.IfMissing && !created {
		reg, loadErr := registry.Load(h.regPath)
		if loadErr != nil {
			return writeAPIError(out, apiActionAdd, loadErr)
		}
		for _, existing := range reg.Services {
			if existing.Name == req.Name {
				svc = existing
				break
			}
		}
	}
	result, err := buildAddResult(context.Background(), svc, created, h.pidPath, h.regPath, h.runtimeSnapshotPath, 0)
	if err != nil {
		return writeAPIError(out, apiActionAdd, err)
	}
	return writeAPISuccess(out, apiActionAdd, result)
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
	if req.NoAutoProvision && !req.Funnel {
		return AddParams{}, fmt.Errorf("no_auto_provision is supported only when funnel is true")
	}
	if req.FunnelTTL != "" && !req.Funnel {
		return AddParams{}, fmt.Errorf("funnel_ttl is supported only when funnel is true")
	}
	if len(req.Allow) > 0 && req.Type == registry.TypeTCP {
		return AddParams{}, registry.AllowUnsupportedTCPError()
	}

	params := AddParams{
		Name:            req.Name,
		Ephemeral:       req.Ephemeral,
		Tags:            strings.Join(req.Tags, ","),
		Allow:           strings.Join(req.Allow, ","),
		Funnel:          req.Funnel,
		FunnelTTL:       req.FunnelTTL,
		FunnelTTLSet:    req.FunnelTTL != "",
		Public:          req.PublicAck,
		NoAutoProvision: req.NoAutoProvision,
		ControlURL:      req.ControlURL,
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
		return AddParams{}, registry.ServiceTypeAmbiguousError()
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
		return writeAPIError(out, apiActionRemove, err)
	}
	return writeAPISuccess(out, apiActionRemove, RemoveResult{Name: req.Name, Removed: removed})
}

func (h *apiHandler) handleStatus(req APIRequest, out io.Writer) output.Result {
	if req.URLs {
		return h.handleStatusURLs(req, out)
	}
	if req.Name != "" {
		return writeAPICommandError(out, apiActionStatus, output.ErrUsage("name is supported only when urls is true"))
	}
	snapshotPath, authHandoffPath, err := h.statusPaths()
	if err != nil {
		return writeAPIError(out, apiActionStatus, err)
	}
	result, err := getPollableStatus(h.pidPath, h.regPath, snapshotPath, authHandoffPath)
	if err != nil {
		return writeAPIError(out, apiActionStatus, err)
	}
	return writeAPISuccess(out, apiActionStatus, result)
}

func (h *apiHandler) handleStatusURLs(req APIRequest, out io.Writer) output.Result {
	snapshotPath, authHandoffPath, err := h.statusPaths()
	if err != nil {
		return writeAPIError(out, apiActionStatus, err)
	}
	result, err := getStatusURLsWithAuth(h.pidPath, h.regPath, snapshotPath, authHandoffPath)
	if err != nil {
		return writeAPIError(out, apiActionStatus, err)
	}
	if req.Name != "" {
		result, err = filterStatusURLsResult(result, req.Name)
		if err != nil {
			return writeAPIError(out, apiActionStatus, err)
		}
	}
	return writeAPISuccess(out, apiActionStatus, result)
}

func (h *apiHandler) statusPaths() (snapshotPath, authHandoffPath string, err error) {
	snapshotPath = h.runtimeSnapshotPath
	if snapshotPath == "" {
		snapshotPath, err = statusRuntimeSnapshotPathFn()
		if err != nil {
			return "", "", err
		}
	}
	authHandoffPath = h.authHandoffPath
	if authHandoffPath == "" {
		authHandoffPath, err = statusAuthHandoffPathFn()
		if err != nil {
			return "", "", err
		}
	}
	return snapshotPath, authHandoffPath, nil
}

func (h *apiHandler) handleDoctor(req APIRequest, out io.Writer) output.Result {
	result := buildDoctorResult(doctorOptions{
		ProbeExternal:       req.ProbeExternal,
		RegistryPath:        h.regPath,
		PIDPath:             h.pidPath,
		RuntimeSnapshotPath: h.runtimeSnapshotPath,
		AuthHandoffPath:     h.authHandoffPath,
	})
	return writeAPISuccess(out, apiActionDoctor, result)
}

func (h *apiHandler) handleAccessExplain(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	reg, err := registry.Load(h.regPath)
	if err != nil {
		return writeAPIError(out, apiActionAccessExplain, err)
	}
	for _, svc := range reg.Services {
		if svc.Name != req.Name {
			continue
		}
		result := buildAccessExplainResult(svc)
		return writeAPISuccess(out, apiActionAccessExplain, result)
	}
	return writeAPINotFoundError(out, fmt.Sprintf("service not found: %s", req.Name))
}

func (h *apiHandler) handleTemplateList(out io.Writer) output.Result {
	result := listTemplatesResult()
	return writeAPISuccess(out, apiActionTemplateList, result)
}

func (h *apiHandler) handleTemplatePlan(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	result, err := applyTemplate(req.Name, h.regPath, true)
	if err != nil {
		return writeAPICommandError(out, apiActionTemplatePlan, err)
	}
	return writeAPISuccess(out, apiActionTemplatePlan, result)
}

func (h *apiHandler) handleTemplateApply(req APIRequest, out io.Writer) output.Result {
	if req.Name == "" {
		return writeAPIUsageError(out, "name is required")
	}
	result, err := applyTemplate(req.Name, h.regPath, false)
	if err != nil {
		return writeAPICommandError(out, apiActionTemplateApply, err)
	}
	return writeAPISuccess(out, apiActionTemplateApply, result)
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
  {"action":"manifest"}
  {"action":"list"}
  {"action":"add","name":"myapp","type":"proxy","target":"localhost:3000","tags":["tag:tsmain"],"allow":["user@example.com"],"ephemeral":false,"funnel":false,"public_ack":false,"control_url":"https://headscale.example.com"}
  {"action":"add","name":"public-app","type":"proxy","target":"localhost:3000","funnel":true,"funnel_ttl":"7d","public_ack":true}
  {"action":"add","name":"docs","type":"file","path":"/path/to/dir","tags":["tag:docs"]}
  {"action":"add","name":"mydb","type":"tcp","target":"localhost:5432","tags":["tag:db"]}
  {"action":"remove","name":"myapp"}
  {"action":"status"}
  {"action":"status","urls":true}
  {"action":"doctor","probe_external":false}
  {"action":"access_explain","name":"myapp"}
  {"action":"template_list"}
  {"action":"template_plan","name":"personal-harness"}
  {"action":"template_apply","name":"personal-harness"}

Invite actions use the same user-owned tskey-api- credential boundary as the
invite command group:
  {"action":"invite_user","email":"alice@example.com","role":"member","print_link":false}
  {"action":"invite_device","service":"myapp","email":"alice@example.com","print_link":false,"multi_use":false,"allow_exit_node":false}
  {"action":"invite_list","show_urls":false}
  {"action":"invite_revoke","kind":"user","invite_id":"12345"}
  {"action":"invite_resend","kind":"device","invite_id":"12345"}`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			if err := ensureDirFn(); err != nil {
				result := writeAPIError(out, "api", err)
				return output.SilentExit(result.Code)
			}
			regPath, err := registryPathFn()
			if err != nil {
				result := writeAPIError(out, "api", err)
				return output.SilentExit(result.Code)
			}
			pidPath, err := pidPathFn()
			if err != nil {
				result := writeAPIError(out, "api", err)
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
