package cli

import (
	"strconv"
	"strings"
	"time"

	"loop-now/internal/loopctl/output"
)

// parsed 是一次解析的结果。
type parsed struct {
	path     []string // 解析到的命令或命令组路径
	cmd      *Command // 解析到命令时不为空
	help     bool
	args     []string
	flags    map[string]string
	repeated map[string][]string
}

// parse 解析命令行（不含程序名）。help 为 true 时忽略其余的参数错误。
func parse(r *Registry, argv []string) (*parsed, *output.Error) {
	p := &parsed{flags: map[string]string{}, repeated: map[string][]string{}}
	i := 0
	for i < len(argv) {
		tok := argv[i]
		if strings.HasPrefix(tok, "-") {
			break
		}
		next := append(append([]string(nil), p.path...), tok)
		if c := r.Find(next...); c != nil {
			p.path, p.cmd = next, c
			i++
			// 命令也可拥有子命令；只消费明确注册的路径，保留普通位置参数。
			if i < len(argv) && r.Find(append(append([]string(nil), next...), argv[i])...) != nil {
				continue
			}
			break
		}
		if !r.isGroup(next) {
			break
		}
		p.path = next
		i++
	}
	rest := argv[i:]
	if p.cmd == nil {
		return p, parseGroup(r, p, rest)
	}
	return p, parseCommand(p, rest)
}

// parseGroup 处理没有解析到具体命令的情况：命令组、未知命令或空命令行。
func parseGroup(r *Registry, p *parsed, rest []string) *output.Error {
	for _, tok := range rest {
		if tok == "--" {
			break
		}
		if tok == "--help" {
			p.help = true
			return nil
		}
	}
	if len(rest) == 0 && len(p.path) == 0 {
		p.help = true // 不带参数运行，输出完整说明
		return nil
	}
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		if len(p.path) == 0 {
			return output.Errorf(output.CodeUnknownCommand, "未知命令 %q，运行 loopctl schema 查看全部命令", rest[0])
		}
		return output.Errorf(output.CodeUnknownCommand, "%q 不是 %s 的子命令", rest[0], strings.Join(p.path, " ")).
			WithDetails(map[string]any{"subcommands": r.children(p.path)})
	}
	if len(p.path) == 0 {
		return output.Errorf(output.CodeInvalidArgument, "选项必须写在命令路径之后，运行 loopctl schema 查看全部命令")
	}
	return output.Errorf(output.CodeUnknownCommand, "%s 是命令组，需要指定子命令", strings.Join(p.path, " ")).
		WithDetails(map[string]any{"subcommands": r.children(p.path)})
}

// parseCommand 解析命令路径之后的选项与位置参数。
func parseCommand(p *parsed, rest []string) *output.Error {
	c := p.cmd
	known := map[string]Flag{}
	for _, f := range effectiveFlags(c) {
		known[f.Name] = f
	}
	var first *output.Error
	fail := func(e *output.Error) {
		if first == nil {
			first = e
		}
	}
	dashdash := false
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if dashdash || tok == "-" || !strings.HasPrefix(tok, "-") {
			p.args = append(p.args, tok)
			continue
		}
		if tok == "--" {
			dashdash = true
			continue
		}
		if !strings.HasPrefix(tok, "--") {
			fail(output.Errorf(output.CodeInvalidArgument, "未知选项 %s：选项写作 --名称", tok))
			continue
		}
		name, value, hasValue := strings.Cut(tok[2:], "=")
		f, ok := known[name]
		if !ok {
			fail(output.Errorf(output.CodeInvalidArgument, "未知选项 --%s", name))
			continue
		}
		if _, dup := p.flags[name]; dup && !f.Repeatable {
			fail(output.Errorf(output.CodeInvalidArgument, "选项 --%s 重复出现", name))
			continue
		}
		if f.Type == TypeBool {
			if hasValue {
				fail(output.Errorf(output.CodeInvalidArgument, "选项 --%s 不带值", name))
				continue
			}
			p.flags[name] = "true"
			if f.Repeatable {
				p.repeated[name] = append(p.repeated[name], "true")
			}
			if name == HelpFlag.Name {
				p.help = true
			}
			continue
		}
		if !hasValue {
			if i+1 >= len(rest) {
				fail(output.Errorf(output.CodeMissingArgument, "选项 --%s 缺少值", name))
				continue
			}
			i++
			value = rest[i]
		}
		p.flags[name] = value
		if f.Repeatable {
			p.repeated[name] = append(p.repeated[name], value)
		}
	}
	if p.help {
		return nil
	}
	if first != nil {
		return first
	}
	return validate(c, p)
}

// validate 检查位置参数个数、必填选项、选项取值与选项之间的约束。
func validate(c *Command, p *parsed) *output.Error {
	min, variadic := len(c.Args), false
	if n := len(c.Args); n > 0 && c.Args[n-1].Variadic {
		min, variadic = n-1, true
	}
	if len(p.args) < min {
		return output.Errorf(output.CodeMissingArgument, "缺少参数 <%s>", c.Args[len(p.args)].Name)
	}
	if !variadic && len(p.args) > min {
		return output.Errorf(output.CodeInvalidArgument, "多余的参数 %q", p.args[min])
	}
	for _, f := range effectiveFlags(c) {
		v, set := p.flags[f.Name]
		if !set {
			if f.Required {
				return output.Errorf(output.CodeMissingArgument, "缺少选项 --%s", f.Name)
			}
			continue
		}
		values := []string{v}
		if f.Repeatable {
			values = p.repeated[f.Name]
		}
		for _, value := range values {
			if e := checkValue(f, value); e != nil {
				return e
			}
		}
	}
	for _, k := range c.Constraints {
		n := 0
		for _, name := range k.Flags {
			if _, set := p.flags[name]; set {
				n++
			}
		}
		list := "--" + strings.Join(k.Flags, "、--")
		hint := ""
		if k.Hint != "" {
			hint = "；" + k.Hint
		}
		switch {
		case k.Kind == ExactlyOne && n == 0:
			return output.Errorf(output.CodeMissingArgument, "需要提供 %s 中的一个%s", list, hint)
		case (k.Kind == ExactlyOne || k.Kind == AtMostOne) && n > 1:
			return output.Errorf(output.CodeInvalidArgument, "%s 只能提供一个%s", list, hint)
		case k.Kind == AtLeastOne && n == 0:
			return output.Errorf(output.CodeMissingArgument, "至少需要提供 %s 中的一个%s", list, hint)
		case k.Kind == Together && n != 0 && n != len(k.Flags):
			return output.Errorf(output.CodeInvalidArgument, "%s 必须同时提供%s", list, hint)
		}
	}
	return nil
}

func checkValue(f Flag, v string) *output.Error {
	if v == "" {
		return output.Errorf(output.CodeInvalidArgument, "选项 --%s 的值不能为空", f.Name)
	}
	switch f.Type {
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return output.Errorf(output.CodeInvalidArgument, "选项 --%s 需要整数", f.Name)
		}
		if f.Min != nil && n < *f.Min {
			return output.Errorf(output.CodeInvalidArgument, "选项 --%s 不能小于 %d", f.Name, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return output.Errorf(output.CodeInvalidArgument, "选项 --%s 不能大于 %d", f.Name, *f.Max)
		}
	case TypeDuration:
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return output.Errorf(output.CodeInvalidArgument, "选项 --%s 需要正的时长，例如 30s", f.Name)
		}
	case TypeTime:
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return output.Errorf(output.CodeInvalidArgument, "选项 --%s 需要 RFC 3339 时间，例如 2026-10-13T00:00:00+08:00", f.Name)
		}
	case TypeEnum:
		for _, x := range f.Values {
			if x == v {
				return nil
			}
		}
		return output.Errorf(output.CodeInvalidArgument, "选项 --%s 只能取 %s", f.Name, strings.Join(f.Values, "、"))
	}
	return nil
}
