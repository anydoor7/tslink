package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/registry"
	runtimesnapshot "github.com/anydoor7/tslink/internal/runtime"
	tailscale "tailscale.com/client/tailscale/v2"
)

// TestRecoverableServiceFailureIncludesCredentialAuthCodes pins the new
// whitelist entries: an auth-key derivation rejected with 401/403 is persisted
// into runtime.json as a coded per-service failure with recovery steps instead
// of aborting the sync with an opaque error.
func TestRecoverableServiceFailureIncludesCredentialAuthCodes(t *testing.T) {
	svc := registry.Service{Name: "app", Type: registry.TypeProxy, Target: "http://localhost:3000", Tags: []string{"tag:tsmain"}}
	for _, tc := range []struct {
		name     string
		status   int
		code     string
		wantNext string
	}{
		{"unauthorized", http.StatusUnauthorized, registry.CodeAPITokenUnauthorized, credentials.KeysPageURL},
		{"forbidden", http.StatusForbidden, registry.CodeAPIForbidden, "role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			derive := credentials.ClassifyAPIError("derive auth key", tailscale.APIError{Status: tc.status, Message: "synthetic"})
			// startNodeLocked wraps the provider error exactly like this.
			err := fmt.Errorf("auth key for service %q: %w", svc.Name, derive)
			failure, ok := recoverableServiceFailure(svc, err)
			if !ok || failure.RuntimeState != runtimesnapshot.ServiceRuntimeFailed || failure.Error == nil {
				t.Fatalf("recoverableServiceFailure() = %+v, %v; want recoverable coded failure", failure, ok)
			}
			if failure.Error.Code != tc.code {
				t.Fatalf("code = %q, want %s", failure.Error.Code, tc.code)
			}
			if !strings.Contains(strings.Join(failure.Error.Next, "\n"), tc.wantNext) {
				t.Fatalf("next = %v, want %q", failure.Error.Next, tc.wantNext)
			}
			if !strings.Contains(failure.Error.Message, "auth key for service") || !strings.Contains(failure.Error.Message, fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("message = %q, want wrapped chain with HTTP status", failure.Error.Message)
			}
			if failure.Error.Provision != nil {
				t.Fatalf("provision = %+v, want nil for credential failures", failure.Error.Provision)
			}
		})
	}

	// Other derivation failures (network, 5xx) remain non-recoverable so the
	// existing startup abort semantics are unchanged.
	uncoded := fmt.Errorf("auth key for service %q: %w", svc.Name, credentials.ClassifyAPIError("derive auth key", errors.New("dial tcp: refused")))
	if _, ok := recoverableServiceFailure(svc, uncoded); ok {
		t.Fatal("uncoded derivation failure was treated as recoverable")
	}
}
