package pluginmanifest_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode"

	"loop-now/internal/pluginmanifest"
)

func text(en, zh string) map[string]any { return map[string]any{"en-US": en, "zh-CN": zh} }

// localized returns full() with every label, and title and description at several schema levels, given per language.
func localized() map[string]any {
	p := full()
	ln := p["loopNow"].(map[string]any)
	ln["permissions"] = []any{"inbox.read", "state", "identities.manage"}
	ln["configSchema"] = map[string]any{"type": "object", "title": text("Webhook", "Webhook"), "properties": map[string]any{
		"url": map[string]any{"type": "string", "title": text("Callback URL", "回调地址"), "description": text("An http or https URL.", "http 或 https 地址。")},
		"recipients": map[string]any{"type": "string", "title": text("Which messages to send", "推送哪些消息"), "default": "external", "oneOf": []any{
			map[string]any{"const": "external", "title": text("Only external identities", "只推给外部身份")},
			map[string]any{"const": "all", "title": "All recipients"},
		}},
		"retry": map[string]any{"type": "object", "properties": map[string]any{
			"limit": map[string]any{"type": "integer", "title": text("Retries on failure", "失败重试次数")},
		}},
		"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string", "description": text("A tag.", "标签。")}},
	}}
	ln["accounts"] = []any{map[string]any{"kind": "lark", "label": text("Lark account", "飞书账号"), "multiple": true, "fields": map[string]any{
		"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string", "x-secret": true, "title": text("Token", "令牌")}}}}}
	ln["actions"] = []any{map[string]any{"id": "resend", "label": text("Resend", "重发"), "where": []any{"identity"}, "fields": map[string]any{
		"type": "object", "properties": map[string]any{"note": map[string]any{"type": "string", "title": text("Note", "备注")}}}}}
	ln["channels"] = []any{map[string]any{"id": "lark", "label": text("Lark", "飞书"), "manageIdentities": true}}
	return p
}

func readPkg(t *testing.T, pkg map[string]any) (pluginmanifest.Manifest, error) {
	t.Helper()
	return pluginmanifest.ReadManifest(writePlugin(t, pkg, "agent/index.ts", "skills/", "platform/index.ts", "bin/"), "2026.11")
}

// Labels and schema titles and descriptions given per language are accepted and returned as written, and the config
// schema still validates values: it is compiled with the English text in place of each localized annotation.
func TestLocalizedLabelsAndSchemaText(t *testing.T) {
	m, err := readPkg(t, localized())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m.Public())
	for _, want := range []string{
		`"label":{"en-US":"Lark account","zh-CN":"飞书账号"}`,
		`"label":{"en-US":"Resend","zh-CN":"重发"}`,
		`"label":{"en-US":"Lark","zh-CN":"飞书"}`,
		`"title":{"en-US":"Callback URL","zh-CN":"回调地址"}`,
		`{"const":"external","title":{"en-US":"Only external identities","zh-CN":"只推给外部身份"}}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the API should return %s as written:\n%s", want, b)
		}
	}
	if c := m.Components.Channels[0].Label; c.English() != "Lark" || c.String() != "飞书" || c.In("ja") != "飞书" {
		t.Fatalf("channel label: English %q, String %q", c.English(), c.String())
	}
	if err := pluginmanifest.Validate(m.ConfigSchema.Map(), false, map[string]any{"url": "https://example.com", "recipients": "all", "retry": map[string]any{"limit": 3}}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := pluginmanifest.Validate(m.ConfigSchema.Map(), false, map[string]any{"recipients": "nobody"}); err == nil || !strings.Contains(err.Error(), "Does not match the plugin's config schema") {
		t.Fatalf("a value outside oneOf should be rejected: %v", err)
	}
	props := m.ConfigSchema.Map()["properties"].(map[string]any)
	if _, ok := props["url"].(map[string]any)["title"].(map[string]any); !ok {
		t.Fatalf("compiling must not change the stored schema: %v", props["url"])
	}
	if err := pluginmanifest.Validate(m.Accounts[0].Fields.Map(), true, map[string]any{"token": "x"}); err != nil {
		t.Fatalf("account fields: %v", err)
	}
}

// A localized value with the wrong shape is rejected with its path; other non-string annotations are still rejected by
// the metaschema; keys that only look like annotations (a property named title, data under default) are not checked.
func TestLocalizedTextRejects(t *testing.T) {
	cases := []struct {
		name string
		edit func(ln map[string]any)
		want string
	}{
		{"empty title object", func(ln map[string]any) {
			ln["configSchema"].(map[string]any)["properties"].(map[string]any)["url"].(map[string]any)["title"] = map[string]any{}
		}, "loopNow.configSchema.properties.url.title must be a string, or an object with language tags"},
		{"blank oneOf title", func(ln map[string]any) {
			ln["configSchema"].(map[string]any)["properties"].(map[string]any)["recipients"].(map[string]any)["oneOf"].([]any)[1].(map[string]any)["title"] = map[string]any{"en-US": " "}
		}, "loopNow.configSchema.properties.recipients.oneOf[1].title must be a string, or an object with language tags"},
		{"nested description", func(ln map[string]any) {
			ln["configSchema"].(map[string]any)["properties"].(map[string]any)["tags"].(map[string]any)["items"].(map[string]any)["description"] = map[string]any{"en_US": "x"}
		}, "loopNow.configSchema.properties.tags.items.description must be a string, or an object with language tags"},
		{"account field title", func(ln map[string]any) {
			ln["accounts"].([]any)[0].(map[string]any)["fields"].(map[string]any)["properties"].(map[string]any)["token"].(map[string]any)["title"] = map[string]any{"en-US": 3}
		}, "loopNow.accounts[0].fields.properties.token.title must be a string, or an object with language tags"},
		{"action field title", func(ln map[string]any) {
			ln["actions"].([]any)[0].(map[string]any)["fields"].(map[string]any)["properties"].(map[string]any)["note"].(map[string]any)["title"] = map[string]any{}
		}, "loopNow.actions[0].fields.properties.note.title must be a string, or an object with language tags"},
		{"account label", func(ln map[string]any) { ln["accounts"].([]any)[0].(map[string]any)["label"] = []any{"x"} },
			"loopNow.accounts[0].label must be a string, or an object with language tags"},
		{"action label", func(ln map[string]any) { ln["actions"].([]any)[0].(map[string]any)["label"] = 3 },
			"loopNow.actions[0].label must be a string, or an object with language tags"},
		{"channel label", func(ln map[string]any) { ln["channels"].([]any)[0].(map[string]any)["label"] = map[string]any{} },
			"loopNow.channels[0].label must be a string, or an object with language tags"},
		{"empty channel label", func(ln map[string]any) { ln["channels"].([]any)[0].(map[string]any)["label"] = "" },
			"Each entry in loopNow.channels must have a unique id and a label."},
		{"number title", func(ln map[string]any) {
			ln["configSchema"].(map[string]any)["properties"].(map[string]any)["url"].(map[string]any)["title"] = 3
		}, "loopNow.configSchema is not a valid JSON Schema"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := localized()
			c.edit(p["loopNow"].(map[string]any))
			if _, err := readPkg(t, p); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
	p := localized()
	props := p["loopNow"].(map[string]any)["configSchema"].(map[string]any)["properties"].(map[string]any)
	props["title"] = map[string]any{"type": "string"}
	props["meta"] = map[string]any{"type": "object", "default": map[string]any{"title": map[string]any{}, "description": 3}}
	if _, err := readPkg(t, p); err != nil {
		t.Fatalf("a property named title and data under default are not annotations: %v", err)
	}
}

// A manifest stored by the previous version (labels and schema titles as plain strings, written by 05d7852) reads back
// without errors and marshals to the same JSON.
func TestStoredManifestFromPreviousVersion(t *testing.T) {
	old, err := os.ReadFile(filepath.Join("testdata", "stored-manifest-05d7852.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m pluginmanifest.Manifest
	if err := json.Unmarshal(old, &m); err != nil {
		t.Fatal(err)
	}
	if m.Accounts[0].Label.English() != "飞书账号" || m.Components.Actions[0].Label.String() != "重发" || m.Components.Channels[0].Label.English() != "飞书" {
		t.Fatalf("plain labels: %+v", m.Components)
	}
	again, _ := json.Marshal(m)
	var a, b any
	if json.Unmarshal(old, &a) != nil || json.Unmarshal(again, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("stored manifest changed on a round trip:\n%s\n%s", old, again)
	}
	if err := pluginmanifest.Validate(m.ConfigSchema.Map(), false, map[string]any{"recipients": "all"}); err != nil {
		t.Fatal(err)
	}
}

// Text.In picks the way the console does; English is In("en-US"); String keeps its zh-CN first order.
func TestTextIn(t *testing.T) {
	both := pluginmanifest.Text{}
	if err := json.Unmarshal([]byte(`{"en-US":"Lark","zh-CN":"飞书"}`), &both); err != nil {
		t.Fatal(err)
	}
	only := pluginmanifest.Text{}
	if err := json.Unmarshal([]byte(`{"ja":"ラーク"}`), &only); err != nil {
		t.Fatal(err)
	}
	plain := pluginmanifest.PlainText("Lark")
	for _, c := range []struct{ got, want string }{
		{both.English(), "Lark"}, {both.String(), "飞书"}, {both.In("zh-CN"), "飞书"}, {both.In("ja"), "飞书"},
		{only.English(), "ラーク"}, {plain.English(), "Lark"}, {plain.In("zh-CN"), "Lark"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if !plain.Valid() || !both.Valid() || (pluginmanifest.Text{}).Valid() != true {
		t.Fatal("valid texts reported invalid")
	}
}

// The official plugins read with the server's rules, and every label, title and description in them is in English
// first: a string with no Chinese, or an object whose first key is en-US and which has zh-CN.
func TestOfficialPluginsEnglishFirst(t *testing.T) {
	dirs, _ := filepath.Glob(filepath.Join("..", "..", "plugins", "official", "*", "package.json"))
	if len(dirs) != 3 {
		t.Fatalf("want 3 official plugins, got %v", dirs)
	}
	for _, pkg := range dirs {
		dir := filepath.Dir(pkg)
		if _, err := pluginmanifest.ReadManifest(dir, ""); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
		raw, err := os.ReadFile(pkg)
		if err != nil {
			t.Fatal(err)
		}
		checkEnglishFirst(t, filepath.Base(dir), json.NewDecoder(bytes.NewReader(raw)), "")
	}
}

func hasHan(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) }) >= 0
}

// checkEnglishFirst reads one value from dec, whose key is key, and checks every displayName, description, label and
// title in it: a string with no Chinese (a string counts as English), or an object with exactly en-US then zh-CN.
func checkEnglishFirst(t *testing.T, name string, dec *json.Decoder, key string) {
	t.Helper()
	tok, err := dec.Token()
	if err != nil {
		t.Fatal(err)
	}
	localized := key == "displayName" || key == "description" || key == "label" || key == "title"
	switch tok {
	case json.Delim('{'):
		var keys []string
		for dec.More() {
			k, _ := dec.Token()
			keys = append(keys, k.(string))
			if !localized {
				checkEnglishFirst(t, name, dec, k.(string))
				continue
			}
			var s string
			if err := dec.Decode(&s); err != nil || k == "en-US" && hasHan(s) {
				t.Errorf("%s %s.%s: %q %v", name, key, k, s, err)
			}
		}
		_, _ = dec.Token()
		if localized && !slices.Equal(keys, []string{"en-US", "zh-CN"}) {
			t.Errorf("%s %s: want en-US then zh-CN, got %v", name, key, keys)
		}
	case json.Delim('['):
		for dec.More() {
			checkEnglishFirst(t, name, dec, "")
		}
		_, _ = dec.Token()
	default:
		if s, ok := tok.(string); ok && localized && hasHan(s) {
			t.Errorf("%s %s: a string counts as English: %q", name, key, s)
		}
	}
}
