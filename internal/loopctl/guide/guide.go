// Package guide 生成服务端写给每个 Agent 的内置 Skill（loopctl 的用法说明）与 Inbox 提醒命令。
// 内容由内嵌模板与命令注册表生成，只依赖代码，同一版本逐字节相同。
package guide

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"text/template"

	"loop-now/internal/buildinfo"
	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/loopctl/output"
)

// SkillName 是内置 Skill 的名称；模板库与 Agent 的 Skill 副本不能使用这个名称。
const SkillName = "loopctl"

//go:embed skill.md.tmpl
var skillTemplate string

var files = sync.OnceValue(func() map[string][]byte {
	md, err := Render(buildinfo.Version())
	if err != nil {
		panic(err) // 内容只依赖代码，测试保证生成成功
	}
	return map[string][]byte{"SKILL.md": md}
})

// Files 返回内置 Skill 的文件：相对路径 → 内容。第一版只有 SKILL.md。调用方不应修改返回的内容。
func Files() map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files() {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// InboxReminderCommand 返回 Inbox 提醒文字中要求 Agent 运行的命令。
func InboxReminderCommand() string {
	c := commands.Registry().Find("inbox", "list")
	if c == nil {
		panic("命令 inbox list 不存在")
	}
	return "loopctl " + c.Name() + " --unread"
}

// InboxReadCommand 返回平台提醒点名消息时要求 Agent 逐条读取所用的命令（后接消息 ID）。
func InboxReadCommand() string {
	c := commands.Registry().Find("inbox", "read")
	if c == nil {
		panic("命令 inbox read 不存在")
	}
	return "loopctl " + c.Name()
}

// Render 按指定版本生成 SKILL.md。
func Render(version string) ([]byte, error) {
	r := commands.Registry()
	funcs := template.FuncMap{
		"skillName": func() string { return SkillName },
		"version":   func() string { return version },
		"reminder":  InboxReminderCommand,
		"cmd": func(path string) (string, error) {
			c := r.Find(strings.Fields(path)...)
			if c == nil {
				return "", fmt.Errorf("说明模板引用了不存在的命令 %q", path)
			}
			if !c.AllowedFor(cli.IdentityAgent) {
				return "", fmt.Errorf("说明模板引用了 Agent 身份不能使用的命令 %q", path)
			}
			return "loopctl " + c.Name(), nil
		},
		"exitCodes": exitCodes,
		"reference": func() string { return reference(r) },
	}
	t, err := template.New("SKILL.md").Option("missingkey=error").Funcs(funcs).Parse(skillTemplate)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func exitCodes() string {
	var b strings.Builder
	b.WriteString("| 退出码 | 类别 | 含义 |\n|---|---|---|\n")
	for _, e := range output.ExitTable() {
		cat := "—"
		if e.Category != "" {
			cat = "`" + string(e.Category) + "`"
		}
		fmt.Fprintf(&b, "| %d | %s | %s |\n", e.Code, cat, e.Description)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// reference 列出 Agent 身份可用的全部命令：说明、参数、选项与一个可以直接解析的用法示例。
func reference(r *cli.Registry) string {
	var sections []string
	for _, c := range r.Commands() {
		if !c.AllowedFor(cli.IdentityAgent) {
			continue
		}
		var items []string
		for _, a := range c.Args {
			items = append(items, fmt.Sprintf("- `<%s>`：%s", a.Name, a.Description))
		}
		for _, f := range c.Flags {
			name := "--" + f.Name
			if f.Type != cli.TypeBool {
				name += " <" + string(f.Type) + ">"
			}
			item := fmt.Sprintf("- `%s`：%s", name, f.Description)
			if f.Repeatable {
				item += "（可重复，按出现顺序）"
			}
			if len(f.Values) > 0 {
				item += fmt.Sprintf("（可选值：%s）", strings.Join(f.Values, "、"))
			}
			items = append(items, item)
		}
		for _, k := range c.Constraints {
			items = append(items, fmt.Sprintf("- %s：--%s", constraintText(k.Kind), strings.Join(k.Flags, "、--")))
		}
		s := fmt.Sprintf("### loopctl %s\n\n%s\n\n用法：`%s`", c.Name(), c.Summary, usage(c))
		if len(items) > 0 {
			s += "\n\n" + strings.Join(items, "\n")
		}
		sections = append(sections, s)
	}
	return strings.Join(sections, "\n\n")
}

func constraintText(k cli.ConstraintKind) string {
	switch k {
	case cli.ExactlyOne:
		return "必须且只能提供其中一个"
	case cli.AtMostOne:
		return "最多提供其中一个"
	case cli.AtLeastOne:
		return "至少提供其中一个"
	default:
		return "必须同时提供"
	}
}

// usage 生成能被解析的最简用法：位置参数与必填选项用占位符，二选一的选项取第一个。
func usage(c *cli.Command) string {
	parts := []string{"loopctl", c.Name()}
	for _, a := range c.Args {
		if !a.Variadic {
			parts = append(parts, "<"+a.Name+">")
		}
	}
	need := map[string]bool{}
	for _, f := range c.Flags {
		need[f.Name] = f.Required
	}
	for _, k := range c.Constraints {
		if k.Kind == cli.ExactlyOne || k.Kind == cli.AtLeastOne {
			need[k.Flags[0]] = true
		}
	}
	for _, f := range c.Flags {
		if !need[f.Name] {
			continue
		}
		switch {
		case f.Type == cli.TypeBool:
			parts = append(parts, "--"+f.Name)
		case f.Type == cli.TypeInt && f.Required && f.Min != nil:
			parts = append(parts, "--"+f.Name, fmt.Sprint(*f.Min))
		case len(f.Values) > 0:
			parts = append(parts, "--"+f.Name, f.Values[0])
		default:
			parts = append(parts, "--"+f.Name, "<"+f.Name+">")
		}
	}
	return strings.Join(parts, " ")
}
