package tailapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/mcpscope"
	"github.com/anydoor7/tslink/internal/registry"
	tailscale "tailscale.com/client/tailscale/v2"
)

const (
	InviteKindUser   = "user"
	InviteKindDevice = "device"

	InviteRoleMember       = "member"
	InviteRoleAdmin        = "admin"
	InviteRoleITAdmin      = "it-admin"
	InviteRoleNetworkAdmin = "network-admin"
	InviteRoleBillingAdmin = "billing-admin"
	InviteRoleAuditor      = "auditor"

	inviteResponseLimit = 4 * 1024 * 1024
)

var inviteRoles = []string{
	InviteRoleMember,
	InviteRoleAdmin,
	InviteRoleITAdmin,
	InviteRoleNetworkAdmin,
	InviteRoleBillingAdmin,
	InviteRoleAuditor,
}

// InviteRoles returns the exact role enum accepted by the archived first-party
// OpenAPI contract.
func InviteRoles() []string {
	return append([]string(nil), inviteRoles...)
}

// ValidateInviteRole rejects unsupported roles before any API client or
// network request is created.
func ValidateInviteRole(role string) error {
	for _, allowed := range inviteRoles {
		if role == allowed {
			return nil
		}
	}
	return registry.CodedError{
		Code:        registry.CodeInviteRoleInvalid,
		Message:     fmt.Sprintf("invalid invite role %q; supported roles: %s", role, strings.Join(inviteRoles, ", ")),
		Next:        []string{"tslink invite user --help"},
		MessageOnly: true,
	}
}

func validInviteID(id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
}

// ValidateInviteID accepts the bare decimal strings used by both first-party
// invite schemas. Rejecting every non-ASCII digit also keeps path separators,
// percent escapes, and dot segments away from the HTTP boundary.
func ValidateInviteID(id string) error {
	if validInviteID(id) {
		return nil
	}
	return registry.CodedError{
		Code:        registry.CodeInviteIDInvalid,
		Message:     "invite ID must match [0-9]+ (ASCII decimal digits only)",
		Next:        []string{"tslink invite list --json"},
		MessageOnly: true,
	}
}

// ValidateInviteKind requires callers to select a namespace explicitly so a
// colliding numeric ID can never make TSLink mutate the other invite kind.
func ValidateInviteKind(kind string) error {
	if kind == InviteKindUser || kind == InviteKindDevice {
		return nil
	}
	return registry.CodedError{
		Code:        registry.CodeInviteKindInvalid,
		Message:     "invite kind must be exactly user or device",
		Next:        []string{"tslink invite --help"},
		MessageOnly: true,
	}
}

// DeviceTarget combines the collision-aware hostname with the exact stable
// node ID recorded by the running TSLink node. Hostname matching finds
// candidates; NodeID is the ownership proof.
type DeviceTarget struct {
	Service  string
	Hostname string
	NodeID   string
}

// Invite is the stable TSLink representation of either first-party invite
// schema. InviteURL is copied verbatim from the API and is never constructed.
type Invite struct {
	Kind            string `json:"kind"`
	ID              string `json:"id"`
	Recipient       string `json:"recipient,omitempty"`
	Email           string `json:"email,omitempty"`
	Emailed         bool   `json:"emailed"`
	InviteURL       string `json:"invite_url,omitempty"`
	Role            string `json:"role,omitempty"`
	Service         string `json:"service,omitempty"`
	DeviceID        int64  `json:"device_id,omitempty"`
	MultiUse        bool   `json:"multi_use,omitempty"`
	AllowExitNode   bool   `json:"allow_exit_node,omitempty"`
	Accepted        bool   `json:"accepted,omitempty"`
	Created         string `json:"created,omitempty"`
	LastEmailSentAt string `json:"last_email_sent_at,omitempty"`
}

type InviteTargetError struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Next    []string `json:"next,omitempty"`
}

type InviteTargetStatus struct {
	Service     string             `json:"service"`
	Checked     bool               `json:"checked"`
	InviteCount int                `json:"invite_count"`
	Error       *InviteTargetError `json:"error,omitempty"`
}

type InviteList struct {
	Complete      bool                 `json:"complete"`
	UserInvites   []Invite             `json:"user_invites"`
	DeviceInvites []Invite             `json:"device_invites"`
	DeviceTargets []InviteTargetStatus `json:"device_targets"`
	Count         int                  `json:"count"`
}

type userInviteRequest struct {
	Role  string `json:"role"`
	Email string `json:"email,omitempty"`
}

type deviceInviteRequest struct {
	MultiUse      bool   `json:"multiUse"`
	AllowExitNode bool   `json:"allowExitNode"`
	Email         string `json:"email,omitempty"`
}

type userInviteResponse struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	Email           string `json:"email"`
	InviteURL       string `json:"inviteUrl"`
	Created         string `json:"created"`
	LastEmailSentAt string `json:"lastEmailSentAt"`
}

type deviceInviteResponse struct {
	ID              string `json:"id"`
	DeviceID        int64  `json:"deviceId"`
	Email           string `json:"email"`
	InviteURL       string `json:"inviteUrl"`
	MultiUse        bool   `json:"multiUse"`
	AllowExitNode   bool   `json:"allowExitNode"`
	Accepted        bool   `json:"accepted"`
	Created         string `json:"created"`
	LastEmailSentAt string `json:"lastEmailSentAt"`
}

type inviteDevice struct {
	ID       string   `json:"id"`
	NodeID   string   `json:"nodeId"`
	Hostname string   `json:"hostname"`
	Tags     []string `json:"tags"`
}

type inviteAPI interface {
	ListDevices(context.Context) ([]inviteDevice, error)
	CreateUserInvites(context.Context, []userInviteRequest) ([]userInviteResponse, error)
	ListUserInvites(context.Context) ([]userInviteResponse, error)
	GetUserInvite(context.Context, string) (userInviteResponse, error)
	DeleteUserInvite(context.Context, string) error
	ResendUserInvite(context.Context, string) error
	CreateDeviceInvites(context.Context, string, []deviceInviteRequest) ([]deviceInviteResponse, error)
	ListDeviceInvites(context.Context, string) ([]deviceInviteResponse, error)
	GetDeviceInvite(context.Context, string) (deviceInviteResponse, error)
	DeleteDeviceInvite(context.Context, string) error
	ResendDeviceInvite(context.Context, string) error
}

// inviteClientFn is the invite equivalent of aclClientFn: tests replace the
// complete network boundary, while production always obtains an API-key-only
// client from internal/credentials.
var inviteClientFn = newInviteClient

func newInviteClient() (inviteAPI, error) {
	client, err := newUserOwnedTailscaleClient()
	if err != nil {
		if errors.Is(err, credentials.ErrUserOwnedAPIKeyRequired) {
			return nil, registry.CodedError{
				Code:        registry.CodeInviteAPIKeyRequired,
				Message:     err.Error(),
				Next:        append(credentials.NextAPIKeyBootstrap(), "tslink invite --help"),
				MessageOnly: true,
			}
		}
		return nil, err
	}
	if rawBaseURL, set := lookupAPIClientEnvFn(APIBaseURLEnv); set && strings.TrimSpace(rawBaseURL) != "" && client.BaseURL == nil {
		return nil, fmt.Errorf("%s override produced an invite client without a BaseURL; refusing to fall back to the public Tailscale API", APIBaseURLEnv)
	}
	return newInviteHTTPClient(client), nil
}

type inviteHTTPClient struct {
	baseURL   *url.URL
	http      *http.Client
	apiKey    string
	userAgent string
}

func newInviteHTTPClient(client *tailscale.Client) *inviteHTTPClient {
	baseURL := client.BaseURL
	if baseURL == nil {
		baseURL, _ = url.Parse("https://api.tailscale.com")
	}
	httpClient := client.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: time.Minute}
	}
	userAgent := client.UserAgent
	if userAgent == "" {
		userAgent = "tslink"
	}
	return &inviteHTTPClient{baseURL: baseURL, http: httpClient, apiKey: client.APIKey, userAgent: userAgent}
}

func inviteEndpointURL(baseURL *url.URL, escapedPath string) (string, error) {
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", fmt.Errorf("decode invite endpoint path: %w", err)
	}
	endpoint := *baseURL
	endpoint.Path = decodedPath
	endpoint.RawPath = escapedPath
	return endpoint.String(), nil
}

func (c *inviteHTTPClient) endpoint(parts ...string) (string, error) {
	all := append([]string{"api", "v2"}, parts...)
	escapedPath := strings.TrimSuffix(c.baseURL.EscapedPath(), "/")
	for _, part := range all {
		escaped := url.PathEscape(part)
		// PathEscape deliberately leaves dots unescaped. Encoding them as well
		// prevents an exact "." or ".." element from becoming a path segment.
		if escaped == "." || escaped == ".." {
			escaped = strings.ReplaceAll(escaped, ".", "%2E")
		}
		escapedPath += "/" + escaped
	}
	return inviteEndpointURL(c.baseURL, escapedPath)
}

func (c *inviteHTTPClient) do(ctx context.Context, method string, parts []string, body, out any) error {
	return c.doResponse(ctx, method, parts, body, out, false)
}

// Lists are absence evidence only with the endpoint's complete-success status
// and a nonempty JSON body. Mutation responses retain their separate contract.
func (c *inviteHTTPClient) doList(ctx context.Context, parts []string, out any) error {
	return c.doResponse(ctx, http.MethodGet, parts, nil, out, true)
}

func invalidInviteListing(message string) error {
	return registry.CodedError{Code: registry.CodeInviteResponseInvalid, Message: message, MessageOnly: true}
}

func (c *inviteHTTPClient) doResponse(ctx context.Context, method string, parts []string, body, out any, list bool) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	endpoint, err := c.endpoint(parts...)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(c.apiKey, "")

	if method != http.MethodGet {
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return err
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, inviteResponseLimit+1))
	if err != nil {
		return err
	}
	if len(data) > inviteResponseLimit {
		return registry.CodedError{
			Code:        registry.CodeInviteResponseInvalid,
			Message:     fmt.Sprintf("Tailscale invite response exceeds %d bytes", inviteResponseLimit),
			Next:        []string{"Retry once; if the response remains oversized, report the Tailscale API contract mismatch"},
			MessageOnly: true,
		}
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		if list && resp.StatusCode != http.StatusOK {
			return invalidInviteListing(fmt.Sprintf("Tailscale listing returned HTTP %d; expected complete HTTP 200", resp.StatusCode))
		}
		if list && len(bytes.TrimSpace(data)) == 0 {
			return invalidInviteListing("Tailscale listing returned an empty body; absence is unproven")
		}
		if out == nil || len(bytes.TrimSpace(data)) == 0 {
			return nil
		}
		return json.Unmarshal(data, out)
	}

	apiErr := tailscale.APIError{Status: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	_ = json.Unmarshal(data, &apiErr)
	apiErr.Status = resp.StatusCode
	return apiErr
}

func (c *inviteHTTPClient) ListDevices(ctx context.Context) ([]inviteDevice, error) {
	var response struct {
		Devices []inviteDevice `json:"devices"`
	}
	err := c.doList(ctx, []string{"tailnet", "-", "devices"}, &response)
	if err == nil && response.Devices == nil {
		err = invalidInviteListing("device listing omitted a non-null devices array")
	}
	return response.Devices, err
}

func (c *inviteHTTPClient) CreateUserInvites(ctx context.Context, body []userInviteRequest) ([]userInviteResponse, error) {
	var response []userInviteResponse
	err := c.do(ctx, http.MethodPost, []string{"tailnet", "-", "user-invites"}, body, &response)
	return response, err
}

func (c *inviteHTTPClient) ListUserInvites(ctx context.Context) ([]userInviteResponse, error) {
	var response []userInviteResponse
	err := c.doList(ctx, []string{"tailnet", "-", "user-invites"}, &response)
	if err == nil && response == nil {
		err = invalidInviteListing("user invite listing omitted a non-null array")
	}
	return response, err
}

func (c *inviteHTTPClient) GetUserInvite(ctx context.Context, id string) (userInviteResponse, error) {
	var response userInviteResponse
	err := c.do(ctx, http.MethodGet, []string{"user-invites", id}, nil, &response)
	return response, err
}

func (c *inviteHTTPClient) DeleteUserInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, []string{"user-invites", id}, nil, nil)
}

func (c *inviteHTTPClient) ResendUserInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, []string{"user-invites", id, "resend"}, nil, nil)
}

func (c *inviteHTTPClient) CreateDeviceInvites(ctx context.Context, deviceID string, body []deviceInviteRequest) ([]deviceInviteResponse, error) {
	var response []deviceInviteResponse
	err := c.do(ctx, http.MethodPost, []string{"device", deviceID, "device-invites"}, body, &response)
	return response, err
}

func (c *inviteHTTPClient) ListDeviceInvites(ctx context.Context, deviceID string) ([]deviceInviteResponse, error) {
	var response []deviceInviteResponse
	err := c.doList(ctx, []string{"device", deviceID, "device-invites"}, &response)
	if err == nil && response == nil {
		err = invalidInviteListing("device invite listing omitted a non-null array")
	}
	return response, err
}

func (c *inviteHTTPClient) GetDeviceInvite(ctx context.Context, id string) (deviceInviteResponse, error) {
	var response deviceInviteResponse
	err := c.do(ctx, http.MethodGet, []string{"device-invites", id}, nil, &response)
	return response, err
}

func (c *inviteHTTPClient) DeleteDeviceInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, []string{"device-invites", id}, nil, nil)
}

func (c *inviteHTTPClient) ResendDeviceInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, []string{"device-invites", id, "resend"}, nil, nil)
}

func inviteAPIError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr tailscale.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			// 401 is the credential itself: expired, revoked, or invalid. The
			// fix is a new user-owned token, so next carries the bootstrap steps.
			return registry.CodedError{
				Code:        registry.CodeInviteAPIUnauthorized,
				Message:     fmt.Sprintf("%s was rejected by Tailscale as unauthenticated (HTTP 401); the stored tskey-api- token is expired, revoked, or invalid", operation),
				Next:        credentials.NextAPIKeyBootstrap(),
				MessageOnly: true,
			}
		case http.StatusForbidden:
			// 403 is the token's user: authenticated but not allowed. A new
			// token from the same user would fail the same way.
			return registry.CodedError{
				Code:        registry.CodeInviteAPIForbidden,
				Message:     fmt.Sprintf("%s was rejected by Tailscale as an authorization failure (HTTP 403); verify the tskey-api- token belongs to a tailnet owner/admin allowed to perform this invite operation", operation),
				Next:        []string{"Verify the stored tskey-api- token belongs to a tailnet user whose role allows this invite operation", "tslink doctor --probe-remote --json"},
				MessageOnly: true,
			}
		case http.StatusNotFound:
			return registry.CodedError{
				Code:        registry.CodeInviteNotFound,
				Message:     fmt.Sprintf("%s was not found by the Tailscale API", operation),
				Next:        []string{"tslink invite list --json"},
				MessageOnly: true,
			}
		case http.StatusConflict:
			return registry.CodedError{
				Code:        registry.CodeInviteStateConflict,
				Message:     fmt.Sprintf("%s conflicted with current Tailscale state (HTTP 409)", operation),
				Next:        []string{"Refresh the invite list and current tailnet state before retrying"},
				MessageOnly: true,
			}
		case http.StatusTooManyRequests:
			return registry.CodedError{
				Code:        registry.CodeInviteRateLimited,
				Message:     fmt.Sprintf("%s was rate limited by Tailscale (HTTP 429)", operation),
				Next:        []string{"Wait at least one minute before retrying the invite operation"},
				MessageOnly: true,
			}
		}
		if apiErr.Status >= http.StatusBadRequest && apiErr.Status < http.StatusInternalServerError {
			return registry.CodedError{
				Code:        registry.CodeInviteRequestInvalid,
				Message:     fmt.Sprintf("%s was rejected by Tailscale as an invalid request (HTTP %d)", operation, apiErr.Status),
				Next:        []string{"Review the invite kind, ID, recipient, role, and current invite state before retrying"},
				MessageOnly: true,
			}
		}
		return fmt.Errorf("%s failed with HTTP %d", operation, apiErr.Status)
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}

func validateCreatedInvite(kind string, printLink bool, count int, id, inviteURL string) error {
	if count != 1 {
		return registry.CodedError{
			Code:        registry.CodeInviteResponseInvalid,
			Message:     fmt.Sprintf("create %s invite returned %d records; expected exactly one", kind, count),
			Next:        []string{"Retry once; if the response remains invalid, report the Tailscale API contract mismatch"},
			MessageOnly: true,
		}
	}
	if !validInviteID(id) {
		return registry.CodedError{
			Code:        registry.CodeInviteResponseInvalid,
			Message:     fmt.Sprintf("create %s invite returned an invalid record; expected a numeric id", kind),
			Next:        []string{"Retry once; if the response remains invalid, report the Tailscale API contract mismatch"},
			MessageOnly: true,
		}
	}
	if inviteURL == "" && (kind == InviteKindDevice || printLink) {
		return registry.CodedError{
			Code:        registry.CodeInviteResponseInvalid,
			Message:     fmt.Sprintf("create %s invite returned an incomplete record; expected a non-empty inviteUrl for this delivery mode", kind),
			Next:        []string{"Retry once; if the response remains invalid, report the Tailscale API contract mismatch"},
			MessageOnly: true,
		}
	}
	return nil
}

func userInvite(response userInviteResponse, recipient string, emailed bool) Invite {
	return Invite{
		Kind:            InviteKindUser,
		ID:              response.ID,
		Recipient:       recipient,
		Email:           response.Email,
		Emailed:         emailed,
		InviteURL:       response.InviteURL,
		Role:            response.Role,
		Created:         response.Created,
		LastEmailSentAt: response.LastEmailSentAt,
	}
}

func deviceInvite(response deviceInviteResponse, service, recipient string, emailed bool) Invite {
	return Invite{
		Kind:            InviteKindDevice,
		ID:              response.ID,
		Recipient:       recipient,
		Email:           response.Email,
		Emailed:         emailed,
		InviteURL:       response.InviteURL,
		Service:         service,
		DeviceID:        response.DeviceID,
		MultiUse:        response.MultiUse,
		AllowExitNode:   response.AllowExitNode,
		Accepted:        response.Accepted,
		Created:         response.Created,
		LastEmailSentAt: response.LastEmailSentAt,
	}
}

func deviceLabels(devices []inviteDevice) []string {
	labels := make([]string, 0, len(devices))
	for _, device := range devices {
		labels = append(labels, fmt.Sprintf("%s (nodeId=%s, id=%s)", device.Hostname, device.NodeID, device.ID))
	}
	return labels
}

func resolveOwnedDevice(devices []inviteDevice, target DeviceTarget) (inviteDevice, error) {
	if err := registry.ValidateName(target.Hostname); err != nil {
		return inviteDevice{}, err
	}
	matches := make([]inviteDevice, 0)
	for _, device := range devices {
		if hostnameMatchesCleanupTarget(device.Hostname, target.Hostname) {
			matches = append(matches, device)
		}
	}
	if len(matches) == 0 {
		return inviteDevice{}, registry.CodedError{
			Code:        registry.CodeInviteNotFound,
			Message:     fmt.Sprintf("no tailnet device matched TSLink service %q", target.Service),
			Next:        []string{fmt.Sprintf("tslink status --urls --name %s --json", target.Service), "tslink invite list --json"},
			MessageOnly: true,
		}
	}
	exact := make([]inviteDevice, 0, 1)
	if target.NodeID != "" {
		for _, device := range matches {
			if device.NodeID == target.NodeID {
				exact = append(exact, device)
			}
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}

	labels := strings.Join(deviceLabels(matches), ", ")
	if len(matches) > 1 {
		return inviteDevice{}, registry.CodedError{
			Code:        registry.CodeInviteDeviceAmbiguous,
			Message:     fmt.Sprintf("service %q matched multiple tailnet devices and exact TSLink ownership did not select one; matched devices: %s", target.Service, labels),
			Next:        []string{fmt.Sprintf("tslink status --urls --name %s --json", target.Service), "Resolve duplicate tailnet device hostnames before retrying"},
			MessageOnly: true,
		}
	}
	return inviteDevice{}, registry.CodedError{
		Code:        registry.CodeInviteOwnershipUnproven,
		Message:     fmt.Sprintf("TSLink cannot prove ownership of the tailnet device matched for service %q; matched devices: %s", target.Service, labels),
		Next:        []string{fmt.Sprintf("tslink status --urls --name %s --json", target.Service), "Restart the TSLink daemon with the current binary so runtime.json records the exact node ID"},
		MessageOnly: true,
	}
}

func inviteTargetError(err error) *InviteTargetError {
	code := "internal_error"
	if stable, ok := registry.ErrorCode(err); ok {
		code = stable
	}
	var nextCarrier interface{ NextCommands() []string }
	var next []string
	if errors.As(err, &nextCarrier) {
		next = nextCarrier.NextCommands()
	}
	return &InviteTargetError{Code: code, Message: err.Error(), Next: next}
}

func inviteTargetsComplete(targets []InviteTargetStatus) bool {
	for _, target := range targets {
		if !target.Checked || target.Error != nil {
			return false
		}
	}
	return true
}

// CreateUserInvite creates exactly one invite. email is always the named
// recipient; printLink controls whether it is omitted from the API body.
func CreateUserInvite(ctx context.Context, email, role string, printLink bool) (Invite, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return Invite{}, registry.CodedError{Code: registry.CodeInviteRecipientInvalid, Message: "invite recipient email must not be empty", Next: []string{"tslink invite user --help"}, MessageOnly: true}
	}
	if err := ValidateInviteRole(role); err != nil {
		return Invite{}, err
	}
	client, err := inviteClientFn()
	if err != nil {
		return Invite{}, err
	}
	request := userInviteRequest{Role: role}
	if !printLink {
		request.Email = email
	}
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return Invite{}, err
	}
	responses, err := client.CreateUserInvites(ctx, []userInviteRequest{request})
	if err != nil {
		return Invite{}, inviteAPIError("create user invite", err)
	}
	inviteID, inviteURL := "", ""
	inviteIDs := make([]string, 0, len(responses))
	for _, response := range responses {
		inviteIDs = append(inviteIDs, response.ID)
	}
	if len(responses) > 0 {
		inviteID, inviteURL = responses[0].ID, responses[0].InviteURL
	}
	// A nil POST error means the outward-facing mutation may be live. Record it
	// before every response-contract rejection so no created invite is unaudited.
	slog.Warn("security.remote_invite_created", "kind", InviteKindUser, "invite_id", inviteID, "invite_ids", inviteIDs, "response_count", len(responses), "recipient", email, "emailed", !printLink)
	if err := validateCreatedInvite(InviteKindUser, printLink, len(responses), inviteID, inviteURL); err != nil {
		return Invite{}, err
	}
	result := userInvite(responses[0], email, !printLink)
	return result, nil
}

func CreateDeviceInvite(ctx context.Context, target DeviceTarget, email string, printLink, multiUse, allowExitNode bool) (Invite, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return Invite{}, registry.CodedError{Code: registry.CodeInviteRecipientInvalid, Message: "invite recipient email must not be empty", Next: []string{"tslink invite device --help"}, MessageOnly: true}
	}
	if err := registry.ValidateName(target.Hostname); err != nil {
		return Invite{}, err
	}
	client, err := inviteClientFn()
	if err != nil {
		return Invite{}, err
	}
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return Invite{}, inviteAPIError("list tailnet devices for invite ownership proof", err)
	}
	device, err := resolveOwnedDevice(devices, target)
	if err != nil {
		return Invite{}, err
	}
	request := deviceInviteRequest{MultiUse: multiUse, AllowExitNode: allowExitNode}
	if !printLink {
		request.Email = email
	}
	if err := mcpscope.CheckEffect(ctx); err != nil {
		return Invite{}, err
	}
	responses, err := client.CreateDeviceInvites(ctx, device.NodeID, []deviceInviteRequest{request})
	if err != nil {
		return Invite{}, &DeviceInviteOutcomeUnknown{Err: inviteAPIError("create device invite", err)}
	}
	inviteID, inviteURL := "", ""
	inviteIDs := make([]string, 0, len(responses))
	for _, response := range responses {
		inviteIDs = append(inviteIDs, response.ID)
	}
	if len(responses) > 0 {
		inviteID, inviteURL = responses[0].ID, responses[0].InviteURL
	}
	// Keep the audit structurally before all post-create validation failures.
	slog.Warn("security.remote_invite_created", "kind", InviteKindDevice, "invite_id", inviteID, "invite_ids", inviteIDs, "response_count", len(responses), "service", target.Service, "device_node_id", device.NodeID, "recipient", email, "emailed", !printLink)
	if err := validateCreatedInvite(InviteKindDevice, printLink, len(responses), inviteID, inviteURL); err != nil {
		return Invite{}, &DeviceInviteOutcomeUnknown{Err: err}
	}
	result := deviceInvite(responses[0], target.Service, email, !printLink)
	return result, nil
}

func ListInvites(ctx context.Context, targets []DeviceTarget) (InviteList, error) {
	client, err := inviteClientFn()
	if err != nil {
		return InviteList{}, err
	}
	userResponses, err := client.ListUserInvites(ctx)
	if err != nil {
		return InviteList{}, inviteAPIError("list user invites", err)
	}
	result := InviteList{UserInvites: []Invite{}, DeviceInvites: []Invite{}, DeviceTargets: []InviteTargetStatus{}}
	for _, response := range userResponses {
		result.UserInvites = append(result.UserInvites, userInvite(response, response.Email, response.Email != ""))
	}

	if len(targets) > 0 {
		devices, err := client.ListDevices(ctx)
		if err != nil {
			mapped := inviteAPIError("list tailnet devices for invite ownership proof", err)
			for _, target := range targets {
				result.DeviceTargets = append(result.DeviceTargets, InviteTargetStatus{Service: target.Service, Error: inviteTargetError(mapped)})
			}
			result.Count = len(result.UserInvites)
			result.Complete = false
			return result, nil
		}
		type cachedDeviceInvites struct {
			responses []deviceInviteResponse
			err       error
		}
		cache := make(map[string]cachedDeviceInvites)
		included := make(map[string]struct{})
		for _, target := range targets {
			status := InviteTargetStatus{Service: target.Service}
			device, err := resolveOwnedDevice(devices, target)
			if err != nil {
				status.Error = inviteTargetError(err)
				result.DeviceTargets = append(result.DeviceTargets, status)
				continue
			}
			cached, ok := cache[device.NodeID]
			if !ok {
				responses, listErr := client.ListDeviceInvites(ctx, device.NodeID)
				if listErr != nil {
					listErr = inviteAPIError(fmt.Sprintf("list device invites for service %q", target.Service), listErr)
				}
				cached = cachedDeviceInvites{responses: responses, err: listErr}
				cache[device.NodeID] = cached
			}
			if cached.err != nil {
				status.Error = inviteTargetError(cached.err)
				result.DeviceTargets = append(result.DeviceTargets, status)
				continue
			}
			status.Checked = true
			status.InviteCount = len(cached.responses)
			result.DeviceTargets = append(result.DeviceTargets, status)
			if _, ok := included[device.NodeID]; ok {
				continue
			}
			included[device.NodeID] = struct{}{}
			for _, response := range cached.responses {
				result.DeviceInvites = append(result.DeviceInvites, deviceInvite(response, target.Service, response.Email, response.Email != ""))
			}
		}
	}
	result.Complete = inviteTargetsComplete(result.DeviceTargets)
	result.Count = len(result.UserInvites) + len(result.DeviceInvites)
	return result, nil
}

func getInvite(ctx context.Context, client inviteAPI, kind, id string) (Invite, error) {
	switch kind {
	case InviteKindUser:
		user, err := client.GetUserInvite(ctx, id)
		if err != nil {
			return Invite{}, inviteAPIError(fmt.Sprintf("get user invite %q", id), err)
		}
		return userInvite(user, user.Email, user.Email != ""), nil
	case InviteKindDevice:
		device, err := client.GetDeviceInvite(ctx, id)
		if err != nil {
			return Invite{}, inviteAPIError(fmt.Sprintf("get device invite %q", id), err)
		}
		return deviceInvite(device, "", device.Email, device.Email != ""), nil
	default:
		return Invite{}, ValidateInviteKind(kind)
	}
}

func proveInviteDeviceOwnership(ctx context.Context, client inviteAPI, invite Invite, targets []DeviceTarget) (string, error) {
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return "", inviteAPIError("list tailnet devices for invite ownership proof", err)
	}
	legacyID := strconv.FormatInt(invite.DeviceID, 10)
	for _, device := range devices {
		if device.ID != legacyID {
			continue
		}
		for _, target := range targets {
			if target.NodeID == device.NodeID && hostnameMatchesCleanupTarget(device.Hostname, target.Hostname) {
				return target.Service, nil
			}
		}
		return "", registry.CodedError{
			Code:        registry.CodeInviteOwnershipUnproven,
			Message:     fmt.Sprintf("TSLink cannot prove ownership of device invite %q; matched devices: %s", invite.ID, strings.Join(deviceLabels([]inviteDevice{device}), ", ")),
			Next:        []string{"tslink status --urls --json", "Restart the TSLink daemon with the current binary so runtime.json records exact node IDs"},
			MessageOnly: true,
		}
	}
	return "", registry.CodedError{
		Code:        registry.CodeInviteOwnershipUnproven,
		Message:     fmt.Sprintf("TSLink cannot prove ownership of device invite %q; its device id %s was not returned by the tailnet device list", invite.ID, legacyID),
		Next:        []string{"tslink invite list --json", "tslink status --urls --json"},
		MessageOnly: true,
	}
}

func RevokeInvite(ctx context.Context, kind, id string, targets []DeviceTarget) (Invite, error) {
	if err := ValidateInviteID(id); err != nil {
		return Invite{}, err
	}
	if err := ValidateInviteKind(kind); err != nil {
		return Invite{}, err
	}
	client, err := inviteClientFn()
	if err != nil {
		return Invite{}, err
	}
	invite, err := getInvite(ctx, client, kind, id)
	if err != nil {
		return Invite{}, err
	}
	if invite.Kind == InviteKindDevice {
		invite.Service, err = proveInviteDeviceOwnership(ctx, client, invite, targets)
		if err != nil {
			return Invite{}, err
		}
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return Invite{}, err
		}
		err = client.DeleteDeviceInvite(ctx, id)
	} else {
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return Invite{}, err
		}
		err = client.DeleteUserInvite(ctx, id)
	}
	if err != nil {
		return Invite{}, inviteAPIError(fmt.Sprintf("revoke %s invite %q", invite.Kind, id), err)
	}
	slog.Warn("security.remote_invite_revoked", "kind", invite.Kind, "invite_id", id, "service", invite.Service, "recipient", invite.Email)
	return invite, nil
}

func ResendInvite(ctx context.Context, kind, id string, targets []DeviceTarget) (Invite, error) {
	if err := ValidateInviteID(id); err != nil {
		return Invite{}, err
	}
	if err := ValidateInviteKind(kind); err != nil {
		return Invite{}, err
	}
	client, err := inviteClientFn()
	if err != nil {
		return Invite{}, err
	}
	invite, err := getInvite(ctx, client, kind, id)
	if err != nil {
		return Invite{}, err
	}
	if invite.Email == "" {
		return Invite{}, registry.CodedError{
			Code:        registry.CodeInviteResendEmailMissing,
			Message:     fmt.Sprintf("invite %q was created without an email and cannot be resent; deliver its API-returned invite_url through your own channel", id),
			Next:        []string{"tslink invite list --show-urls --json"},
			MessageOnly: true,
		}
	}
	if invite.Kind == InviteKindDevice {
		invite.Service, err = proveInviteDeviceOwnership(ctx, client, invite, targets)
		if err != nil {
			return Invite{}, err
		}
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return Invite{}, err
		}
		err = client.ResendDeviceInvite(ctx, id)
	} else {
		if err := mcpscope.CheckEffect(ctx); err != nil {
			return Invite{}, err
		}
		err = client.ResendUserInvite(ctx, id)
	}
	if err != nil {
		return Invite{}, inviteAPIError(fmt.Sprintf("resend %s invite %q", invite.Kind, id), err)
	}
	slog.Warn("security.remote_invite_resent", "kind", invite.Kind, "invite_id", id, "service", invite.Service, "recipient", invite.Email, "emailed", true)
	return invite, nil
}
