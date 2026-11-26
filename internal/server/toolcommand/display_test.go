package toolcommand

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDisplayShellCommand(t *testing.T) {
	plain := "git status --short\nprintf '%s\\n' done"
	if got := Display(plain); got != plain {
		t.Fatal("ordinary command changed", got)
	}
	if Display(" \n") != "" {
		t.Fatal("empty projected")
	}
	for _, command := range []string{
		`TOKEN='private value' curl localhost`, `MYSQL_PWD="private value" mysql`,
		`curl -H 'Authorization: Bearer private-value' localhost`,
		`mysql --password 'private value'`, `psql postgres://user:private@localhost/db`,
		"printf '%s' '-----BEGIN PRIVATE KEY-----\nprivate'",
	} {
		if got := Display(command); got != Hidden {
			t.Fatalf("sensitive command not hidden: %q", got)
		}
	}
	// Secret detection precedes truncation: a secret outside the visible prefix
	// must not let an earlier portion of the same sensitive command escape.
	long := strings.Repeat("界", MaxChars+10) // check-english: zh (rune-based truncation keeps UTF-8 valid)
	if got := Display(long); !utf8.ValidString(got) || utf8.RuneCountInString(got) != MaxChars || !strings.HasSuffix(got, "…") {
		t.Fatal("bound/UTF8", len(got))
	}
	if Display(long+" TOKEN=private") != Hidden {
		t.Fatal("truncated before redaction")
	}
	if strings.ContainsRune(Display("echo \x1b[31mtest\x00"), '\x1b') {
		t.Fatal("terminal escape leaked")
	}
}
