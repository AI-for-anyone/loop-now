package cli

import (
	"strconv"
	"strings"
	"time"

	"loop-now/internal/loopctl/output"
)

// parsed is the result of one parse.
type parsed struct {
	path     []string // path of the command or command group that was parsed
	cmd      *Command // non-nil when a command was parsed
	help     bool
	args     []string
	flags    map[string]string
	repeated map[string][]string
}

// parse parses the command line (without the program name). When help is true, other argument errors are ignored.
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
			// A command may also have subcommands; only consume paths that are explicitly registered and keep ordinary
			// positional arguments.
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

// parseGroup handles the case where no concrete command was parsed: a command group, an unknown command or an
// empty command line.
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
		p.help = true // run without arguments: print the full description
		return nil
	}
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		if len(p.path) == 0 {
			return output.Errorf(output.CodeUnknownCommand, "Unknown command %q. Run loopctl schema to see all commands.", rest[0])
		}
		return output.Errorf(output.CodeUnknownCommand, "%q is not a subcommand of %s. See details.subcommands for the available ones.", rest[0], strings.Join(p.path, " ")).
			WithDetails(map[string]any{"subcommands": r.children(p.path)})
	}
	if len(p.path) == 0 {
		return output.Errorf(output.CodeInvalidArgument, "Options must come after the command path. Run loopctl schema to see all commands.")
	}
	return output.Errorf(output.CodeUnknownCommand, "%s is a command group. Specify one of its subcommands (listed in details.subcommands).", strings.Join(p.path, " ")).
		WithDetails(map[string]any{"subcommands": r.children(p.path)})
}

// parseCommand parses the options and positional arguments after the command path.
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
			fail(output.Errorf(output.CodeInvalidArgument, "Unknown option %s. Options are written as --name.", tok))
			continue
		}
		name, value, hasValue := strings.Cut(tok[2:], "=")
		f, ok := known[name]
		if !ok {
			fail(output.Errorf(output.CodeInvalidArgument, "Unknown option --%s. Run the command with --help to see its options.", name))
			continue
		}
		if _, dup := p.flags[name]; dup && !f.Repeatable {
			fail(output.Errorf(output.CodeInvalidArgument, "Option --%s is given more than once.", name))
			continue
		}
		if f.Type == TypeBool {
			if hasValue {
				fail(output.Errorf(output.CodeInvalidArgument, "Option --%s does not take a value.", name))
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
				fail(output.Errorf(output.CodeMissingArgument, "Option --%s needs a value.", name))
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

// validate checks the number of positional arguments, required options, option values and the constraints between
// options.
func validate(c *Command, p *parsed) *output.Error {
	min, variadic := len(c.Args), false
	if n := len(c.Args); n > 0 && c.Args[n-1].Variadic {
		min, variadic = n-1, true
	}
	if len(p.args) < min {
		return output.Errorf(output.CodeMissingArgument, "Argument <%s> is required.", c.Args[len(p.args)].Name)
	}
	if !variadic && len(p.args) > min {
		return output.Errorf(output.CodeInvalidArgument, "Too many arguments: %q is not expected.", p.args[min])
	}
	for _, f := range effectiveFlags(c) {
		v, set := p.flags[f.Name]
		if !set {
			if f.Required {
				return output.Errorf(output.CodeMissingArgument, "Option --%s is required.", f.Name)
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
		list := "--" + strings.Join(k.Flags, ", --")
		hint := ""
		if k.Hint != "" {
			hint = " " + k.Hint
		}
		switch {
		case k.Kind == ExactlyOne && n == 0:
			return output.Errorf(output.CodeMissingArgument, "Exactly one of %s is required.%s", list, hint)
		case (k.Kind == ExactlyOne || k.Kind == AtMostOne) && n > 1:
			return output.Errorf(output.CodeInvalidArgument, "Only one of %s can be given.%s", list, hint)
		case k.Kind == AtLeastOne && n == 0:
			return output.Errorf(output.CodeMissingArgument, "At least one of %s is required.%s", list, hint)
		case k.Kind == Together && n != 0 && n != len(k.Flags):
			return output.Errorf(output.CodeInvalidArgument, "%s must be given together.%s", list, hint)
		}
	}
	return nil
}

func checkValue(f Flag, v string) *output.Error {
	if v == "" {
		return output.Errorf(output.CodeInvalidArgument, "Option --%s must not be empty.", f.Name)
	}
	switch f.Type {
	case TypeInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return output.Errorf(output.CodeInvalidArgument, "Option --%s must be an integer.", f.Name)
		}
		if f.Min != nil && n < *f.Min {
			return output.Errorf(output.CodeInvalidArgument, "Option --%s must be at least %d.", f.Name, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return output.Errorf(output.CodeInvalidArgument, "Option --%s must be at most %d.", f.Name, *f.Max)
		}
	case TypeDuration:
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return output.Errorf(output.CodeInvalidArgument, "Option --%s must be a positive duration, for example 30s.", f.Name)
		}
	case TypeTime:
		if _, err := time.Parse(time.RFC3339, v); err != nil {
			return output.Errorf(output.CodeInvalidArgument, "Option --%s must be an RFC 3339 time, for example 2026-10-13T00:00:00+08:00.", f.Name)
		}
	case TypeEnum:
		for _, x := range f.Values {
			if x == v {
				return nil
			}
		}
		return output.Errorf(output.CodeInvalidArgument, "Option --%s must be one of: %s", f.Name, strings.Join(f.Values, ", "))
	}
	return nil
}
