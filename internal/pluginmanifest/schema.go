package pluginmanifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Config and account fields are validated against the JSON Schema in the manifest (plugins/SPEC.md §2): a top-level
// property with "x-secret": true is a secret field, write-only and never echoed back; one with "x-scope": "project"
// can be overridden per project.

// CompileSchema compiles a JSON Schema declared by a plugin. When required is false the top-level required is
// ignored: global and project config are stored separately and either may not be filled in completely yet, so
// required fields are pointed out by the form instead.
func CompileSchema(schema map[string]any, required bool) (*jsonschema.Schema, error) {
	if !required {
		schema = maps.Clone(schema)
		delete(schema, "required")
	}
	doc, err := toSchemaValue(schema)
	if err != nil {
		return nil, err
	}
	// doc is a fresh copy, so the annotations can be replaced in place.
	walkSchema(doc, "", func(node map[string]any, _ string) error {
		for _, k := range localizedKeywords {
			if obj, ok := node[k].(map[string]any); ok {
				if text, ok := textOf(obj); ok {
					node[k] = text.English()
				}
			}
		}
		return nil
	})
	c := jsonschema.NewCompiler()
	if err := c.AddResource("plugin.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("plugin.json")
}

// localizedKeywords are the annotations that may be localized text (plugins/SPEC.md §2). The JSON Schema metaschema
// wants strings there, so CompileSchema compiles a copy with their English text; the schema itself is stored and
// returned as written.
var localizedKeywords = []string{"title", "description"}

// Keywords whose values hold sub-schemas, by shape: a schema (or, for items in older drafts, a list of schemas), a list
// of schemas, or an object of schemas.
var (
	schemaKeywords    = []string{"additionalProperties", "items", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "contentSchema"}
	schemaListKeyword = []string{"allOf", "anyOf", "oneOf", "prefixItems"}
	schemaMapKeywords = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"}
)

// walkSchema calls visit for v and every sub-schema in it that is an object, with its path such as
// ".properties.url" (empty for v itself). Values of other keywords (const, enum, default, examples) are data and are
// not visited. It stops at the first error.
func walkSchema(v any, path string, visit func(node map[string]any, path string) error) error {
	node, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	if err := visit(node, path); err != nil {
		return err
	}
	for _, k := range schemaKeywords {
		switch sub := node[k].(type) {
		case map[string]any:
			if err := walkSchema(sub, path+"."+k, visit); err != nil {
				return err
			}
		case []any:
			for i, s := range sub {
				if err := walkSchema(s, fmt.Sprintf("%s.%s[%d]", path, k, i), visit); err != nil {
					return err
				}
			}
		}
	}
	for _, k := range schemaListKeyword {
		list, _ := node[k].([]any)
		for i, s := range list {
			if err := walkSchema(s, fmt.Sprintf("%s.%s[%d]", path, k, i), visit); err != nil {
				return err
			}
		}
	}
	for _, k := range schemaMapKeywords {
		m, _ := node[k].(map[string]any)
		for _, name := range slices.Sorted(maps.Keys(m)) {
			if err := walkSchema(m[name], path+"."+k+"."+name, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// textOf reads an object-valued annotation as Text and reports whether it has an accepted shape.
func textOf(obj map[string]any) (Text, bool) {
	b, err := json.Marshal(obj)
	if err != nil {
		return Text{}, false
	}
	t := Text{raw: b}
	return t, t.Valid()
}

// CheckSchemaText checks that every object-valued title and description in schema is valid localized text; field is
// where the schema sits in the manifest, such as loopNow.configSchema. Other non-string values are left to the
// metaschema, which rejects them when the schema is compiled.
func CheckSchemaText(field string, schema map[string]any) error {
	return walkSchema(schema, "", func(node map[string]any, path string) error {
		for _, k := range localizedKeywords {
			if obj, ok := node[k].(map[string]any); ok {
				if _, ok := textOf(obj); !ok {
					return invalid(field + path + "." + k + " " + errText.Error())
				}
			}
		}
		return nil
	})
}

// toSchemaValue converts a Go value into the form the validation library expects (numbers as json.Number).
func toSchemaValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

// Validate validates value against schema; it returns an *Error when value breaks the rules.
func Validate(schema map[string]any, required bool, value map[string]any) error {
	sch, err := CompileSchema(schema, required)
	if err != nil {
		return invalid("The JSON Schema declared by the plugin cannot be used: " + err.Error())
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
		return invalid("Does not match the plugin's config schema: " + msg)
	}
	return nil
}

// properties returns the sub-schemas of the top-level properties.
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

// SecretFields returns the top-level x-secret fields, in manifest order.
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

// ProjectFields returns the top-level fields that can be overridden per project (x-scope: project), in manifest
// order.
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

// Schema is one JSON Schema from the manifest (configSchema, accounts[].fields, actions[].fields). It marshals to
// exactly what the manifest wrote: the order of properties is the order of form fields, and it stays unchanged when
// stored in the database and when returned by the API. Map is the parsed value, used for validation and for looking
// up fields. The zero value is an empty object.
type Schema struct {
	raw json.RawMessage
	m   map[string]any
}

// NewSchema builds a Schema from a parsed value (there is no original text, so properties are sorted by key; used
// by tests and for defaults).
func NewSchema(m map[string]any) Schema {
	raw, _ := json.Marshal(m)
	return Schema{raw: raw, m: m}
}

// MarshalJSON outputs exactly what the manifest wrote.
func (s Schema) MarshalJSON() ([]byte, error) {
	if len(s.raw) == 0 {
		return []byte("{}"), nil
	}
	return s.raw, nil
}

// UnmarshalJSON keeps the original text (with whitespace removed) and also parses it into Map; null counts as not
// given.
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

// Map returns the parsed value; an empty object when the schema was not given.
func (s Schema) Map() map[string]any {
	if s.m == nil {
		return map[string]any{}
	}
	return s.m
}

// IsZero reports whether this schema was absent from the manifest.
func (s Schema) IsZero() bool { return len(s.raw) == 0 && s.m == nil }

// Properties returns the keys of the top-level properties, in manifest order.
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
