// Package toolcommand projects only bounded bash command text, never arbitrary
// tool inputs or results. Credential-bearing commands are hidden as a whole.
package toolcommand

import (
	"regexp"
	"strings"
	"unicode"
)

const MaxChars = 4096
const Hidden = "[含敏感凭证的命令已隐藏]"

// Conservative display guard, not a shell parser. Hiding the whole command
// avoids leaking quoted/multiline values through partial regex replacement.
var sensitive = regexp.MustCompile(`(?i)(authorization\s*:|proxy-authorization\s*:|bearer\s+|-----BEGIN [A-Z ]*PRIVATE KEY|(?:password|passwd|pwd|token|secret|api[_-]?key|access[_-]?key|credential)[\w-]*[\s"']*(?:=|:)|--[\w-]*(?:password|passwd|token|secret|api-key|credential)(?:\s|=)|(?:https?|postgres(?:ql)?|mysql)://[^\s/]+:[^\s/]+@|(?:^|\s)(?:-p\S+|--password\b)|\b(?:gh[pousr]_|github_pat_|sk-)[A-Za-z0-9_\-]{8,})`)

func Display(name, command string) string {
	if name != "bash" || strings.TrimSpace(command) == "" {
		return ""
	}
	if sensitive.MatchString(command) {
		return Hidden
	}
	command = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return ' '
		}
		return r
	}, command)
	// Bound the public field by Unicode characters without breaking UTF-8.
	runes := []rune(command)
	if len(runes) > MaxChars {
		command = string(runes[:MaxChars-1]) + "…"
	}
	return command
}
