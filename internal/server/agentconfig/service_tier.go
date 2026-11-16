package agentconfig

import "loop-now/internal/server/apperr"

// ResolveServiceTier returns an explicit standard tier for new Codex rounds.
// Non-Codex and legacy task snapshots have no injected tier.
func ResolveServiceTier(provider string, selected *string) (*string, error) {
	if selected != nil {
		if provider != "openai-codex" {
			return nil, apperr.Invalid("serviceTier", "Only openai-codex supports a service tier.")
		}
		switch *selected {
		case "default", "priority", "ultrafast":
		default:
			return nil, apperr.Invalid("serviceTier", "Must be default, priority, ultrafast or null.")
		}
	}
	if provider != "openai-codex" {
		return nil, nil
	}
	value := "default"
	if selected != nil {
		value = *selected
	}
	return &value, nil
}
