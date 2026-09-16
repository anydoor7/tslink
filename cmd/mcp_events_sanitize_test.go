package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// snapshotErrorSentinel is the credential this test smuggles into a snapshot
// failure. It exists only here; the product code must never contain it, or the
// "does not appear" assertions below would be tautologies.
const snapshotErrorSentinel = "tskey-auth-SNAPSHOTERRSENTINEL"

func TestSanitizedSnapshotErrorRedactsCredentials(t *testing.T) {
	in := fmt.Errorf("read registry: bad key %s at offset 12", snapshotErrorSentinel)
	got := sanitizedSnapshotError(in).Error()

	if strings.Contains(got, snapshotErrorSentinel) {
		t.Fatalf("credential survived sanitization: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("expected a redaction marker, got %q", got)
	}
	// The non-secret context must survive, or the sanitizer is just erasing.
	if !strings.Contains(got, "read registry") || !strings.Contains(got, "offset 12") {
		t.Fatalf("sanitizer erased non-secret context: %q", got)
	}
}

// TestSanitizedSnapshotErrorLeavesCleanErrorsAlone is the control group for the
// test above. Without it, a sanitizer that blanket-erased every error would
// pass the "credential does not appear" assertion just as well.
func TestSanitizedSnapshotErrorLeavesCleanErrorsAlone(t *testing.T) {
	sentinelErr := errors.New("registry.json: unexpected end of JSON input")
	wrapped := fmt.Errorf("build state: %w", sentinelErr)

	got := sanitizedSnapshotError(wrapped)

	if got.Error() != wrapped.Error() {
		t.Fatalf("clean error was altered:\n got %q\nwant %q", got.Error(), wrapped.Error())
	}
	if !errors.Is(got, sentinelErr) {
		t.Fatal("clean error lost its wrap chain; errors.Is no longer matches")
	}
}

func TestSanitizedSnapshotErrorHandlesNil(t *testing.T) {
	if got := sanitizedSnapshotError(nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// TestMCPEventsSnapshotFnSanitizesActionErrors drives the real builder, so it
// fails if a future refactor returns an action error without sanitizing it.
func TestMCPEventsSnapshotFnSanitizesActionErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() mcpActions
	}{
		{
			name: "list action fails",
			build: func() mcpActions {
				return mcpActions{
					list:   func() (any, error) { return nil, fmt.Errorf("list: %s expired", snapshotErrorSentinel) },
					status: func() (any, error) { return map[string]any{}, nil },
				}
			},
		},
		{
			name: "status action fails",
			build: func() mcpActions {
				return mcpActions{
					list:   func() (any, error) { return map[string]any{}, nil },
					status: func() (any, error) { return nil, fmt.Errorf("status: %s expired", snapshotErrorSentinel) },
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fn := mcpEventsSnapshotFn(tc.build())
			if fn == nil {
				t.Fatal("snapshot builder was nil")
			}
			_, err := fn(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			if strings.Contains(err.Error(), snapshotErrorSentinel) {
				t.Fatalf("credential reached the caller: %q", err.Error())
			}
		})
	}
}
