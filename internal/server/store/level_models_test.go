package store_test

import (
	"context"
	"testing"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

// Agents that name a model at one thinking level by the id the CLI listed for it, with no level or off (recorded before
// Antigravity's levels were merged), move to the model and that level, without a new config version. An Agent with a
// level of its own, another provider, an id that is not such a row, or one already moved stays as it is.
func TestAdoptLevelModels(t *testing.T) {
	s := sqlitetest.NewStore(t)
	ctx := context.Background()
	defaults, err := s.EnsureDefaults(ctx, "o", "p")
	if err != nil {
		t.Fatal(err)
	}
	type agent struct {
		name, provider, model string
		level                 *string
	}
	off, high := "off", "high"
	agents := []agent{
		{"none", "antigravity-cli", "gemini-4-neon-xhigh", nil},
		{"off", "antigravity-cli", "gemini-4-neon-low", &off},
		{"own-level", "antigravity-cli", "gemini-4-neon-high", &high},
		{"no-levels", "antigravity-cli", "gemini-pro-orchestrator-neon", nil},
		{"other-provider", "grok-cli", "gemini-4-neon-xhigh", nil},
		{"moved", "antigravity-cli", "gemini-4-neon", &high},
	}
	ids := map[string]store.Agent{}
	for _, a := range agents {
		created, err := s.CreateAgent(ctx, store.ProjectScope(defaults.Project.ID), a.name, a.provider, a.model, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE agents SET thinking_level = $2 WHERE id = $1`, created.ID, a.level); err != nil {
			t.Fatal(err)
		}
		ids[a.name], _ = s.GetAgent(ctx, created.ID)
	}
	moved, err := s.AdoptLevelModels(ctx, "antigravity-cli", map[string]store.LevelModel{
		"gemini-4-neon-low":   {ModelID: "gemini-4-neon", Level: "low"},
		"gemini-4-neon-high":  {ModelID: "gemini-4-neon", Level: "high"},
		"gemini-4-neon-xhigh": {ModelID: "gemini-4-neon", Level: "xhigh"},
	})
	if err != nil || moved != 2 {
		t.Fatalf("moved %d %v", moved, err)
	}
	want := map[string]string{
		"none": "gemini-4-neon xhigh", "off": "gemini-4-neon low", "own-level": "gemini-4-neon-high high",
		"no-levels": "gemini-pro-orchestrator-neon -", "other-provider": "gemini-4-neon-xhigh -", "moved": "gemini-4-neon high",
	}
	for name, before := range ids {
		a, err := s.GetAgent(ctx, before.ID)
		if err != nil {
			t.Fatal(err)
		}
		level := "-"
		if a.ThinkingLevel != nil {
			level = *a.ThinkingLevel
		}
		if got := a.ModelID + " " + level; got != want[name] || a.ConfigVersion != before.ConfigVersion {
			t.Errorf("%s: %q version %d, want %q version %d", name, got, a.ConfigVersion, want[name], before.ConfigVersion)
		}
	}
	if again, err := s.AdoptLevelModels(ctx, "antigravity-cli", map[string]store.LevelModel{"gemini-4-neon-xhigh": {ModelID: "gemini-4-neon", Level: "xhigh"}}); err != nil || again != 0 {
		t.Fatalf("a second time moves nothing: %d %v", again, err)
	}
}
