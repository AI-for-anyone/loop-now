// Package guide generates the built-in Skill the server writes for every Agent (instructions for using loopctl) and
// the Inbox reminder commands.
// The content is generated from an embedded template and the command registry; it depends only on code and is
// byte-for-byte identical within a version.
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

// SkillName is the name of the built-in Skill; the template library and an Agent's Skill copies may not use this
// name.
const SkillName = "loopctl"

//go:embed skill.md.tmpl
var skillTemplate string

var files = sync.OnceValue(func() map[string][]byte {
	md, err := Render(buildinfo.Version())
	if err != nil {
		panic(err) // the content depends only on code; tests make sure generation succeeds
	}
	return map[string][]byte{"SKILL.md": md}
})

// Files returns the files of the built-in Skill: relative path -> content. The first version only has SKILL.md.
// Callers must not modify the returned content.
func Files() map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files() {
		out[k] = append([]byte(nil), v...)
	}
	return out
}

// InboxReminderCommand returns the command the Inbox reminder text asks the Agent to run.
func InboxReminderCommand() string {
	c := commands.Registry().Find("inbox", "list")
	if c == nil {
		panic("命令 inbox list 不存在")
	}
	return "loopctl " + c.Name() + " --unread"
}

// InboxReadCommand returns the command the Agent is asked to use to read messages one by one when a platform
// reminder names specific messages (followed by the message ID).
func InboxReadCommand() string {
	c := commands.Registry().Find("inbox", "read")
	if c == nil {
		panic("命令 inbox read 不存在")
	}
	return "loopctl " + c.Name()
}

// Render generates SKILL.md for the given version.
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
	b.WriteString("| Exit code | Category | Meaning |\n|---|---|---|\n")
	for _, e := range output.ExitTable() {
		cat := "—"
		if e.Category != "" {
			cat = "`" + string(e.Category) + "`"
		}
		fmt.Fprintf(&b, "| %d | %s | %s |\n", e.Code, cat, e.Description)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// reference lists every command available to the Agent identity: summary, arguments, options and a usage example
// that parses as is.
func reference(r *cli.Registry) string {
	var sections []string
	for _, c := range r.Commands() {
		if !c.AllowedFor(cli.IdentityAgent) {
			continue
		}
		var items []string
		for _, a := range c.Args {
			items = append(items, fmt.Sprintf("- `<%s>`: %s", a.Name, a.Description))
		}
		for _, f := range c.Flags {
			name := "--" + f.Name
			if f.Type != cli.TypeBool {
				name += " <" + string(f.Type) + ">"
			}
			item := fmt.Sprintf("- `%s`: %s", name, f.Description)
			if f.Repeatable {
				item += " (repeatable, in the order given)"
			}
			if len(f.Values) > 0 {
				item += fmt.Sprintf(" (values: %s)", strings.Join(f.Values, ", "))
			}
			items = append(items, item)
		}
		for _, k := range c.Constraints {
			items = append(items, fmt.Sprintf("- %s: --%s", constraintText(k.Kind), strings.Join(k.Flags, ", --")))
		}
		s := fmt.Sprintf("### loopctl %s\n\n%s\n\nUsage: `%s`", c.Name(), c.Summary, usage(c))
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
		return "Exactly one of these is required"
	case cli.AtMostOne:
		return "At most one of these"
	case cli.AtLeastOne:
		return "At least one of these is required"
	default:
		return "These must be given together"
	}
}

// usage builds the simplest usage that parses: positional arguments and required options get placeholders, and for
// options where one of two must be given, the first is used.
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
