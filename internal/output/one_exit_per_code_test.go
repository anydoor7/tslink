package output

import (
	"errors"
	"fmt"
	"testing"

	"github.com/monody0007/tslink/internal/errcode"
	"github.com/monody0007/tslink/internal/registry"
)

// TestEveryCodeExitsAsTheTableSays: the exit code of a stable code comes from
// the errcode table whatever type carries it. It used to come from a hand
// switch that defaulted to 1, so a code missing from the switch, or carried
// by a different type, exited differently from what the manifest said.
func TestEveryCodeExitsAsTheTableSays(t *testing.T) {
	rows := errcode.All()
	if len(rows) < 50 {
		t.Fatalf("errcode table has %d rows; the probe is blind", len(rows))
	}
	for _, row := range rows {
		carriers := map[string]error{
			"CodedError":         registry.CodedError{Code: row.Code, Message: "probe"},
			"StableCodeError":    &registry.StableCodeError{Code: row.Code, Err: errors.New("probe")},
			"wrapped StableCode": fmt.Errorf("context: %w", &registry.StableCodeError{Code: row.Code, Err: errors.New("probe")}),
			"over a CodeError":   &registry.StableCodeError{Code: row.Code, Err: &CodeError{Code: ExitWarning, Message: "probe"}},
		}
		for carrier, err := range carriers {
			if got := ExitCode(err); got != row.Exit {
				t.Errorf("%s via %s exits %d, want %d", row.Code, carrier, got, row.Exit)
			}
			failure := NewFailureForError("probe", err)
			if failure.Code != row.Exit || failure.Error == nil || failure.Error.Code != row.Code {
				t.Errorf("%s via %s envelope code=%d error=%+v, want %d and %s", row.Code, carrier, failure.Code, failure.Error, row.Exit, row.Code)
			}
		}
	}
}

// TestConflictExitsFourWhateverCarriesIt is A3's probe: cleanup --adopt
// returned CodedError{Code: "conflict"} (exit 1) on one branch and
// output.ErrConflict (exit 4) on the next, and the credential lock carried
// "conflict" in a StableCodeError.
func TestConflictExitsFourWhateverCarriesIt(t *testing.T) {
	for carrier, err := range map[string]error{
		"CodedError":      registry.CodedError{Code: "conflict", Message: "matched 2 devices, 0 of them TSLink-tagged", MessageOnly: true},
		"StableCodeError": &registry.StableCodeError{Code: "conflict", Err: errors.New("credential transaction lock busy")},
		"ErrConflict":     ErrConflict("--adopt requires exactly one literal hostname match; matched 2"),
	} {
		if got := ExitCode(err); got != ExitConflict {
			t.Errorf("conflict via %s exits %d, want %d", carrier, got, ExitConflict)
		}
		if code := NewFailureForError("cleanup", err).Error.Code; code != "conflict" {
			t.Errorf("conflict via %s has error.code %q", carrier, code)
		}
	}
}

// TestInputRefusalExitsAsUsage: link_local_target_refused is an input
// refusal like its sibling path_must_be_absolute, so it exits 2, not 1.
// enrollment_required waits for a person to authorize the node, the auth
// class, so it exits 3.
func TestInputRefusalExitsAsUsage(t *testing.T) {
	if got := ExitCode(registry.LinkLocalTargetRefusedError("http://169.254.169.254:80")); got != ExitUsage {
		t.Errorf("link_local_target_refused exits %d, want %d", got, ExitUsage)
	}
	if got := ExitCode(registry.PathMustBeAbsoluteError("relative")); got != ExitUsage {
		t.Errorf("control path_must_be_absolute exits %d, want %d", got, ExitUsage)
	}
	enrollment := registry.CodedError{Code: registry.CodeEnrollmentRequired, Message: "Authorize TSLink before waiting for a service URL"}
	if got := ExitCode(enrollment); got != ExitAuth {
		t.Errorf("enrollment_required exits %d, want %d", got, ExitAuth)
	}
}
