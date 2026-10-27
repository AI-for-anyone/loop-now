package modelcatalog_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/preflight"
)

func TestInstalledPi14ThinkingCatalogUsesOfficialModelCapabilities(t *testing.T) {
	r, e := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node", WorkRoot: filepath.Join(t.TempDir(), "work"), HomeDirs: []string{"/nonexistent"}})
	if e != nil {
		t.Fatal(e)
	}
	c, e := modelcatalog.Load(context.Background(), modelcatalog.Options{NodeBin: r.NodeBin, PiAIDir: r.PiAIDir, PiVersion: "1.4.0", Path: r.ExecBasePath})
	if e != nil {
		t.Fatal(e)
	}
	p, ok := c.Provider("openai-codex")
	if !ok {
		t.Fatal("provider missing")
	}
	found, plain := false, false
	for _, m := range p.Models {
		if m.ID == "gpt-6.1-sol" {
			found = true
			if slices.Contains(m.ThinkingLevels, "off") || !slices.Contains(m.ThinkingLevels, "max") || !slices.Contains(m.ThinkingLevels, "xhigh") || m.DefaultThinkingLevel != "medium" {
				t.Fatal("real Codex capabilities differ", m.ThinkingLevels, m.DefaultThinkingLevel)
			}
		}
	}
	for _, p := range c.Providers {
		for _, m := range p.Models {
			if !slices.Contains(m.ThinkingLevels, m.DefaultThinkingLevel) {
				t.Fatal("invalid resolved default")
			}
			if !m.Reasoning {
				plain = true
				if len(m.ThinkingLevels) != 1 || m.ThinkingLevels[0] != "off" || m.DefaultThinkingLevel != "off" {
					t.Fatal("nonreasoning levels guessed")
				}
			}
		}
	}
	if !found || !plain {
		t.Fatal("required positive controls absent")
	}
	off := "off"
	if _, e = c.ResolveThinking("openai-codex", "gpt-6.1-sol", &off); e == nil {
		t.Fatal("unsupported explicit level accepted")
	}
}
