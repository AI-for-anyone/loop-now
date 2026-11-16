// Package content provides validation for uploaded content: relative paths, repository URLs, SKILL.md, Skill
// archives and text that can be edited online.
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

// MaxPathLen is the maximum length of a relative path.
const MaxPathLen = 512

// CleanRelPath validates and returns a clean relative path: it must not be empty, absolute, or contain `..` or `.`
// segments, backslashes or control characters, and must not end with a slash.
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

// PathsOverlap reports whether two relative paths are the same or one contains the other.
func PathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

var scpLike = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[^/].*$`)

// ValidateGitURL validates a code repository URL: https, http, ssh, file and the git@host:path form are supported;
// http(s) URLs must not embed an account or password, and ssh URLs may carry only a user name, never a password.
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

// ValidateBranch validates a branch name against the main rules for git ref names, and rejects names starting with -
// so they cannot be taken as command-line options.
func ValidateBranch(b string) error {
	if b == "" {
		return errors.New("分支不能为空")
	}
	if len(b) > 255 || badBranch.MatchString(b) {
		return errors.New("分支名不合法")
	}
	return nil
}
