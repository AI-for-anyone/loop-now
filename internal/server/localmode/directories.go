// Package localmode 实现个人版“本机模式”：绑定本机目录、“从 Git 拉取”用本机的 git 身份、执行 PATH 接上用户的 PATH。
// 三项各有一个全局开关（默认开启）。Pi 不在沙箱里，本机目录清单是告知与管理，不是权限隔离。
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

// 清单的上限，与 api/openapi.yaml 的 LocalDirectoriesPut、LocalDirectoryInput 一致。
const (
	MaxDirectories = 50
	MaxPathBytes   = 4096
	MaxNoteRunes   = 200
)

// 目录状态，与 api/openapi.yaml 的 LocalDirectory.status 一致。
const (
	StatusOK           = "ok"
	StatusMissing      = "missing"
	StatusNotDirectory = "not_directory"
	StatusInaccessible = "inaccessible"
)

// DirectoryInput 是保存时的一项。
type DirectoryInput struct {
	Path string  `json:"path"`
	Note *string `json:"note"`
}

// Validate 校验整份清单并给出要保存的条目：路径必须是规范的绝对路径（filepath.Clean 不改变它）、不含控制字符、
// 同一清单内不重复（Windows 上不分大小写）；备注去掉首尾空白，不含控制字符。字段错误按下标定位，形如 directories[2].path。
// 目录是否存在不影响保存。
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

// pathKey 是判断两项是否重复的键：Windows 的路径不分大小写。
func pathKey(p string) string { return execenv.PathKey(p) }

// Status 返回路径此刻的状态（跟随符号链接）。
func Status(p string) string {
	info, err := os.Stat(p)
	switch {
	case err == nil && info.IsDir():
		return StatusOK
	case err == nil:
		return StatusNotDirectory
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		// 路径中间某一级是文件（Unix 上报 ENOTDIR）同样算不存在。
		return StatusMissing
	}
	return StatusInaccessible
}
