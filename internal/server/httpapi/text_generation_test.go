package httpapi_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/server/textgen"
)

type tgCatalog map[string][]modelcatalog.Model

func (c tgCatalog) Provider(id string) (*modelcatalog.Provider, bool) {
	m, ok := c[id]
	return &modelcatalog.Provider{ID: id, Models: m}, ok
}

// tgRunner answers with reply and records the requests.
type tgRunner struct {
	mu    sync.Mutex
	reqs  []executor.TextRequest
	reply func() (string, error)
}

func (r *tgRunner) WriteText(_ context.Context, req executor.TextRequest) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req)
	return r.reply()
}

// withTextGeneration gives the server a writer service whose ready runtimes are ready (Claude Code and Codex have
// models).
func withTextGeneration(e *env, ready *map[string]bool) *tgRunner {
	r := &tgRunner{reply: func() (string, error) { return "```\nYou triage issues.\n```", nil }}
	e.api.TextGeneration = &textgen.Service{Store: e.st, Runner: r,
		Models: tgCatalog{
			agentruntime.ClaudeCodeProvider: {{ID: "opus", Name: "Claude Opus"}, {ID: "haiku", Name: "Claude Haiku", ThinkingLevels: []string{"low", "medium"}}},
			agentruntime.CodexProvider:      {{ID: "gpt-6-mini", Name: "GPT-6 mini", ThinkingLevels: []string{"low", "medium"}}},
		},
		Statuses: func(context.Context) []runtimes.Status {
			var out []runtimes.Status
			for id, ok := range *ready {
				out = append(out, runtimes.Status{ID: id, Available: ok})
			}
			return out
		}}
	return r
}

// The setting is read and saved as the contract says, with who would write now; a provider that is not a local
// runtime's is refused.
func TestTextGenerationSettingsAPI(t *testing.T) {
	e := newEnv(t)
	ready := map[string]bool{agentruntime.ClaudeCode: true, agentruntime.Codex: true}
	withTextGeneration(e, &ready)
	s := decode[textgen.Settings](t, e.expect(200, "GET", "/text-generation-settings", nil))
	if s.Selection != nil || s.Writer == nil || s.Writer.Runtime != agentruntime.ClaudeCode || s.Writer.ModelID != "haiku" || s.Writer.ModelName != "Claude Haiku" {
		t.Fatalf("Automatic: %+v %+v", s.Selection, s.Writer)
	}
	s = decode[textgen.Settings](t, e.expect(200, "PUT", "/text-generation-settings", map[string]any{
		"selection": map[string]any{"provider": agentruntime.CodexProvider, "modelId": "gpt-6-mini", "thinkingLevel": "medium"}}))
	if s.Selection == nil || s.Selection.ModelID != "gpt-6-mini" || s.Writer == nil || s.Writer.Runtime != agentruntime.Codex || *s.Writer.ThinkingLevel != "medium" || s.Writer.FellBack {
		t.Fatalf("chosen: %+v %+v", s.Selection, s.Writer)
	}
	ready[agentruntime.Codex] = false
	s = decode[textgen.Settings](t, e.expect(200, "GET", "/text-generation-settings", nil))
	if s.Writer == nil || s.Writer.Runtime != agentruntime.ClaudeCode || !s.Writer.FellBack {
		t.Fatalf("fell back: %+v", s.Writer)
	}
	e.expectError(422, "validation_failed", "PUT", "/text-generation-settings", map[string]any{"selection": map[string]any{"provider": "anthropic", "modelId": "claude-x"}})
	invalidRequest(e, 422, "validation_failed", "PUT", "/text-generation-settings", map[string]any{})
	s = decode[textgen.Settings](t, e.expect(200, "PUT", "/text-generation-settings", map[string]any{"selection": nil}))
	if s.Selection != nil || s.Writer == nil || s.Writer.FellBack {
		t.Fatalf("back to Automatic: %+v %+v", s.Selection, s.Writer)
	}
	ready = map[string]bool{}
	if s := decode[textgen.Settings](t, e.expect(200, "GET", "/text-generation-settings", nil)); s.Writer != nil {
		t.Fatalf("nobody ready: %+v", s.Writer)
	}
}

// A draft comes back cleaned with its writer; refusals and failures use the contract's codes.
func TestPromptDraftsAPI(t *testing.T) {
	e := newEnv(t)
	ready := map[string]bool{agentruntime.ClaudeCode: true}
	r := withTextGeneration(e, &ready)
	project := e.dc.Project.ID.String()
	d := decode[textgen.Draft](t, e.expect(200, "POST", "/prompt-drafts", map[string]any{"target": "agent", "projectId": project, "agentName": "Triage",
		"request": "sort new issues every morning", "currentText": ""}))
	if d.Text != "You triage issues." || d.Writer.Runtime != agentruntime.ClaudeCode {
		t.Fatalf("draft: %+v", d)
	}
	if p := r.reqs[0].Prompt; !strings.Contains(p, "Triage") || !strings.Contains(p, "sort new issues every morning") {
		t.Fatalf("material: %s", p)
	}
	e.expect(200, "POST", "/prompt-drafts", map[string]any{"target": "project", "projectId": project, "request": "", "currentText": "We sell tea."})
	e.expectError(422, "validation_failed", "POST", "/prompt-drafts", map[string]any{"target": "agent", "request": "", "currentText": ""})
	e.expectError(422, "validation_failed", "POST", "/prompt-drafts", map[string]any{"target": "project", "request": "x", "currentText": ""})
	invalidRequest(e, 422, "validation_failed", "POST", "/prompt-drafts", map[string]any{"target": "agent", "request": "x"})
	invalidRequest(e, 422, "validation_failed", "POST", "/prompt-drafts", map[string]any{"target": "agent", "request": "x", "currentText": "", "extra": 1})
	e.expectError(404, "not_found", "POST", "/prompt-drafts", map[string]any{"target": "agent", "agentId": "0192f2c4-7b1e-7000-8000-000000000001", "request": "x", "currentText": ""})

	r.reply = func() (string, error) {
		return "", &executor.TextFailure{Message: "Claude Code could not write it: You've hit your usage limit"}
	}
	e.expectError(502, "text_generation_failed", "POST", "/prompt-drafts", map[string]any{"target": "project", "projectId": project, "request": "x", "currentText": ""})
	ready = map[string]bool{}
	e.expectError(409, "text_generation_unavailable", "POST", "/prompt-drafts", map[string]any{"target": "project", "projectId": project, "request": "x", "currentText": ""})
}

// invalidRequest sends a request that does not match the contract on purpose and expects the error.
func invalidRequest(e *env, status int, code, method, path string, body any) {
	e.t.Helper()
	b := e.expect(status, method, path, body, "X-Contract-Invalid-Request", "1")
	if !strings.Contains(string(b), `"code":"`+code+`"`) {
		e.t.Fatalf("%s %s: want %s, got %s", method, path, code, b)
	}
}
