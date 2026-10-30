package modelconfig_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
	"loop-now/internal/server/modelconfig"
	"loop-now/internal/server/store"
)

type memoryStore struct{ row store.ModelSettings }

func (s *memoryStore) GetModelSettings(context.Context) (store.ModelSettings, error) {
	return s.row, nil
}
func (s *memoryStore) CompareAndSwapModelSettings(_ context.Context, text string, expected int64) (store.ModelSettings, error) {
	if s.row.Revision != expected {
		return store.ModelSettings{}, apperr.PreconditionFailed("模型配置已被修改")
	}
	s.row.ModelsJSON, s.row.Revision = text, s.row.Revision+1
	return s.row, nil
}

func TestPutPublishesOnlyValidLatestRevision(t *testing.T) {
	ctx := context.Background()
	st := &memoryStore{row: store.ModelSettings{ModelsJSON: modelconfig.DefaultModelsJSON, Revision: 1}}
	options := catalogtest.Options(t)
	builtin, err := modelcatalog.Load(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	registry := modelcatalog.NewRegistry(builtin)
	service := &modelconfig.Service{Store: st, Models: registry, Options: options}
	initial, err := service.Reload(ctx)
	if err != nil || initial.Revision != 1 {
		t.Fatal("initial reload", initial, err)
	}
	custom := `{"providers":{"example":{"baseUrl":"https://models.example.test/v1","api":"openai-completions","apiKey":"$EXAMPLE_KEY","models":[{"id":"vision","reasoning":true,"input":["text","image"]}]}}}`
	saved, err := service.Put(ctx, custom, initial.Revision)
	if err != nil || saved.Revision != 2 || !registry.HasModel("example", "vision") {
		t.Fatal("valid save", saved, err)
	}
	if _, err = service.Put(ctx, custom, initial.Revision); err == nil {
		t.Fatal("stale save should conflict", err)
	} else if apiErr := new(apperr.Error); !errors.As(err, &apiErr) || apiErr.Code != apperr.CodePreconditionFailed {
		t.Fatal("stale save returned wrong error", err)
	}
	if _, err = service.Put(ctx, `{"providers":`, saved.Revision); err == nil {
		t.Fatal("invalid JSON should fail")
	}
	if _, err = service.Put(ctx, `{"providers":{"broken":{"models":[]}}}`, saved.Revision); err == nil {
		t.Fatal("invalid native provider should fail")
	}
	current, err := service.Get(ctx)
	if err != nil || current != saved || !registry.HasModel("example", "vision") {
		t.Fatal("failed save changed settings or catalog", current, err)
	}
}

func TestPutRejectsPlaintextAndCommandSecrets(t *testing.T) {
	ctx := context.Background()
	st := &memoryStore{row: store.ModelSettings{ModelsJSON: modelconfig.DefaultModelsJSON, Revision: 1}}
	options := catalogtest.Options(t)
	builtin, err := modelcatalog.Load(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	registry := modelcatalog.NewRegistry(builtin)
	service := &modelconfig.Service{Store: st, Models: registry, Options: options}
	cases := map[string]string{
		"plaintext api key": `{"providers":{"example":{"apiKey":"plaintext","models":[]}}}`,
		"command api key":   `{"providers":{"example":{"apiKey":"!printenv","models":[]}}}`,
		"provider header":   `{"providers":{"example":{"headers":{"Authorization":"Bearer secret"},"models":[]}}}`,
		"model header":      `{"providers":{"example":{"models":[{"headers":{"X-Key":"secret"}}]}}}`,
		"override header":   `{"providers":{"example":{"modelOverrides":{"m":{"headers":{"X-Key":"!command"}}},"models":[]}}}`,
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.Put(ctx, text, 1)
			apiErr := new(apperr.Error)
			if !errors.As(err, &apiErr) || apiErr.Code != apperr.CodeValidationFailed || st.row.Revision != 1 {
				t.Fatal("unsafe secret reference accepted", err)
			}
		})
	}
	if _, err = service.Put(ctx, modelconfig.DefaultModelsJSON, 0); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatal("zero expectedRevision must be a validation error", err)
	}
	_, err = service.Put(ctx, `{"providers":{"broken":{"models":[]}}}`, 1)
	apiErr := new(apperr.Error)
	if !errors.As(err, &apiErr) || len(apiErr.Fields) != 1 || strings.Contains(apiErr.Fields[0].Message, "loopnow-catalog-") || !strings.Contains(apiErr.Fields[0].Message, "Pi 校验") {
		t.Fatal("raw Pi error leaked to field message", err)
	}
}

func TestReloadFallsBackToBuiltinAndReportsError(t *testing.T) {
	ctx := context.Background()
	st := &memoryStore{row: store.ModelSettings{ModelsJSON: `{"providers":`, Revision: 7}}
	options := catalogtest.Options(t)
	builtin, err := modelcatalog.Load(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	registry := modelcatalog.NewRegistry(builtin)
	service := &modelconfig.Service{Store: st, Models: registry, Options: options}
	settings, err := service.Reload(ctx)
	if err != nil || settings.LoadError == "" || settings.ModelsJSON != st.row.ModelsJSON {
		t.Fatal("invalid saved config must not block startup", settings, err)
	}
	current, err := service.Get(ctx)
	if err != nil || current.LoadError != settings.LoadError {
		t.Fatal("GET must expose the fallback warning", current, err)
	}
	if _, err = service.Put(ctx, modelconfig.DefaultModelsJSON, 7); err != nil {
		t.Fatal(err)
	}
	current, err = service.Get(ctx)
	if err != nil || current.LoadError != "" || current.Revision != 8 {
		t.Fatal("successful repair must clear the fallback warning", current, err)
	}
}
