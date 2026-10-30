package modelconfig_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelconfig"
	"loop-now/internal/server/preflight"
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

func catalogOptions(t *testing.T) modelcatalog.Options {
	t.Helper()
	if root := os.Getenv("LOOP_NOW_TEST_PI_PACKAGE_DIR"); root != "" {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skipf("本机没有 Node：%v", err)
		}
		piAIDir := os.Getenv("LOOP_NOW_TEST_PI_AI_DIR")
		if piAIDir == "" {
			piAIDir = filepath.Join(root, "node_modules", "@earendil-works", "pi-ai")
		}
		return modelcatalog.Options{NodeBin: node, PiAIDir: piAIDir, PiPackageDir: root, PiVersion: "1.4.0", Path: os.Getenv("PATH")}
	}
	r, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(t.TempDir(), "root"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.4.0：%v", err)
	}
	return modelcatalog.Options{NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiPackageDir: r.PiPackageDir, PiVersion: "1.4.0", Path: r.ExecBasePath}
}

func TestPutPublishesOnlyValidLatestRevision(t *testing.T) {
	ctx := context.Background()
	st := &memoryStore{row: store.ModelSettings{ModelsJSON: modelconfig.DefaultModelsJSON, Revision: 1}}
	builtin, err := modelcatalog.Load(ctx, catalogOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	registry := modelcatalog.NewRegistry(builtin)
	service := &modelconfig.Service{Store: st, Models: registry, Options: catalogOptions(t)}
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
