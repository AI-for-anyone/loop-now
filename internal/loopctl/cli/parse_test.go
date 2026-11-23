package cli

import (
	"context"
	"reflect"
	"testing"

	"loop-now/internal/loopctl/output"
)

func testRegistry() *Registry {
	r := NewRegistry()
	r.Group("box", "test command group")
	r.Group("box sub", "subcommand group")
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
		{"flags and arguments interleaved", []string{"box", "send", "--need", "x", "to1", "--text", "hi"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "hi"}}},
		{"equals form", []string{"box", "send", "to1", "--need=x", "--text=a=b"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "a=b"}}},
		{"value may start with a dash", []string{"box", "send", "to1", "--need", "x", "--text", "-5"},
			want{path: []string{"box", "send"}, args: []string{"to1"}, flags: map[string]string{"need": "x", "text": "-5"}}},
		{"a lone - is a positional argument", []string{"box", "send", "-", "--need", "x", "--file", "-"},
			want{path: []string{"box", "send"}, args: []string{"-"}, flags: map[string]string{"need": "x", "file": "-"}}},
		{"everything after -- is positional", []string{"box", "send", "--need", "x", "--text", "t", "--", "--not-a-flag"},
			want{path: []string{"box", "send"}, args: []string{"--not-a-flag"}, flags: map[string]string{"need": "x", "text": "t"}}},
		{"boolean flag", []string{"box", "send", "to", "--need", "x", "--text", "t", "--flag"},
			want{path: []string{"box", "send"}, args: []string{"to"}, flags: map[string]string{"need": "x", "text": "t", "flag": "true"}}},
		{"boolean flag takes no value", []string{"box", "send", "to", "--need", "x", "--text", "t", "--flag=true"}, want{code: output.CodeInvalidArgument}},
		{"unknown flag", []string{"box", "send", "to", "--need", "x", "--text", "t", "--nope"}, want{code: output.CodeInvalidArgument}},
		{"short flag", []string{"box", "send", "to", "-x"}, want{code: output.CodeInvalidArgument}},
		{"repeated flag", []string{"box", "send", "to", "--need", "x", "--need", "y", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"flag missing its value", []string{"box", "send", "to", "--text", "t", "--need"}, want{code: output.CodeMissingArgument}},
		{"missing positional argument", []string{"box", "send", "--need", "x", "--text", "t"}, want{code: output.CodeMissingArgument}},
		{"extra positional argument", []string{"box", "send", "a", "b", "--need", "x", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"missing required flag", []string{"box", "send", "to", "--text", "t"}, want{code: output.CodeMissingArgument}},
		{"empty value", []string{"box", "send", "to", "--need", "", "--text", "t"}, want{code: output.CodeInvalidArgument}},
		{"bad integer", []string{"box", "send", "to", "--need", "x", "--text", "t", "--n", "abc"}, want{code: output.CodeInvalidArgument}},
		{"integer below minimum", []string{"box", "send", "to", "--need", "x", "--text", "t", "--n", "0"}, want{code: output.CodeInvalidArgument}},
		{"bad duration", []string{"box", "send", "to", "--need", "x", "--text", "t", "--wait", "5"}, want{code: output.CodeInvalidArgument}},
		{"bad enum value", []string{"box", "send", "to", "--need", "x", "--text", "t", "--mode", "c"}, want{code: output.CodeInvalidArgument}},
		{"bad time", []string{"box", "send", "to", "--need", "x", "--text", "t", "--at", "2026-10-13"}, want{code: output.CodeInvalidArgument}},
		{"valid time", []string{"box", "send", "to", "--need", "x", "--text", "t", "--at", "2026-10-13T00:00:00+08:00"},
			want{path: []string{"box", "send"}, args: []string{"to"}, flags: map[string]string{"need": "x", "text": "t", "at": "2026-10-13T00:00:00+08:00"}}},
		{"neither of exactly-one given", []string{"box", "send", "to", "--need", "x"}, want{code: output.CodeMissingArgument}},
		{"both of exactly-one given", []string{"box", "send", "to", "--need", "x", "--text", "t", "--file", "f"}, want{code: output.CodeInvalidArgument}},
		{"flags required together", []string{"box", "net", "--a", "1"}, want{code: output.CodeInvalidArgument}},
		{"connection flags only for network commands", []string{"box", "net", "--profile", "p", "--timeout", "5s"},
			want{path: []string{"box", "net"}, flags: map[string]string{"profile": "p", "timeout": "5s"}}},
		{"local command rejects connection flags", []string{"box", "sub", "rm", "1", "--profile", "p"}, want{code: output.CodeInvalidArgument}},
		{"destructive command has --yes", []string{"box", "sub", "rm", "1", "--yes"},
			want{path: []string{"box", "sub", "rm"}, args: []string{"1"}, flags: map[string]string{"yes": "true"}}},
		{"variadic arguments", []string{"many", "a", "b"}, want{path: []string{"many"}, args: []string{"a", "b"}, flags: map[string]string{}}},
		{"variadic arguments may be empty", []string{"many"}, want{path: []string{"many"}, flags: map[string]string{}}},
		{"--help wins over argument errors", []string{"box", "send", "--nope", "--help"}, want{path: []string{"box", "send"}, help: true}},
		{"--help on a command group", []string{"box", "--help"}, want{path: []string{"box"}, help: true}},
		{"no arguments", []string{}, want{help: true}},
		{"unknown command", []string{"nope"}, want{code: output.CodeUnknownCommand}},
		{"unknown subcommand", []string{"box", "nope"}, want{code: output.CodeUnknownCommand}},
		{"command group only", []string{"box"}, want{code: output.CodeUnknownCommand}},
		{"flag before the command", []string{"--profile", "p", "box", "net"}, want{code: output.CodeInvalidArgument}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, e := parse(r, c.argv)
			if c.want.code != "" {
				if e == nil || e.Code != c.want.code {
					t.Fatalf("error code should be %s, got %v", c.want.code, e)
				}
				if e.Category != output.CategoryUsage {
					t.Errorf("category should be usage, got %s", e.Category)
				}
				return
			}
			if e != nil {
				t.Fatalf("unexpected error: %v", e)
			}
			if !reflect.DeepEqual(p.path, c.want.path) && !(len(p.path) == 0 && len(c.want.path) == 0) {
				t.Errorf("path %v, want %v", p.path, c.want.path)
			}
			if p.help != c.want.help {
				t.Errorf("help is %v", p.help)
			}
			if c.want.help {
				return
			}
			if !reflect.DeepEqual(p.args, c.want.args) {
				t.Errorf("arguments %q, want %q", p.args, c.want.args)
			}
			if !reflect.DeepEqual(p.flags, c.want.flags) {
				t.Errorf("flags %v, want %v", p.flags, c.want.flags)
			}
		})
	}
}

func TestRegistryRejectsBadDefinitions(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: should panic", name)
			}
		}()
		fn()
	}
	mustPanic("missing command group description", func() { NewRegistry().Add(&Command{Path: []string{"x", "y"}}) })
	mustPanic("duplicate command", func() {
		r := NewRegistry()
		r.Add(&Command{Path: []string{"x"}})
		r.Add(&Command{Path: []string{"x"}})
	})
	mustPanic("command named like a command group", func() {
		r := NewRegistry()
		r.Group("x", "")
		r.Add(&Command{Path: []string{"x"}})
	})
}
