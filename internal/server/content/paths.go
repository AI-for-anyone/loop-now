// Package content 提供上传内容相关的校验：相对路径、仓库地址、SKILL.md、Skill 压缩包与可在线编辑的文本。
package content

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// MaxPathLen 是相对路径的最大长度。
const MaxPathLen = 512

// CleanRelPath 校验并返回规范的相对路径：不能为空、不能是绝对路径、不能含 `..` 或 `.` 段、
// 不能含反斜杠或控制字符，也不能以斜杠结尾。
func CleanRelPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("路径不能为空")
	}
	if len(p) > MaxPathLen {
		return "", fmt.Errorf("路径不能超过 %d 个字节", MaxPathLen)
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("必须是相对路径")
	}
	if strings.ContainsRune(p, '\\') {
		return "", errors.New("路径不能包含反斜杠")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", errors.New("路径不能包含控制字符")
		}
	}
	for seg := range strings.SplitSeq(p, "/") {
		switch seg {
		case "":
			return "", errors.New("路径不能包含空段或以斜杠结尾")
		case ".", "..":
			return "", errors.New("路径不能包含 . 或 .. 段")
		}
	}
	if path.Clean(p) != p {
		return "", errors.New("路径不规范")
	}
	return p, nil
}

// PathsOverlap 判断两个相对路径是否相同或互相包含。
func PathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/].*$`)

// ValidateGitURL 校验代码仓库地址：支持 https、http、ssh、file 与 git@host:path 形式；
// http(s) 地址不能内嵌账号或密码，ssh 地址只能带用户名、不能带密码。
func ValidateGitURL(raw string) error {
	if raw == "" {
		return errors.New("仓库地址不能为空")
	}
	if strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("仓库地址格式不对")
	}
	if scpLike.MatchString(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("仓库地址无法解析")
	}
	switch u.Scheme {
	case "https", "http":
		if u.User != nil {
			return errors.New("仓库地址不能内嵌账号或密码；私有仓库请为 Agent 录入 gh 凭证")
		}
		if u.Host == "" {
			return errors.New("仓库地址缺少主机")
		}
	case "ssh":
		if _, hasPassword := u.User.Password(); u.User != nil && hasPassword {
			return errors.New("仓库地址不能内嵌密码")
		}
		if u.Host == "" {
			return errors.New("仓库地址缺少主机")
		}
	case "file":
		if u.Path == "" {
			return errors.New("仓库地址缺少路径")
		}
	default:
		return errors.New("仓库地址只支持 https、http、ssh、file 或 git@host:path 形式")
	}
	return nil
}

var badBranch = regexp.MustCompile(`(^-)|(\.\.)|([\x00-\x20~^:?*\[\\])|(@\{)|(/$)|(\.lock$)|(^/)|(//)`)

// ValidateBranch 按 git 引用名的主要规则校验分支名，并拒绝以 - 开头以免被当作命令行选项。
func ValidateBranch(b string) error {
	if b == "" {
		return errors.New("分支不能为空")
	}
	if len(b) > 255 || badBranch.MatchString(b) {
		return errors.New("分支名不合法")
	}
	return nil
}
