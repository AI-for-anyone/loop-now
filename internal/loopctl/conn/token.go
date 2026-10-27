package conn

import (
	"regexp"
	"strings"
)

// tokenPattern 是令牌的格式：前缀 + 32 位十六进制 ID + _ + base64url 密钥。
var tokenPattern = regexp.MustCompile(`^(lna_|lne_)([0-9a-fA-F]{32})_[A-Za-z0-9_-]+$`)

// TokenID 从令牌中取出 ID 与显示前缀。ID 为带连字符的小写 UUID，与服务端的 ExternalToken.id 相同；
// 显示前缀为令牌前缀加 ID 的前 8 位十六进制，与 ExternalToken.prefix 相同。格式不符时 ok 为 false。
// 返回值都不含令牌的密钥部分。
func TokenID(token string) (id, prefix string, ok bool) {
	m := tokenPattern.FindStringSubmatch(token)
	if m == nil {
		return "", "", false
	}
	hex := strings.ToLower(m[2])
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:32], m[1] + hex[:8], true
}
