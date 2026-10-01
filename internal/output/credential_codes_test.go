package output

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/anydoor7/tslink/internal/credentials"
	"github.com/anydoor7/tslink/internal/registry"
)

func TestCredentialAuthCodesMapToAuthExitAndCarryNext(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantExit int
		wantNext string
	}{
		{"invite unauthorized", registry.CodedError{Code: registry.CodeInviteAPIUnauthorized, Message: "401", Next: credentials.NextAPIKeyBootstrap(), MessageOnly: true}, ExitAuth, credentials.KeysPageURL},
		{"api token unauthorized", &registry.StableCodeError{Code: registry.CodeAPITokenUnauthorized, Next: credentials.NextAPIKeyBootstrap(), Err: errors.New("derive auth key: 401")}, ExitAuth, credentials.KeysPageURL},
		{"api forbidden", &registry.StableCodeError{Code: registry.CodeAPIForbidden, Next: credentials.NextAPIForbidden(), Err: errors.New("403")}, ExitAuth, "tslink doctor --probe-remote"},
		{"login verify failed", &registry.StableCodeError{Code: registry.CodeLoginVerifyFailed, Next: []string{"retry"}, Err: errors.New("verify")}, ExitError, "retry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.wantExit {
				t.Fatalf("ExitCode() = %d, want %d", got, tc.wantExit)
			}
			result := NewFailureForError("probe", tc.err)
			code, _ := registry.ErrorCode(tc.err)
			if result.Error == nil || result.Error.Code != code || result.Code != tc.wantExit {
				t.Fatalf("envelope = %+v, want code %s exit %d", result, code, tc.wantExit)
			}
			if !strings.Contains(strings.Join(result.Error.Next, "\n"), tc.wantNext) {
				t.Fatalf("envelope next = %v, want %q", result.Error.Next, tc.wantNext)
			}
			if !reflect.DeepEqual(NextCommandsForError(tc.err), result.Error.Next) {
				t.Fatalf("NextCommandsForError() = %v, envelope next = %v; human and JSON must agree", NextCommandsForError(tc.err), result.Error.Next)
			}
			// StableCodeError keeps the wrapped message for humans.
			if !strings.Contains(result.Error.Message, strings.TrimSpace(strings.SplitN(tc.err.Error(), ":", 2)[0])) {
				t.Fatalf("message = %q, want wrapped error text", result.Error.Message)
			}
		})
	}
}

func TestDefaultAuthNextIsSingleLoginCommand(t *testing.T) {
	// A generic exit-3 auth error covers the Tier 1 browser-login case, so its
	// default guidance is only "tslink login". The three-step tskey-api-*
	// bootstrap is reserved for the specific credential codes (invite/api-token
	// unauthorized, status/doctor expiry branches), not this generic default.
	err := ErrAuth("not authenticated")
	next := NextCommandsForError(err)
	if !reflect.DeepEqual(next, []string{"tslink login"}) {
		t.Fatalf("default auth next = %v, want exactly [tslink login]", next)
	}
	envelope := NewFailureForError("status", err)
	if !reflect.DeepEqual(envelope.Error.Next, next) {
		t.Fatalf("envelope next = %v, human next = %v", envelope.Error.Next, next)
	}
}

func TestNextCommandsForErrorDefaultsAndNil(t *testing.T) {
	if NextCommandsForError(nil) != nil {
		t.Fatal("nil error must have no next")
	}
	if got := NextCommandsForError(errors.New("boom")); got != nil {
		t.Fatalf("plain error next = %v, want none", got)
	}
	if got := NextCommandsForError(ErrUsage("bad")); !reflect.DeepEqual(got, []string{"tslink --help"}) {
		t.Fatalf("usage next = %v", got)
	}
	// A coded error without its own next falls back to the exit-category default.
	coded := registry.CodedError{Code: registry.CodeInviteNotFound, Message: "missing"}
	if got := NextCommandsForError(coded); !reflect.DeepEqual(got, []string{"tslink list --json"}) {
		t.Fatalf("coded-without-next = %v, want not-found default", got)
	}
	// The returned slice is a copy.
	source := &registry.StableCodeError{Code: registry.CodeAPIForbidden, Next: []string{"a"}, Err: errors.New("x")}
	got := NextCommandsForError(source)
	got[0] = "mutated"
	if source.Next[0] != "a" {
		t.Fatal("NextCommandsForError() aliased the error's next slice")
	}
}

func TestStableCodeErrorAccessors(t *testing.T) {
	inner := errors.New("inner")
	err := &registry.StableCodeError{Code: "x", Next: []string{"n"}, Err: inner}
	if err.Error() != "inner" || !errors.Is(err, inner) || err.StableCode() != "x" {
		t.Fatalf("StableCodeError accessors wrong: %+v", err)
	}
	next := err.NextCommands()
	next[0] = "mutated"
	if err.Next[0] != "n" {
		t.Fatal("NextCommands aliased the slice")
	}
	if (&registry.StableCodeError{Code: "only-code"}).Error() != "only-code" {
		t.Fatal("nil Err must render the code")
	}
}
