// Package localmode implements the personal edition's "local mode": binding local directories, using the local git
// identity for "Pull from Git", and appending the user's PATH to the execution PATH. Each of the three has a global
// switch (on by default). Pi is not sandboxed: the list of local directories is for disclosure and management, not
// permission isolation.
package localmode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"loop-now/internal/execenv"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Limits of the list, matching LocalDirectoriesPut and LocalDirectoryInput in api/openapi.yaml.
const (
	MaxDirectories = 50
	MaxPathBytes   = 4096
	MaxNoteRunes   = 200
)

// Directory statuses, matching LocalDirectory.status in api/openapi.yaml.
const (
	StatusOK           = "ok"
	StatusMissing      = "missing"
	StatusNotDirectory = "not_directory"
	StatusInaccessible = "inaccessible"
)

// DirectoryInput is one entry when saving.
type DirectoryInput struct {
	Path string  `json:"path"`
	Note *string `json:"note"`
}

// Validate checks a whole list and returns the entries to save: a path must be a canonical absolute path
// (filepath.Clean does not change it), contain no control characters, and not repeat within the same list
// (case-insensitive on Windows); notes are trimmed of leading and trailing whitespace and contain no control
// characters. Field errors are located by index, as in directories[2].path. Whether a directory exists does not affect
// saving.
func Validate(in []DirectoryInput) ([]store.LocalDirectoryEntry, error) {
	if in == nil {
		return nil, apperr.Invalid("directories", "必须提供清单（可以为空数组）")
	}
	if len(in) > MaxDirectories {
		return nil, apperr.Invalid("directories", fmt.Sprintf("最多 %d 项", MaxDirectories))
	}
	var errs []apperr.FieldError
	out := make([]store.LocalDirectoryEntry, 0, len(in))
	seen := map[string]int{}
	for i, d := range in {
		field := fmt.Sprintf("directories[%d]", i)
		if msg := checkPath(d.Path); msg != "" {
			errs = append(errs, apperr.Field(field+".path", msg))
		} else if j, dup := seen[pathKey(d.Path)]; dup {
			errs = append(errs, apperr.Field(field+".path", fmt.Sprintf("与第 %d 项重复", j+1)))
		} else {
			seen[pathKey(d.Path)] = i
		}
		note := ""
		if d.Note != nil {
			note = strings.TrimSpace(*d.Note)
		}
		if msg := checkNote(note); msg != "" {
			errs = append(errs, apperr.Field(field+".note", msg))
		}
		out = append(out, store.LocalDirectoryEntry{Path: d.Path, Note: note})
	}
	if len(errs) > 0 {
		return nil, apperr.Validation(errs...)
	}
	return out, nil
}

func checkPath(p string) string {
	switch {
	case p == "":
		return "不能为空"
	case len(p) > MaxPathBytes:
		return fmt.Sprintf("最长 %d 字节", MaxPathBytes)
	case !utf8.ValidString(p):
		return "不是合法的 UTF-8 文本"
	case strings.ContainsFunc(p, unicode.IsControl):
		return "不能含控制字符（换行、制表符等）"
	case !filepath.IsAbs(p):
		if runtime.GOOS == "windows" {
			return `必须是绝对路径，带盘符（如 C:\Users\me\code）或 UNC（\\server\share\dir）`
		}
		return "必须是绝对路径（以 / 开头）"
	}
	if clean := filepath.Clean(p); clean != p {
		return "不是规范写法，应写作 " + clean
	}
	return ""
}

func checkNote(note string) string {
	switch {
	case !utf8.ValidString(note):
		return "不是合法的 UTF-8 文本"
	case utf8.RuneCountInString(note) > MaxNoteRunes:
		return fmt.Sprintf("最长 %d 个字符", MaxNoteRunes)
	case strings.ContainsFunc(note, unicode.IsControl):
		return "不能含控制字符（换行、制表符等）"
	}
	return ""
}

// pathKey is the key for deciding whether two entries are duplicates: Windows paths are case-insensitive.
func pathKey(p string) string { return execenv.PathKey(p) }

// Status returns the current status of a path (following symlinks).
func Status(p string) string {
	info, err := os.Stat(p)
	switch {
	case err == nil && info.IsDir():
		return StatusOK
	case err == nil:
		return StatusNotDirectory
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		// A path with a file somewhere in the middle (reported as ENOTDIR on Unix) also counts as missing.
		return StatusMissing
	}
	return StatusInaccessible
}
