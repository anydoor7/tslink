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
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
	"github.com/monody0007/tslink/internal/testenv"
	tailscale "tailscale.com/client/tailscale/v2"
)

func withInviteServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	old := inviteClientFn
	inviteClientFn = func() (inviteAPI, error) {
		return newInviteHTTPClient(&tailscale.Client{
			BaseURL: baseURL,
			HTTP:    server.Client(),
			APIKey:  "tskey-api-placeholder",
		}), nil
	}
	t.Cleanup(func() { inviteClientFn = old })
}

func TestInvitePathUsesLoopbackAPIBaseURLOverride(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	requests := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(server.Close)
	t.Setenv(APIBaseURLEnv, server.URL)
	t.Setenv("TSLINK_API_KEY", "test-placeholder")
	oldInviteClient := inviteClientFn
	inviteClientFn = newInviteClient
	t.Cleanup(func() { inviteClientFn = oldInviteClient })

	result, err := ListInvites(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListInvites() error = %v", err)
	}
	if !result.Complete || result.Count != 0 {
		t.Fatalf("ListInvites() = %+v, want complete empty loopback response", result)
	}
	select {
	case got := <-requests:
		if want := "GET /api/v2/tailnet/-/user-invites"; got != want {
			t.Fatalf("invite request = %q, want %q", got, want)
		}
	default:
		t.Fatal("loopback invite fake received no request")
	}
}

func assertInviteRequest(t *testing.T, req *http.Request, method, path, body string) {
	t.Helper()
	if req.Method != method || req.URL.Path != path {
		t.Fatalf("request = %s %s, want %s %s", req.Method, req.URL.Path, method, path)
	}
	user, password, ok := req.BasicAuth()
	if !ok || user != "tskey-api-placeholder" || password != "" {
		t.Fatalf("BasicAuth = %q/%q ok=%v, want API-key placeholder as username and empty password", user, password, ok)
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatalf("request body = %s, want %s", data, body)
	}
}

func codedError(t *testing.T, err error, wantCode string) registry.CodedError {
	t.Helper()
	var coded registry.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("error = %v, want registry.CodedError %s", err, wantCode)
	}
	if coded.Code != wantCode {
		t.Fatalf("error code = %q, want %q (error=%v)", coded.Code, wantCode, err)
	}
	if len(coded.NextCommands()) == 0 {
		t.Fatalf("error %s has empty next[]", wantCode)
	}
	return coded
}

type listDevicesInviteAPI struct {
	inviteAPI
	devices       []inviteDevice
	err           error
	deviceInvite  deviceInviteResponse
	getDeviceCall *int
}

func (c listDevicesInviteAPI) ListDevices(context.Context) ([]inviteDevice, error) {
	return c.devices, c.err
}

func (c listDevicesInviteAPI) GetDeviceInvite(context.Context, string) (deviceInviteResponse, error) {
	if c.getDeviceCall != nil {
		*c.getDeviceCall++
	}
	return c.deviceInvite, nil
}

func TestCreateUserInvite_WireDeliveryAndVerbatimURL(t *testing.T) {
	tests := []struct {
		name        string
		role        string
		printLink   bool
		wantBody    string
		responseURL string
		wantEmailed bool
	}{
		{
			name:        "tailscale_emails_same_domain_without_url",
			role:        InviteRoleMember,
			wantBody:    `[{"role":"member","email":"alice@example.com"}]`,
			wantEmailed: true,
		},
		{
			name:        "self_delivery_omits_email",
			role:        InviteRoleAdmin,
			printLink:   true,
			wantBody:    `[{"role":"admin"}]`,
			responseURL: "https://login.tailscale.com/admin/invite/verbatim-contradictory-schema-form",
			wantEmailed: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
				assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", tc.wantBody)
				fmt.Fprintf(w, `[{"id":"10001","role":%q,"email":%q,"inviteUrl":%q}]`, tc.role, map[bool]string{true: "", false: "alice@example.com"}[tc.printLink], tc.responseURL)
			})

			result, err := CreateUserInvite(context.Background(), "alice@example.com", tc.role, tc.printLink)
			if err != nil {
				t.Fatal(err)
			}
			if result.InviteURL != tc.responseURL || result.Emailed != tc.wantEmailed || result.Recipient != "alice@example.com" {
				t.Fatalf("result = %+v, want verbatim URL, emailed=%v, named recipient", result, tc.wantEmailed)
			}
		})
	}
}

func TestCreateDeviceInvite_ExactNodeIDSelectsCollisionCandidateAndSendsExactBody(t *testing.T) {
	requestNumber := 0
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requestNumber++
		switch requestNumber {
		case 1:
			assertInviteRequest(t, req, http.MethodGet, "/api/v2/tailnet/-/devices", "")
			io.WriteString(w, `{"devices":[{"id":"100","nodeId":"n-other","hostname":"app"},{"id":"101","nodeId":"n-owned","hostname":"app-2"}]}`)
		case 2:
			assertInviteRequest(t, req, http.MethodPost, "/api/v2/device/n-owned/device-invites", `[{"multiUse":true,"allowExitNode":true,"email":"bob@example.com"}]`)
			io.WriteString(w, `[{"id":"20001","deviceId":101,"email":"bob@example.com","multiUse":true,"allowExitNode":true,"inviteUrl":"https://login.tailscale.com/admin/invite/verbatim-device"}]`)
		default:
			t.Fatalf("unexpected request %d: %s %s", requestNumber, req.Method, req.URL.Path)
		}
	})

	target := DeviceTarget{Service: "app", Hostname: "app", NodeID: "n-owned"}
	result, err := CreateDeviceInvite(context.Background(), target, "bob@example.com", false, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if requestNumber != 2 || result.Service != "app" || !result.Emailed || result.InviteURL != "https://login.tailscale.com/admin/invite/verbatim-device" {
		t.Fatalf("requests=%d result=%+v", requestNumber, result)
	}
}

func TestCreateDeviceInvite_PrintLinkIncludesExplicitFalseBooleansAndNoEmail(t *testing.T) {
	requestNumber := 0
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requestNumber++
		if requestNumber == 1 {
			assertInviteRequest(t, req, http.MethodGet, "/api/v2/tailnet/-/devices", "")
			io.WriteString(w, `{"devices":[{"id":"100","nodeId":"n-owned","hostname":"docs"}]}`)
			return
		}
		assertInviteRequest(t, req, http.MethodPost, "/api/v2/device/n-owned/device-invites", `[{"multiUse":false,"allowExitNode":false}]`)
		io.WriteString(w, `[{"id":"20002","deviceId":100,"inviteUrl":"https://login.tailscale.com/admin/invite/self-delivery"}]`)
	})

	result, err := CreateDeviceInvite(context.Background(), DeviceTarget{Service: "docs", Hostname: "docs", NodeID: "n-owned"}, "chat-recipient@example.com", true, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Emailed || result.Recipient != "chat-recipient@example.com" || result.InviteURL == "" {
		t.Fatalf("result = %+v, want explicit self-delivery", result)
	}
}

func TestCreateDeviceInvite_AmbiguousAndUnprovenFailBeforePOST(t *testing.T) {
	tests := []struct {
		name     string
		devices  string
		target   DeviceTarget
		wantCode string
		matched  []string
	}{
		{
			name:     "ambiguous",
			devices:  `{"devices":[{"id":"100","nodeId":"n-one","hostname":"app"},{"id":"101","nodeId":"n-two","hostname":"app-2"}]}`,
			target:   DeviceTarget{Service: "app", Hostname: "app"},
			wantCode: registry.CodeInviteDeviceAmbiguous,
			matched:  []string{"app (nodeId=n-one, id=100)", "app-2 (nodeId=n-two, id=101)"},
		},
		{
			name:     "one_candidate_but_no_exact_proof",
			devices:  `{"devices":[{"id":"100","nodeId":"n-one","hostname":"app"}]}`,
			target:   DeviceTarget{Service: "app", Hostname: "app"},
			wantCode: registry.CodeInviteOwnershipUnproven,
			matched:  []string{"app (nodeId=n-one, id=100)"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
				requests++
				assertInviteRequest(t, req, http.MethodGet, "/api/v2/tailnet/-/devices", "")
				io.WriteString(w, tc.devices)
			})
			_, err := CreateDeviceInvite(context.Background(), tc.target, "bob@example.com", false, false, false)
			coded := codedError(t, err, tc.wantCode)
			for _, matched := range tc.matched {
				if !strings.Contains(coded.Message, matched) {
					t.Fatalf("message = %q, want matched device %q", coded.Message, matched)
				}
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want only read-only device lookup and no POST", requests)
			}
		})
	}
}

func TestCreateUserInvite_OAuthOnlyFailsBeforeNetwork(t *testing.T) {
	setup(t)
	old := inviteClientFn
	inviteClientFn = newInviteClient
	t.Cleanup(func() { inviteClientFn = old })
	if err := credentials.SaveClientSecret("tskey-client-placeholder-placeholder"); err != nil {
		t.Fatal(err)
	}
	called := false
	withDefaultTransport(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("network must not be reached")
	}))

	_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
	coded := codedError(t, err, registry.CodeInviteAPIKeyRequired)
	if called {
		t.Fatal("network was called with OAuth-only credentials")
	}
	if !strings.Contains(coded.Message, "OAuth client secret") || !strings.Contains(coded.Message, "user-owned tskey-api- token") {
		t.Fatalf("message = %q, want actionable credential type", coded.Message)
	}
}

// TestInviteHTTP401And403MapToDistinctAuthCodes pins the split: 401 is the
// token itself (expired/revoked/invalid) and carries the key-bootstrap steps,
// 403 is the token's user permissions and must not tell the operator to mint a
// new token from the same user.
func TestInviteHTTP401And403MapToDistinctAuthCodes(t *testing.T) {
	tests := []struct {
		status   int
		wantCode string
		wantNext string
		noNext   string
	}{
		{http.StatusUnauthorized, registry.CodeInviteAPIUnauthorized, credentials.KeysPageURL, "doctor --probe-remote"},
		{http.StatusForbidden, registry.CodeInviteAPIForbidden, "role", credentials.KeysPageURL},
	}
	for _, tc := range tests {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
				assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"alice@example.com"}]`)
				w.WriteHeader(tc.status)
				io.WriteString(w, `{"message":"authentication detail must not define classification"}`)
			})
			_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
			coded := codedError(t, err, tc.wantCode)
			if !strings.Contains(coded.Message, fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("message = %q, want HTTP status", coded.Message)
			}
			joined := strings.Join(coded.Next, "\n")
			if !strings.Contains(joined, tc.wantNext) {
				t.Fatalf("next = %v, want %q", coded.Next, tc.wantNext)
			}
			if strings.Contains(joined, tc.noNext) {
				t.Fatalf("next = %v, must not contain %q", coded.Next, tc.noNext)
			}
			if strings.Contains(coded.Message, "authentication detail must not define classification") {
				t.Fatalf("message = %q, server text must not drive classification", coded.Message)
			}
		})
	}
}

func TestInviteAPIKeyRequiredNextCarriesKeysPage(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	t.Setenv("TSLINK_DISABLE_KEYRING", "1")
	old := inviteClientFn
	inviteClientFn = newInviteClient
	t.Cleanup(func() { inviteClientFn = old })
	_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
	coded := codedError(t, err, registry.CodeInviteAPIKeyRequired)
	joined := strings.Join(coded.Next, "\n")
	if !strings.Contains(joined, credentials.KeysPageURL) || !strings.Contains(joined, "tslink invite --help") {
		t.Fatalf("next = %v, want Keys page bootstrap plus invite help", coded.Next)
	}
	if !strings.Contains(coded.Message, credentials.KeysPageURL) {
		t.Fatalf("message = %q, want Keys page URL", coded.Message)
	}
}

func TestInviteEndpointEscapesEveryPathElementWithoutCleaning(t *testing.T) {
	baseURL, err := url.Parse("https://api.tailscale.com/root/")
	if err != nil {
		t.Fatal(err)
	}
	client := &inviteHTTPClient{baseURL: baseURL}
	got, err := client.endpoint("user-invites", "../device/nodeid-VICTIM")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.tailscale.com/root/api/v2/user-invites/..%2Fdevice%2Fnodeid-VICTIM"
	if got != want {
		t.Fatalf("endpoint = %q, want each supplied element escaped as %q", got, want)
	}
	got, err = client.endpoint("tailnet", "example.com", "devices")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.tailscale.com/root/api/v2/tailnet/example.com/devices"; got != want {
		t.Fatalf("dotted endpoint = %q, want legitimate dotted element unchanged as %q", got, want)
	}
	got, err = client.endpoint("user-invites", "..")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.tailscale.com/root/api/v2/user-invites/%2E%2E"; got != want {
		t.Fatalf("dot-segment endpoint = %q, want %q", got, want)
	}
	got, err = client.endpoint("user-invites", ".")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://api.tailscale.com/root/api/v2/user-invites/%2E"; got != want {
		t.Fatalf("single-dot endpoint = %q, want %q", got, want)
	}
	if got, err := inviteEndpointURL(baseURL, "/api/v2/%zz"); err == nil || got != "" {
		t.Fatalf("invalid escaped path endpoint = %q err=%v, want explicit decode failure", got, err)
	}
}

func TestInviteRequestEmitsEscapedPathAndClientDefaultsAreUsable(t *testing.T) {
	client := newInviteHTTPClient(&tailscale.Client{})
	if got := client.baseURL.String(); got != "https://api.tailscale.com" {
		t.Fatalf("default base URL = %q", got)
	}
	if client.http == nil || client.userAgent != "tslink" {
		t.Fatalf("default client = %+v, want HTTP transport and tslink user agent", client)
	}
	var escapedPath string
	client.http = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		escapedPath = req.URL.EscapedPath()
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := client.do(context.Background(), http.MethodGet, []string{"user-invites", ".."}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if escapedPath != "/api/v2/user-invites/%2E%2E" {
		t.Fatalf("request escaped path = %q, want opaque dot segment", escapedPath)
	}
}

func TestInviteMutationRejectsTraversalBeforeAnyRequest(t *testing.T) {
	requests := 0
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	})

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "revoke", run: func() error {
			_, err := RevokeInvite(context.Background(), InviteKindUser, "../device/nodeid-VICTIM", nil)
			return err
		}},
		{name: "resend", run: func() error {
			_, err := ResendInvite(context.Background(), InviteKindDevice, "../tailnet/-/keys/kVICTIM", nil)
			return err
		}},
	}
	errs := make(map[string]error, len(operations))
	for _, operation := range operations {
		errs[operation.name] = operation.run()
	}
	if requests != 0 {
		t.Fatalf("invalid traversal IDs issued %d HTTP requests, want zero", requests)
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			codedError(t, errs[operation.name], registry.CodeInviteIDInvalid)
		})
	}
}

func TestInviteIDAndKindValidationAreStrict(t *testing.T) {
	for _, id := range []string{"0", "12346", "000123"} {
		if err := ValidateInviteID(id); err != nil {
			t.Fatalf("ValidateInviteID(%q) = %v, want valid ASCII decimal ID", id, err)
		}
	}
	for _, id := range []string{"", " 12346", "12346 ", "12.346", "12/346", "..", "１２３４６", "+12346", "%31"} {
		codedError(t, ValidateInviteID(id), registry.CodeInviteIDInvalid)
	}
	for _, kind := range []string{InviteKindUser, InviteKindDevice} {
		if err := ValidateInviteKind(kind); err != nil {
			t.Fatalf("ValidateInviteKind(%q) = %v", kind, err)
		}
	}
	for _, kind := range []string{"", "USER", "both", " user"} {
		codedError(t, ValidateInviteKind(kind), registry.CodeInviteKindInvalid)
	}
}

func TestDeviceTargetDoesNotCarryUnusedCleanupTags(t *testing.T) {
	if _, ok := reflect.TypeOf(DeviceTarget{}).FieldByName("Tags"); ok {
		t.Fatal("DeviceTarget carries unused cleanup Tags instead of the invite ownership inputs")
	}
}

func TestInvalidDeviceTargetHostnameIsRejectedAtBothOwnershipBoundaries(t *testing.T) {
	target := DeviceTarget{Service: "app", Hostname: "Bad_Name", NodeID: "n-owned"}
	_, err := resolveOwnedDevice([]inviteDevice{{ID: "100", NodeID: "n-owned", Hostname: "Bad_Name"}}, target)
	codedError(t, err, registry.CodeInvalidServiceName)
	old := inviteClientFn
	clientCalls := 0
	inviteClientFn = func() (inviteAPI, error) {
		clientCalls++
		return listDevicesInviteAPI{devices: []inviteDevice{{ID: "100", NodeID: "n-owned", Hostname: "Bad_Name"}}}, nil
	}
	t.Cleanup(func() { inviteClientFn = old })
	_, err = CreateDeviceInvite(context.Background(), target, "bob@example.com", false, false, false)
	codedError(t, err, registry.CodeInvalidServiceName)
	if clientCalls != 0 {
		t.Fatalf("invalid target created %d API clients, want zero", clientCalls)
	}
}

func TestProveInviteDeviceOwnershipCoversEveryBranch(t *testing.T) {
	invite := Invite{Kind: InviteKindDevice, ID: "44001", DeviceID: 11055}
	matchingTarget := DeviceTarget{Service: "app", Hostname: "app", NodeID: "n-owned"}
	tests := []struct {
		name        string
		devices     []inviteDevice
		listErr     error
		targets     []DeviceTarget
		wantService string
		wantCode    string
		wantMessage string
	}{
		{name: "device_list_error", listErr: errors.New("synthetic list failure"), wantMessage: "list tailnet devices for invite ownership proof failed"},
		{name: "empty_device_list", wantCode: registry.CodeInviteOwnershipUnproven, wantMessage: "was not returned"},
		{name: "foreign_device_cannot_bind_invite", devices: []inviteDevice{{ID: "99", NodeID: "n-owned", Hostname: "app"}}, targets: []DeviceTarget{matchingTarget}, wantCode: registry.CodeInviteOwnershipUnproven, wantMessage: "was not returned"},
		{name: "foreign_match_cannot_mask_real_mismatch", devices: []inviteDevice{{ID: "77", NodeID: "n-owned", Hostname: "app"}, {ID: "11055", NodeID: "n-foreign", Hostname: "foreign"}}, targets: []DeviceTarget{matchingTarget}, wantCode: registry.CodeInviteOwnershipUnproven, wantMessage: "matched devices"},
		{name: "matching_device_without_targets", devices: []inviteDevice{{ID: "11055", NodeID: "n-owned", Hostname: "app"}}, wantCode: registry.CodeInviteOwnershipUnproven, wantMessage: "matched devices"},
		{name: "matching_hostname_wrong_node", devices: []inviteDevice{{ID: "11055", NodeID: "n-foreign", Hostname: "app"}}, targets: []DeviceTarget{matchingTarget}, wantCode: registry.CodeInviteOwnershipUnproven},
		{name: "matching_node_wrong_hostname", devices: []inviteDevice{{ID: "11055", NodeID: "n-owned", Hostname: "foreign"}}, targets: []DeviceTarget{matchingTarget}, wantCode: registry.CodeInviteOwnershipUnproven},
		{name: "foreign_then_real_exact_match", devices: []inviteDevice{{ID: "77", NodeID: "n-foreign", Hostname: "foreign"}, {ID: "11055", NodeID: "n-owned", Hostname: "app-2"}}, targets: []DeviceTarget{matchingTarget}, wantService: "app"},
		{name: "second_target_exact_match", devices: []inviteDevice{{ID: "11055", NodeID: "n-owned", Hostname: "app"}}, targets: []DeviceTarget{{Service: "foreign", Hostname: "foreign", NodeID: "n-owned"}, matchingTarget}, wantService: "app"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service, err := proveInviteDeviceOwnership(context.Background(), listDevicesInviteAPI{devices: tc.devices, err: tc.listErr}, invite, tc.targets)
			if tc.wantService != "" {
				if err != nil || service != tc.wantService {
					t.Fatalf("service=%q err=%v, want service %q", service, err, tc.wantService)
				}
				return
			}
			if service != "" || err == nil {
				t.Fatalf("service=%q err=%v, want refusal", service, err)
			}
			if tc.wantCode != "" {
				codedError(t, err, tc.wantCode)
			}
			if tc.wantMessage != "" && !strings.Contains(err.Error(), tc.wantMessage) {
				t.Fatalf("error = %q, want %q", err, tc.wantMessage)
			}
		})
	}
}

func TestInviteAPIErrorClassifiesRateLimitAndRequestErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode string
	}{
		{name: "rate_limited", status: http.StatusTooManyRequests, wantCode: registry.CodeInviteRateLimited},
		{name: "state_conflict", status: http.StatusConflict, wantCode: registry.CodeInviteStateConflict},
		{name: "bad_request", status: http.StatusBadRequest, wantCode: registry.CodeInviteRequestInvalid},
		{name: "unprocessable", status: http.StatusUnprocessableEntity, wantCode: registry.CodeInviteRequestInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := inviteAPIError("resend user invite", tailscale.APIError{Status: tc.status, Message: "server detail must not define classification"})
			coded := codedError(t, err, tc.wantCode)
			if !strings.Contains(coded.Message, fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("message = %q, want HTTP status", coded.Message)
			}
		})
	}
}

func TestInviteHTTPRateLimitAndUserInputResponsesKeepStableCodes(t *testing.T) {
	t.Run("remote_recipient_rejected", func(t *testing.T) {
		withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
			assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"invalid-recipient"}]`)
			w.WriteHeader(http.StatusUnprocessableEntity)
		})
		_, err := CreateUserInvite(context.Background(), "invalid-recipient", InviteRoleMember, false)
		codedError(t, err, registry.CodeInviteRequestInvalid)
	})

	t.Run("second_resend_rate_limited", func(t *testing.T) {
		requests := 0
		withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
			requests++
			switch req.Method + " " + req.URL.Path {
			case "GET /api/v2/user-invites/43001":
				io.WriteString(w, `{"id":"43001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/rate-limit-placeholder"}`)
			case "POST /api/v2/user-invites/43001/resend":
				w.WriteHeader(http.StatusTooManyRequests)
			default:
				t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
			}
		})
		_, err := ResendInvite(context.Background(), InviteKindUser, "43001", nil)
		coded := codedError(t, err, registry.CodeInviteRateLimited)
		if requests != 2 || !strings.Contains(strings.Join(coded.Next, " "), "one minute") {
			t.Fatalf("requests=%d error=%+v, want real resend attempt and one-minute guidance", requests, coded)
		}
	})
}

func TestValidateCreatedInviteUsesStableWireContractError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      string
		printLink bool
		count     int
		id        string
		inviteURL string
		wantText  string
	}{
		{name: "empty_batch", kind: InviteKindUser, count: 0, id: "12346", inviteURL: "https://example.invalid/placeholder", wantText: "returned 0 records"},
		{name: "multi_batch", kind: InviteKindUser, count: 2, id: "12346", inviteURL: "https://example.invalid/placeholder", wantText: "returned 2 records"},
		{name: "non_numeric_id", kind: InviteKindUser, count: 1, id: "uinv-example", inviteURL: "https://example.invalid/placeholder", wantText: "numeric id"},
		{name: "self_delivery_user_missing_url", kind: InviteKindUser, printLink: true, count: 1, id: "12346", wantText: "non-empty inviteUrl"},
		{name: "emailed_device_missing_url", kind: InviteKindDevice, count: 1, id: "12346", wantText: "non-empty inviteUrl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coded := codedError(t, validateCreatedInvite(tc.kind, tc.printLink, tc.count, tc.id, tc.inviteURL), registry.CodeInviteResponseInvalid)
			if !strings.Contains(coded.Message, tc.wantText) {
				t.Fatalf("error = %+v, want predicate-specific text %q", coded, tc.wantText)
			}
		})
	}
	if err := validateCreatedInvite(InviteKindUser, false, 1, "12346", ""); err != nil {
		t.Fatalf("documented same-domain emailed user response was rejected: %v", err)
	}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"alice@example.com"}]`)
		io.WriteString(w, `[]`)
	})
	_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
	codedError(t, err, registry.CodeInviteResponseInvalid)
}

func TestCreateInviteAuditsSuccessfulPOSTBeforeResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      string
		wantCount string
		wantIDs   []string
		run       func(*testing.T) error
	}{
		{
			name:      "user_empty_response",
			kind:      InviteKindUser,
			wantCount: "response_count=0",
			run: func(t *testing.T) error {
				withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
					assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"alice@example.com"}]`)
					io.WriteString(w, `[]`)
				})
				_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
				return err
			},
		},
		{
			name:      "device_missing_required_url",
			kind:      InviteKindDevice,
			wantCount: "response_count=1",
			wantIDs:   []string{"20003"},
			run: func(t *testing.T) error {
				requests := 0
				withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
					requests++
					if requests == 1 {
						io.WriteString(w, `{"devices":[{"id":"100","nodeId":"n-owned","hostname":"app"}]}`)
						return
					}
					io.WriteString(w, `[{"id":"20003","deviceId":100,"email":"bob@example.com"}]`)
				})
				_, err := CreateDeviceInvite(context.Background(), DeviceTarget{Service: "app", Hostname: "app", NodeID: "n-owned"}, "bob@example.com", false, false, false)
				return err
			},
		},
		{
			name:      "user_multi_response",
			kind:      InviteKindUser,
			wantCount: "response_count=2",
			wantIDs:   []string{"61001", "61002"},
			run: func(t *testing.T) error {
				withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
					assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"alice@example.com"}]`)
					io.WriteString(w, `[{"id":"61001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/live-one"},{"id":"61002","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/live-two"}]`)
				})
				_, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false)
				return err
			},
		},
		{
			name:      "device_multi_response",
			kind:      InviteKindDevice,
			wantCount: "response_count=2",
			wantIDs:   []string{"61003", "61004"},
			run: func(t *testing.T) error {
				requests := 0
				withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
					requests++
					if requests == 1 {
						io.WriteString(w, `{"devices":[{"id":"100","nodeId":"n-owned","hostname":"app"}]}`)
						return
					}
					io.WriteString(w, `[{"id":"61003","deviceId":100,"email":"bob@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/device-live-one"},{"id":"61004","deviceId":100,"email":"bob@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/device-live-two"}]`)
				})
				_, err := CreateDeviceInvite(context.Background(), DeviceTarget{Service: "app", Hostname: "app", NodeID: "n-owned"}, "bob@example.com", false, false, false)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			oldLogger := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{})))
			t.Cleanup(func() { slog.SetDefault(oldLogger) })

			codedError(t, tc.run(t), registry.CodeInviteResponseInvalid)
			text := logs.String()
			for _, want := range []string{"msg=security.remote_invite_created", "kind=" + tc.kind, tc.wantCount} {
				if !strings.Contains(text, want) {
					t.Fatalf("logs = %q, want %q after successful POST and failed validation", text, want)
				}
			}
			for _, wantID := range tc.wantIDs {
				if !strings.Contains(text, wantID) {
					t.Fatalf("logs = %q, want every returned invite ID including %q", text, wantID)
				}
			}
			if strings.Contains(text, "login.tailscale.com") {
				t.Fatalf("audit log leaked invite URL: %q", text)
			}
		})
	}
}

func TestInviteHTTPResponseLimitUsesStableWireCode(t *testing.T) {
	const fourMiB = 4_194_304
	if inviteResponseLimit != fourMiB {
		t.Fatalf("inviteResponseLimit = %d, want pinned four-MiB contract %d", inviteResponseLimit, fourMiB)
	}
	for _, tc := range []struct {
		name      string
		bodyBytes int
		wantError bool
	}{
		{name: "exact_limit_is_accepted", bodyBytes: fourMiB},
		{name: "one_byte_over_is_rejected", bodyBytes: fourMiB + 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/api/v2/tailnet/-/user-invites" {
					t.Fatalf("unexpected path %s", req.URL.Path)
				}
				io.WriteString(w, strings.Repeat(" ", tc.bodyBytes))
			})
			_, err := ListInvites(context.Background(), nil)
			if !tc.wantError {
				if err != nil {
					t.Fatalf("exact-limit response rejected: %v", err)
				}
				return
			}
			coded := codedError(t, err, registry.CodeInviteResponseInvalid)
			if !strings.Contains(coded.Message, "4194304 bytes") {
				t.Fatalf("message = %q, want fixed response limit", coded.Message)
			}
		})
	}
}

func TestInviteHTTPDoIgnoresNonEmptySuccessBodyWhenOutputIsNil(t *testing.T) {
	client := newInviteHTTPClient(&tailscale.Client{HTTP: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not-json"))}, nil
	})}})
	if err := client.do(context.Background(), http.MethodDelete, []string{"user-invites", "12346"}, nil, nil); err != nil {
		t.Fatalf("nil output must ignore a non-empty success body: %v", err)
	}
}

func TestListInvites_ReturnsBothKindsFromExactOwnedDevice(t *testing.T) {
	requests := []string{}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.URL.Path {
		case "/api/v2/tailnet/-/user-invites":
			io.WriteString(w, `[{"id":"30001","role":"auditor","email":"audit@example.com","inviteUrl":"https://login.tailscale.com/uinv/list"}]`)
		case "/api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"77","nodeId":"n-docs","hostname":"docs-3"}]}`)
		case "/api/v2/device/n-docs/device-invites":
			io.WriteString(w, `[{"id":"30002","deviceId":77,"inviteUrl":"https://login.tailscale.com/admin/invite/list"}]`)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ListInvites(context.Background(), []DeviceTarget{{Service: "docs", Hostname: "docs", NodeID: "n-docs"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Count != 2 || len(result.UserInvites) != 1 || len(result.DeviceInvites) != 1 || result.DeviceInvites[0].Service != "docs" || result.DeviceInvites[0].Emailed || len(result.DeviceTargets) != 1 || !result.DeviceTargets[0].Checked || result.DeviceTargets[0].InviteCount != 1 || result.DeviceTargets[0].Error != nil {
		t.Fatalf("result = %+v", result)
	}
	wantRequests := "GET /api/v2/tailnet/-/user-invites,GET /api/v2/tailnet/-/devices,GET /api/v2/device/n-docs/device-invites"
	if strings.Join(requests, ",") != wantRequests {
		t.Fatalf("requests = %s, want %s", strings.Join(requests, ","), wantRequests)
	}
}

func TestListInvites_PreservesUsersAndReportsUnresolvableTargets(t *testing.T) {
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v2/tailnet/-/user-invites":
			io.WriteString(w, `[{"id":"31001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/list-preserved"}]`)
		case "/api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"81","nodeId":"n-ready","hostname":"ready"}]}`)
		case "/api/v2/device/n-ready/device-invites":
			io.WriteString(w, `[]`)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ListInvites(context.Background(), []DeviceTarget{
		{Service: "ready", Hostname: "ready", NodeID: "n-ready"},
		{Service: "never-started", Hostname: "never-started"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.Count != 1 || len(result.UserInvites) != 1 || result.UserInvites[0].ID != "31001" {
		t.Fatalf("user invites were discarded: %+v", result)
	}
	if len(result.DeviceTargets) != 2 {
		t.Fatalf("device target results = %+v, want one entry per service", result.DeviceTargets)
	}
	if ready := result.DeviceTargets[0]; !ready.Checked || ready.InviteCount != 0 || ready.Error != nil {
		t.Fatalf("ready target = %+v, want checked empty result", ready)
	}
	if missing := result.DeviceTargets[1]; missing.Checked || missing.Error == nil || missing.Error.Code != registry.CodeInviteNotFound {
		t.Fatalf("never-started target = %+v, want structured unresolved result", missing)
	}
}

func TestListInvites_DeviceDiscoveryFailureStillReturnsUsers(t *testing.T) {
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v2/tailnet/-/user-invites":
			io.WriteString(w, `[{"id":"31002","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/list-preserved"}]`)
		case "/api/v2/tailnet/-/devices":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ListInvites(context.Background(), []DeviceTarget{{Service: "app", Hostname: "app", NodeID: "n-app"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.Count != 1 || len(result.UserInvites) != 1 || len(result.DeviceTargets) != 1 || result.DeviceTargets[0].Error == nil || result.DeviceTargets[0].Error.Code != "internal_error" {
		t.Fatalf("result = %+v, want user result plus per-target discovery failure", result)
	}
}

func TestListInvites_PerTargetAPIFailureDoesNotStopLaterTargets(t *testing.T) {
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v2/tailnet/-/user-invites":
			io.WriteString(w, `[{"id":"31003","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/list-preserved"}]`)
		case "/api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"91","nodeId":"n-app","hostname":"app"},{"id":"92","nodeId":"n-docs","hostname":"docs"}]}`)
		case "/api/v2/device/n-app/device-invites":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/api/v2/device/n-docs/device-invites":
			io.WriteString(w, `[{"id":"31004","deviceId":92,"inviteUrl":"https://login.tailscale.com/admin/invite/list-later-target"}]`)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ListInvites(context.Background(), []DeviceTarget{
		{Service: "app", Hostname: "app", NodeID: "n-app"},
		{Service: "docs", Hostname: "docs", NodeID: "n-docs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete || result.Count != 2 || len(result.UserInvites) != 1 || len(result.DeviceInvites) != 1 || result.DeviceInvites[0].ID != "31004" {
		t.Fatalf("result = %+v, want user invite and later device target", result)
	}
	if len(result.DeviceTargets) != 2 || result.DeviceTargets[0].Error == nil || result.DeviceTargets[0].Error.Code != registry.CodeInviteRateLimited || !result.DeviceTargets[1].Checked || result.DeviceTargets[1].InviteCount != 1 {
		t.Fatalf("target statuses = %+v", result.DeviceTargets)
	}
}

func TestListInvites_NoDeviceTargetsIsComplete(t *testing.T) {
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v2/tailnet/-/user-invites" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		io.WriteString(w, `[]`)
	})
	result, err := ListInvites(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Count != 0 || result.UserInvites == nil || result.DeviceInvites == nil || result.DeviceTargets == nil {
		t.Fatalf("result = %+v, want complete empty arrays", result)
	}
}

func TestListInvites_DeduplicatesAndCachesPerOwnedNode(t *testing.T) {
	deviceInviteCalls := 0
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v2/tailnet/-/user-invites":
			io.WriteString(w, `[]`)
		case "/api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"93","nodeId":"n-shared","hostname":"app"}]}`)
		case "/api/v2/device/n-shared/device-invites":
			deviceInviteCalls++
			if deviceInviteCalls > 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			io.WriteString(w, `[{"id":"31005","deviceId":93,"inviteUrl":"https://login.tailscale.com/admin/invite/shared-node"}]`)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ListInvites(context.Background(), []DeviceTarget{
		{Service: "app", Hostname: "app", NodeID: "n-shared"},
		{Service: "app-shadow", Hostname: "app", NodeID: "n-shared"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if deviceInviteCalls != 1 {
		t.Fatalf("device invite requests = %d, want one cached request for the shared node", deviceInviteCalls)
	}
	if !result.Complete || result.Count != 1 || len(result.DeviceInvites) != 1 || result.DeviceInvites[0].ID != "31005" || result.DeviceInvites[0].Service != "app" {
		t.Fatalf("result = %+v, want one deduplicated invite and complete=true", result)
	}
	if len(result.DeviceTargets) != 2 || !result.DeviceTargets[0].Checked || !result.DeviceTargets[1].Checked || result.DeviceTargets[0].InviteCount != 1 || result.DeviceTargets[1].InviteCount != 1 {
		t.Fatalf("target statuses = %+v, want both targets satisfied by cached response", result.DeviceTargets)
	}
}

func TestResendDeviceInvite_ExplicitKindAndExactOwnership(t *testing.T) {
	requests := []string{}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.Method + " " + req.URL.Path {
		case "GET /api/v2/device-invites/40001":
			io.WriteString(w, `{"id":"40001","deviceId":11055,"email":"bob@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/resend-source"}`)
		case "GET /api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"11055","nodeId":"n-owned","hostname":"app-2"}]}`)
		case "POST /api/v2/device-invites/40001/resend":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := ResendInvite(context.Background(), InviteKindDevice, "40001", []DeviceTarget{{Service: "app", Hostname: "app", NodeID: "n-owned"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != InviteKindDevice || result.Service != "app" || !result.Emailed || result.InviteURL != "https://login.tailscale.com/admin/invite/resend-source" {
		t.Fatalf("result = %+v", result)
	}
	want := "GET /api/v2/device-invites/40001,GET /api/v2/tailnet/-/devices,POST /api/v2/device-invites/40001/resend"
	if strings.Join(requests, ",") != want {
		t.Fatalf("requests = %s, want %s", strings.Join(requests, ","), want)
	}
}

func TestRevokeDeviceInvite_ProductionShapeProvesOwnershipBeforeDelete(t *testing.T) {
	requests := []string{}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.Method + " " + req.URL.Path {
		case "GET /api/v2/device-invites/40002":
			io.WriteString(w, `{"id":"40002","deviceId":11055,"email":"bob@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/revoke-source"}`)
		case "GET /api/v2/tailnet/-/devices":
			io.WriteString(w, `{"devices":[{"id":"11055","nodeId":"n-owned","hostname":"app-3"}]}`)
		case "DELETE /api/v2/device-invites/40002":
			assertInviteRequest(t, req, http.MethodDelete, "/api/v2/device-invites/40002", "")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := RevokeInvite(context.Background(), InviteKindDevice, "40002", []DeviceTarget{{Service: "app", Hostname: "app", NodeID: "n-owned"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != InviteKindDevice || result.Service != "app" || result.ID != "40002" {
		t.Fatalf("result = %+v, want exact device invite and resolved service", result)
	}
	want := "GET /api/v2/device-invites/40002,GET /api/v2/tailnet/-/devices,DELETE /api/v2/device-invites/40002"
	if strings.Join(requests, ",") != want {
		t.Fatalf("requests = %s, want %s", strings.Join(requests, ","), want)
	}
}

func TestDeviceInviteMutationsRefuseEachMissingOwnershipPredicate(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		inviteID  string
		devices   string
	}{
		{name: "revoke_hostname_collision_different_node_id", operation: "revoke", inviteID: "41001", devices: `{"devices":[{"id":"11055","nodeId":"n-foreign","hostname":"app"}]}`},
		{name: "revoke_matching_node_id_wrong_hostname", operation: "revoke", inviteID: "41002", devices: `{"devices":[{"id":"11055","nodeId":"n-owned","hostname":"foreign"}]}`},
		{name: "resend_hostname_collision_different_node_id", operation: "resend", inviteID: "41003", devices: `{"devices":[{"id":"11055","nodeId":"n-foreign","hostname":"app"}]}`},
		{name: "resend_matching_node_id_wrong_hostname", operation: "resend", inviteID: "41004", devices: `{"devices":[{"id":"11055","nodeId":"n-owned","hostname":"foreign"}]}`},
		{name: "revoke_invite_device_absent", operation: "revoke", inviteID: "41005", devices: `{"devices":[]}`},
		{name: "revoke_foreign_device_cannot_bind_invite", operation: "revoke", inviteID: "41006", devices: `{"devices":[{"id":"99","nodeId":"n-owned","hostname":"app"}]}`},
		{name: "resend_foreign_match_cannot_mask_real_device_mismatch", operation: "resend", inviteID: "41007", devices: `{"devices":[{"id":"77","nodeId":"n-owned","hostname":"app"},{"id":"11055","nodeId":"n-foreign","hostname":"foreign"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutationCalls := 0
			withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/v2/device-invites/"+tc.inviteID:
					fmt.Fprintf(w, `{"id":%q,"deviceId":11055,"email":"bob@example.com","inviteUrl":"https://login.tailscale.com/admin/invite/ownership-proof"}`, tc.inviteID)
				case req.Method == http.MethodGet && req.URL.Path == "/api/v2/tailnet/-/devices":
					io.WriteString(w, tc.devices)
				case req.Method == http.MethodDelete || req.Method == http.MethodPost:
					mutationCalls++
					w.WriteHeader(http.StatusOK)
				default:
					t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
				}
			})

			targets := []DeviceTarget{{Service: "app", Hostname: "app", NodeID: "n-owned"}}
			var err error
			if tc.operation == "revoke" {
				_, err = RevokeInvite(context.Background(), InviteKindDevice, tc.inviteID, targets)
			} else {
				_, err = ResendInvite(context.Background(), InviteKindDevice, tc.inviteID, targets)
			}
			if mutationCalls != 0 {
				t.Fatalf("ownership refusal allowed %d mutation requests, want zero", mutationCalls)
			}
			codedError(t, err, registry.CodeInviteOwnershipUnproven)
		})
	}
}

func TestRevokeInvite_ExplicitKindProtectsCollidingNamespace(t *testing.T) {
	requests := []string{}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests = append(requests, req.Method+" "+req.URL.Path)
		switch req.Method + " " + req.URL.Path {
		case "GET /api/v2/user-invites/42001":
			io.WriteString(w, `{"id":"42001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/collision-placeholder"}`)
		case "DELETE /api/v2/user-invites/42001":
			w.WriteHeader(http.StatusOK)
		case "GET /api/v2/device-invites/42001", "DELETE /api/v2/device-invites/42001":
			t.Fatal("explicit user mutation touched the colliding device namespace")
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	result, err := RevokeInvite(context.Background(), InviteKindUser, "42001", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != InviteKindUser || strings.Join(requests, ",") != "GET /api/v2/user-invites/42001,DELETE /api/v2/user-invites/42001" {
		t.Fatalf("result=%+v requests=%v, want only explicit user namespace", result, requests)
	}
}

func TestInviteMutationLibraryAndLookupDefaultDenyUnknownKind(t *testing.T) {
	for _, kind := range []string{"", "both"} {
		t.Run("lookup_"+kind, func(t *testing.T) {
			getDeviceCalls := 0
			client := listDevicesInviteAPI{deviceInvite: deviceInviteResponse{ID: "42002", DeviceID: 11055}, getDeviceCall: &getDeviceCalls}
			_, err := getInvite(context.Background(), client, kind, "42002")
			codedError(t, err, registry.CodeInviteKindInvalid)
			if getDeviceCalls != 0 {
				t.Fatalf("unknown kind issued %d device lookups, want zero", getDeviceCalls)
			}
		})
	}

	old := inviteClientFn
	clientCalls := 0
	inviteClientFn = func() (inviteAPI, error) {
		clientCalls++
		return listDevicesInviteAPI{deviceInvite: deviceInviteResponse{ID: "42002", DeviceID: 11055}}, nil
	}
	t.Cleanup(func() { inviteClientFn = old })
	operations := []struct {
		name string
		run  func(string) error
	}{
		{name: "revoke", run: func(kind string) error {
			_, err := RevokeInvite(context.Background(), kind, "42002", nil)
			return err
		}},
		{name: "resend", run: func(kind string) error {
			_, err := ResendInvite(context.Background(), kind, "42002", nil)
			return err
		}},
	}
	for _, operation := range operations {
		for _, kind := range []string{"", "both"} {
			t.Run(operation.name+"_"+kind, func(t *testing.T) {
				codedError(t, operation.run(kind), registry.CodeInviteKindInvalid)
			})
		}
	}
	if clientCalls != 0 {
		t.Fatalf("invalid mutation kinds created %d API clients, want zero", clientCalls)
	}
}

func TestInviteRoleEnumAndEmptyRecipientValidationAreExact(t *testing.T) {
	want := []string{"member", "admin", "it-admin", "network-admin", "billing-admin", "auditor"}
	roles := InviteRoles()
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		t.Fatalf("roles = %v, want exact OpenAPI enum %v", roles, want)
	}
	roles[0] = "mutated"
	if InviteRoles()[0] != InviteRoleMember {
		t.Fatal("InviteRoles returned mutable package state")
	}
	if err := ValidateInviteRole(InviteRoleAuditor); err != nil {
		t.Fatalf("auditor role rejected: %v", err)
	}
	codedError(t, ValidateInviteRole("owner"), registry.CodeInviteRoleInvalid)

	old := inviteClientFn
	inviteClientFn = func() (inviteAPI, error) {
		t.Fatal("empty recipient validation reached API client creation")
		return nil, errors.New("unreachable")
	}
	t.Cleanup(func() { inviteClientFn = old })
	_, err := CreateUserInvite(context.Background(), "  ", InviteRoleMember, false)
	codedError(t, err, registry.CodeInviteRecipientInvalid)
	_, err = CreateUserInvite(context.Background(), "alice@example.com", "owner", false)
	codedError(t, err, registry.CodeInviteRoleInvalid)
	_, err = CreateDeviceInvite(context.Background(), DeviceTarget{Service: "app"}, "", false, false, false)
	codedError(t, err, registry.CodeInviteRecipientInvalid)
}

func TestResolveOwnedDeviceRequiresNonEmptyUniqueNodeID(t *testing.T) {
	devices := []inviteDevice{{ID: "81", NodeID: "", Hostname: "app"}}
	_, err := resolveOwnedDevice(devices, DeviceTarget{Service: "app", Hostname: "app"})
	codedError(t, err, registry.CodeInviteOwnershipUnproven)

	devices = []inviteDevice{
		{ID: "81", NodeID: "n-duplicate", Hostname: "app"},
		{ID: "82", NodeID: "n-duplicate", Hostname: "app-2"},
	}
	_, err = resolveOwnedDevice(devices, DeviceTarget{Service: "app", Hostname: "app", NodeID: "n-duplicate"})
	codedError(t, err, registry.CodeInviteDeviceAmbiguous)
}

func TestInviteTargetsCompleteRequiresCheckedAndNoError(t *testing.T) {
	if !inviteTargetsComplete([]InviteTargetStatus{{Service: "app", Checked: true}}) {
		t.Fatal("checked target without an error must be complete")
	}
	if inviteTargetsComplete([]InviteTargetStatus{{Service: "app", Checked: false}}) {
		t.Fatal("unchecked target without an error must be incomplete")
	}
	if inviteTargetsComplete([]InviteTargetStatus{{Service: "app", Checked: true, Error: &InviteTargetError{Code: "probe", Message: "probe"}}}) {
		t.Fatal("checked target with an error must be incomplete")
	}
}

func TestListInvitesUserEmailedReflectsReturnedEmail(t *testing.T) {
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v2/tailnet/-/user-invites" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		io.WriteString(w, `[{"id":"62001","role":"member","inviteUrl":"https://example.invalid/self-delivery"}]`)
	})
	result, err := ListInvites(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.UserInvites) != 1 || result.UserInvites[0].Emailed {
		t.Fatalf("user invites = %+v, want returned empty email to mean emailed=false", result.UserInvites)
	}
}

func TestResendInvite_WithoutOriginalEmailFailsBeforePOST(t *testing.T) {
	requests := 0
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != http.MethodGet || req.URL.Path != "/api/v2/user-invites/50001" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		io.WriteString(w, `{"id":"50001","role":"member","inviteUrl":"https://login.tailscale.com/uinv/self-delivery"}`)
	})
	_, err := ResendInvite(context.Background(), InviteKindUser, "50001", nil)
	codedError(t, err, registry.CodeInviteResendEmailMissing)
	if requests != 1 {
		t.Fatalf("requests = %d, want GET only and no impossible resend POST", requests)
	}
}

func TestRevokeInvite_NotFoundChecksOnlyExplicitNamespace(t *testing.T) {
	paths := []string{}
	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		paths = append(paths, req.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"not found"}`)
	})
	_, err := RevokeInvite(context.Background(), InviteKindUser, "99999", nil)
	codedError(t, err, registry.CodeInviteNotFound)
	if strings.Join(paths, ",") != "/api/v2/user-invites/99999" {
		t.Fatalf("paths = %v, want only explicit user lookup", paths)
	}
}

func TestInviteResponseJSONCarriesExplicitEmailedFalse(t *testing.T) {
	data, err := json.Marshal(Invite{Kind: InviteKindUser, ID: "61001", InviteURL: "https://example.invalid/verbatim", Emailed: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"emailed":false`) || !strings.Contains(string(data), `"invite_url":"https://example.invalid/verbatim"`) {
		t.Fatalf("JSON = %s, want explicit emailed=false and invite_url", data)
	}
}

func TestInviteMutationsEmitSecurityLogsWithoutInviteURL(t *testing.T) {
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{})))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	withInviteServer(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.Method + " " + req.URL.Path {
		case "POST /api/v2/tailnet/-/user-invites":
			assertInviteRequest(t, req, http.MethodPost, "/api/v2/tailnet/-/user-invites", `[{"role":"member","email":"alice@example.com"}]`)
			io.WriteString(w, `[{"id":"60001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/must-not-be-logged"}]`)
		case "GET /api/v2/user-invites/60001":
			assertInviteRequest(t, req, http.MethodGet, "/api/v2/user-invites/60001", "")
			io.WriteString(w, `{"id":"60001","role":"member","email":"alice@example.com","inviteUrl":"https://login.tailscale.com/uinv/must-not-be-logged"}`)
		case "POST /api/v2/user-invites/60001/resend":
			assertInviteRequest(t, req, http.MethodPost, "/api/v2/user-invites/60001/resend", "")
			w.WriteHeader(http.StatusOK)
		case "DELETE /api/v2/user-invites/60001":
			assertInviteRequest(t, req, http.MethodDelete, "/api/v2/user-invites/60001", "")
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
	})

	if _, err := CreateUserInvite(context.Background(), "alice@example.com", InviteRoleMember, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ResendInvite(context.Background(), InviteKindUser, "60001", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := RevokeInvite(context.Background(), InviteKindUser, "60001", nil); err != nil {
		t.Fatal(err)
	}
	text := logs.String()
	for _, event := range []string{"security.remote_invite_created", "security.remote_invite_resent", "security.remote_invite_revoked"} {
		if !strings.Contains(text, "msg="+event+" ") {
			t.Fatalf("logs = %q, want event %q", text, event)
		}
	}
	if !strings.Contains(text, "invite_id=60001") || !strings.Contains(text, "recipient=alice@example.com") {
		t.Fatalf("logs = %q, want clear invite ID and recipient audit fields", text)
	}
	if strings.Contains(text, "must-not-be-logged") || strings.Contains(text, "tskey-") {
		t.Fatalf("logs leaked invite URL or credential-shaped material: %q", text)
	}
}
