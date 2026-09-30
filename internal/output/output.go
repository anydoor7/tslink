package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/monody0007/tslink/internal/registry"
)

const (
	SchemaVersion = 1
	SchemaType    = "tslink.result"
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
	Data    any
}

func (e *CodeError) Error() string { return e.Message }

// ErrorData returns optional stable machine-readable context for the failure.
func (e *CodeError) ErrorData() any { return e.Data }

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

// ErrConflictWithData returns a conflict with additive structured context.
func ErrConflictWithData(msg string, data any) *CodeError {
	return &CodeError{Code: ExitConflict, Message: msg, Data: data}
}

// ErrNotFound returns a not-found error (exit code 5).
func ErrNotFound(msg string) *CodeError {
	return &CodeError{Code: ExitNotFound, Message: msg}
}

// ErrorObject is the stable machine/human error payload in the JSON envelope.
type ErrorObject struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Next    []string `json:"next,omitempty"`
	Data    any      `json:"data,omitempty"`
}

// Result is the versioned JSON envelope for structured output.
type Result struct {
	Type          string       `json:"type"`
	OK            bool         `json:"ok"`
	SchemaVersion int          `json:"schema_version"`
	Command       string       `json:"command,omitempty"`
	Code          int          `json:"code"`
	Data          any          `json:"data,omitempty"`
	Error         *ErrorObject `json:"error,omitempty"`
}

func (r Result) MarshalJSON() ([]byte, error) {
	type resultAlias Result
	if r.SchemaVersion == 0 {
		r.SchemaVersion = SchemaVersion
	}
	if r.Type == "" {
		r.Type = SchemaType
	}
	return json.Marshal(resultAlias(r))
}

// NewSuccess returns a successful JSON result.
func NewSuccess(command string, data any) Result {
	return Result{
		OK:            true,
		Type:          SchemaType,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Code:          ExitSuccess,
		Data:          data,
	}
}

// NewFailure returns a failed JSON result with a generic stable error code
// derived from the numeric exit code.
func NewFailure(command string, code int, message string) Result {
	return Result{
		OK:            false,
		Type:          SchemaType,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Code:          code,
		Error:         NewErrorObject(code, message),
	}
}

// NewFailureForError returns a failed JSON result using any stable error code
// carried by err, falling back to the generic numeric-code mapping.
func NewFailureForError(command string, err error) Result {
	code := ExitCode(err)
	return Result{
		OK:            false,
		Type:          SchemaType,
		SchemaVersion: SchemaVersion,
		Command:       command,
		Code:          code,
		Error:         ErrorObjectForError(code, err),
	}
}

// NewErrorObject returns a generic stable error object for code/message.
func NewErrorObject(code int, message string) *ErrorObject {
	return &ErrorObject{Code: StableErrorCode(code), Message: message, Next: defaultNextForExitCode(code)}
}

// ErrorObjectForError returns the structured error object for err.
func ErrorObjectForError(code int, err error) *ErrorObject {
	if err == nil {
		return nil
	}
	var result *ErrorObject
	if stable, next, ok := stableErrorMetadata(err); ok {
		if len(next) == 0 {
			next = defaultNextForExitCode(code)
		}
		result = &ErrorObject{Code: stable, Message: errorMessage(err), Next: next}
	} else {
		result = NewErrorObject(code, err.Error())
	}
	var carrier interface{ ErrorData() any }
	if errors.As(err, &carrier) {
		result.Data = carrier.ErrorData()
	}
	return result
}

func defaultNextForExitCode(code int) []string {
	switch code {
	case ExitUsage:
		return []string{"tslink --help"}
	case ExitAuth:
		return []string{"tslink login"}
	case ExitConflict:
		return []string{"tslink status --json"}
	case ExitNotFound:
		return []string{"tslink list --json"}
	default:
		return nil
	}
}

func stableErrorMetadata(err error) (string, []string, bool) {
	if stable, ok := registry.ErrorCode(err); ok {
		var recovery interface{ NextCommands() []string }
		if errors.As(err, &recovery) {
			return stable, recovery.NextCommands(), true
		}
		return stable, nil, true
	}
	if err != nil && strings.HasPrefix(err.Error(), "unknown config key:") {
		return registry.CodeUnknownConfigKey, []string{"tslink config list"}, true
	}
	return "", nil, false
}

func errorMessage(err error) string {
	var coded registry.CodedError
	if errors.As(err, &coded) {
		if coded.Message != "" {
			return coded.Message
		}
		return coded.Code
	}
	return err.Error()
}

// NextCommandsForError returns the recovery commands a human-mode caller should
// print after the error message: the error's own next list when it carries a
// stable code, otherwise the default continuation for its exit category.
func NextCommandsForError(err error) []string {
	if err == nil {
		return nil
	}
	code := ExitCode(err)
	if _, next, ok := stableErrorMetadata(err); ok && len(next) > 0 {
		return append([]string(nil), next...)
	}
	return defaultNextForExitCode(code)
}

// StableErrorCode maps numeric semantic exit codes to stable machine strings.
func StableErrorCode(code int) string {
	switch code {
	case ExitUsage:
		return "usage_error"
	case ExitAuth:
		return "auth_error"
	case ExitConflict:
		return "conflict"
	case ExitNotFound:
		return "not_found"
	default:
		return "internal_error"
	}
}

// WriteJSON writes a Result as JSON to the given writer.
func WriteJSON(w io.Writer, r Result) {
	data, _ := json.Marshal(r)
	fmt.Fprintf(w, "%s\n", data)
}

// Success writes a successful JSON result to stdout.
func Success(command string, data any) {
	WriteJSON(os.Stdout, NewSuccess(command, data))
}

// Failure writes a failed JSON result to stdout with the given exit code.
func Failure(command string, code int, message string) {
	WriteJSON(os.Stdout, NewFailure(command, code, message))
}

// FailureForError writes a failed JSON result to stdout for err.
func FailureForError(command string, err error) {
	WriteJSON(os.Stdout, NewFailureForError(command, err))
}

// ExitCode extracts the exit code from an error.
// Returns ExitError (1) for non-CodeError errors, ExitSuccess (0) for nil.
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var ce *CodeError
	if errors.As(err, &ce) {
		return ce.Code
	}
	var se *SilentCodeError
	if errors.As(err, &se) {
		return se.Code
	}
	if stable, _, ok := stableErrorMetadata(err); ok {
		return exitCodeForStableError(stable)
	}
	if isUsageErrorMessage(err.Error()) {
		return ExitUsage
	}
	return ExitError
}

func exitCodeForStableError(stable string) int {
	switch stable {
	case registry.CodeInviteRoleInvalid,
		registry.CodeInviteRecipientInvalid,
		registry.CodeInviteIDInvalid,
		registry.CodeInviteKindInvalid,
		registry.CodeInviteRequestInvalid:
		return ExitUsage
	case registry.CodeInviteAPIKeyRequired,
		registry.CodeInviteAPIForbidden,
		registry.CodeInviteAPIUnauthorized,
		registry.CodeAPITokenUnauthorized,
		registry.CodeAPIForbidden:
		return ExitAuth
	case registry.CodeLoginVerifyFailed:
		return ExitError
	case registry.CodeInviteNotFound:
		return ExitNotFound
	case registry.CodeInviteDeviceAmbiguous,
		registry.CodeInviteOwnershipUnproven,
		registry.CodeInviteResendEmailMissing,
		registry.CodeInviteStateConflict,
		registry.CodeLegacyConfigDirPresent:
		return ExitConflict
	case registry.CodeInviteRateLimited,
		registry.CodeInviteResponseInvalid:
		return ExitError
	case registry.CodeFunnelPublicAckRequired,
		registry.CodeFunnelExpiryRequired:
		return ExitUsage
	case registry.CodeServiceTypeAmbiguous,
		registry.CodeInvalidServiceName,
		registry.CodeInvalidTag,
		registry.CodeAllowUnsupportedTCP,
		registry.CodePathMustBeAbsolute,
		registry.CodePathNotFound,
		registry.CodePathNotDirectory,
		registry.CodePathNotAccessible,
		registry.CodePathExposesConfigDir,
		registry.CodeConfigLoadFailed,
		registry.CodeUnknownConfigKey:
		return ExitUsage
	case registry.CodeURLNotReady:
		return ExitNotFound
	case registry.CodeFunnelAllowConflict,
		registry.CodeFunnelControlURLConflict,
		registry.CodeFunnelTypeConflict,
		registry.CodeCredentialURLMismatch:
		return ExitConflict
	default:
		return ExitError
	}
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
