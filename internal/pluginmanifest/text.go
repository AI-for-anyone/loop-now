package pluginmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Text 是可以按语言给出的文字（plugins/SPEC.md §2）：字符串，或以语言标签为键、非空字符串为值的对象，
// 例如 {"zh-CN": "飞书", "en-US": "Lark"}。原样保存与返回（对象保留作者写的键顺序），由控制台按当前语言取字；
// 服务端需要单个字符串时用 String。缺省时是空字符串。
type Text struct{ raw json.RawMessage }

// PlainText 返回字符串形式的文字。
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

// UnmarshalJSON 原样记下；是否合规则由 Check 判断。
func (t *Text) UnmarshalJSON(b []byte) error {
	t.raw = append(json.RawMessage(nil), b...)
	return nil
}

// langTag 是语言标签的写法（不做完整的 BCP 47 解析，与 SDK 的检查相同）。
var langTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

var errText = errors.New("须是字符串，或以语言标签（如 zh-CN、en-US）为键、非空字符串为值的对象，至少一项")

// parse 返回字符串形式的值，或对象形式的键（按作者写的顺序）与各项。
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

// Check 检查文字的写法；field 是出现在提示里的字段名。
func (t Text) Check(field string) error {
	if _, _, _, err := t.parse(); err != nil {
		return invalid(field + " " + errText.Error())
	}
	return nil
}

// String 返回服务端需要单个字符串时用的文字：字符串原样；对象依次取 zh-CN、en-US、作者写的第一项。
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
