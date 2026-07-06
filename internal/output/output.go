package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Semantic exit codes for programmatic consumers.
const (
	ExitSuccess  = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitAuth     = 3
	ExitConflict = 4
	ExitNotFound = 5
	ExitWarning  = 64
	ExitCritical = 65
)

// CodeError is an error that carries a semantic exit code.
type CodeError struct {
	Code    int
	Message string
}

func (e *CodeError) Error() string { return e.Message }

// SilentCodeError carries an exit code for commands that have already printed
// their complete result and should not receive the generic failure envelope.
type SilentCodeError struct {
	Code int
}

func (e *SilentCodeError) Error() string { return "" }

// SilentExit returns a non-printing error for the requested exit code.
func SilentExit(code int) *SilentCodeError {
	return &SilentCodeError{Code: code}
}

// IsSilent reports whether err should set an exit code without extra output.
func IsSilent(err error) bool {
	_, ok := err.(*SilentCodeError)
	return ok
}

// ErrAuth returns an authentication error (exit code 3).
func ErrAuth(msg string) *CodeError {
	return &CodeError{Code: ExitAuth, Message: msg}
}

// ErrUsage returns a usage error (exit code 2).
func ErrUsage(msg string) *CodeError {
	return &CodeError{Code: ExitUsage, Message: msg}
}

// ErrConflict returns a conflict error (exit code 4).
func ErrConflict(msg string) *CodeError {
	return &CodeError{Code: ExitConflict, Message: msg}
}

// ErrNotFound returns a not-found error (exit code 5).
func ErrNotFound(msg string) *CodeError {
	return &CodeError{Code: ExitNotFound, Message: msg}
}

// Result is the JSON envelope for structured output.
type Result struct {
	OK      bool   `json:"ok"`
	Command string `json:"command,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    int    `json:"code"`
	Data    any    `json:"data,omitempty"`
}

// WriteJSON writes a Result as JSON to the given writer.
func WriteJSON(w io.Writer, r Result) {
	data, _ := json.Marshal(r)
	fmt.Fprintf(w, "%s\n", data)
}

// Success writes a successful JSON result to stdout.
func Success(command string, data any) {
	WriteJSON(os.Stdout, Result{
		OK:      true,
		Command: command,
		Code:    ExitSuccess,
		Data:    data,
	})
}

// Failure writes a failed JSON result to stdout with the given exit code.
func Failure(command string, code int, message string) {
	WriteJSON(os.Stdout, Result{
		OK:      false,
		Command: command,
		Code:    code,
		Error:   message,
	})
}

// ExitCode extracts the exit code from an error.
// Returns ExitError (1) for non-CodeError errors, ExitSuccess (0) for nil.
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	if ce, ok := err.(*CodeError); ok {
		return ce.Code
	}
	if se, ok := err.(*SilentCodeError); ok {
		return se.Code
	}
	if isUsageErrorMessage(err.Error()) {
		return ExitUsage
	}
	return ExitError
}

func isUsageErrorMessage(msg string) bool {
	return strings.HasPrefix(msg, "unknown command ") ||
		strings.HasPrefix(msg, "unknown flag: ") ||
		strings.HasPrefix(msg, "unknown shorthand flag: ") ||
		strings.Contains(msg, "required flag(s)") ||
		(strings.Contains(msg, "accepts ") && strings.Contains(msg, " arg(s)")) ||
		(strings.Contains(msg, "requires ") && strings.Contains(msg, " arg(s)")) ||
		(strings.Contains(msg, "requires ") && strings.Contains(msg, " argument"))
}
