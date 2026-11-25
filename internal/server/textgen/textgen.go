// Package textgen writes short texts with AI ("Write with AI"), such as a draft of an Agent's or a project's Prompt:
// who writes (the runtime model a person chose, or Automatic: the first ready runtime that writes, with its small
// model), what the model is told, and the one run of that runtime's local CLI (the local executor's WriteText). Only
// runtimes whose accounts are local write; the registry says which and what Automatic prefers (TextGeneration).
//
// The texts of requests and drafts are never logged.
package textgen

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/runtimes"
	"loop-now/internal/server/store"
)

// Defaults of a Service.
const (
	// DefaultTimeout is how long one draft may take, waiting for its turn included.
	DefaultTimeout = 3 * time.Minute
	// DefaultSlots is how many drafts are written at a time.
	DefaultSlots = 2
)

// Limits of a draft request, in characters.
const (
	MaxRequest   = 4000
	MaxAgentName = 100
	MaxModelID   = 256
)

// Targets of a draft.
const (
	TargetAgent   = "agent"
	TargetProject = "project"
)

// Runner writes one text with a runtime's local CLI (the local executor).
type Runner interface {
	WriteText(ctx context.Context, req executor.TextRequest) (string, error)
}

// Models gives a model provider of the live catalog.
type Models interface {
	Provider(id string) (*modelcatalog.Provider, bool)
}

// Service decides who writes and writes drafts.
type Service struct {
	Store  *store.Store
	Models Models
	// Statuses reports the runtimes on this computer (GET /runtimes, with its cache).
	Statuses func(ctx context.Context) []runtimes.Status
	Runner   Runner
	// Timeout is how long one draft may take; 0 means DefaultTimeout. Slots is how many are written at a time; 0 means
	// DefaultSlots.
	Timeout time.Duration
	Slots   int
	// Log gets one line per draft: the runtime, the model, how long it took and whether it worked; never the texts.
	Log *slog.Logger

	once  sync.Once
	slots chan struct{}
}

// Selection is the runtime model a person chose (TextGenerationSelection).
type Selection struct {
	Provider      string  `json:"provider"`
	ModelID       string  `json:"modelId"`
	ThinkingLevel *string `json:"thinkingLevel"`
}

// Writer is the runtime model that writes, or would write now (TextGenerationWriter).
type Writer struct {
	Runtime       string  `json:"runtime"`
	Provider      string  `json:"provider"`
	ModelID       string  `json:"modelId"`
	ModelName     string  `json:"modelName"`
	ThinkingLevel *string `json:"thinkingLevel"`
	FellBack      bool    `json:"fellBack"`
}

// Settings are the setting and who would write now (TextGenerationSettings); a nil Selection is Automatic, a nil
// Writer means no runtime is ready.
type Settings struct {
	Selection *Selection `json:"selection"`
	Writer    *Writer    `json:"writer"`
}

// DraftRequest asks for a Prompt draft (PromptDraftRequest).
type DraftRequest struct {
	Target      string
	AgentID     *uuid.UUID
	ProjectID   *uuid.UUID
	AgentName   *string
	Request     string
	CurrentText string
}

// Draft is a written draft and who wrote it (PromptDraft).
type Draft struct {
	Text   string `json:"text"`
	Writer Writer `json:"writer"`
}

// Settings reads who writes with AI and who would write now.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	sel, err := s.Store.GetTextGenerationSelection(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{Selection: fromStore(sel), Writer: s.writer(ctx, sel)}, nil
}

// PutSettings saves who writes with AI (nil is Automatic) and returns the settings after the change. The provider must
// be one a runtime that writes runs; the model and level are not checked against the live list.
func (s *Service) PutSettings(ctx context.Context, sel *Selection) (Settings, error) {
	var saved *store.TextGenerationSelection
	if sel != nil {
		if rt := agentruntime.ForProvider(sel.Provider); rt.TextGeneration == nil || !slices.Contains(rt.Providers, sel.Provider) {
			return Settings{}, apperr.Invalid("selection.provider", "Choose the model provider of a runtime that uses this computer's own login, such as Claude Code or Codex.")
		}
		if sel.ModelID == "" || utf8.RuneCountInString(sel.ModelID) > MaxModelID {
			return Settings{}, apperr.Invalid("selection.modelId", fmt.Sprintf("Give a model id of 1 to %d characters.", MaxModelID))
		}
		if sel.ThinkingLevel != nil && !modelcatalog.ValidThinkingLevel(*sel.ThinkingLevel) {
			return Settings{}, apperr.Invalid("selection.thinkingLevel", "Unknown thinking level.")
		}
		saved = &store.TextGenerationSelection{Provider: sel.Provider, ModelID: sel.ModelID, ThinkingLevel: sel.ThinkingLevel}
	}
	if err := s.Store.PutTextGenerationSelection(ctx, saved); err != nil {
		return Settings{}, err
	}
	return s.Settings(ctx)
}

func fromStore(sel *store.TextGenerationSelection) *Selection {
	if sel == nil {
		return nil
	}
	return &Selection{Provider: sel.Provider, ModelID: sel.ModelID, ThinkingLevel: sel.ThinkingLevel}
}

// writer decides who writes now: the chosen runtime model when its runtime is ready and its model listed; otherwise
// Automatic (FellBack when a choice was passed over); nil when no runtime is ready.
func (s *Service) writer(ctx context.Context, sel *store.TextGenerationSelection) *Writer {
	ready := map[string]bool{}
	if s.Statuses != nil {
		for _, st := range s.Statuses(ctx) {
			ready[st.ID] = st.Available && (st.Login == nil || st.Login.State != runtimes.LoginLoggedOut)
		}
	}
	if sel != nil {
		rt := agentruntime.ForProvider(sel.Provider)
		if rt.TextGeneration != nil && ready[rt.ID] {
			if m, ok := s.model(sel.Provider, sel.ModelID); ok {
				var level *string
				if sel.ThinkingLevel != nil && slices.Contains(m.ThinkingLevels, *sel.ThinkingLevel) {
					level = sel.ThinkingLevel
				}
				return &Writer{Runtime: rt.ID, Provider: sel.Provider, ModelID: m.ID, ModelName: modelName(m), ThinkingLevel: level}
			}
		}
	}
	for _, rt := range agentruntime.Writers() {
		if !ready[rt.ID] {
			continue
		}
		for _, provider := range rt.Providers {
			p, ok := s.provider(provider)
			if !ok || len(p.Models) == 0 {
				continue
			}
			m := preferredModel(p.Models, rt.TextGeneration.Model)
			return &Writer{Runtime: rt.ID, Provider: provider, ModelID: m.ID, ModelName: modelName(m),
				ThinkingLevel: preferredLevel(m, rt.TextGeneration.Level), FellBack: sel != nil}
		}
	}
	return nil
}

func (s *Service) provider(id string) (*modelcatalog.Provider, bool) {
	if s.Models == nil {
		return nil, false
	}
	return s.Models.Provider(id)
}

func (s *Service) model(provider, id string) (modelcatalog.Model, bool) {
	p, ok := s.provider(provider)
	if !ok {
		return modelcatalog.Model{}, false
	}
	for _, m := range p.Models {
		if m.ID == id {
			return m, true
		}
	}
	return modelcatalog.Model{}, false
}

func modelName(m modelcatalog.Model) string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// preferredModel is the first listed model whose id or name contains prefer (ignoring case), else the first listed.
func preferredModel(models []modelcatalog.Model, prefer string) modelcatalog.Model {
	if prefer != "" {
		p := strings.ToLower(prefer)
		for _, m := range models {
			if strings.Contains(strings.ToLower(m.ID), p) || strings.Contains(strings.ToLower(m.Name), p) {
				return m
			}
		}
	}
	return models[0]
}

// preferredLevel is the level Automatic uses on m: the preferred one when m offers it (its lowest for
// agentruntime.LowestLevel), else nil for the model's default.
func preferredLevel(m modelcatalog.Model, prefer string) *string {
	if prefer == "" || len(m.ThinkingLevels) == 0 {
		return nil
	}
	if prefer == agentruntime.LowestLevel {
		l := m.ThinkingLevels[0]
		return &l
	}
	if slices.Contains(m.ThinkingLevels, prefer) {
		return &prefer
	}
	return nil
}

// Draft writes one Prompt draft. Nothing is saved.
func (s *Service) Draft(ctx context.Context, in DraftRequest) (Draft, error) {
	m, err := s.material(ctx, in)
	if err != nil {
		return Draft{}, err
	}
	sel, err := s.Store.GetTextGenerationSelection(ctx)
	if err != nil {
		return Draft{}, err
	}
	w := s.writer(ctx, sel)
	if w == nil {
		return Draft{}, unavailable("No program on this computer can write with AI yet. Install Claude Code, Codex, Antigravity or Grok, log in to it on this computer, and try again (Settings → Runtimes shows how).")
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	text, err := s.run(run, executor.TextRequest{Runtime: w.Runtime, Model: w.ModelID, ThinkingLevel: w.ThinkingLevel,
		Instructions: instructions(in.Target), Prompt: m})
	if err == nil {
		if text = Clean(text); text == "" {
			err = failed(fmt.Sprintf("%s returned nothing to use. Try again, or choose another writer.", runtimeName(w.Runtime)))
		}
	}
	if err != nil {
		var tf *executor.TextFailure
		switch {
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			err = failed(fmt.Sprintf("%s did not finish within %s. Try again, or choose another writer.", runtimeName(w.Runtime), minutes(timeout)))
		case errors.As(err, &tf) && tf.Unavailable:
			err = unavailable(tf.Message)
		case errors.As(err, &tf):
			err = failed(tf.Message)
		}
	}
	s.logDraft(w, time.Since(started), err)
	if err != nil {
		return Draft{}, err
	}
	return Draft{Text: text, Writer: *w}, nil
}

// run waits for a slot and writes.
func (s *Service) run(ctx context.Context, req executor.TextRequest) (string, error) {
	s.once.Do(func() {
		n := s.Slots
		if n <= 0 {
			n = DefaultSlots
		}
		s.slots = make(chan struct{}, n)
	})
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-s.slots }()
	return s.Runner.WriteText(ctx, req)
}

func (s *Service) logDraft(w *Writer, took time.Duration, err error) {
	if s.Log == nil {
		return
	}
	attrs := []any{"runtime", w.Runtime, "model", w.ModelID, "fellBack", w.FellBack, "ms", took.Milliseconds()}
	if ae, ok := apperr.As(err); ok {
		s.Log.Info("a Prompt draft was not written", append(attrs, "code", ae.Code)...)
		return
	}
	if err != nil {
		s.Log.Info("a Prompt draft was stopped", attrs...)
		return
	}
	s.Log.Info("wrote a Prompt draft", attrs...)
}

func runtimeName(id string) string {
	if rt, ok := agentruntime.ByID(id); ok {
		return rt.Name
	}
	return id
}

func minutes(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if d == time.Minute {
			return "a minute"
		}
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	}
	return d.String() + " (less than a minute)"
}

func unavailable(msg string) error {
	return &apperr.Error{Status: http.StatusConflict, Code: apperr.CodeTextGenerationUnavailable, Message: msg}
}

func failed(msg string) error {
	return &apperr.Error{Status: http.StatusBadGateway, Code: apperr.CodeTextGenerationFailed, Message: msg}
}

// Clean trims a reply and removes one code fence around the whole of it (a fence line, with or without a language,
// first and a matching one last, and no line between them that would close it).
func Clean(s string) string {
	t := strings.TrimSpace(s)
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return t
	}
	first := strings.TrimSpace(lines[0])
	n := 0
	for n < len(first) && (first[n] == '`' || first[n] == '~') && first[n] == first[0] {
		n++
	}
	if n < 3 {
		return t
	}
	fence := first[:n]
	closes := func(line string) bool {
		l := strings.TrimSpace(line)
		return len(l) >= n && strings.Trim(l, fence[:1]) == ""
	}
	if !closes(lines[len(lines)-1]) {
		return t
	}
	for _, l := range lines[1 : len(lines)-1] {
		if closes(l) {
			return t
		}
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
