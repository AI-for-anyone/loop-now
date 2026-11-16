// Package output defines loopctl's output envelope, error categories, exit codes and error codes.
// The mapping between error codes, categories and exit codes is defined only in this package; the schema, the
// built-in Skill and the tests all treat it as authoritative.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Category is an error category; each category maps to one exit code.
type Category string

// Error categories.
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

// ExitInfo describes one exit code.
type ExitInfo struct {
	Code        int      `json:"code"`
	Category    Category `json:"category,omitempty"`
	Description string   `json:"description"`
}

// exitTable is the exit code table, ordered by exit code.
var exitTable = []ExitInfo{
	{0, "", "Success, possibly with warnings"},
	{1, CategoryInternal, "loopctl itself failed; a successful server response could not be parsed; the server returned a redirect"},
	{2, CategoryUsage, "Wrong command or arguments; the current identity cannot use this command; confirmation missing; a local input file cannot be read or an output file cannot be written"},
	{3, CategoryConfig, "Connection configuration missing, conflicting or invalid; token format does not match the identity; certificate verification failed"},
	{4, CategoryUnavailable, "Connection failed or timed out, or HTTP 502, 503, 504"},
	{5, CategoryUnauthenticated, "HTTP 401"},
	{6, CategoryForbidden, "HTTP 403"},
	{7, CategoryNotFound, "HTTP 404"},
	{8, CategoryConflict, "HTTP 409, 412"},
	{9, CategoryInvalid, "HTTP 400, 413, 422"},
	{10, CategoryServer, "HTTP 500 and any other status code not listed"},
}

// ExitTable returns a copy of the exit code table.
func ExitTable() []ExitInfo { return append([]ExitInfo(nil), exitTable...) }

// ExitCode returns the exit code for a category; unknown categories are treated as internal.
func ExitCode(c Category) int {
	for _, e := range exitTable {
		if e.Category == c && e.Code != 0 {
			return e.Code
		}
	}
	return 1
}

// CategoryForStatus determines the error category from an HTTP status code.
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

// Error codes produced by loopctl itself. Error codes returned by the server are kept as they are and are not
// listed here.
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
	CodeServerUnhealthy  = "server_unhealthy" // health: the server reports that a dependency is unavailable (HTTP 503)

	CodeNotFound = "not_found" // no Agent found at the address; same error code as the server's 404
)

// localCodes maps local error codes to categories. The category of unexpected_response depends on the HTTP status
// code; the one recorded here is the category for a 2xx.
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

// CodeInfo describes one local error code.
type CodeInfo struct {
	Code     string   `json:"code"`
	Category Category `json:"category"`
}

// LocalCodes returns all local error codes, sorted by the exit code of their category and then by name.
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

// Error is the error object in the output, and also the error type commands return.
type Error struct {
	Code       string          `json:"code"`
	Category   Category        `json:"category"`
	Message    string          `json:"message"`
	HTTPStatus int             `json:"httpStatus,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	Fields     json.RawMessage `json:"fields,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf creates a local error; its category is determined by the error code.
func Errorf(code, format string, args ...any) *Error {
	cat, ok := localCodes[code]
	if !ok {
		cat = CategoryInternal
	}
	return &Error{Code: code, Category: cat, Message: fmt.Sprintf(format, args...)}
}

// WithDetails attaches details to the error; if the details cannot be encoded, the error is left unchanged.
func (e *Error) WithDetails(v any) *Error {
	if b, err := marshal(v); err == nil {
		e.Details = b
	}
	return e
}

// Warning is one entry in the warnings array.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Warning codes.
const (
	WarnVersionMismatch      = "version_mismatch"
	WarnServerVersionUnknown = "server_version_unknown"
)

// Meta is the meta object in the output.
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

// WriteSuccess writes a success envelope. When data is a json.RawMessage it is used as is (after compacting
// whitespace), otherwise it is encoded as JSON; nil is written as null.
func WriteSuccess(w io.Writer, data any, warnings []Warning, meta Meta) error {
	raw, err := Raw(data)
	if err != nil {
		return err
	}
	return writeLine(w, success{OK: true, Data: raw, Warnings: nonNil(warnings), Meta: meta})
}

// WriteFailure writes a failure envelope.
func WriteFailure(w io.Writer, e *Error, warnings []Warning, meta Meta) error {
	return writeLine(w, failure{OK: false, Error: e, Warnings: nonNil(warnings), Meta: meta})
}

// Raw turns any value into single-line JSON; a json.RawMessage only has its whitespace compacted, which also
// checks that it is valid JSON.
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
