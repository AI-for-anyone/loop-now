package cliauth

import (
	"net/url"
	"strings"
)

// ValidAuthorizationURL is shared in behavior with the Google UI validator;
// URL values never enter logs. Redirects are only the actual local callback.
func ValidAuthorizationURL(raw string) bool {
	if len(raw) > 16384 {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Hostname() != "accounts.google.com" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return false
	}
	if u.Path != "/o/oauth2/auth" && u.Path != "/o/oauth2/v2/auth" || u.RawPath != "" {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	required := []string{"client_id", "response_type", "redirect_uri", "scope", "state", "code_challenge", "code_challenge_method", "access_type"}
	allowed := map[string]bool{"prompt": true, "include_granted_scopes": true, "login_hint": true}
	for _, k := range required {
		allowed[k] = true
		if len(q[k]) != 1 || q.Get(k) == "" {
			return false
		}
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 || v[0] == "" || strings.ContainsAny(v[0], "\r\n\x00") {
			return false
		}
	}
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" {
		return false
	}
	switch q.Get("redirect_uri") {
	case "http://localhost:8085/", "http://127.0.0.1:8085/":
	default:
		return false
	}
	if value := q.Get("prompt"); value != "" && value != "consent" && value != "select_account" && value != "consent select_account" && value != "select_account consent" {
		return false
	}
	if value := q.Get("include_granted_scopes"); value != "" && value != "true" && value != "false" {
		return false
	}
	return true
}
