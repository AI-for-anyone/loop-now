// Package auth 定义请求身份（Principal）与平台令牌的格式。
//
// 令牌为 `lna_`（Agent）或 `lne_`（外部身份）后接 `<令牌 ID 的 32 位十六进制>_<32 字节随机数的 base64url>`。
// 服务端只保存密钥部分的 sha256，按 ID 查表后用常量时间比较；令牌只从 `Authorization: Bearer` 读取。
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

// Kind 是身份类别，取值与契约中的 PrincipalKind 相同。
type Kind string

const (
	KindConsole  Kind = "console"
	KindExternal Kind = "external"
	KindAgent    Kind = "agent"
	// KindPlugin 是插件平台半边经插件令牌调用控制台接口（只在回环监听器上）。
	KindPlugin Kind = "plugin"
)

// ConsoleAddress 与 ConsoleName 是控制台操作者的地址与显示名称。
const (
	ConsoleAddress = "@console"
	ConsoleName    = "控制台"
)

// Principal 是一次请求的身份。
type Principal struct {
	Kind      Kind
	ID        uuid.UUID // 外部身份 ID 或 Agent ID；控制台操作者为 uuid.Nil
	Address   string    // @console、@<名称>，或 Agent 的地址（名称不满足命名规则时为空）
	Name      string    // 显示名称
	TaskID    uuid.UUID // 任务实例令牌绑定；常态身份为空
	RuntimeID uuid.UUID
	// Plugin 与 Scopes：插件令牌所属的插件与它的接口范围。
	Plugin string
	Scopes []string
}

// PluginPrincipal 返回插件令牌的身份。
func PluginPrincipal(plugin string, scopes []string) Principal {
	return Principal{Kind: KindPlugin, Address: "plugin:" + plugin, Name: plugin, Plugin: plugin, Scopes: scopes}
}

// Console 返回控制台操作者。
func Console() Principal {
	return Principal{Kind: KindConsole, Address: ConsoleAddress, Name: ConsoleName}
}

// External 返回外部身份。
func External(id uuid.UUID, name string) Principal {
	return Principal{Kind: KindExternal, ID: id, Address: "@" + name, Name: name}
}

type principalKey struct{}

// With 把身份放进 context。
func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// From 取出 context 中的身份；没有时返回 false。
func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// 令牌前缀。
const (
	AgentPrefix    = "lna_"
	ExternalPrefix = "lne_"
	PluginPrefix   = "lnp_"
)

const secretBytes = 32

// Token 是新签发的令牌。Plain 只在签发时返回一次，服务端只保存 Hash。
type Token struct {
	ID    uuid.UUID
	Plain string
	Hash  []byte
}

// NewToken 生成一个带指定前缀的令牌。
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

// ParseToken 拆分令牌；格式不对时 ok 为 false。只做格式检查，不查库。
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

// HashSecret 返回密钥部分的 sha256。
func HashSecret(secret string) []byte {
	h := sha256.Sum256([]byte(secret))
	return h[:]
}

// SecretMatches 用常量时间比较密钥与保存的摘要。
func SecretMatches(hash []byte, secret string) bool {
	return subtle.ConstantTimeCompare(HashSecret(secret), hash) == 1
}

// DisplayPrefix 返回外部令牌可识别的前缀：lne_ 加 ID 去掉连字符后的前 8 位。
func DisplayPrefix(id uuid.UUID) string {
	return ExternalPrefix + hex.EncodeToString(id[:])[:8]
}

// BearerToken 返回 Authorization 头中的 Bearer 令牌；没有该头时 present 为 false。
// 有该头但不是 Bearer 写法时，present 为 true、token 为空。URL 中的令牌一律不读。
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

// LooksLikeToken 报告字符串是否带平台令牌的前缀（用于拒绝把令牌带到控制台接口上）。
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, AgentPrefix) || strings.HasPrefix(s, ExternalPrefix)
}
