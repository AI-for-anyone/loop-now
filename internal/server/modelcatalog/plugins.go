package modelcatalog

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"loop-now/internal/procgroup"
)

//go:embed providers.mjs
var providersScript []byte

// CapturedProvider is one model provider registered by a plugin's Pi extension (plugins/SPEC.md §13).
type CapturedProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Extension is the extension file that registered it, relative to the plugin directory.
	Extension        string  `json:"extension"`
	OAuth            bool    `json:"oauth"`
	APIKey           bool    `json:"apiKey"`
	OverridesBuiltin bool    `json:"overridesBuiltin"`
	Error            *string `json:"error"`
	Models           []Model `json:"models"`
}

// CaptureError is the reason one extension file failed to load (the extension's original text, truncated).
type CaptureError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// Capture is the result of one read.
type Capture struct {
	Providers []CapturedProvider `json:"providers"`
	Errors    []CaptureError     `json:"errors"`
}

// CatalogEligible reports whether this provider goes into the model catalog: a new provider that registered an OAuth
// login without errors at registration. Providers that override a built-in provider (same id as a built-in one or one
// in models.json) are only recorded and do not go into the catalog.
func (p CapturedProvider) CatalogEligible() bool {
	return p.OAuth && !p.OverridesBuiltin && p.Error == nil && len(p.Models) > 0
}

// CaptureProviders loads the extension files under dir with the pinned Pi's own extension loader and reads the model
// providers they register. It runs extension code, so it may only be called after the user confirms the install. Empty
// environment, temporary HOME and PI_CODING_AGENT_DIR, PI_OFFLINE=1; side effects of loading the extensions land only
// in the temporary directory, which is deleted at the end. extensions are paths relative to dir.
func CaptureProviders(ctx context.Context, o Options, dir string, extensions []string) (Capture, error) {
	out := Capture{Providers: []CapturedProvider{}, Errors: []CaptureError{}}
	if len(extensions) == 0 {
		return out, nil
	}
	if o.PiPackageDir == "" {
		return out, fmt.Errorf("reading plugin providers requires the Pi package directory")
	}
	tmp, err := os.MkdirTemp("", "loopnow-providers-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	home := filepath.Join(tmp, "home")
	for _, d := range []string{home, filepath.Join(home, "agent"), filepath.Join(tmp, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return out, err
		}
	}
	scriptPath := filepath.Join(tmp, "providers.mjs")
	if err := os.WriteFile(scriptPath, providersScript, 0o600); err != nil {
		return out, err
	}
	args := []string{scriptPath, o.PiPackageDir, o.PiAIDir}
	abs := map[string]string{}
	for _, rel := range extensions {
		p := filepath.Join(dir, filepath.Clean(rel))
		abs[p] = rel
		args = append(args, p)
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, o.NodeBin, args...)
	cmd.Env = []string{"PATH=" + o.Path, "HOME=" + home, "USERPROFILE=" + home, "PI_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "TMPDIR=" + filepath.Join(tmp, "tmp"), "PI_OFFLINE=1", "NODE_ENV=production"}
	cmd.Dir = home
	procgroup.Configure(cmd)
	cmd.Cancel = procgroup.Cancel(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return out, fmt.Errorf("timed out reading plugin providers")
		}
		return out, fmt.Errorf("read plugin providers: %w: %s", err, clip(bytes.TrimSpace(stderr.Bytes()), 2000))
	}
	var raw struct {
		PiVersion string             `json:"piVersion"`
		Providers []CapturedProvider `json:"providers"`
		Errors    []CaptureError     `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return out, fmt.Errorf("parse plugin providers: %w", err)
	}
	if raw.PiVersion != o.PiVersion {
		return out, fmt.Errorf("Pi version %s does not match the expected %s", raw.PiVersion, o.PiVersion)
	}
	rel := func(p string) string {
		if r, ok := abs[p]; ok {
			return r
		}
		if r, err := filepath.Rel(dir, p); err == nil {
			return filepath.ToSlash(r)
		}
		return p
	}
	for _, p := range raw.Providers {
		p.Extension = rel(p.Extension)
		if p.Models == nil {
			p.Models = []Model{}
		}
		for i := range p.Models {
			m := &p.Models[i]
			if len(m.ThinkingLevels) == 0 || !slices.Contains(m.ThinkingLevels, m.DefaultThinkingLevel) {
				msg := fmt.Sprintf("Model %s has invalid thinking level metadata.", m.ID)
				p.Error = &msg
			}
			if m.Input == nil {
				m.Input = []string{}
			}
		}
		out.Providers = append(out.Providers, p)
	}
	for _, e := range raw.Errors {
		out.Errors = append(out.Errors, CaptureError{Path: rel(e.Path), Error: e.Error})
	}
	return out, nil
}

func clip(b []byte, max int) string {
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}

// PluginProvider is one plugin provider to merge into the model catalog.
type PluginProvider struct {
	PluginID string
	Provider CapturedProvider
}

// withPlugins returns base plus the plugin providers: a plugin provider with the same id as one in base is skipped
// (built-in and models.json take precedence), and between plugins the first one given wins for the same id. When there
// is no provider to merge, base is returned as is.
func withPlugins(base *Catalog, plugins []PluginProvider) *Catalog {
	if base == nil {
		return nil
	}
	var extra []Provider
	seen := map[string]bool{}
	for _, pp := range plugins {
		p := pp.Provider
		if !p.CatalogEligible() || seen[p.ID] {
			continue
		}
		if _, exists := base.byID[p.ID]; exists {
			continue
		}
		seen[p.ID] = true
		id := pp.PluginID
		extra = append(extra, Provider{ID: p.ID, Name: p.Name, Source: "plugin", PluginID: &id, AuthMethods: []string{"oauth"}, APIKeyEnvVars: []string{}, Models: slices.Clone(p.Models)})
	}
	if len(extra) == 0 {
		return base
	}
	c := New(base.PiVersion, base.GeneratedAt, append(slices.Clone(base.Providers), extra...))
	c.Revision, c.providerConfig = base.Revision, base.providerConfig
	return c
}
