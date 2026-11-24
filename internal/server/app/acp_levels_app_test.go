package app_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/testutil/fakeacpbin"
)

// fakeAgy copies the fake ACP agent as agy (it plays Antigravity under that name).
func fakeAgy(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(fakeacpbin.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "agy")
	if err := os.WriteFile(p, b, 0o755); err != nil {
		t.Fatal(err)
	}
	// The system checks a new executable the first time it runs (slow on macOS); pay that here.
	if out, err := exec.Command(p, "--version").CombinedOutput(); err != nil {
		t.Fatalf("pre-run agy: %v %s", err, out)
	}
	return p
}

// Antigravity's models have thinking levels: agy lists fake-high "Fake (High)" and fake-low "Fake (Low)", the catalog
// has fake with low and high. An Agent saved before that names the model at one level by agy's id with no level; once
// agy's models are read it is the model at that level, as the console shows it, and it runs by the same id as before.
func TestAntigravityLevelsAndRecordedIDs(t *testing.T) {
	w := newWorld(t)
	realHome := t.TempDir()
	agy := fakeAgy(t)
	w.options = func(o *app.Options) {
		o.AntigravityBin = agy
		o.RealHome = func() (string, error) { return realHome, nil }
	}
	s := w.start(func(c *config.Config) { c.CodexModelsRefresh = time.Hour })
	defer s.stop()
	path, agent := s.createAgent("agy Agent")
	// What an earlier version saved for an Agent on Antigravity.
	if _, err := s.a.Store.Pool.Exec(context.Background(), `UPDATE agents SET model_provider = 'antigravity-cli', model_id = 'fake-high', thinking_level = NULL WHERE id = $1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	s.must(200, "GET", "/runtimes?refresh=true", nil) // read the local CLIs' models now

	models := decode[apitypes.ModelList](t, s.must(200, "GET", "/model-providers/antigravity-cli/models", nil))
	if len(models.Models) != 1 || models.Models[0].ID != "fake" || models.Models[0].Name != "Fake" || !slices.Equal(models.Models[0].ThinkingLevels, []string{"low", "high"}) || models.Models[0].DefaultThinkingLevel != "low" {
		t.Fatalf("Antigravity's models: %+v", models.Models)
	}
	got := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	if got.Model.Provider != "antigravity-cli" || got.Model.ModelID != "fake" || got.ThinkingLevel == nil || *got.ThinkingLevel != "high" || got.ConfigVersion != agent.ConfigVersion {
		t.Fatalf("the saved Agent is the model at its level: %+v %v", got.Model, got.ThinkingLevel)
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "fake:config"})
	s.waitIdle(path)
	r := s.replies(path)
	var cfg struct {
		Model string `json:"model"`
	}
	if len(r) == 0 || r[len(r)-1].Run.Status != "completed" || json.Unmarshal([]byte(r[len(r)-1].Text), &cfg) != nil || cfg.Model != "fake-high" {
		t.Fatalf("it runs by agy's id for the level: %+v", r)
	}
	// Another level from the picker runs by that level's id.
	s.must(200, "PATCH", path, map[string]any{"thinkingLevel": "low"})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "fake:config"})
	s.waitIdle(path)
	r = s.replies(path)
	if json.Unmarshal([]byte(r[len(r)-1].Text), &cfg) != nil || cfg.Model != "fake-low" {
		t.Fatalf("another level: %+v", r[len(r)-1])
	}
}
