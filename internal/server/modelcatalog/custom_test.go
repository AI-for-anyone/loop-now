package modelcatalog_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
)

func TestLoadNativeCustomProviderAndBuiltinOverride(t *testing.T) {
	options := catalogtest.Options(t)
	builtin, err := modelcatalog.Load(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	builtinAnthropic, _ := builtin.Provider("anthropic")
	config := []byte(`{"providers":{"example":{"baseUrl":"https://models.example.test/v1","api":"openai-completions","apiKey":"$EXAMPLE_MODEL_KEY","models":[{"id":"reasoning-vision","reasoning":true,"input":["text","image"],"contextWindow":12345,"maxTokens":678}]},"anthropic":{"baseUrl":"https://proxy.example.test"}}}`)
	options.ModelsJSON, options.Revision = config, 7
	catalog, err := modelcatalog.Load(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	example, ok := catalog.Provider("example")
	if !ok || example.Source != "config" || !slices.Contains(example.AuthMethods, "api_key") || !slices.Contains(example.APIKeyEnvVars, "EXAMPLE_MODEL_KEY") {
		t.Fatal("custom provider auth metadata", example, ok)
	}
	if !catalog.HasModel("example", "reasoning-vision") || !catalog.SupportsImages("example", "reasoning-vision") {
		t.Fatal("custom model or image support missing")
	}
	level, err := catalog.ResolveThinking("example", "reasoning-vision", nil)
	if err != nil || level != "medium" {
		t.Fatal("native default thinking", level, err)
	}
	high := "high"
	if level, err = catalog.ResolveThinking("example", "reasoning-vision", &high); err != nil || level != "high" {
		t.Fatal("native explicit thinking", level, err)
	}
	anthropic, ok := catalog.Provider("anthropic")
	if !ok || anthropic.Source != "config" || len(anthropic.Models) != len(builtinAnthropic.Models) {
		t.Fatal("built-in provider override changed catalog shape", anthropic, ok)
	}
	fragment, identity := catalog.ModelsJSONFor("example")
	if identity == "" || identity != modelcatalog.ConfigIdentity(fragment) {
		t.Fatal("unstable model config identity")
	}
	var decoded struct {
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err = json.Unmarshal(fragment, &decoded); err != nil || len(decoded.Providers) != 1 || decoded.Providers["example"] == nil {
		t.Fatal("wrong provider fragment", string(fragment), err)
	}
	if data, id := catalog.ModelsJSONFor("openai"); len(data) != 0 || id != "" {
		t.Fatal("untouched built-in provider must not receive native config", string(data), id)
	}
	if catalog.Revision != 7 {
		t.Fatal("catalog revision missing", catalog.Revision)
	}
}
