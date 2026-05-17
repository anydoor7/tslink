package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

func TestResult_JSON(t *testing.T) {
	r := Result{
		OK:      true,
		Command: "test",
		Code:    ExitSuccess,
		Data:    map[string]string{"key": "value"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK || got.Command != "test" || got.Code != ExitSuccess {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestResult_FailureJSON(t *testing.T) {
	r := Result{
		OK:      false,
		Command: "fail",
		Code:    ExitNotFound,
		Error:   "not found",
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Result
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK || got.Code != ExitNotFound || got.Error != "not found" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	WriteJSON(&buf, Result{OK: true, Command: "test", Code: 0})

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
	}{
		{"auth", ErrAuth("bad key"), ExitAuth, "bad key"},
		{"conflict", ErrConflict("exists"), ExitConflict, "exists"},
		{"not found", ErrNotFound("missing"), ExitNotFound, "missing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.code {
				t.Errorf("code: got %d, want %d", tt.err.Code, tt.code)
			}
			if tt.err.Error() != tt.msg {
				t.Errorf("msg: got %q, want %q", tt.err.Error(), tt.msg)
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
	if got.Error != "not found" {
		t.Errorf("error: got %q, want %q", got.Error, "not found")
	}
}

func TestExitCode(t *testing.T) {
	if ExitCode(nil) != ExitSuccess {
		t.Error("nil should return ExitSuccess")
	}
	if ExitCode(errors.New("generic")) != ExitError {
		t.Error("generic error should return ExitError")
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
