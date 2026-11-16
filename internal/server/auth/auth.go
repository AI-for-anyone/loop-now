// Package auth defines the request identity (Principal) and the format of platform tokens.
//
// A token is `lna_` (Agent) or `lne_` (external identity) followed by
// `<token ID as 32 hex digits>_<base64url of 32 random bytes>`. The server stores only the sha256 of the secret part,
// looks the token up by ID and compares in constant time; tokens are read only from `Authorization: Bearer`.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Kind is the identity kind; its values are the same as PrincipalKind in the contract.
type Kind string

const (
	KindConsole  Kind = "console"
	KindExternal Kind = "external"
	KindAgent    Kind = "agent"
	// KindPlugin is a plugin's platform side calling the console API with a plugin token (loopback listener only).
	KindPlugin Kind = "plugin"
)

// ConsoleAddress and ConsoleName are the address and display name of the console operator.
const (
	ConsoleAddress = "@console"
	ConsoleName    = "控制台"
)

// Principal is the identity of one request.
type Principal struct {
	Kind      Kind
	ID        uuid.UUID // external identity ID or Agent ID; uuid.Nil for the console operator
	Address   string    // @console, @<name>, or the Agent's address (empty when the name does not satisfy the naming rules)
	Name      string    // display name
	TaskID    uuid.UUID // binding of a task instance token; empty for regular identities
	RuntimeID uuid.UUID
	// Plugin and Scopes: the plugin a plugin token belongs to, and its API scopes.
	Plugin string
	Scopes []string
}

// PluginPrincipal returns the identity of a plugin token.
func PluginPrincipal(plugin string, scopes []string) Principal {
	return Principal{Kind: KindPlugin, Address: "plugin:" + plugin, Name: plugin, Plugin: plugin, Scopes: scopes}
}

// Console returns the console operator.
func Console() Principal {
	return Principal{Kind: KindConsole, Address: ConsoleAddress, Name: ConsoleName}
}

// External returns an external identity.
func External(id uuid.UUID, name string) Principal {
	return Principal{Kind: KindExternal, ID: id, Address: "@" + name, Name: name}
}

type principalKey struct{}

// With puts the identity into the context.
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// From takes the identity out of the context; it returns false when there is none.
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Token prefixes.
const (
	AgentPrefix    = "lna_"
	ExternalPrefix = "lne_"
	PluginPrefix   = "lnp_"
)

const secretBytes = 32

// Token is a newly issued token. Plain is returned only once, at issue time; the server stores only Hash.
type Token struct {
	ID    uuid.UUID
	Plain string
	Hash  []byte
}

// NewToken generates a token with the given prefix.
func NewToken(prefix string) (Token, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Token{}, err
	}
	key := make([]byte, secretBytes)
	if _, err := rand.Read(key); err != nil {
		return Token{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(key)
	return Token{ID: id, Plain: prefix + hex.EncodeToString(id[:]) + "_" + secret, Hash: HashSecret(secret)}, nil
}

// ParseToken splits a token; ok is false when the format is wrong. It only checks the format, without a database lookup.
func ParseToken(s string) (prefix string, id uuid.UUID, secret string, ok bool) {
	for _, p := range []string{AgentPrefix, ExternalPrefix, PluginPrefix} {
		if rest, found := strings.CutPrefix(s, p); found {
			idHex, sec, found := strings.Cut(rest, "_")
			if !found || len(idHex) != 32 || len(sec) != base64.RawURLEncoding.EncodedLen(secretBytes) {
				return "", uuid.Nil, "", false
			}
			raw, err := hex.DecodeString(idHex)
			if err != nil || strings.ToLower(idHex) != idHex {
				return "", uuid.Nil, "", false
			}
			if _, err := base64.RawURLEncoding.DecodeString(sec); err != nil {
				return "", uuid.Nil, "", false
			}
			copy(id[:], raw)
			return p, id, sec, true
		}
	}
	return "", uuid.Nil, "", false
}

// HashSecret returns the sha256 of the secret part.
func HashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// SecretMatches compares a secret with the stored digest in constant time.
func SecretMatches(hash []byte, secret string) bool {
	return subtle.ConstantTimeCompare(HashSecret(secret), hash) == 1
}

// DisplayPrefix returns the recognizable prefix of an external token: lne_ plus the first 8 characters of the ID
// without hyphens.
func DisplayPrefix(id uuid.UUID) string {
	return ExternalPrefix + hex.EncodeToString(id[:])[:8]
}

// BearerToken returns the Bearer token from the Authorization header; present is false when there is no such header.
// When the header is there but not in Bearer form, present is true and token is empty. URL tokens are never read.
func BearerToken(r *http.Request) (token string, present bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(tok), true
}

// LooksLikeToken reports whether a string carries a platform token prefix (used to reject tokens sent to the console API).
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, AgentPrefix) || strings.HasPrefix(s, ExternalPrefix)
}
