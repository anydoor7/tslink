package credentials

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anydoor7/tslink/internal/registry"
	tailscale "tailscale.com/client/tailscale/v2"
)

func TestClassifyAPIErrorSplits401And403AndKeepsChain(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantNext string
	}{
		{"401", tailscale.APIError{Status: http.StatusUnauthorized, Message: "invalid key"}, registry.CodeAPITokenUnauthorized, KeysPageURL},
		{"403", tailscale.APIError{Status: http.StatusForbidden, Message: "forbidden"}, registry.CodeAPIForbidden, "tslink doctor --probe-remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("outer: %w", tc.err)
			err := ClassifyAPIError("derive auth key", wrapped)
			code, ok := registry.ErrorCode(err)
			if !ok || code != tc.wantCode {
				t.Fatalf("ClassifyAPIError() code = %q ok=%v, want %s", code, ok, tc.wantCode)
			}
			var carrier interface{ NextCommands() []string }
			if !errors.As(err, &carrier) || !strings.Contains(strings.Join(carrier.NextCommands(), "\n"), tc.wantNext) {
				t.Fatalf("next = %v, want %q", err, tc.wantNext)
			}
			var apiErr tailscale.APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.err.(tailscale.APIError).Status {
				t.Fatalf("classified error lost the APIError chain: %v", err)
			}
			if !strings.Contains(err.Error(), "derive auth key") || !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", apiErr.Status)) {
				t.Fatalf("message = %q, want operation label and HTTP status", err.Error())
			}
		})
	}

	other := ClassifyAPIError("list devices", tailscale.APIError{Status: http.StatusServiceUnavailable})
	if _, coded := registry.ErrorCode(other); coded || !strings.Contains(other.Error(), "list devices") {
		t.Fatalf("non-auth API error must stay uncoded with its label: %v", other)
	}
	network := ClassifyAPIError("list devices", errors.New("dial tcp: refused"))
	if _, coded := registry.ErrorCode(network); coded || !strings.Contains(network.Error(), "list devices: dial tcp") {
		t.Fatalf("network error must stay uncoded with its label: %v", network)
	}
	if ClassifyAPIError("noop", nil) != nil {
		t.Fatal("nil error must classify to nil")
	}
}

func TestVerificationResultForError(t *testing.T) {
	cases := map[string]error{
		VerifyResultOK:           nil,
		VerifyResultUnauthorized: tailscale.APIError{Status: http.StatusUnauthorized},
		VerifyResultForbidden:    fmt.Errorf("wrapped: %w", tailscale.APIError{Status: http.StatusForbidden}),
		VerifyResultUnreachable:  errors.New("dial tcp: timeout"),
	}
	for want, err := range cases {
		if got := VerificationResultForError(err); got != want {
			t.Fatalf("VerificationResultForError(%v) = %s, want %s", err, got, want)
		}
	}
	if got := VerificationResultForError(tailscale.APIError{Status: http.StatusInternalServerError}); got != VerifyResultUnreachable {
		t.Fatalf("5xx = %s, want unreachable", got)
	}
}

func probeServer(t *testing.T, status int) (*url.URL, *http.Client, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(`{"devices":[]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"status":%d,"message":"synthetic"}`, status)
	}))
	t.Cleanup(server.Close)
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return baseURL, server.Client(), &calls
}

func withProbeClientFactory(t *testing.T, baseURL *url.URL, httpClient *http.Client) {
	t.Helper()
	old := probeClientFactoryFn
	t.Cleanup(func() { probeClientFactoryFn = old })
	probeClientFactoryFn = func(slot, value string) (*tailscale.Client, error) {
		client, err := NewTailscaleClientWithAPIKey(value)
		if err != nil {
			return nil, err
		}
		client.BaseURL = baseURL
		client.HTTP = httpClient
		return client, nil
	}
}

func TestProbeStoredCredentialRecordsEachOutcome(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantResult string
		wantCode   string
	}{
		{"ok", http.StatusOK, VerifyResultOK, ""},
		{"unauthorized", http.StatusUnauthorized, VerifyResultUnauthorized, registry.CodeAPITokenUnauthorized},
		{"forbidden", http.StatusForbidden, VerifyResultForbidden, registry.CodeAPIForbidden},
		{"unreachable", http.StatusBadGateway, VerifyResultUnreachable, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setup(t)
			const key = "tskey-api-<test-only-FAKE-probe-value>"
			if err := SetAPIKey(key); err != nil {
				t.Fatal(err)
			}
			if _, _, err := RecordCredentialStored(SlotAPIKey, key, StoredOptions{Now: metaTestNow}); err != nil {
				t.Fatal(err)
			}
			baseURL, httpClient, calls := probeServer(t, tc.status)
			withProbeClientFactory(t, baseURL, httpClient)

			later := metaTestNow.Add(time.Hour)
			outcome, err := ProbeStoredCredential(context.Background(), SlotAPIKey, later)
			if err != nil {
				t.Fatalf("ProbeStoredCredential() error = %v", err)
			}
			if *calls != 1 {
				t.Fatalf("remote calls = %d, want exactly one device list", *calls)
			}
			if !outcome.Present || outcome.Result != tc.wantResult || !outcome.Recorded {
				t.Fatalf("outcome = %+v, want present/%s/recorded", outcome, tc.wantResult)
			}
			if tc.wantCode != "" {
				if code, _ := registry.ErrorCode(outcome.Cause); code != tc.wantCode {
					t.Fatalf("cause = %v, want code %s", outcome.Cause, tc.wantCode)
				}
			} else if tc.status == http.StatusOK && outcome.Cause != nil {
				t.Fatalf("ok probe carried a cause: %v", outcome.Cause)
			}
			if outcome.Cause != nil && strings.Contains(outcome.Cause.Error(), "FAKE-probe-value") {
				t.Fatalf("cause leaks credential: %v", outcome.Cause)
			}
			meta, err := ReadSlotMetadata(SlotAPIKey)
			if err != nil || meta == nil || meta.LastVerifiedResult != tc.wantResult || !meta.LastVerifiedAt.Equal(later) {
				t.Fatalf("metadata after probe = %+v err=%v", meta, err)
			}
			raw := readMetaFile(t)
			if strings.Contains(raw, "FAKE-probe-value") {
				t.Fatalf("metadata leaks credential:\n%s", raw)
			}
		})
	}
}

func TestProbeStoredCredentialAbsentAndErrors(t *testing.T) {
	setup(t)
	outcome, err := ProbeStoredCredential(context.Background(), SlotAPIKey, metaTestNow)
	if err != nil || outcome.Present || outcome.Result != "" {
		t.Fatalf("absent slot outcome = %+v err=%v", outcome, err)
	}
	if _, err := ProbeStoredCredential(context.Background(), "bogus", metaTestNow); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("unknown slot error = %v", err)
	}

	// A client factory failure is classified as unreachable, still recorded.
	if err := SaveClientSecret("tskey-client-FAKE"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RecordCredentialStored(SlotClientSecret, "tskey-client-FAKE", StoredOptions{Now: metaTestNow}); err != nil {
		t.Fatal(err)
	}
	old := probeClientFactoryFn
	t.Cleanup(func() { probeClientFactoryFn = old })
	probeClientFactoryFn = func(string, string) (*tailscale.Client, error) { return nil, errors.New("bad secret format") }
	outcome, err = ProbeStoredCredential(context.Background(), SlotClientSecret, metaTestNow)
	if err != nil || !outcome.Present || outcome.Result != VerifyResultUnreachable || outcome.Cause == nil || !outcome.Recorded {
		t.Fatalf("factory failure outcome = %+v err=%v", outcome, err)
	}
	probeClientFactoryFn = func(string, string) (*tailscale.Client, error) { return nil, nil }
	outcome, err = ProbeStoredCredential(context.Background(), SlotClientSecret, metaTestNow)
	if err != nil || outcome.Result != VerifyResultUnreachable {
		t.Fatalf("nil client outcome = %+v err=%v", outcome, err)
	}
}

func TestProbeStoredCredentialWithoutMetadataDoesNotRecordButReports(t *testing.T) {
	setup(t)
	if err := SetAPIKey("tskey-api-<test-only-FAKE>"); err != nil {
		t.Fatal(err)
	}
	baseURL, httpClient, _ := probeServer(t, http.StatusOK)
	withProbeClientFactory(t, baseURL, httpClient)
	outcome, err := ProbeStoredCredential(context.Background(), SlotAPIKey, metaTestNow)
	if err != nil || outcome.Result != VerifyResultOK || !outcome.Recorded {
		t.Fatalf("outcome = %+v err=%v; RecordVerification without metadata is a no-op success", outcome, err)
	}
	if meta, _ := ReadSlotMetadata(SlotAPIKey); meta != nil {
		t.Fatalf("probe invented metadata: %+v", meta)
	}
}

func TestProbeStoredCredentialReadFailureIsLocalError(t *testing.T) {
	setup(t)
	stubKeyring(t, func(string, string) (string, error) { return "", errors.New("keyring unavailable") }, nil, nil)
	apiKeyPathFunc = func() (string, error) { return "", errors.New("no path") }
	t.Cleanup(func() { apiKeyPathFunc = func() (string, error) { return apiKeyPath(t), nil } })
	_, err := ProbeStoredCredential(context.Background(), SlotAPIKey, metaTestNow)
	if err == nil || !strings.Contains(err.Error(), "read api-key credential") {
		t.Fatalf("ProbeStoredCredential() error = %v, want local read failure", err)
	}
}

func TestDefaultProbeClientFactoryPerSlot(t *testing.T) {
	client, err := probeClientFactoryFn(SlotAPIKey, "tskey-api-<test-only-FAKE>")
	if err != nil || client == nil || client.APIKey != "tskey-api-<test-only-FAKE>" {
		t.Fatalf("api-key factory = %+v, %v", client, err)
	}
	client, err = probeClientFactoryFn(SlotClientSecret, "tskey-client-FAKEID-FAKESECRET")
	if err != nil || client == nil || client.Auth == nil {
		t.Fatalf("client-secret factory = %+v, %v", client, err)
	}
	if _, err := probeClientFactoryFn(SlotClientSecret, "malformed"); err == nil {
		t.Fatal("malformed client secret must fail the factory")
	}
	if _, err := probeClientFactoryFn("bogus", "x"); !errors.Is(err, ErrUnknownCredentialSlot) {
		t.Fatalf("unknown slot factory error = %v", err)
	}
}

func TestDeriveAuthKeyCodes401And403(t *testing.T) {
	for _, tc := range []struct {
		status   int
		wantCode string
	}{
		{http.StatusUnauthorized, registry.CodeAPITokenUnauthorized},
		{http.StatusForbidden, registry.CodeAPIForbidden},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			setup(t)
			baseURL, httpClient, _ := probeServer(t, tc.status)
			factory := func() (*tailscale.Client, error) {
				return &tailscale.Client{Tailnet: "-", APIKey: "tskey-api-<test-only-FAKE>", BaseURL: baseURL, HTTP: httpClient}, nil
			}
			_, err := DeriveAuthKey(context.Background(), AuthKeyOptions{ClientFactory: factory})
			code, ok := registry.ErrorCode(err)
			if !ok || code != tc.wantCode || !strings.Contains(err.Error(), "derive auth key") {
				t.Fatalf("DeriveAuthKey() error = %v code=%q, want %s", err, code, tc.wantCode)
			}
		})
	}
}
