// Package naming 定义组织名、项目名与 Agent 名的共同规则。
// 这些名称会组成 Inbox 地址（`组织名/项目名/Agent名`，任务内再加 `#任务ID`），
// 所以不能含分隔符，不能与 `@` 开头的人员地址或形如 UUID 的内部 ID 混淆。
//
// api/openapi.yaml 中 EntityName 的 pattern 与本包等价，由 httpapi 的测试比对。
package naming

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxLen 是名称的最大字符数（按 Unicode 码点计）。
const MaxLen = 100

var uuidForm = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUIDForm 报告 s 是否为 8-4-4-4-12 的十六进制 UUID 写法（不区分大小写）。
// 名称不能取这种写法；地址中这种写法的单段值按 Agent 内部 ID 解析。
func IsUUIDForm(s string) bool { return uuidForm.MatchString(s) }

// Validate 检查名称，返回第一条违反的规则；满足全部规则时返回空字符串。
// what 是出现在提示中的名称类别，例如“Agent 名称”。
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

// AgentAddress 返回 Agent 的 Inbox 地址：项目内的 Agent 为 `组织名/项目名/Agent名`，平台持有的 Agent 为名称本身。
// 任一段名称不满足规则（规则生效前创建）时 ok 为 false，此时只能用 Agent 的内部 ID 寻址。
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
