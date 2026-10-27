package modelcredential

import (
	"errors"
	"maps"
)

// ErrLegacyUnresolved is a compatibility gate, not an approved migration policy.
// Original stored payloads and v1 mappings remain untouched. Unknown/proxy
// inputs are not silently projected into an API key or process environment.
var ErrLegacyUnresolved = errors.New("旧模型凭证的等价接入尚待兼容评审")

func LegacySingleKey(provider string, env map[string]string) (Credential, error) {
	if len(env) == 0 {
		return Credential{}, nil
	}
	if provider == "anthropic" && validLegacyAnthropicEnv(env) {
		if len(env) == 1 {
			for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN"} {
				if value, ok := env[name]; ok {
					return APIKey(value)
				}
			}
		}
		return validated(wire{Type: "api_key", Env: maps.Clone(env)})
	}
	if len(env) != 1 {
		return Credential{}, ErrLegacyUnresolved
	}
	allowed := map[string][]string{"anthropic": {"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN"}, "openai": {"OPENAI_API_KEY"}}
	for _, name := range allowed[provider] {
		if value, ok := env[name]; ok {
			return APIKey(value)
		}
	}
	return Credential{}, ErrLegacyUnresolved
}

func validLegacyAnthropicEnv(env map[string]string) bool {
	if len(env) == 0 || len(env) > 3 {
		return false
	}
	found := false
	for name, value := range env {
		switch name {
		case "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_API_KEY":
		default:
			return false
		}
		if value != "" {
			found = true
		}
	}
	return found
}
