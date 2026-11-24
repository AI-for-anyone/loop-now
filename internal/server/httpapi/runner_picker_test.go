package httpapi_test

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/modelcatalog"
)

// usable reads GET /model-providers with the query and returns usable per provider (absent key = null).
func (e *env) usable(query string) map[string]bool {
	e.t.Helper()
	list := decode[apitypes.ModelProviderList](e.t, e.expect(200, "GET", "/model-providers"+query, nil))
	out := map[string]bool{}
	for _, p := range list.Providers {
		if p.Usable != nil {
			out[p.ID] = *p.Usable
		}
	}
	return out
}

// GET /model-providers says, for an Agent, a project or a new platform Agent, which providers can run without
// entering an account first: those with an account or key the platform keeps, those that need none, and those of a
// runtime that uses its own local login.
func TestModelProvidersUsable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	catalog := modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X", Input: []string{"text"}}}},
		{ID: "openai", APIKeyEnvVars: []string{"OPENAI_API_KEY"}, Models: []modelcatalog.Model{{ID: "gpt-x", Name: "GPT X", Input: []string{"text"}}}},
		{ID: "local-free", Models: []modelcatalog.Model{{ID: "free", Name: "Free", Input: []string{"text"}}}},
		modelcatalog.ClaudeCodeProvider(),
	})
	e.api.Models, e.api.Config.Models = catalog, catalog

	if got := e.usable(""); len(got) != 0 {
		t.Fatalf("without a scope usable is null: %v", got)
	}
	want := func(query string, expect map[string]bool) {
		t.Helper()
		if got := e.usable(query); len(got) != len(expect) || got["anthropic"] != expect["anthropic"] || got["openai"] != expect["openai"] ||
			got["local-free"] != expect["local-free"] || got["claude-code"] != expect["claude-code"] {
			t.Fatalf("%s: got %v, want %v", query, got, expect)
		}
	}
	nothing := map[string]bool{"anthropic": false, "openai": false, "local-free": true, "claude-code": true}
	want("?scope=platform", nothing)

	managed := e.platformAgent("with-key")
	want("?agentId="+managed.ID.String(), nothing)
	e.expect(200, "PUT", "/agents/"+managed.ID.String()+"/model-credential", map[string]any{"provider": "anthropic", "key": "synthetic-key"})
	want("?agentId="+managed.ID.String(), map[string]bool{"anthropic": true, "openai": false, "local-free": true, "claude-code": true})

	legacy := e.platformAgent("older-key")
	if _, err := e.api.Config.PutModelCredential(ctx, legacy.ID, map[string]string{"OPENAI_API_KEY": "synthetic-openai"}); err != nil {
		t.Fatal(err)
	}
	want("?agentId="+legacy.ID.String(), map[string]bool{"anthropic": false, "openai": true, "local-free": true, "claude-code": true})

	project := "?projectId=" + e.dc.Project.ID.String()
	member := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": "member", "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}}))
	want(project, nothing)
	want("?agentId="+member.ID.String(), nothing)
	e.expect(201, "POST", "/projects/"+e.dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "Team key",
		"credential": map[string]string{"type": "api_key", "key": "synthetic-team"}})
	withAccount := map[string]bool{"anthropic": true, "openai": false, "local-free": true, "claude-code": true}
	want(project, withAccount)
	want("?agentId="+member.ID.String(), withAccount)

	e.expect(422, "GET", "/model-providers?scope=platform&projectId="+e.dc.Project.ID.String(), nil)
	e.expect(422, "GET", "/model-providers?scope=project", nil, "X-Contract-Invalid-Request", "1")
	e.expect(404, "GET", "/model-providers?projectId="+uuid.NewString(), nil)
}

// include=models lists every provider's models, so a picker can search across providers.
func TestModelProvidersIncludeModels(t *testing.T) {
	e := newEnv(t)
	list := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", "/model-providers?include=models", nil))
	for _, p := range list.Providers {
		if len(p.Models) != p.ModelCount {
			t.Fatalf("%s: %d models listed, %d counted", p.ID, len(p.Models), p.ModelCount)
		}
	}
	if list.Providers[0].ID != "anthropic" || list.Providers[0].Models[0].ID != "claude-x" || list.Providers[0].Models[0].Name != "Claude X" {
		t.Fatalf("wrong models: %+v", list.Providers[0].Models)
	}
	plain := decode[apitypes.ModelProviderList](t, e.expect(200, "GET", "/model-providers", nil))
	if plain.Providers[0].Models != nil {
		t.Fatal("models are listed only with include=models")
	}
	e.expect(422, "GET", "/model-providers?include=everything", nil, "X-Contract-Invalid-Request", "1")
}

// Each runtime definition says how a person sets it up on this computer's operating system; the bundled Pi needs
// nothing.
func TestRuntimeDefinitionsSetup(t *testing.T) {
	e := newEnv(t)
	type setup struct {
		Install *string `json:"install"`
		Login   *string `json:"login"`
		DocsURL *string `json:"docsUrl"`
	}
	list := decode[struct {
		Runtimes []struct {
			ID    string `json:"id"`
			Setup *setup `json:"setup"`
		} `json:"runtimes"`
	}](t, e.expect(200, "GET", "/runtime-definitions", nil))
	for _, rt := range list.Runtimes {
		switch rt.ID {
		case "pi":
			if rt.Setup != nil {
				t.Fatalf("the bundled Pi needs no setup: %+v", rt.Setup)
			}
		default:
			s := rt.Setup
			if s == nil || s.Install == nil || s.Login == nil || s.DocsURL == nil || !strings.Contains(*s.Login, "claude") {
				t.Fatalf("%s setup: %+v", rt.ID, s)
			}
			if runtime.GOOS == "windows" && !strings.Contains(*s.Install, "install.ps1") || runtime.GOOS != "windows" && !strings.Contains(*s.Install, "install.sh") {
				t.Fatalf("install command for %s: %s", runtime.GOOS, *s.Install)
			}
		}
	}
}
