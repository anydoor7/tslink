package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/monody0007/tslink/internal/registry"
)

func assertJSONKeys(t *testing.T, data []byte, want ...string) {
	t.Helper()
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw object: %v", err)
	}
	if len(raw) != len(want) {
		t.Fatalf("keys = %v, want exactly %v", raw, want)
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing key %q in %v", key, raw)
		}
	}
}

func TestResult_SuccessJSONEnvelope(t *testing.T) {
	r := NewSuccess("", map[string]string{"key": "value"})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertJSONKeys(t, data, "type", "ok", "schema_version", "code", "data")

	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != SchemaType || !got.OK || got.SchemaVersion != SchemaVersion || got.Code != ExitSuccess || got.Error != nil {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestResult_FailureJSONEnvelope(t *testing.T) {
	r := NewFailureForError("", registry.FunnelAllowedUsersError())
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertJSONKeys(t, data, "type", "ok", "schema_version", "code", "error")

	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != SchemaType || got.OK || got.SchemaVersion != SchemaVersion || got.Code != ExitConflict {
		t.Fatalf("unexpected result: %+v", got)
	}
	if got.Error == nil || got.Error.Code != registry.CodeFunnelAllowConflict || got.Error.Message != registry.ErrFunnelAllowedUsers {
		t.Fatalf("error = %+v, want stable code/message", got.Error)
	}
}

func TestCodedValidationErrorCarriesStableCodeExitAndNext(t *testing.T) {
	err := registry.URLNotReadyError("newapp")
	result := NewFailureForError("url", err)
	if result.Code != ExitNotFound || result.Error == nil || result.Error.Code != registry.CodeURLNotReady {
		t.Fatalf("result = %+v, want url_not_ready exit %d", result, ExitNotFound)
	}
	if len(result.Error.Next) != 2 || result.Error.Next[1] != "tslink url newapp --wait=30s" {
		t.Fatalf("next = %v, want deterministic recovery", result.Error.Next)
	}

	unknown := errors.New(`unknown config key: "bogus" (valid keys: control-url)`)
	unknownResult := NewFailureForError("config set", unknown)
	if unknownResult.Code != ExitUsage || unknownResult.Error == nil || unknownResult.Error.Code != registry.CodeUnknownConfigKey {
		t.Fatalf("unknown config result = %+v", unknownResult)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteJSON(&buf, NewSuccess("test", nil))

	var got Result
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK || got.Command != "test" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestCodeError(t *testing.T) {
	tests := []struct {
		name string
		err  *CodeError
		code int
		msg  string
		next string
	}{
		{"auth", ErrAuth("bad key"), ExitAuth, "bad key", "tslink login"},
		{"usage", ErrUsage("bad args"), ExitUsage, "bad args", "tslink --help"},
		{"conflict", ErrConflict("exists"), ExitConflict, "exists", "tslink status --json"},
		{"not found", ErrNotFound("missing"), ExitNotFound, "missing", "tslink list --json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.code {
				t.Errorf("code: got %d, want %d", tt.err.Code, tt.code)
			}
			if tt.err.Error() != tt.msg {
				t.Errorf("msg: got %q, want %q", tt.err.Error(), tt.msg)
			}
			result := NewFailureForError("test", tt.err)
			if result.Error == nil || len(result.Error.Next) != 1 || result.Error.Next[0] != tt.next {
				t.Fatalf("failure next = %v, want exact actionable command %q", result.Error, tt.next)
			}
		})
	}
}

func TestSilentCodeError(t *testing.T) {
	err := SilentExit(ExitWarning)
	if !IsSilent(err) {
		t.Fatal("SilentExit error should be silent")
	}
	if err.Error() != "" {
		t.Fatalf("silent error message = %q, want empty", err.Error())
	}
	if ExitCode(err) != ExitWarning {
		t.Fatalf("ExitCode(SilentExit) = %d, want %d", ExitCode(err), ExitWarning)
	}
	if IsSilent(ErrAuth("bad key")) {
		t.Fatal("CodeError should not be silent")
	}
}

func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestSuccess(t *testing.T) {
	raw := captureStdout(func() {
		Success("test", map[string]string{"k": "v"})
	})

	var got Result
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK {
		t.Error("expected ok=true")
	}
	if got.Command != "test" {
		t.Errorf("command: got %q, want %q", got.Command, "test")
	}
	if got.Code != ExitSuccess {
		t.Errorf("code: got %d, want %d", got.Code, ExitSuccess)
	}
	if got.Data == nil {
		t.Error("expected data to be present")
	}
}

func TestFailure(t *testing.T) {
	raw := captureStdout(func() {
		Failure("fail", ExitNotFound, "not found")
	})

	var got Result
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK {
		t.Error("expected ok=false")
	}
	if got.Code != ExitNotFound {
		t.Errorf("code: got %d, want %d", got.Code, ExitNotFound)
	}
	if got.Error == nil || got.Error.Code != "not_found" || got.Error.Message != "not found" {
		t.Errorf("error: got %+v, want not_found/not found", got.Error)
	}
}

func TestFailureForErrorWritesStableAgentRecoveryEnvelope(t *testing.T) {
	raw := captureStdout(func() {
		FailureForError("serve", registry.ServiceStartTimeoutError("stuck", 30*time.Second))
	})
	var got Result
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK || got.Code != ExitError || got.Error == nil || got.Error.Code != registry.CodeServiceStartTimeout || len(got.Error.Next) != 2 {
		t.Fatalf("failure envelope = %+v, want stable startup timeout recovery", got)
	}
}

func TestStableAgentErrorsMapToSemanticExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "funnel public acknowledgement", err: registry.FunnelPublicAckError(), want: ExitUsage},
		{name: "service type", err: registry.ServiceTypeAmbiguousError(), want: ExitUsage},
		{name: "runtime timeout", err: registry.ServiceStartTimeoutError("stuck", time.Second), want: ExitError},
		{name: "funnel capability", err: registry.FunnelCapabilityMissingError("public-app", errors.New("missing capability")), want: ExitError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != ExitSuccess {
		t.Error("nil should return ExitSuccess")
	}
	if ExitCode(errors.New("generic")) != ExitError {
		t.Error("generic error should return ExitError")
	}
	if ExitCode(errors.New("accepts 1 arg(s), received 0")) != ExitUsage {
		t.Error("cobra arg error should return ExitUsage")
	}
	if ExitCode(errors.New(`unknown command "bogus" for "tslink"`)) != ExitUsage {
		t.Error("cobra unknown command error should return ExitUsage")
	}
	if ExitCode(ErrUsage("bad args")) != ExitUsage {
		t.Error("ErrUsage should return ExitUsage")
	}
	if ExitCode(ErrNotFound("x")) != ExitNotFound {
		t.Error("ErrNotFound should return ExitNotFound")
	}
	if ExitCode(ErrAuth("x")) != ExitAuth {
		t.Error("ErrAuth should return ExitAuth")
	}
	if ExitCode(ErrConflict("x")) != ExitConflict {
		t.Error("ErrConflict should return ExitConflict")
	}
	if ExitCode(SilentExit(ExitCritical)) != ExitCritical {
		t.Error("SilentExit should return its embedded exit code")
	}
}
