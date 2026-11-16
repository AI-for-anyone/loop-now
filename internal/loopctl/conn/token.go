package conn

import (
	"regexp"
	"strings"
)

// tokenPattern is the token format: prefix + 32-digit hex ID + _ + base64url secret.
var tokenPattern = regexp.MustCompile(`^(lna_|lne_)([0-9a-fA-F]{32})_[A-Za-z0-9_-]+$`)

// TokenID extracts the ID and the display prefix from a token. The ID is a lowercase UUID with hyphens, the same as
// the server's ExternalToken.id; the display prefix is the token prefix plus the first 8 hex digits of the ID, the
// same as ExternalToken.prefix. ok is false when the format does not match.
// None of the return values contain the secret part of the token.
func TokenID(token string) (id, prefix string, ok bool) {
	m := tokenPattern.FindStringSubmatch(token)
	if m == nil {
		return "", "", false
	}
	hex := strings.ToLower(m[2])
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32], m[1] + hex[:8], true
}
