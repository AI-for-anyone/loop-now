// Package output 定义 loopctl 的输出信封、错误类别、退出码与错误码。
// 错误码、类别与退出码的对应关系只在本包中定义；schema、内置 Skill 与测试都以这里为准。
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Category 是错误类别，每个类别对应一个退出码。
type Category string

// 错误类别。
const (
	CategoryInternal        Category = "internal"
	CategoryUsage           Category = "usage"
	CategoryConfig          Category = "config"
	CategoryUnavailable     Category = "unavailable"
	CategoryUnauthenticated Category = "unauthenticated"
	CategoryForbidden       Category = "forbidden"
	CategoryNotFound        Category = "not_found"
	CategoryConflict        Category = "conflict"
	CategoryInvalid         Category = "invalid"
	CategoryServer          Category = "server"
)

// ExitInfo 描述一个退出码。
type ExitInfo struct {
	Code        int      `json:"code"`
	Category    Category `json:"category,omitempty"`
	Description string   `json:"description"`
}

// exitTable 是退出码表，按退出码排列。
var exitTable = []ExitInfo{
	{0, "", "成功，可能带警告"},
	{1, CategoryInternal, "loopctl 自身出错；服务端的成功响应无法解析；服务端返回重定向"},
	{2, CategoryUsage, "命令或参数有误；当前身份不能使用这个命令；缺少确认；本地输入文件读不了，或输出文件写不了"},
	{3, CategoryConfig, "连接配置缺失、冲突或无效；令牌格式与身份不符；证书校验失败"},
	{4, CategoryUnavailable, "连接失败、超时，或 HTTP 502、503、504"},
	{5, CategoryUnauthenticated, "HTTP 401"},
	{6, CategoryForbidden, "HTTP 403"},
	{7, CategoryNotFound, "HTTP 404"},
	{8, CategoryConflict, "HTTP 409、412"},
	{9, CategoryInvalid, "HTTP 400、413、422"},
	{10, CategoryServer, "HTTP 500，以及其他未列出的状态码"},
}

// ExitTable 返回退出码表的副本。
func ExitTable() []ExitInfo { return append([]ExitInfo(nil), exitTable...) }

// ExitCode 返回类别对应的退出码；未知类别按 internal 处理。
func ExitCode(c Category) int {
	for _, e := range exitTable {
		if e.Category == c && e.Code != 0 {
			return e.Code
		}
	}
	return 1
}

// CategoryForStatus 按 HTTP 状态码确定错误类别。
func CategoryForStatus(status int) Category {
	switch {
	case status >= 300 && status < 400:
		return CategoryInternal
	case status == http.StatusBadGateway, status == http.StatusServiceUnavailable, status == http.StatusGatewayTimeout:
		return CategoryUnavailable
	case status == http.StatusUnauthorized:
		return CategoryUnauthenticated
	case status == http.StatusForbidden:
		return CategoryForbidden
	case status == http.StatusNotFound:
		return CategoryNotFound
	case status == http.StatusConflict, status == http.StatusPreconditionFailed:
		return CategoryConflict
	case status == http.StatusBadRequest, status == http.StatusRequestEntityTooLarge, status == http.StatusUnprocessableEntity:
		return CategoryInvalid
	default:
		return CategoryServer
	}
}

// loopctl 自身产生的错误码。服务端返回的错误码原样保留，不在此列。
const (
	CodeInternalError      = "internal_error"
	CodeUnexpectedRedirect = "unexpected_redirect"
	CodeUnexpectedResponse = "unexpected_response"

	CodeUnknownCommand       = "unknown_command"
	CodeInvalidArgument      = "invalid_argument"
	CodeMissingArgument      = "missing_argument"
	CodeCommandNotAllowed    = "command_not_allowed"
	CodeConfirmationRequired = "confirmation_required"
	CodeInputUnreadable      = "input_unreadable"
	CodeOutputUnwritable     = "output_unwritable"
	CodeSymlinkNotSupported  = "symlink_not_supported"

	CodeConnectionNotConfigured   = "connection_not_configured"
	CodeIdentityConflict          = "identity_conflict"
	CodeInvalidServerURL          = "invalid_server_url"
	CodeCAFileUnreadable          = "ca_file_unreadable"
	CodeTokenKindMismatch         = "token_kind_mismatch"
	CodeTLSVerifyFailed           = "tls_verify_failed"
	CodeProfileNotFound           = "profile_not_found"
	CodeConfigInvalid             = "config_invalid"
	CodeConfigInsecurePermissions = "config_insecure_permissions"

	CodeConnectionFailed = "connection_failed"
	CodeTimeout          = "timeout"
	CodeServerUnhealthy  = "server_unhealthy" // health：服务端报告依赖不可用（HTTP 503）

	CodeNotFound = "not_found" // 按地址找不到 Agent；与服务端 404 的错误码相同
)

// localCodes 是本地错误码到类别的对应。unexpected_response 的类别随 HTTP 状态码变化，这里记的是 2xx 时的类别。
var localCodes = map[string]Category{
	CodeInternalError:      CategoryInternal,
	CodeUnexpectedRedirect: CategoryInternal,
	CodeUnexpectedResponse: CategoryInternal,

	CodeUnknownCommand:       CategoryUsage,
	CodeInvalidArgument:      CategoryUsage,
	CodeMissingArgument:      CategoryUsage,
	CodeCommandNotAllowed:    CategoryUsage,
	CodeConfirmationRequired: CategoryUsage,
	CodeInputUnreadable:      CategoryUsage,
	CodeOutputUnwritable:     CategoryUsage,
	CodeSymlinkNotSupported:  CategoryUsage,

	CodeConnectionNotConfigured:   CategoryConfig,
	CodeIdentityConflict:          CategoryConfig,
	CodeInvalidServerURL:          CategoryConfig,
	CodeCAFileUnreadable:          CategoryConfig,
	CodeTokenKindMismatch:         CategoryConfig,
	CodeTLSVerifyFailed:           CategoryConfig,
	CodeProfileNotFound:           CategoryConfig,
	CodeConfigInvalid:             CategoryConfig,
	CodeConfigInsecurePermissions: CategoryConfig,

	CodeConnectionFailed: CategoryUnavailable,
	CodeTimeout:          CategoryUnavailable,
	CodeServerUnhealthy:  CategoryUnavailable,

	CodeNotFound: CategoryNotFound,
}

// CodeInfo 描述一个本地错误码。
type CodeInfo struct {
	Code     string   `json:"code"`
	Category Category `json:"category"`
}

// LocalCodes 返回全部本地错误码，按类别的退出码、再按名称排序。
func LocalCodes() []CodeInfo {
	out := make([]CodeInfo, 0, len(localCodes))
	for code, cat := range localCodes {
		out = append(out, CodeInfo{code, cat})
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := ExitCode(out[i].Category), ExitCode(out[j].Category); a != b {
			return a < b
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// Error 是输出中的 error 对象，也是命令返回的错误类型。
type Error struct {
	Code       string          `json:"code"`
	Category   Category        `json:"category"`
	Message    string          `json:"message"`
	HTTPStatus int             `json:"httpStatus,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	Fields     json.RawMessage `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf 创建一个本地错误，类别由错误码决定。
func Errorf(code, format string, args ...any) *Error {
	cat, ok := localCodes[code]
	if !ok {
		cat = CategoryInternal
	}
	return &Error{Code: code, Category: cat, Message: fmt.Sprintf(format, args...)}
}

// WithDetails 给错误附上详情；详情无法编码时保持不变。
func (e *Error) WithDetails(v any) *Error {
	if b, err := marshal(v); err == nil {
		e.Details = b
	}
	return e
}

// Warning 是 warnings 数组中的一项。
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// 警告码。
const (
	WarnVersionMismatch      = "version_mismatch"
	WarnServerVersionUnknown = "server_version_unknown"
)

// Meta 是输出中的 meta 对象。
type Meta struct {
	ClientVersion string `json:"clientVersion"`
	Identity      string `json:"identity,omitempty"`
	Profile       string `json:"profile,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
	RequestID     string `json:"requestId,omitempty"`
	Attempts      int    `json:"attempts,omitempty"`
}

type success struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Warnings []Warning       `json:"warnings"`
	Meta     Meta            `json:"meta"`
}

type failure struct {
	OK       bool      `json:"ok"`
	Error    *Error    `json:"error"`
	Warnings []Warning `json:"warnings"`
	Meta     Meta      `json:"meta"`
}

// WriteSuccess 写出成功信封。data 为 json.RawMessage 时原样（压缩空白后）使用，否则编码为 JSON；nil 输出为 null。
func WriteSuccess(w io.Writer, data any, warnings []Warning, meta Meta) error {
	raw, err := Raw(data)
	if err != nil {
		return err
	}
	return writeLine(w, success{OK: true, Data: raw, Warnings: nonNil(warnings), Meta: meta})
}

// WriteFailure 写出失败信封。
func WriteFailure(w io.Writer, e *Error, warnings []Warning, meta Meta) error {
	return writeLine(w, failure{OK: false, Error: e, Warnings: nonNil(warnings), Meta: meta})
}

// Raw 把任意值转为单行 JSON；json.RawMessage 只压缩空白，校验其为合法 JSON。
func Raw(v any) (json.RawMessage, error) {
	switch x := v.(type) {
	case nil:
		return json.RawMessage("null"), nil
	case json.RawMessage:
		if len(x) == 0 {
			return json.RawMessage("null"), nil
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, x); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		return marshal(v)
	}
}

func marshal(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func writeLine(w io.Writer, v any) error {
	b, err := marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func nonNil(ws []Warning) []Warning {
	if ws == nil {
		return []Warning{}
	}
	return ws
}
