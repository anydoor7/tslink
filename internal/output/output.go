package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Semantic exit codes for programmatic consumers.
const (
	ExitSuccess  = 0
	ExitError    = 1
	ExitUsage    = 2
	ExitAuth     = 3
	ExitConflict = 4
	ExitNotFound = 5
)

// CodeError is an error that carries a semantic exit code.
type CodeError struct {
	Code    int
	Message string
}

func (e *CodeError) Error() string { return e.Message }

// ErrAuth returns an authentication error (exit code 3).
func ErrAuth(msg string) *CodeError {
	return &CodeError{Code: ExitAuth, Message: msg}
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
	return ExitError
}
