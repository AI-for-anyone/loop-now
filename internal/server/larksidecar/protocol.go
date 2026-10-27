// Package larksidecar implements the platform's private, user-only adapter for
// the official f065bf5 authsidecar v1 protocol. It never returns tokens to CLI.
package larksidecar

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	Version         = "lark-cli-user-1.0.94-loop-authsidecar.1"
	headerVersion   = "X-Lark-Proxy-Version"
	headerTarget    = "X-Lark-Proxy-Target"
	headerIdentity  = "X-Lark-Proxy-Identity"
	headerSignature = "X-Lark-Proxy-Signature"
	headerTimestamp = "X-Lark-Proxy-Timestamp"
	headerBodySHA   = "X-Lark-Body-SHA256"
	headerAuth      = "X-Lark-Proxy-Auth-Header"
)

func bodyHash(body []byte) string { h := sha256.Sum256(body); return hex.EncodeToString(h[:]) }
func signature(key string, r *http.Request, host, digest string) string {
	fields := []string{"v1", r.Method, host, r.URL.RequestURI(), digest, r.Header.Get(headerTimestamp), "user", "Authorization"}
	mac := hmac.New(sha256.New, []byte(key)) // official key is RAW env bytes, not hex-decoded.
	mac.Write([]byte(strings.Join(fields, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}
func validTime(s string, now time.Time) bool {
	ts, err := strconv.ParseInt(s, 10, 64)
	return err == nil && ts >= now.Unix()-60 && ts <= now.Unix()+60
}
func single(h http.Header, name string) bool {
	v := h.Values(name)
	return len(v) == 1 && !strings.ContainsAny(v[0], "\r\n")
}

// The first release exposes the approved QA text API surface, not an arbitrary
// Lark proxy, token endpoint, attachment downloader, or CLI management service.
func allowedRequest(r *http.Request) bool {
	p := r.URL.Path
	if p != r.URL.EscapedPath() || strings.Contains(p, "//") || strings.Contains(p, "..") {
		return false
	}
	if r.Method == "GET" && p == "/open-apis/authen/v1/user_info" {
		return true
	}
	const base = "/open-apis/im/v1/messages"
	if p == base {
		return r.Method == "GET" || r.Method == "POST"
	}
	if !strings.HasPrefix(p, base+"/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(p, base+"/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	for _, c := range parts[0] {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return len(parts) == 1 && r.Method == "GET" || len(parts) == 2 && parts[1] == "reply" && r.Method == "POST"
}
