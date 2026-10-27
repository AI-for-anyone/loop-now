package cli

import (
	"context"
	"reflect"
	"testing"

	"loop-now/internal/loopctl/output"
)

func testRegistry() *Registry {
	r := NewRegistry()
	r.Group("box", "测试命令组")
	r.Group("box sub", "子命令组")
	noop := func(context.Context, *Invocation) (any, error) { return nil, nil }
	r.Add(&Command{Path: []string{"box", "send"}, Args: []Arg{{Name: "to"}}, Run: noop, Flags: []Flag{
		{Name: "text", Type: TypeString},
		{Name: "file", Type: TypePathOrStdin},
		{Name: "mode", Type: TypeEnum, Values: []string{"a", "b"}},
		{Name: "n", Type: TypeInt, Min: func() *int { n := 1; return &n }()},
		{Name: "wait", Type: TypeDuration},
		{Name: "at", Type: TypeTime},
		{Name: "flag", Type: TypeBool},
		{Name: "need", Type: TypeString, Required: true},
	}, Constraints: []Constraint{{Kind: ExactlyOne, Flags: []string{"text", "file"}}}})
	r.Add(&Command{Path: []string{"box", "sub", "rm"}, Args: []Arg{{Name: "id"}}, Destructive: true, Run: noop})
	r.Add(&Command{Path: []string{"box", "net"}, Network: true, Run: noop, Flags: []Flag{{Name: "a", Type: TypeString}, {Name: "b", Type: TypeString}},
		Constraints: []Constraint{{Kind: Together, Flags: []string{"a", "b"}}, {Kind: AtMostOne, Flags: []string{"a", "x"}}}})
	r.Add(&Command{Path: []string{"many"}, Args: []Arg{{Name: "p", Variadic: true}}, Run: noop})
	return r
}

func TestParse(t *testing.T) {
	r := testRegistry()
	type want struct {
		path  []string
		help  bool
		args  []string
		flags map[string]string
		code  string
	}
	cases := []struct {
		name string
		argv []string
		want want
	}{
		{"选项与参数交错", []string{"box", "send", "--need", "x", "to1", "--text", "hi"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "hi"}}},
		{"等号写法", []string{"box", "send", "to1", "--need=x", "--text=a=b"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "a=b"}}},
		{"值可以以短横线开头", []string{"box", "send", "to1", "--need", "x", "--text", "-5"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "-5"}}},
		{"单独的 - 是位置参数", []string{"box", "send", "-", "--need", "x", "--file", "-"},
			want{path: []string{"box", "send"}, args: []string{"-"}, flags: map[string]string{"need": "x", "file": "-"}}},
		{"-- 之后都是位置参数", []string{"box", "send", "--need", "x", "--text", "t", "--", "--not-a-flag"},
			want{path: []string{"box", "send"}, args: []string{"--not-a-flag"}, flags: map[string]string{"need": "x", "text": "t"}}},
		{"布尔选项", []string{"box", "send", "to", "--need", "x", "--text", "t", "--flag"},
			want{path: []string{"box", "send"}, args: []string{"to"}, flags: map[string]string{"need": "x", "text": "t", "flag": "true"}}},
		{"布尔选项不带值", []string{"box", "send", "to", "--need", "x", "--text", "t", "--flag=true"}, want{code: output.CodeInvalidArgument}},
		{"未知选项", []string{"box", "send", "to", "--need", "x", "--text", "t", "--nope"}, want{code: output.CodeInvalidArgument}},
		{"短选项", []string{"box", "send", "to", "-x"}, want{code: output.CodeInvalidArgument}},
		{"重复选项", []string{"box", "send", "to", "--need", "x", "--need", "y", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"选项缺少值", []string{"box", "send", "to", "--text", "t", "--need"}, want{code: output.CodeMissingArgument}},
		{"缺少位置参数", []string{"box", "send", "--need", "x", "--text", "t"}, want{code: output.CodeMissingArgument}},
		{"多余的位置参数", []string{"box", "send", "a", "b", "--need", "x", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"缺少必填选项", []string{"box", "send", "to", "--text", "t"}, want{code: output.CodeMissingArgument}},
		{"空值", []string{"box", "send", "to", "--need", "", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"整数", []string{"box", "send", "to", "--need", "x", "--text", "t", "--n", "abc"}, want{code: output.CodeInvalidArgument}},
		{"整数下限", []string{"box", "send", "to", "--need", "x", "--text", "t", "--n", "0"}, want{code: output.CodeInvalidArgument}},
		{"时长", []string{"box", "send", "to", "--need", "x", "--text", "t", "--wait", "5"}, want{code: output.CodeInvalidArgument}},
		{"枚举", []string{"box", "send", "to", "--need", "x", "--text", "t", "--mode", "c"}, want{code: output.CodeInvalidArgument}},
		{"时间", []string{"box", "send", "to", "--need", "x", "--text", "t", "--at", "2026-10-13"}, want{code: output.CodeInvalidArgument}},
		{"合法的时间", []string{"box", "send", "to", "--need", "x", "--text", "t", "--at", "2026-10-13T00:00:00+08:00"},
			want{path: []string{"box", "send"}, args: []string{"to"}, flags: map[string]string{"need": "x", "text": "t", "at": "2026-10-13T00:00:00+08:00"}}},
		{"二选一都没有", []string{"box", "send", "to", "--need", "x"}, want{code: output.CodeMissingArgument}},
		{"二选一都提供", []string{"box", "send", "to", "--need", "x", "--text", "t", "--file", "f"}, want{code: output.CodeInvalidArgument}},
		{"必须同时提供", []string{"box", "net", "--a", "1"}, want{code: output.CodeInvalidArgument}},
		{"连接选项只用于连接服务端的命令", []string{"box", "net", "--profile", "p", "--timeout", "5s"},
			want{path: []string{"box", "net"}, flags: map[string]string{"profile": "p", "timeout": "5s"}}},
		{"非连接命令不接受连接选项", []string{"box", "sub", "rm", "1", "--profile", "p"}, want{code: output.CodeInvalidArgument}},
		{"删除类命令有 --yes", []string{"box", "sub", "rm", "1", "--yes"},
			want{path: []string{"box", "sub", "rm"}, args: []string{"1"}, flags: map[string]string{"yes": "true"}}},
		{"可变参数", []string{"many", "a", "b"}, want{path: []string{"many"}, args: []string{"a", "b"}, flags: map[string]string{}}},
		{"可变参数可以为空", []string{"many"}, want{path: []string{"many"}, flags: map[string]string{}}},
		{"--help 优先于参数错误", []string{"box", "send", "--nope", "--help"}, want{path: []string{"box", "send"}, help: true}},
		{"命令组的 --help", []string{"box", "--help"}, want{path: []string{"box"}, help: true}},
		{"不带参数", []string{}, want{help: true}},
		{"未知命令", []string{"nope"}, want{code: output.CodeUnknownCommand}},
		{"未知子命令", []string{"box", "nope"}, want{code: output.CodeUnknownCommand}},
		{"只有命令组", []string{"box"}, want{code: output.CodeUnknownCommand}},
		{"选项写在命令之前", []string{"--profile", "p", "box", "net"}, want{code: output.CodeInvalidArgument}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, e := parse(r, c.argv)
			if c.want.code != "" {
				if e == nil || e.Code != c.want.code {
					t.Fatalf("错误码应为 %s，得到 %v", c.want.code, e)
				}
				if e.Category != output.CategoryUsage {
					t.Errorf("类别应为 usage，得到 %s", e.Category)
				}
				return
			}
			if e != nil {
				t.Fatalf("意外的错误：%v", e)
			}
			if !reflect.DeepEqual(p.path, c.want.path) && !(len(p.path) == 0 && len(c.want.path) == 0) {
				t.Errorf("路径 %v，应为 %v", p.path, c.want.path)
			}
			if p.help != c.want.help {
				t.Errorf("help 为 %v", p.help)
			}
			if c.want.help {
				return
			}
			if !reflect.DeepEqual(p.args, c.want.args) {
				t.Errorf("参数 %q，应为 %q", p.args, c.want.args)
			}
			if !reflect.DeepEqual(p.flags, c.want.flags) {
				t.Errorf("选项 %v，应为 %v", p.flags, c.want.flags)
			}
		})
	}
}

func TestRegistryRejectsBadDefinitions(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s：应当 panic", name)
			}
		}()
		fn()
	}
	mustPanic("缺少命令组说明", func() { NewRegistry().Add(&Command{Path: []string{"x", "y"}}) })
	mustPanic("重复命令", func() {
		r := NewRegistry()
		r.Add(&Command{Path: []string{"x"}})
		r.Add(&Command{Path: []string{"x"}})
	})
	mustPanic("命令与命令组同名", func() {
		r := NewRegistry()
		r.Group("x", "")
		r.Add(&Command{Path: []string{"x"}})
	})
}
