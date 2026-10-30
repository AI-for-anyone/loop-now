package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelconfig"
	"loop-now/internal/server/preflight"
)

func nativeCatalogOptions(t *testing.T) modelcatalog.Options {
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

func TestModelSettingsHTTPAndConsoleOnly(t *testing.T) {
	e := newEnv(t)
	options := nativeCatalogOptions(t)
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
	custom := `{"providers":{"example":{"baseUrl":"https://models.example.test/v1","api":"openai-completions","apiKey":"$EXAMPLE_KEY","models":[{"id":"vision","reasoning":true,"input":["text","image"]}]}}}`
	saved := decode[modelconfig.Settings](t, e.expect(http.StatusOK, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": initial.Revision}))
	if saved.Revision != 2 {
		t.Fatal("saved revision", saved)
	}
	e.expect(http.StatusPreconditionFailed, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": initial.Revision})
	e.expect(http.StatusUnprocessableEntity, http.MethodPut, "/model-settings", map[string]any{"modelsJson": `{"providers":`, "expectedRevision": saved.Revision})
	providers := e.expect(http.StatusOK, http.MethodGet, "/model-providers", nil)
	if !bytes.Contains(providers, []byte(`"id":"example"`)) || !bytes.Contains(providers, []byte(`"source":"config"`)) {
		t.Fatal("merged provider not exposed", string(providers))
	}

	_, token := e.identity("model-settings-reader")
	external := e.external(token)
	external.expect(http.StatusForbidden, http.MethodGet, "/model-settings", nil)
	external.expect(http.StatusForbidden, http.MethodPut, "/model-settings", map[string]any{"modelsJson": custom, "expectedRevision": saved.Revision})
}
