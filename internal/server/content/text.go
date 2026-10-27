package content

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// TextRules 决定哪些资料文件可以在线编辑。
type TextRules struct {
	Extensions []string // 小写、带点
	MaxBytes   int64
}

// ExtensionAllowed 判断路径的扩展名是否在名单中。
func (r TextRules) ExtensionAllowed(p string) bool {
	return slices.Contains(r.Extensions, strings.ToLower(path.Ext(p)))
}

// Editable 判断给定路径与内容能否在线编辑：扩展名在名单中、不超过大小上限、内容是合法 UTF-8。
func (r TextRules) Editable(p string, data []byte) bool {
	return r.ExtensionAllowed(p) && int64(len(data)) <= r.MaxBytes && utf8.Valid(data)
}

// SHA256Hex 返回内容的 sha256 十六进制字符串。
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
