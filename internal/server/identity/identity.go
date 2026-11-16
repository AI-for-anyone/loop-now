// Package identity holds the naming rules and token issuing for external identities, shared by the console API and the
// server command line.
package identity

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidateName checks an external identity name with the same rules as ExternalIdentityName in the contract; it returns
// an empty string when the name is valid.
func ValidateName(name string) string {
	switch {
	case !namePattern.MatchString(name):
		return "只能由小写字母、数字、点、下划线和连字符组成，以小写字母或数字开头，最长 64 个字符"
	case name == "console" || name == "platform":
		return "不能是 console 或 platform"
	}
	return ""
}

// IssueToken issues a token for an external identity that has not been deleted; expiresAt, when set, must be later than
// now. It returns the token metadata and the plaintext, which appears only this once.
func IssueToken(ctx context.Context, st *store.Store, identityID uuid.UUID, expiresAt *time.Time) (store.ExternalToken, string, error) {
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return store.ExternalToken{}, "", apperr.Invalid("expiresAt", "过期时间必须晚于当前时间")
	}
	tok, err := auth.NewToken(auth.ExternalPrefix)
	if err != nil {
		return store.ExternalToken{}, "", err
	}
	t, err := st.InsertExternalToken(ctx, identityID, tok.ID, tok.Hash, auth.DisplayPrefix(tok.ID), expiresAt)
	if err != nil {
		return store.ExternalToken{}, "", err
	}
	return t, tok.Plain, nil
}
