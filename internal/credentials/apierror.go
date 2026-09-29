package credentials

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/monody0007/tslink/internal/registry"
	tailscale "tailscale.com/client/tailscale/v2"
)

// ClassifyAPIError turns a Tailscale REST failure into a stable, actionable
// error. HTTP 401 means the credential itself is expired, revoked, or invalid
// and maps to api_token_unauthorized with the key-bootstrap steps; HTTP 403
// means the credential is valid but lacks the role or scope and maps to
// api_forbidden. Every other failure is returned wrapped with the operation
// label and no stable code, exactly as before. The wrapped chain is preserved
// so callers that classify with errors.Is keep working.
func ClassifyAPIError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr tailscale.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return &registry.StableCodeError{
				Code: registry.CodeAPITokenUnauthorized,
				Next: NextAPIKeyBootstrap(),
				Err:  fmt.Errorf("%s: Tailscale rejected the API credential as unauthenticated (HTTP 401); the token is expired, revoked, or invalid: %w", operation, err),
			}
		case http.StatusForbidden:
			return &registry.StableCodeError{
				Code: registry.CodeAPIForbidden,
				Next: NextAPIForbidden(),
				Err:  fmt.Errorf("%s: Tailscale refused the API credential (HTTP 403); its user role or OAuth scopes do not permit this operation: %w", operation, err),
			}
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// VerificationResultForError maps a remote probe outcome to the stored
// last_verified_result vocabulary.
func VerificationResultForError(err error) string {
	if err == nil {
		return VerifyResultOK
	}
	var apiErr tailscale.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case http.StatusUnauthorized:
			return VerifyResultUnauthorized
		case http.StatusForbidden:
			return VerifyResultForbidden
		}
	}
	return VerifyResultUnreachable
}

// ProbeOutcome is the value-free result of one remote credential probe.
type ProbeOutcome struct {
	Slot     string
	Present  bool
	Result   string
	Recorded bool
	// Cause is the sanitized probe failure; nil when Result is ok.
	Cause error
}

// Testable seams for remote probing. Production builds a per-slot client and
// lists devices, the cheapest read every credential kind is allowed to make.
var (
	probeClientFactoryFn = func(slot, value string) (*tailscale.Client, error) {
		switch slot {
		case SlotAPIKey:
			return NewTailscaleClientWithAPIKey(value)
		case SlotClientSecret:
			return newTailscaleClientWithOAuthSecret(value)
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
		}
	}
	probeListDevicesFn = func(ctx context.Context, client *tailscale.Client) error {
		_, err := client.Devices().List(ctx)
		return err
	}
)

// ProbeStoredCredential verifies one stored slot against the Tailscale API and
// records the outcome in credential-meta.json. The returned error covers only
// local failures (unreadable store, unknown slot); remote rejection is data in
// ProbeOutcome so doctor can classify it without parsing text.
func ProbeStoredCredential(ctx context.Context, slot string, now time.Time) (ProbeOutcome, error) {
	outcome := ProbeOutcome{Slot: slot}
	var value string
	var err error
	switch slot {
	case SlotAPIKey:
		value, err = GetAPIKey()
	case SlotClientSecret:
		value, err = GetClientSecret()
	default:
		return outcome, fmt.Errorf("%w: %q", ErrUnknownCredentialSlot, slot)
	}
	if err != nil {
		return outcome, fmt.Errorf("read %s credential: %w", slot, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return outcome, nil
	}
	outcome.Present = true

	client, err := probeClientFactoryFn(slot, value)
	if err != nil {
		outcome.Result = VerifyResultUnreachable
		outcome.Cause = fmt.Errorf("build %s API client: %w", slot, err)
	} else if client == nil {
		outcome.Result = VerifyResultUnreachable
		outcome.Cause = fmt.Errorf("build %s API client: empty client", slot)
	} else {
		probeErr := probeListDevicesFn(ctx, client)
		outcome.Result = VerificationResultForError(probeErr)
		if probeErr != nil {
			outcome.Cause = ClassifyAPIError("probe "+slot+" credential", probeErr)
		}
	}
	if recordErr := RecordVerification(slot, Fingerprint(value), outcome.Result, now); recordErr == nil {
		outcome.Recorded = true
	} else {
		outcome.Cause = errors.Join(outcome.Cause, fmt.Errorf("record %s verification: %w", slot, recordErr))
	}
	return outcome, nil
}
