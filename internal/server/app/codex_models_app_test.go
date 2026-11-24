package app_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/app"
	"loop-now/internal/server/config"
	"loop-now/internal/testutil/fakecodexbin"
)

// codexModelsWorld starts the app on fake Codex whose home is codexHome, with the given ages for re-reading its models.
func codexModelsWorld(t *testing.T, maxAge, staleAge time.Duration) (*server, string) {
	w := newWorld(t)
	realHome := t.TempDir()
	codexHome := filepath.Join(realHome, ".codex")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	setCodexModels(t, codexHome, "gpt-old")
	w.options = func(o *app.Options) {
		o.CodexBin = fakecodexbin.Path(t)
		o.RealHome = func() (string, error) { return realHome, nil }
		o.CodexModelsMaxAge, o.CodexModelsStaleAge = maxAge, staleAge
	}
	s := w.start(func(c *config.Config) { c.CodexModelsRefresh = time.Hour })
	return s, codexHome
}

func setCodexModels(t *testing.T, codexHome string, ids ...string) {
	t.Helper()
	var models []map[string]any
	for _, id := range ids {
		models = append(models, map[string]any{"id": id, "model": id, "displayName": id, "hidden": false, "isDefault": len(models) == 0,
			"defaultReasoningEffort": "low", "inputModalities": []string{"text"}, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}}})
	}
	b, _ := json.Marshal(models)
	if err := os.WriteFile(filepath.Join(codexHome, ".fakecodex-models.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *server) codexModels() []string {
	s.t.Helper()
	code, body := s.call("GET", "/model-providers/codex-cli/models", nil)
	if code != 200 {
		return nil
	}
	var list apitypes.ModelList
	if json.Unmarshal(body, &list) != nil {
		return nil
	}
	var ids []string
	for _, m := range list.Models {
		ids = append(ids, m.ID)
	}
	return ids
}

func (s *server) waitCodexModel(id string) {
	s.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if slices.Contains(s.codexModels(), id) {
			return
		}
	}
	s.t.Fatalf("Codex's model %s did not show; the list is %v", id, s.codexModels())
}

// Reading the model list reads Codex's list again in the background once it is older than the limit: the read that
// notices returns the list it has, a later one shows the new list.
func TestCodexModelsReadAgainWhenOld(t *testing.T) {
	s, codexHome := codexModelsWorld(t, 100*time.Millisecond, 0)
	defer s.stop()
	s.must(200, "GET", "/runtimes?refresh=true", nil)
	if got := s.codexModels(); !slices.Equal(got, []string{"gpt-old"}) {
		t.Fatalf("the list read at the refresh: %v", got)
	}
	setCodexModels(t, codexHome, "gpt-new")
	time.Sleep(150 * time.Millisecond)
	s.waitCodexModel("gpt-new")
}

// A list read while Codex's login was rejected may be out of date: it is read again at the first read of the lists once
// the login works, without waiting for the usual limit; after that, the usual limit applies again.
func TestCodexModelsReadAgainAfterARejectedLogin(t *testing.T) {
	s, codexHome := codexModelsWorld(t, time.Hour, 100*time.Millisecond)
	defer s.stop()
	revoked := filepath.Join(codexHome, ".fakecodex-revoked")
	if err := os.WriteFile(revoked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setCodexModels(t, codexHome, "gpt-cached")
	s.must(200, "GET", "/runtimes?refresh=true", nil)
	if got := s.codexModels(); !slices.Equal(got, []string{"gpt-cached"}) {
		t.Fatalf("the list Codex answered from its cache: %v", got)
	}
	if err := os.Remove(revoked); err != nil { // the user logs in again
		t.Fatal(err)
	}
	setCodexModels(t, codexHome, "gpt-new")
	time.Sleep(150 * time.Millisecond)
	s.waitCodexModel("gpt-new")
	setCodexModels(t, codexHome, "gpt-newer")
	for range 5 {
		time.Sleep(100 * time.Millisecond)
		if got := s.codexModels(); !slices.Equal(got, []string{"gpt-new"}) {
			t.Fatalf("a fresh list waits for the usual limit: %v", got)
		}
	}
}
