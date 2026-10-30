package local

import (
	"os"
	"path/filepath"
	"testing"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
)

func TestModelSecretEnvUsesLatestCatalog(t *testing.T) {
	h := newHarness(t, nil, "")
	registry := modelcatalog.NewRegistry(modelcatalog.New("1.4.0", nil, nil))
	h.e.o.Models = registry
	if h.e.modelSecretEnv("EXAMPLE_KEY") {
		t.Fatal("unrelated environment variable must not be blocked")
	}
	registry.Replace(modelcatalog.New("1.4.0", nil, []modelcatalog.Provider{{ID: "example", APIKeyEnvVars: []string{"EXAMPLE_KEY"}}}))
	if !h.e.modelSecretEnv("EXAMPLE_KEY") {
		t.Fatal("new provider environment variable must be blocked without restarting the server")
	}
}

func TestModelsJSONSyncAndIdentityRestart(t *testing.T) {
	h := newHarness(t, nil, "")
	first := []byte(`{"providers":{"example":{"baseUrl":"https://one.example.test","api":"openai-completions","models":[{"id":"m"}]}}}`)
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Provider, s.ModelID = "example", "m"
		s.ModelsJSON = first
		s.ModelConfigIdentity = modelcatalog.ConfigIdentity(first)
	})
	if result, _ := h.exec(id, "one"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	pid := h.pid(id)
	modelsPath := filepath.Join(h.e.dirs(id).pi, "models.json")
	info, err := os.Stat(modelsPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("models.json mode", info, err)
	}
	data, err := os.ReadFile(modelsPath)
	if err != nil || string(data) != string(first) {
		t.Fatal("models.json content", string(data), err)
	}

	second := []byte(`{"providers":{"example":{"baseUrl":"https://two.example.test","api":"openai-completions","models":[{"id":"m"}]}}}`)
	h.src.mu.Lock()
	h.src.snaps[id].ModelsJSON = second
	h.src.snaps[id].ModelConfigIdentity = modelcatalog.ConfigIdentity(second)
	h.src.mu.Unlock()
	if result, _ := h.exec(id, "two"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	if h.pid(id) == 0 || h.pid(id) == pid {
		t.Fatal("changed native model config must restart Pi")
	}
	data, err = os.ReadFile(modelsPath)
	if err != nil || string(data) != string(second) {
		t.Fatal("updated models.json content", string(data), err)
	}

	h.src.mu.Lock()
	h.src.snaps[id].ModelsJSON = nil
	h.src.snaps[id].ModelConfigIdentity = ""
	h.src.mu.Unlock()
	if result, _ := h.exec(id, "three"); result.Outcome != executor.OutcomeCompleted {
		t.Fatal(result)
	}
	if _, err = os.Stat(modelsPath); !os.IsNotExist(err) {
		t.Fatal("empty native config must remove old models.json", err)
	}
}
