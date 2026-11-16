package taskmodel

// PublicError uses fixed descriptions: provider stderr and credential-file errors
// are untrusted and may contain keys, environment values or private paths.
// Keeping the code and message/run IDs preserves failure attribution without
// depending on credentials still being decryptable after instance retirement.
func PublicError(code, message *string) *string {
	if message == nil {
		return nil
	}
	safe := "The task turn failed."
	if code != nil {
		switch *code {
		case "session_missing":
			safe = "The task session file is missing. No replacement session was created."
		case "pi_error":
			safe = "Pi returned an error."
		case "pi_exited":
			safe = "The Pi process exited while replying."
		case "pi_start_failed":
			safe = "The Pi process failed to start."
		case "credential_unavailable":
			safe = "The task's captured credentials are not available."
		case "cli_install_failed":
			safe = "Could not install the task's CLIs."
		case "workspace_sync_failed":
			safe = "Could not sync the task working directory."
		case "server_restarted":
			safe = "The server restarted. The turn was not replayed."
		case "internal_error":
			safe = "The execution infrastructure failed."
		case "user_stopped":
			safe = "Stopped by a person."
		}
	}
	return &safe
}
