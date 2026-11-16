// Package naming defines the rules shared by organization, project and Agent names.
// These names make up Inbox addresses (`org/project/agent`, plus `#taskID` inside a task),
// so they must not contain separators and must not be confusable with person addresses starting with `@` or with
// UUID-shaped internal IDs.
//
// The pattern of EntityName in api/openapi.yaml is equivalent to this package; httpapi's tests compare the two.
package naming

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxLen is the maximum number of characters in a name (counted in Unicode code points).
const MaxLen = 100

var uuidForm = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUIDForm reports whether s is written as an 8-4-4-4-12 hex UUID (case-insensitive).
// Names cannot take this form; a single-segment address value in this form is resolved as an Agent's internal ID.
func IsUUIDForm(s string) bool { return uuidForm.MatchString(s) }

// Validate checks a name and returns the first rule it breaks, or an empty string when it satisfies all rules.
// what is the kind of name shown in the message, for example "Agent 名称" ("Agent name").
func Validate(what, name string) string {
	switch {
	case name == "":
		return what + "不能为空"
	case utf8.RuneCountInString(name) > MaxLen:
		return what + "不能超过 100 个字符"
	case !utf8.ValidString(name):
		return what + "不是有效的 UTF-8 文本"
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return what + "不能含控制字符"
	case startsOrEndsWithSpace(name):
		return what + "首尾不能有空白"
	case strings.HasPrefix(name, "@"):
		return what + "不能以 @ 开头"
	case strings.ContainsAny(name, "/#"):
		return what + "不能含 / 或 #"
	case IsUUIDForm(name):
		return what + "不能是 UUID 格式"
	}
	return ""
}

// AgentAddress returns an Agent's Inbox address: `org/project/agent` for an Agent in a project, and the name itself for
// a platform Agent.
// When any segment breaks the rules (created before the rules took effect) ok is false, and the Agent can then only
// be addressed by its internal ID.
func AgentAddress(org, project, agent string, platform bool) (addr string, ok bool) {
	if Validate("", agent) != "" {
		return "", false
	}
	if platform {
		return agent, true
	}
	if Validate("", org) != "" || Validate("", project) != "" {
		return "", false
	}
	return org + "/" + project + "/" + agent, true
}

func startsOrEndsWithSpace(s string) bool {
	first, _ := utf8.DecodeRuneInString(s)
	last, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(first) || unicode.IsSpace(last)
}
