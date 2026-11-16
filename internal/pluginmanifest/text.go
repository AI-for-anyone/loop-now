package pluginmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Text is text that can be given per language (plugins/SPEC.md §2): a string, or an object with language tags as
// keys and non-empty strings as values, for example {"zh-CN": "飞书", "en-US": "Lark"}. It is stored and returned
// as-is (an object keeps the key order the author wrote), and the console picks the text for the current language;
// the server uses String when it needs a single string. When omitted it is the empty string.
type Text struct{ raw json.RawMessage }

// PlainText returns text in its plain string form.
func PlainText(s string) Text {
	b, _ := json.Marshal(s)
	return Text{raw: b}
}

func (t Text) MarshalJSON() ([]byte, error) {
	if len(t.raw) == 0 {
		return []byte(`""`), nil
	}
	return t.raw, nil
}

// UnmarshalJSON records the value as-is; Check decides whether it follows the rules.
func (t *Text) UnmarshalJSON(b []byte) error {
	t.raw = append(json.RawMessage(nil), b...)
	return nil
}

// langTag is the shape of a language tag (no full BCP 47 parsing; the same check as the SDK).
var langTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// ValidLanguageTag reports whether s has the shape of a language tag (such as zh-CN, en-US, en).
func ValidLanguageTag(s string) bool { return langTag.MatchString(s) }

var errText = errors.New("须是字符串，或以语言标签（如 zh-CN、en-US）为键、非空字符串为值的对象，至少一项")

// parse returns the value of the string form, or the keys (in the order the author wrote them) and entries of the
// object form.
func (t Text) parse() (plain string, keys []string, values map[string]string, err error) {
	raw := bytes.TrimSpace(t.raw)
	switch {
	case len(raw) == 0:
		return "", nil, nil, nil
	case raw[0] == '"':
		err = json.Unmarshal(raw, &plain)
		return plain, nil, nil, err
	case raw[0] != '{':
		return "", nil, nil, errText
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err = dec.Token(); err != nil {
		return "", nil, nil, err
	}
	values = map[string]string{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", nil, nil, err
		}
		key, _ := tok.(string)
		var v any
		if err = dec.Decode(&v); err != nil {
			return "", nil, nil, err
		}
		s, ok := v.(string)
		if _, dup := values[key]; dup || !ok || !langTag.MatchString(key) || strings.TrimSpace(s) == "" {
			return "", nil, nil, errText
		}
		keys = append(keys, key)
		values[key] = s
	}
	if len(keys) == 0 {
		return "", nil, nil, errText
	}
	return "", keys, values, nil
}

// Check checks the shape of the text; field is the field name shown in the error message.
func (t Text) Check(field string) error {
	if _, _, _, err := t.parse(); err != nil {
		return invalid(field + " " + errText.Error())
	}
	return nil
}

// String returns the text the server uses when it needs a single string: a string as-is; for an object, zh-CN,
// then en-US, then the first entry the author wrote.
func (t Text) String() string {
	plain, keys, values, err := t.parse()
	if err != nil || len(keys) == 0 {
		return plain
	}
	for _, k := range []string{"zh-CN", "en-US"} {
		if v, ok := values[k]; ok {
			return v
		}
	}
	return values[keys[0]]
}
