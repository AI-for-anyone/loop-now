// Package apperr 定义面向控制台的业务错误，错误码与 api/openapi.yaml 中的 ErrorCode 一致。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// 错误码，与 OpenAPI 的 ErrorCode 枚举一一对应。
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
	CodeRateLimited                     = "rate_limited"
)

// FieldError 是字段级错误。
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error 是业务错误。
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
	Fields  []FieldError
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// As 取出业务错误。
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Is 判断 err 是否为指定错误码的业务错误。
func Is(err error, code string) bool {
	e, ok := As(err)
	return ok && e.Code == code
}

// NotFound 返回 404。
func NotFound(what string) *Error {
	return &Error{Status: http.StatusNotFound, Code: CodeNotFound, Message: what + "不存在"}
}

// Validation 返回 422，带字段级错误。
func Validation(fields ...FieldError) *Error {
	msg := "校验失败"
	if len(fields) > 0 {
		msg = fields[0].Field + "：" + fields[0].Message
	}
	return &Error{Status: http.StatusUnprocessableEntity, Code: CodeValidationFailed, Message: msg, Fields: fields}
}

// Field 构造一个字段级错误。
func Field(field, message string) FieldError { return FieldError{Field: field, Message: message} }

// Invalid 是单字段校验失败的简写。
func Invalid(field, message string) *Error { return Validation(Field(field, message)) }

// Conflict 返回 409。
func Conflict(code, message string) *Error {
	return &Error{Status: http.StatusConflict, Code: code, Message: message}
}

// TooLarge 返回 413。
func TooLarge(message string) *Error {
	return &Error{Status: http.StatusRequestEntityTooLarge, Code: CodePayloadTooLarge, Message: message}
}

// PreconditionFailed 返回 412。
func PreconditionFailed(message string) *Error {
	return &Error{Status: http.StatusPreconditionFailed, Code: CodePreconditionFailed, Message: message}
}

// Unauthenticated 返回 401：令牌缺失、无效、过期或已吊销。信息中不回显令牌。
func Unauthenticated(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: CodeUnauthenticated, Message: message}
}

// Forbidden 返回 403：身份有效，但不允许这个操作。
func Forbidden(message string) *Error {
	return &Error{Status: http.StatusForbidden, Code: CodeForbidden, Message: message}
}
