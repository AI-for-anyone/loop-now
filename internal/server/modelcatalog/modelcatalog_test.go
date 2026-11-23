package modelcatalog_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/modelcatalog/catalogtest"
)

func TestLoadFromLocalPi(t *testing.T) {
	// Reading works offline too: the child process gets only PATH and a temporary HOME, and the script reads only local
	// files.
	c, err := modelcatalog.Load(context.Background(), catalogtest.Options(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Providers) < 10 {
		t.Fatalf("too few providers: %d", len(c.Providers))
	}
	anthropic, ok := c.Provider("anthropic")
	if !ok || len(anthropic.Models) == 0 {
		t.Fatal("the catalog should have anthropic and its models")
	}
	if !slices.Contains(anthropic.APIKeyEnvVars, "ANTHROPIC_API_KEY") {
		t.Fatalf("anthropic should come with ANTHROPIC_API_KEY: %v", anthropic.APIKeyEnvVars)
	}
	if !c.HasModel("anthropic", anthropic.Models[0].ID) || c.HasModel("anthropic", "no-such-model") {
		t.Fatal("unexpected HasModel result")
	}
	if c.GeneratedAt == nil {
		t.Fatal("should carry the catalog generation time")
	}
}

func TestLoadRejectsVersionMismatch(t *testing.T) {
	options := catalogtest.Options(t)
	options.PiVersion = "9.9.9"
	_, err := modelcatalog.Load(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "9.9.9") {
		t.Fatalf("a version mismatch should fail: %v", err)
	}
}

func TestLoadDoesNotTouchAgentDir(t *testing.T) {
	home, _ := os.UserHomeDir()
	before, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if _, err := modelcatalog.Load(context.Background(), catalogtest.Options(t)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(home, ".pi", "agent"))
	if before != nil && after != nil && !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("reading the catalog should not change the host Pi config directory")
	}
}
