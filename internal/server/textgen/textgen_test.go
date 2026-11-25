package textgen_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/server/store"
	"loop-now/internal/server/textgen"
	"loop-now/internal/testutil/sqlitetest"
)

// catalog is a model catalog of the local runtimes' providers.
type catalog map[string][]modelcatalog.Model

func (c catalog) Provider(id string) (*modelcatalog.Provider, bool) {
	models, ok := c[id]
	if !ok {
		return nil, false
	}
	return &modelcatalog.Provider{ID: id, Models: models}, true
}

func model(id, name string, levels ...string) modelcatalog.Model {
	def := ""
	if len(levels) > 0 {
		def = levels[len(levels)/2]
	}
	return modelcatalog.Model{ID: id, Name: name, ThinkingLevels: levels, DefaultThinkingLevel: def}
}

// fullCatalog lists models the way the local CLIs do: the small model is not first.
func fullCatalog() catalog {
	return catalog{
		agentruntime.ClaudeCodeProvider:  {model("opus", "Claude Opus (latest)", "low", "medium", "high"), model("haiku", "Claude Haiku (latest)", "low", "medium", "high")},
		agentruntime.CodexProvider:       {model("gpt-6.2-sol", "GPT-6.2 Sol", "low", "medium", "high"), model("gpt-6-mini", "GPT-6 mini", "minimal", "low", "medium"), model("gpt-6-luna", "GPT-6 Luna", "low", "medium", "high")},
		agentruntime.AntigravityProvider: {model("gemini-4-pro", "Gemini 4 Pro", "low", "high"), model("gemini-4-flash", "Gemini 4 Flash", "low", "medium", "high")},
		agentruntime.GrokProvider:        {model("grok-5", "Grok 5", "low", "high"), model("grok-5-fast", "Grok 5 Fast")},
	}
}

// statuses reports the runtimes in ready as available and logged in, and the others as not found; a runtime in
// loggedOut is found but logged out.
type statuses struct {
	mu        sync.Mutex
	ready     map[string]bool
	loggedOut map[string]bool
}

func (s *statuses) list(context.Context) []runtimes.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []runtimes.Status
	for _, rt := range agentruntime.All() {
		st := runtimes.Status{ID: rt.ID}
		switch {
		case s.ready[rt.ID]:
			st.Available, st.Login = true, &runtimes.Login{State: runtimes.LoginLoggedIn}
		case s.loggedOut[rt.ID]:
			st.Available, st.Login = true, &runtimes.Login{State: runtimes.LoginLoggedOut}
		}
		out = append(out, st)
	}
	return out
}

func (s *statuses) set(ready ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = map[string]bool{}
	for _, id := range ready {
		s.ready[id] = true
	}
}

// runner records the requests and answers with reply.
type runner struct {
	mu    sync.Mutex
	reqs  []executor.TextRequest
	reply func(ctx context.Context, req executor.TextRequest) (string, error)
}

func (r *runner) WriteText(ctx context.Context, req executor.TextRequest) (string, error) {
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	reply := r.reply
	r.mu.Unlock()
	if reply == nil {
		return "A draft.", nil
	}
	return reply(ctx, req)
}

func (r *runner) last(t *testing.T) executor.TextRequest {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.reqs) == 0 {
		t.Fatal("nothing was written")
	}
	return r.reqs[len(r.reqs)-1]
}

type env struct {
	svc      *textgen.Service
	st       *store.Store
	statuses *statuses
	runner   *runner
	project  store.Project
}

func newEnv(t *testing.T, mutate func(s *textgen.Service)) *env {
	t.Helper()
	st := sqlitetest.NewStore(t)
	defaults, err := st.EnsureDefaults(context.Background(), "org", "Shop")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, statuses: &statuses{}, runner: &runner{}, project: defaults.Project}
	e.svc = &textgen.Service{Store: st, Models: fullCatalog(), Statuses: e.statuses.list, Runner: e.runner}
	if mutate != nil {
		mutate(e.svc)
	}
	return e
}

func writerOf(t *testing.T, e *env) *textgen.Writer {
	t.Helper()
	s, err := e.svc.Settings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return s.Writer
}

func level(w *textgen.Writer) string {
	if w.ThinkingLevel == nil {
		return ""
	}
	return *w.ThinkingLevel
}

// Automatic writes with the first ready runtime in the order Claude Code, Codex, Antigravity, Grok, then the local ACP
// commands, each with its small model from the live list and the level it prefers; nobody when none is ready.
func TestAutomatic(t *testing.T) {
	e := newEnv(t, nil)
	if w := writerOf(t, e); w != nil {
		t.Fatalf("no runtime is ready: %+v", w)
	}
	e.statuses.loggedOut = map[string]bool{agentruntime.ClaudeCode: true}
	all := []string{agentruntime.Pi, agentruntime.Codex, agentruntime.Antigravity, agentruntime.Grok}
	for _, c := range []struct {
		ready                   []string
		runtime, model, name, l string
	}{
		{append(all, agentruntime.ClaudeCode), agentruntime.ClaudeCode, "haiku", "Claude Haiku (latest)", ""},
		{all, agentruntime.Codex, "gpt-6-luna", "GPT-6 Luna", "low"},
		{all[2:], agentruntime.Antigravity, "gemini-4-flash", "Gemini 4 Flash", "low"},
		{all[3:], agentruntime.Grok, "grok-5", "Grok 5", ""},
		{[]string{agentruntime.Pi}, "", "", "", ""},
	} {
		e.statuses.set(c.ready...)
		w := writerOf(t, e)
		if c.runtime == "" {
			if w != nil {
				t.Fatalf("Pi does not write: %+v", w)
			}
			continue
		}
		if w == nil || w.Runtime != c.runtime || w.ModelID != c.model || w.ModelName != c.name || level(w) != c.l || w.FellBack {
			t.Fatalf("ready %v: %+v, want %s %s %q", c.ready, w, c.runtime, c.model, c.l)
		}
		if w.Provider != agentruntime.ForProvider(w.Provider).Providers[0] || agentruntime.ForProvider(w.Provider).ID != c.runtime {
			t.Fatalf("provider %s", w.Provider)
		}
	}

	// A runtime whose list has no models (not read yet) is passed over; the next preference serves when the first is
	// not listed; with none listed, the first listed model at its default level.
	e.svc.Models = catalog{agentruntime.CodexProvider: {model("gpt-6.2-sol", "GPT-6.2 Sol"), model("gpt-6-mini", "GPT-6 mini", "low")}, agentruntime.ClaudeCodeProvider: {}}
	e.statuses.set(agentruntime.ClaudeCode, agentruntime.Codex)
	if w := writerOf(t, e); w == nil || w.Runtime != agentruntime.Codex || w.ModelID != "gpt-6-mini" || level(w) != "low" {
		t.Fatalf("no Luna model, a mini one: %+v", w)
	}
	e.svc.Models = catalog{agentruntime.CodexProvider: {model("gpt-6", "GPT-6", "medium", "high")}}
	if w := writerOf(t, e); w == nil || w.Runtime != agentruntime.Codex || w.ModelID != "gpt-6" || w.ThinkingLevel != nil {
		t.Fatalf("no preferred model: %+v", w)
	}

	// A local ACP command writes after the built-in runtimes, with its default model.
	id := uuid.New()
	agentruntime.SetLocalCommands([]agentruntime.Runtime{agentruntime.LocalCommandRuntime(id, "Mine", agentruntime.LocalCommand{Command: "mine"})})
	defer agentruntime.SetLocalCommands(nil)
	rid := agentruntime.LocalCommandRuntimeID(id)
	e.svc.Models = catalog{rid: {model("default", "Default model")}}
	e.statuses.set(rid)
	if w := writerOf(t, e); w == nil || w.Runtime != rid || w.Provider != rid || w.ModelID != "default" {
		t.Fatalf("local command: %+v", w)
	}
}

// A chosen runtime model writes when it is ready; when it is not (not installed, logged out, model no longer listed),
// Automatic writes and says it fell back. A level the model does not offer gives its default.
func TestChosenWriter(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	high, loud := "high", "loud"
	if _, err := e.svc.PutSettings(ctx, &textgen.Selection{Provider: agentruntime.GrokProvider, ModelID: "grok-5", ThinkingLevel: &high}); err != nil {
		t.Fatal(err)
	}
	e.statuses.set(agentruntime.ClaudeCode, agentruntime.Grok)
	s, err := e.svc.Settings(ctx)
	if err != nil || s.Selection == nil || s.Selection.Provider != agentruntime.GrokProvider || s.Writer == nil || s.Writer.Runtime != agentruntime.Grok ||
		s.Writer.ModelID != "grok-5" || level(s.Writer) != "high" || s.Writer.FellBack {
		t.Fatalf("the chosen one: %+v %+v %v", s.Selection, s.Writer, err)
	}
	e.statuses.set(agentruntime.ClaudeCode)
	if w := writerOf(t, e); w == nil || w.Runtime != agentruntime.ClaudeCode || !w.FellBack {
		t.Fatalf("not ready, Automatic writes: %+v", w)
	}
	e.statuses.set(agentruntime.ClaudeCode, agentruntime.Grok)
	if _, err := e.svc.PutSettings(ctx, &textgen.Selection{Provider: agentruntime.GrokProvider, ModelID: "grok-4"}); err != nil {
		t.Fatal(err)
	}
	if w := writerOf(t, e); w == nil || w.Runtime != agentruntime.ClaudeCode || !w.FellBack {
		t.Fatalf("model gone, Automatic writes: %+v", w)
	}
	if _, err := e.svc.PutSettings(ctx, &textgen.Selection{Provider: agentruntime.GrokProvider, ModelID: "grok-5-fast", ThinkingLevel: &high}); err != nil {
		t.Fatal(err)
	}
	if w := writerOf(t, e); w == nil || w.ModelID != "grok-5-fast" || w.ThinkingLevel != nil || w.FellBack {
		t.Fatalf("a level the model lacks: %+v", w)
	}
	e.statuses.set()
	if w := writerOf(t, e); w != nil {
		t.Fatalf("nobody ready: %+v", w)
	}
	s, err = e.svc.PutSettings(ctx, nil)
	if err != nil || s.Selection != nil {
		t.Fatalf("back to Automatic: %+v %v", s, err)
	}

	for _, c := range []struct {
		sel   textgen.Selection
		field string
	}{
		{textgen.Selection{Provider: "anthropic", ModelID: "claude-x"}, "selection.provider"},
		{textgen.Selection{Provider: "nobody-lists-this", ModelID: "m"}, "selection.provider"},
		{textgen.Selection{Provider: agentruntime.CodexProvider, ModelID: ""}, "selection.modelId"},
		{textgen.Selection{Provider: agentruntime.CodexProvider, ModelID: strings.Repeat("m", 257)}, "selection.modelId"},
		{textgen.Selection{Provider: agentruntime.CodexProvider, ModelID: "m", ThinkingLevel: &loud}, "selection.thinkingLevel"},
	} {
		_, err := e.svc.PutSettings(ctx, &c.sel)
		if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 || ae.Fields[0].Field != c.field {
			t.Errorf("%+v: %v", c.sel, err)
		}
	}
}

// A draft for an Agent: the Agent instruction, then the Agent's name, its project's name and Prompt, the current text
// and the request; the writer's runtime, model and level. One code fence around the reply is removed.
func TestAgentDraft(t *testing.T) {
	e := newEnv(t, nil)
	ctx := context.Background()
	e.statuses.set(agentruntime.Codex)
	if _, err := e.st.PutProjectPrompt(ctx, e.project.ID, "We sell tea. Reply in English.", 1); err != nil {
		t.Fatal(err)
	}
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(e.project.ID), "Triage", "codex-cli", "gpt-6", "")
	if err != nil {
		t.Fatal(err)
	}
	e.runner.reply = func(context.Context, executor.TextRequest) (string, error) {
		return "  ```markdown\nYou sort new issues.\n```\n", nil
	}
	d, err := e.svc.Draft(ctx, textgen.DraftRequest{Target: textgen.TargetAgent, AgentID: &agent.ID, Request: "每天早上把新 issue 分类", CurrentText: "Sort issues."}) // check-english: zh (a request in Chinese reaches the writer as is)
	if err != nil || d.Text != "You sort new issues." || d.Writer.Runtime != agentruntime.Codex || d.Writer.ModelID != "gpt-6-luna" {
		t.Fatalf("draft: %+v %v", d, err)
	}
	req := e.runner.last(t)
	if req.Runtime != agentruntime.Codex || req.Model != "gpt-6-luna" || req.ThinkingLevel == nil || *req.ThinkingLevel != "low" {
		t.Fatalf("request: %+v", req)
	}
	if !strings.Contains(req.Instructions, "Prompt of an Agent") || !strings.Contains(req.Instructions, "Reply with the Prompt only") {
		t.Fatalf("instructions: %s", req.Instructions)
	}
	for _, want := range []string{"Triage", "Shop", "We sell tea. Reply in English.", "Sort issues.", "每天早上把新 issue 分类"} { // check-english: zh (a request in Chinese reaches the writer as is)
		if !strings.Contains(req.Prompt, want) {
			t.Errorf("the material lacks %q:\n%s", want, req.Prompt)
		}
	}
	// A rename typed but not saved replaces the saved name.
	name := "Issue triage"
	if _, err := e.svc.Draft(ctx, textgen.DraftRequest{Target: textgen.TargetAgent, AgentID: &agent.ID, AgentName: &name, Request: "x"}); err != nil {
		t.Fatal(err)
	}
	if p := e.runner.last(t).Prompt; !strings.Contains(p, "Issue triage") || strings.Contains(p, "Triage\n") {
		t.Fatalf("the typed name: %s", p)
	}
	// A new platform Agent: no project.
	if _, err := e.svc.Draft(ctx, textgen.DraftRequest{Target: textgen.TargetAgent, Request: "", CurrentText: "Help me."}); err != nil {
		t.Fatal(err)
	}
	if p := e.runner.last(t).Prompt; strings.Contains(p, "Shop") || !strings.Contains(p, "not in a project") {
		t.Fatalf("a platform Agent: %s", p)
	}
	// A new Agent in a project.
	if _, err := e.svc.Draft(ctx, textgen.DraftRequest{Target: textgen.TargetAgent, ProjectID: &e.project.ID, AgentName: &name, Request: "x"}); err != nil {
		t.Fatal(err)
	}
	if p := e.runner.last(t).Prompt; !strings.Contains(p, "Shop") || !strings.Contains(p, "We sell tea.") {
		t.Fatalf("a new Agent in a project: %s", p)
	}
}

// A draft for a project: the project instruction, the project's name and the current text.
func TestProjectDraft(t *testing.T) {
	e := newEnv(t, nil)
	e.statuses.set(agentruntime.ClaudeCode)
	d, err := e.svc.Draft(context.Background(), textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, Request: "a tea shop", CurrentText: ""})
	if err != nil || d.Text != "A draft." || d.Writer.Runtime != agentruntime.ClaudeCode {
		t.Fatalf("draft: %+v %v", d, err)
	}
	req := e.runner.last(t)
	if !strings.Contains(req.Instructions, "Prompt of a project") || !strings.Contains(req.Prompt, "Shop") || !strings.Contains(req.Prompt, "a tea shop") {
		t.Fatalf("request: %+v", req)
	}
}

// Requests that do not fit are refused before anything runs; a missing Agent or project is not found.
func TestDraftValidation(t *testing.T) {
	e := newEnv(t, nil)
	e.statuses.set(agentruntime.ClaudeCode)
	ctx := context.Background()
	agent, err := e.st.CreateAgent(ctx, store.ProjectScope(e.project.ID), "A", "claude-code", "haiku", "")
	if err != nil {
		t.Fatal(err)
	}
	missing, name := uuid.New(), "N"
	for _, c := range []struct {
		req   textgen.DraftRequest
		field string
	}{
		{textgen.DraftRequest{Target: "skill", Request: "x"}, "target"},
		{textgen.DraftRequest{Target: textgen.TargetAgent, Request: " ", CurrentText: "\n"}, "request"},
		{textgen.DraftRequest{Target: textgen.TargetAgent, Request: strings.Repeat("字", 4001)}, "request"}, // check-english: zh (the length limit counts characters, not bytes)
		{textgen.DraftRequest{Target: textgen.TargetAgent, AgentName: func() *string { s := strings.Repeat("n", 101); return &s }(), Request: "x"}, "agentName"},
		{textgen.DraftRequest{Target: textgen.TargetAgent, AgentID: &agent.ID, ProjectID: &e.project.ID, Request: "x"}, "projectId"},
		{textgen.DraftRequest{Target: textgen.TargetProject, Request: "x"}, "projectId"},
		{textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, AgentID: &agent.ID, Request: "x"}, "agentId"},
		{textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, AgentName: &name, Request: "x"}, "agentName"},
	} {
		_, err := e.svc.Draft(ctx, c.req)
		if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) == 0 || ae.Fields[0].Field != c.field {
			t.Errorf("%+v: %v", c.req, err)
		}
	}
	if _, err := e.svc.Draft(ctx, textgen.DraftRequest{Target: textgen.TargetAgent, Request: strings.Repeat("字", 4000)}); err != nil { // check-english: zh (the length limit counts characters, not bytes)
		t.Fatalf("4000 characters are fine: %v", err)
	}
	for _, req := range []textgen.DraftRequest{
		{Target: textgen.TargetAgent, AgentID: &missing, Request: "x"},
		{Target: textgen.TargetAgent, ProjectID: &missing, Request: "x"},
		{Target: textgen.TargetProject, ProjectID: &missing, Request: "x"},
	} {
		if _, err := e.svc.Draft(ctx, req); !apperr.Is(err, apperr.CodeNotFound) {
			t.Errorf("%+v: %v", req, err)
		}
	}
	if n := len(e.runner.reqs); n != 1 {
		t.Fatalf("only the valid request ran: %d", n)
	}
}

// Failures: nobody ready is unavailable (409) and says how to get a writer; the CLI's own failure, an empty reply and a
// run over the time limit are failed (502); a stopped request returns its context's error.
func TestDraftFailures(t *testing.T) {
	e := newEnv(t, func(s *textgen.Service) { s.Timeout = 300 * time.Millisecond })
	ctx := context.Background()
	req := textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, Request: "x"}
	_, err := e.svc.Draft(ctx, req)
	if ae, ok := apperr.As(err); !ok || ae.Code != apperr.CodeTextGenerationUnavailable || ae.Status != 409 || !strings.Contains(ae.Message, "log in") {
		t.Fatalf("nobody ready: %v", err)
	}
	e.statuses.set(agentruntime.ClaudeCode)
	for _, c := range []struct {
		reply func(context.Context, executor.TextRequest) (string, error)
		code  string
		says  string
	}{
		{func(context.Context, executor.TextRequest) (string, error) {
			return "", &executor.TextFailure{Message: "Claude Code could not write it: You've hit your usage limit"}
		}, apperr.CodeTextGenerationFailed, "usage limit"},
		{func(context.Context, executor.TextRequest) (string, error) {
			return "", &executor.TextFailure{Unavailable: true, Message: "Claude Code was not found on this computer"}
		}, apperr.CodeTextGenerationUnavailable, "not found"},
		{func(context.Context, executor.TextRequest) (string, error) { return " ```\n\n``` ", nil }, apperr.CodeTextGenerationFailed, "nothing"},
		{func(ctx context.Context, _ executor.TextRequest) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}, apperr.CodeTextGenerationFailed, "minute"},
	} {
		e.runner.reply = c.reply
		_, err := e.svc.Draft(ctx, req)
		if ae, ok := apperr.As(err); !ok || ae.Code != c.code || !strings.Contains(ae.Message, c.says) {
			t.Errorf("%s: %v", c.says, err)
		}
	}
	stopped, cancel := context.WithCancel(ctx)
	e.runner.reply = func(ctx context.Context, _ executor.TextRequest) (string, error) {
		cancel()
		<-ctx.Done()
		return "", ctx.Err()
	}
	if _, err := e.svc.Draft(stopped, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("a stopped request: %v", err)
	}
}

// A few drafts are written at a time; one more waits for its turn.
func TestDraftSlots(t *testing.T) {
	e := newEnv(t, func(s *textgen.Service) { s.Slots = 2 })
	e.statuses.set(agentruntime.ClaudeCode)
	var running, most atomic.Int32
	release := make(chan struct{})
	e.runner.reply = func(context.Context, executor.TextRequest) (string, error) {
		n := running.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return "ok", nil
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.svc.Draft(context.Background(), textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, Request: "x"}); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond)
	if got := running.Load(); got != 2 {
		t.Fatalf("running at once: %d", got)
	}
	close(release)
	wg.Wait()
	if most.Load() != 2 {
		t.Fatalf("at most two at a time: %d", most.Load())
	}
}

// A reply is cleaned: surrounding blank space, and one code fence around the whole reply (with or without a language).
func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"  Hello\n":                      "Hello",
		"```\nA\nB\n```":                 "A\nB",
		"```md\nA\n```":                  "A",
		"Text\n```\ncode\n```":           "Text\n```\ncode\n```",
		"```\nA\n```\nmore\n```\nB\n```": "```\nA\n```\nmore\n```\nB\n```",
		"````markdown\n```x```\n````":    "```x```",
		"```":                            "```",
	} {
		if got := textgen.Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// The material ends with the language rule again, after the English labels; it names the language when the request
// (or, with no request, the current text) is mostly in Chinese, Japanese or Korean.
func TestDraftLanguage(t *testing.T) {
	e := newEnv(t, nil)
	e.statuses.set(agentruntime.Antigravity)
	e.svc.Models = catalog{agentruntime.AntigravityProvider: {model("gemini-3.8-flash", "Gemini 3.8 Flash", "low", "high")}}
	for _, c := range []struct {
		request, current, names string
	}{
		{"每天下午六点把今天 dbt 跑失败的模型整理成一条日报发给我", "", "Chinese"}, // check-english: zh (a Chinese request must name Chinese)
		{"毎日のレポートをまとめて送ってください", "", "Japanese"},           // check-english: zh (kana make it Japanese, not Chinese)
		{"매일 실패한 모델을 정리해서 보내 주세요", "", "Korean"},          // check-english: zh (Hangul makes it Korean)
		{"", "你负责整理日报。", "Chinese"},                       // check-english: zh (with no request, the current text decides)
		{"a daily report for 数据仓库 failures", "", ""},      // check-english: zh (mostly Latin letters: no language named)
		{"a daily report of failed dbt models", "", ""},
	} {
		if _, err := e.svc.Draft(context.Background(), textgen.DraftRequest{Target: textgen.TargetProject, ProjectID: &e.project.ID, Request: c.request, CurrentText: c.current}); err != nil {
			t.Fatal(err)
		}
		p := strings.TrimSpace(e.runner.last(t).Prompt)
		lines := strings.Split(p, "\n")
		last := lines[len(lines)-1]
		if !strings.Contains(last, "not in English unless that is their language") {
			t.Errorf("%q: the material must end with the language rule: %q", c.request, last)
		}
		for _, lang := range []string{"Chinese", "Japanese", "Korean"} {
			if named := strings.Contains(last, lang); named != (lang == c.names) {
				t.Errorf("%q: names %s %v (%q)", c.request+c.current, lang, named, last)
			}
		}
	}
}
