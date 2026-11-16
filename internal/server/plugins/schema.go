package plugins

import (
	"errors"
	"maps"
	"slices"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// validateAgainst validates value against the schema declared by the plugin; when it breaks the rules it returns a
// 422 on field.
func validateAgainst(schema pluginmanifest.Schema, required bool, value map[string]any, field string) error {
	return asInvalid(pluginmanifest.Validate(schema.Map(), required, value), field)
}

// asInvalid turns an error from the manifest package into a 422 on field.
func asInvalid(err error, field string) error {
	var e *pluginmanifest.Error
	if errors.As(err, &e) {
		return apperr.Invalid(field, e.Message)
	}
	return err
}

var (
	secretFields  = pluginmanifest.SecretFields
	projectFields = pluginmanifest.ProjectFields
)

// mergeSecrets merges the secrets to write into the current values: null clears a secret, anything else overwrites
// it; only fields in allowed are accepted.
func mergeSecrets(current map[string]string, patch map[string]*string, allowed []string, field string) (map[string]string, error) {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}
	for k, v := range patch {
		if !slices.Contains(allowed, k) {
			return nil, apperr.Invalid(field, "secrets 中的 "+k+" 不是这里可以设置的 secret 字段")
		}
		if v == nil {
			delete(out, k)
		} else {
			out[k] = *v
		}
	}
	return out, nil
}

// checkValueFields requires values to contain no secret fields and only fields in allowed (no restriction when
// allowed is nil).
func checkValueFields(values map[string]any, secrets, allowed []string, field string) error {
	for k := range values {
		if slices.Contains(secrets, k) {
			return apperr.Invalid(field, k+" 是 secret 字段，请放在 secrets 里")
		}
		if allowed != nil && !slices.Contains(allowed, k) {
			return apperr.Invalid(field, k+" 不能在项目里覆盖")
		}
	}
	return nil
}

// combine merges values and secrets into one object (used for validation and when handing them to the plugin).
func combine(values map[string]any, secrets map[string]string) map[string]any {
	out := maps.Clone(values)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range secrets {
		out[k] = v
	}
	return out
}

// secretsSet returns the names of the secret fields that are set, sorted.
func secretsSet(secrets map[string]string) []string {
	out := slices.Collect(maps.Keys(secrets))
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return out
}
