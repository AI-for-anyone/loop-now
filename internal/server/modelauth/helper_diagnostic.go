package modelauth

import "errors"

// helperFailure carries only platform-authored fixed identifiers. It never
// retains a provider error, IPC record, URL, credential or subprocess output.
type helperFailure struct {
	cause    error
	stage    string
	provider providerDiagnostic
}

func (e *helperFailure) Error() string { return e.cause.Error() + ": " + e.stage }
func (e *helperFailure) Unwrap() error { return e.cause }
func helperFailed(stage string) error {
	return &helperFailure{cause: ErrHelperUnavailable, stage: stage}
}
func helperDiagnostic(err error) string {
	var failure *helperFailure
	if errors.As(err, &failure) {
		return failure.stage
	}
	return "helper_login"
}
func helperResultStage(code string) string {
	switch code {
	case "pi_incompatible":
		return "helper_pi_incompatible"
	case "invalid_credential":
		return "helper_invalid_credential"
	case "account_mismatch":
		return "helper_account_mismatch"
	case "invalid_prompt_reply":
		return "helper_invalid_prompt_reply"
	case "auth_failed":
		return "helper_provider_failed"
	default:
		return "helper_result_failed"
	}
}

// Only these non-secret protocol facts may leave the helper IPC parser.
// Untrusted strings and non-HTTP integers are discarded, never logged.
type providerDiagnostic struct {
	Stage      string
	Category   string
	HTTPStatus int
}

func (d *providerDiagnostic) observe(f helperFrame) {
	switch f.Stage {
	case "provider_loaded", "exchange_started", "exchange_http", "exchange_json", "credential_received", "credential_validated", "result_queued":
		d.Stage = f.Stage
	default:
		return
	}
	if f.HTTPStatus >= 100 && f.HTTPStatus <= 599 {
		d.HTTPStatus = f.HTTPStatus
	}
	switch f.Category {
	case "network_error", "timeout_error", "tls_error", "http_error", "json_error", "credential_error", "unknown_error":
		d.Category = f.Category
	}
}
