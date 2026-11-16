package content

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// TextRules decides which files can be edited online.
type TextRules struct {
	Extensions []string // lowercase, with the dot
	MaxBytes   int64
}

// ExtensionAllowed reports whether the path's extension is on the list.
func (r TextRules) ExtensionAllowed(p string) bool {
	return slices.Contains(r.Extensions, strings.ToLower(path.Ext(p)))
}

// Editable reports whether the given path and content can be edited online: the extension is on the list, the size
// is within the limit, and the content is valid UTF-8.
func (r TextRules) Editable(p string, data []byte) bool {
	return r.ExtensionAllowed(p) && int64(len(data)) <= r.MaxBytes && utf8.Valid(data)
}

// SHA256Hex returns the content's sha256 as a hex string.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
