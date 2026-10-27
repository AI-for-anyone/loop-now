package pluginmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// 配置与账号字段按清单里的 JSON Schema 校验（plugins/SPEC.md §2）：顶层属性带 "x-secret": true 的是 secret 字段，
// 只写不回显；带 "x-scope": "project" 的可在项目里覆盖。

// CompileSchema 编译插件声明的 JSON Schema。required 为 false 时忽略顶层 required：
// 全局配置与项目配置分开保存，任何一处都可能还没填全，必填项由表单提示。
func CompileSchema(schema map[string]any, required bool) (*jsonschema.Schema, error) {
	if !required {
		schema = maps.Clone(schema)
		delete(schema, "required")
	}
	doc, err := toSchemaValue(schema)
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("plugin.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("plugin.json")
}

// toSchemaValue 把 Go 值转成校验库要求的形式（数字为 json.Number）。
func toSchemaValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

// Validate 用 schema 校验 value；不合规则时返回 *Error。
func Validate(schema map[string]any, required bool, value map[string]any) error {
	sch, err := CompileSchema(schema, required)
	if err != nil {
		return invalid("插件声明的 JSON Schema 无法使用：" + err.Error())
	}
	inst, err := toSchemaValue(value)
	if err != nil {
		return invalid(err.Error())
	}
	if err := sch.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		msg := err.Error()
		if errors.As(err, &ve) {
			msg = strings.TrimSpace(strings.TrimPrefix(ve.Error(), "jsonschema validation failed with 'plugin.json#'"))
		}
		return invalid("不符合插件的配置规则：" + msg)
	}
	return nil
}

// properties 返回顶层属性的子 schema。
func properties(schema map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for k, v := range props {
		if m, ok := v.(map[string]any); ok {
			out[k] = m
		}
	}
	return out
}

// SecretFields 返回 x-secret 的顶层字段，按清单里的先后。
func SecretFields(schema Schema) []string {
	var out []string
	props := properties(schema.Map())
	for _, k := range schema.Properties() {
		if props[k]["x-secret"] == true {
			out = append(out, k)
		}
	}
	return out
}

// ProjectFields 返回可在项目覆盖（x-scope: project）的顶层字段，按清单里的先后。
func ProjectFields(schema Schema) []string {
	out := []string{}
	props := properties(schema.Map())
	for _, k := range schema.Properties() {
		if props[k]["x-scope"] == "project" {
			out = append(out, k)
		}
	}
	return out
}

// Schema 是清单里的一份 JSON Schema（configSchema、accounts[].fields、actions[].fields）。序列化时原样输出清单里的写法：
// 属性的先后就是表单字段的先后，存进数据库与经接口返回都保持不变。Map 是解析后的值，供校验与查找字段。零值是空对象。
type Schema struct {
	raw json.RawMessage
	m   map[string]any
}

// NewSchema 由解析后的值构造（没有原文，属性按键名排序；测试与默认值用）。
func NewSchema(m map[string]any) Schema {
	raw, _ := json.Marshal(m)
	return Schema{raw: raw, m: m}
}

// MarshalJSON 原样输出清单里的写法。
func (s Schema) MarshalJSON() ([]byte, error) {
	if len(s.raw) == 0 {
		return []byte("{}"), nil
	}
	return s.raw, nil
}

// UnmarshalJSON 保留原文（去掉空白），同时解析出 Map；null 视为没有给出。
func (s *Schema) UnmarshalJSON(b []byte) error {
	if string(bytes.TrimSpace(b)) == "null" {
		*s = Schema{}
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		return err
	}
	s.raw, s.m = buf.Bytes(), m
	return nil
}

// Map 返回解析后的值；没有给出时为空对象。
func (s Schema) Map() map[string]any {
	if s.m == nil {
		return map[string]any{}
	}
	return s.m
}

// IsZero 报告清单里有没有给出这份 schema。
func (s Schema) IsZero() bool { return len(s.raw) == 0 && s.m == nil }

// Properties 返回顶层 properties 的键，按清单里的先后。
func (s Schema) Properties() []string {
	var out []string
	dec := json.NewDecoder(bytes.NewReader(s.raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return out
		}
		if key != "properties" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return out
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return out
		}
		for dec.More() {
			name, err := dec.Token()
			if err != nil {
				return out
			}
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return out
			}
			if n, ok := name.(string); ok {
				out = append(out, n)
			}
		}
		return out
	}
	return out
}
