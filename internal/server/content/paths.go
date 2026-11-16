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
		return "", errors.New("path must not be empty")
	}
	if len(p) > MaxPathLen {
		return "", fmt.Errorf("path must be at most %d bytes", MaxPathLen)
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("path must be relative")
	}
	if strings.ContainsRune(p, '\\') {
		return "", errors.New("path must not contain backslashes")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return "", errors.New("path must not contain control characters")
		}
	}
	for seg := range strings.SplitSeq(p, "/") {
		switch seg {
		case "":
			return "", errors.New("path must not contain empty segments or end with a slash")
		case ".", "..":
			return "", errors.New("path must not contain . or .. segments")
		}
	}
	if path.Clean(p) != p {
		return "", errors.New("path is not in clean form")
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
		return errors.New("repository URL must not be empty")
	}
	if strings.HasPrefix(raw, "-") || strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("repository URL has an invalid format")
	}
	if scpLike.MatchString(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("repository URL cannot be parsed")
	}
	switch u.Scheme {
	case "https", "http":
		if u.User != nil {
			return errors.New("repository URL must not embed an account or password; for a private repository, add a gh credential for the Agent")
		}
		if u.Host == "" {
			return errors.New("repository URL has no host")
		}
	case "ssh":
		if _, hasPassword := u.User.Password(); u.User != nil && hasPassword {
			return errors.New("repository URL must not embed a password")
		}
		if u.Host == "" {
			return errors.New("repository URL has no host")
		}
	case "file":
		if u.Path == "" {
			return errors.New("repository URL has no path")
		}
	default:
		return errors.New("repository URL must use https, http, ssh, file or the git@host:path form")
	}
	return nil
}

var badBranch = regexp.MustCompile(`(^-)|(\.\.)|([\x00-\x20~^:?*\[\\])|(@\{)|(/$)|(\.lock$)|(^/)|(//)`)

// ValidateBranch validates a branch name against the main rules for git ref names, and rejects names starting with -
// so they cannot be taken as command-line options.
func ValidateBranch(b string) error {
	if b == "" {
		return errors.New("branch must not be empty")
	}
	if len(b) > 255 || badBranch.MatchString(b) {
		return errors.New("invalid branch name")
	}
	return nil
}
