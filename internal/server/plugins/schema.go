package plugins

import (
	"errors"
	"maps"
	"slices"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/apperr"
)

// validateAgainst 用插件声明的 schema 校验 value；不合规则时返回 field 上的 422。
func validateAgainst(schema pluginmanifest.Schema, required bool, value map[string]any, field string) error {
	return asInvalid(pluginmanifest.Validate(schema.Map(), required, value), field)
}

// asInvalid 把清单包的说明变成 field 上的 422。
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

// mergeSecrets 把要写入的 secret 合并到现有值：null 清除，其余覆盖；只接受 allowed 中的字段。
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

// checkValueFields 要求 values 里没有 secret 字段，且只含 allowed 中的字段（allowed 为 nil 时不限）。
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

// combine 把 values 与 secrets 合成一个对象（校验与交给插件时用）。
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

// secretsSet 返回已设置的 secret 字段名，排序。
func secretsSet(secrets map[string]string) []string {
	out := slices.Collect(maps.Keys(secrets))
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return out
}
