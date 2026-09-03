package tailapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/monody0007/tslink/internal/credentials"
	"github.com/monody0007/tslink/internal/registry"
)

// The ACL and device-list paths used to return bare fmt.Errorf values for
// HTTP 401. They now carry api_token_unauthorized / api_forbidden with recovery
// steps while keeping the existing sentinel chain intact.

func TestEnsureTags_ACLRead401IsCodedUnauthorizedWithBootstrapNext(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return aclJSONResponse(http.StatusUnauthorized, `{"message":"invalid key"}`), nil
	})

	err := EnsureTags(context.Background(), []string{"tag:test"})
	code, ok := registry.ErrorCode(err)
	if !ok || code != registry.CodeAPITokenUnauthorized {
		t.Fatalf("EnsureTags() error = %v code=%q, want api_token_unauthorized", err, code)
	}
	if errors.Is(err, ErrPolicyAccessDenied) {
		t.Fatalf("401 must not be classified as policy access denied: %v", err)
	}
	var carrier interface{ NextCommands() []string }
	if !errors.As(err, &carrier) || !strings.Contains(strings.Join(carrier.NextCommands(), "\n"), credentials.KeysPageURL) {
		t.Fatalf("next = %v, want Keys page bootstrap", err)
	}
	if !strings.Contains(err.Error(), "read ACL") {
		t.Fatalf("message = %q, want read ACL label", err.Error())
	}
}

func TestEnsureTags_ACLRead403KeepsSentinelAndAddsForbiddenCode(t *testing.T) {
	aclSetup(t)
	if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
		t.Fatalf("SetAPIKey() error = %v", err)
	}
	aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
		return aclJSONResponse(http.StatusForbidden, `{"message":"forbidden"}`), nil
	})

	err := EnsureTags(context.Background(), []string{"tag:test"})
	if !errors.Is(err, ErrPolicyAccessDenied) {
		t.Fatalf("EnsureTags() error = %v, want errors.Is ErrPolicyAccessDenied preserved", err)
	}
	code, ok := registry.ErrorCode(err)
	if !ok || code != registry.CodeAPIForbidden {
		t.Fatalf("EnsureTags() code = %q ok=%v, want api_forbidden", code, ok)
	}
}

func TestEnsureTags_ACLWrite401And403AreCoded(t *testing.T) {
	for _, tc := range []struct {
		status       int
		wantCode     string
		wantSentinel bool
	}{
		{http.StatusUnauthorized, registry.CodeAPITokenUnauthorized, false},
		{http.StatusForbidden, registry.CodeAPIForbidden, true},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			aclSetup(t)
			if err := credentials.SetAPIKey("tskey-api-test"); err != nil {
				t.Fatalf("SetAPIKey() error = %v", err)
			}
			aclWithTransport(t, func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return aclJSONResponse(http.StatusOK, `{"tagOwners":{}}`), nil
				}
				return aclJSONResponse(tc.status, `{"message":"nope"}`), nil
			})
			err := EnsureTags(context.Background(), []string{"tag:test"})
			code, ok := registry.ErrorCode(err)
			if !ok || code != tc.wantCode {
				t.Fatalf("EnsureTags() error = %v code=%q, want %s", err, code, tc.wantCode)
			}
			if errors.Is(err, ErrPolicyAccessDenied) != tc.wantSentinel {
				t.Fatalf("errors.Is(ErrPolicyAccessDenied) = %v, want %v for HTTP %d: %v", !tc.wantSentinel, tc.wantSentinel, tc.status, err)
			}
			if !strings.Contains(err.Error(), "update ACL for tagOwners") {
				t.Fatalf("message = %q, want operation label", err.Error())
			}
		})
	}
}

func TestListDevicesPaths401BecomeAPITokenUnauthorized(t *testing.T) {
	t.Run("cleanup", func(t *testing.T) {
		setup(t)
		mustSetAPIKey(t, "tskey-api-test")
		withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusUnauthorized, `{"message":"invalid key"}`), nil
		}))
		err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1")})
		code, ok := registry.ErrorCode(err)
		if !ok || code != registry.CodeAPITokenUnauthorized || !strings.Contains(err.Error(), "list devices") {
			t.Fatalf("CleanupStaleNodes() error = %v code=%q, want api_token_unauthorized with list devices label", err, code)
		}
	})
	t.Run("adoption lookup 403", func(t *testing.T) {
		setup(t)
		mustSetAPIKey(t, "tskey-api-test")
		withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusForbidden, `{"message":"forbidden"}`), nil
		}))
		_, _, err := FindExactDeviceNodeID(context.Background(), "host1")
		code, ok := registry.ErrorCode(err)
		if !ok || code != registry.CodeAPIForbidden || !strings.Contains(err.Error(), "list devices for adoption") {
			t.Fatalf("FindExactDeviceNodeID() error = %v code=%q, want api_forbidden with adoption label", err, code)
		}
	})
	t.Run("network error stays uncoded", func(t *testing.T) {
		setup(t)
		mustSetAPIKey(t, "tskey-api-test")
		withDefaultTransport(t, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}))
		err := CleanupStaleNodes(context.Background(), []CleanupTarget{cleanupTarget("host1")})
		if _, coded := registry.ErrorCode(err); coded || err == nil || !strings.Contains(err.Error(), "list devices") {
			t.Fatalf("CleanupStaleNodes() error = %v, want uncoded list devices failure", err)
		}
	})
}
