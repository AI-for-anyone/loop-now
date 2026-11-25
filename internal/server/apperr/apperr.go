// Package apperr defines the business errors shown to the console; the error codes match ErrorCode in api/openapi.yaml.
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error codes, one to one with the OpenAPI ErrorCode enum.
const (
	CodeVerificationInProgress          = "verification_in_progress"
	CodeVerificationOperationConflict   = "verification_operation_conflict"
	CodeVerificationUnsupported         = "verification_unsupported"
	CodeCredentialSelectionModeConflict = "credential_selection_mode_conflict"
	CodeCredentialSelectionRequired     = "credential_selection_required"
	CodeCredentialAccountDeleted        = "credential_account_deleted"
	CodeCredentialScopeMismatch         = "credential_scope_mismatch"
	CodeProjectCredentialRequired       = "project_credential_required"
	CodeProjectModelAuthInProgress      = "model_auth_in_progress"
	CodeSleepConflict                   = "sleep_conflict"
	CodeNotFound                        = "not_found"
	CodeTaskConflict                    = "task_conflict"
	CodeRunNotQueued                    = "run_not_queued"
	CodeRunNotActive                    = "run_not_active"
	CodeActionConflict                  = "action_conflict"
	CodeTaskNotIdle                     = "task_not_idle"
	CodeValidationFailed                = "validation_failed"
	CodePreconditionFailed              = "precondition_failed"
	CodePayloadTooLarge                 = "payload_too_large"
	CodeAgentBusy                       = "agent_busy"
	CodeAgentNotIdle                    = "agent_not_idle"
	CodeSessionMissing                  = "session_missing"
	CodeSessionNone                     = "session_none"
	CodeRuntimeInUse                    = "runtime_in_use"
	CodeConsoleCommandFailed            = "console_command_failed"
	CodeTemplateDeleted                 = "template_deleted"
	CodeNameConflict                    = "name_conflict"
	CodePathConflict                    = "path_conflict"
	CodeForbiddenOrigin                 = "forbidden_origin"
	CodeInvalidHost                     = "invalid_host"
	CodeUnauthenticated                 = "unauthenticated"
	CodeForbidden                       = "forbidden"
	CodeInternal                        = "internal_error"
	CodePluginSourceFailed              = "plugin_source_failed"
	CodePluginConfirmationRequired      = "plugin_confirmation_required"
	CodePluginVersionConflict           = "plugin_version_conflict"
	CodePluginNotEnabled                = "plugin_not_enabled"
	CodePluginBlocked                   = "plugin_blocked"
	CodePluginFailed                    = "plugin_failed"
	CodePluginTimeout                   = "plugin_timeout"
	CodeTextGenerationUnavailable       = "text_generation_unavailable"
	CodeTextGenerationFailed            = "text_generation_failed"
	CodePathPickerUnavailable           = "path_picker_unavailable"
	CodePathPickerBusy                  = "path_picker_busy"
	CodeBackgroundTaskNotFound          = "background_task_not_found"
	CodeBackgroundTaskEnded             = "background_task_ended"
	CodeRateLimited                     = "rate_limited"
)

// FieldError is a field-level error.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error is a business error.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
	Fields  []FieldError
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// As extracts a business error.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Is reports whether err is a business error with the given code.
func Is(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}

// NotFound returns a 404.
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + " not found."}
}

// NotFoundCode returns a 404 with a code of its own.
func NotFoundCode(code, message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: code, Message: message}
}

// Validation returns a 422 carrying field-level errors.
func Validation(fields ...FieldError) *Error {
	msg := "Validation failed."
	if len(fields) > 0 {
		msg = fields[0].Field + ": " + fields[0].Message
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeValidationFailed, Message: msg, Fields: fields}
}

// Field builds a field-level error.
func Field(field, message string) FieldError { return FieldError{Field: field, Message: message} }

// Invalid is shorthand for a validation failure on a single field.
func Invalid(field, message string) *Error { return Validation(Field(field, message)) }

// Conflict returns a 409.
func Conflict(code, message string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: message}
}

// TooLarge returns a 413.
func TooLarge(message string) *Error {
	return &Error{Status: http.StatusRequestEntityTooLarge, Code: CodePayloadTooLarge, Message: message}
}

// PreconditionFailed returns a 412.
func PreconditionFailed(message string) *Error {
	return &Error{Status: http.StatusPreconditionFailed, Code: CodePreconditionFailed, Message: message}
}

// Unauthenticated returns a 401: the token is missing, invalid, expired or revoked. The message never echoes the token.
func Unauthenticated(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthenticated, Message: message}
}

// Forbidden returns a 403: the identity is valid, but it is not allowed to do this.
func Forbidden(message string) *Error {
	return &Error{Status: http.StatusForbidden, Code: CodeForbidden, Message: message}
}
