package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
	"loop-now/internal/server/modelconfig"
)

func TestModelSettingsHTTPAndConsoleOnly(t *testing.T) {
	e := newEnv(t)
	options := catalogtest.Options(t)
	builtin, err := modelcatalog.Load(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	registry := modelcatalog.NewRegistry(builtin)
	service := &modelconfig.Service{Store: e.st, Models: registry, Options: options}
	if _, err = service.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.api.ModelSettings = service
	e.api.Models = registry
	e.api.Config.Models = registry

	initial := decode[modelconfig.Settings](t, e.expect(http.StatusOK, http.MethodGet, "/model-settings", nil))
	if initial.Revision != 1 || initial.ModelsJSON != modelconfig.DefaultModelsJSON {
		t.Fatal("initial settings", initial)
	}
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": modelconfig.DefaultModelsJSON}, "X-Contract-Invalid-Request", "1")
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": modelconfig.DefaultModelsJSON, "expectedRevision": 0}, "X-Contract-Invalid-Request", "1")
	custom := `{"providers":{"example":{"baseUrl":"https://models.example.test/v1","api":"openai-completions","apiKey":"$EXAMPLE_KEY","models":[{"id":"vision","reasoning":true,"input":["text","image"]}]}}}`
	saved := decode[modelconfig.Settings](t, e.expect(http.StatusOK, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": initial.Revision}))
	if saved.Revision != 2 {
		t.Fatal("saved revision", saved)
	}
	e.expect(http.StatusPreconditionFailed, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": initial.Revision})
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": `{"providers":`, "expectedRevision": saved.Revision})
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": `{"providers":{"example":{"apiKey":"plaintext","models":[]}}}`, "expectedRevision": saved.Revision})
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": `{"providers":{"example":{"headers":{"X-Key":"!command"},"models":[]}}}`, "expectedRevision": saved.Revision})
	rawError := e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": `{"providers":{"broken":{"models":[]}}}`, "expectedRevision": saved.Revision})
	if bytes.Contains(rawError, []byte("loopnow-catalog-")) {
		t.Fatal("raw Pi temporary path leaked", string(rawError))
	}
	providers := e.expect(http.StatusOK, http.MethodGet, "/model-providers", nil)
	if !bytes.Contains(providers, []byte(`"id":"example"`)) || !bytes.Contains(providers, []byte(`"source":"config"`)) {
		t.Fatal("merged provider not exposed", string(providers))
	}

	_, token := e.identity("model-settings-reader")
	external := e.external(token)
	external.expect(http.StatusForbidden, http.MethodGet, "/model-settings", nil)
	external.expect(http.StatusForbidden, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": saved.Revision})
}
