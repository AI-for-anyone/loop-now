package clitemplate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

func hasHan(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

// Every built-in field label is in English first: a string with no Chinese (a string counts as English), or an object
// with en-US then zh-CN, kept in that order when returned.
func TestBuiltinLabelsEnglishFirst(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range c.All() {
		for _, f := range tmpl.Fields {
			raw, _ := json.Marshal(f.Label)
			name := tmpl.Name + "." + f.Name
			if hasHan(f.Label.English()) {
				t.Errorf("%s: English label %q", name, f.Label.English())
			}
			if raw[0] == '"' {
				continue
			}
			dec := json.NewDecoder(bytes.NewReader(raw))
			_, _ = dec.Token()
			var keys []string
			for dec.More() {
				k, _ := dec.Token()
				var s string
				_ = dec.Decode(&s)
				keys = append(keys, k.(string))
			}
			if !slices.Equal(keys, []string{"en-US", "zh-CN"}) {
				t.Errorf("%s: want en-US then zh-CN, got %s", name, raw)
			}
		}
	}
}

// A template from the override directory may give a label per language; a label with the wrong shape fails to load.
func TestLoadOverrideDirLocalizedLabel(t *testing.T) {
	write := func(dir, label string) {
		tmpl := `{"name":"gh","version":"9.9.9","injection":"env","fields":[{"name":"token","label":` + label + `,"type":"secret","required":true}],
		"credential":{"env":{"GH_TOKEN":"{{token}}"}},"binary":"gh","install":{}}`
		if err := os.WriteFile(filepath.Join(dir, "gh.json"), []byte(tmpl), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	write(dir, `{"en-US":"Token","zh-CN":"令牌"}`) // check-english: zh (zh-CN value of a language-tag object)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	gh, _ := c.Get("gh")
	if b, _ := json.Marshal(gh.Fields[0]); !strings.Contains(string(b), `"label":{"en-US":"Token","zh-CN":"令牌"}`) { // check-english: zh (zh-CN value of a language-tag object)
		t.Fatalf("label should be returned as written: %s", b)
	}
	for _, bad := range []string{`{}`, `3`, `{"en-US":" "}`, `null`} {
		write(dir, bad)
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), `field "token" has an invalid label`) {
			t.Fatalf("label %s should fail to load: %v", bad, err)
		}
	}
	write(dir, `""`)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), `field "token" is invalid or duplicated`) {
		t.Fatalf("an empty label should fail to load: %v", err)
	}
}
