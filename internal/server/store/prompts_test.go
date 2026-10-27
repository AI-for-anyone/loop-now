package store_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

func promptExpected(v int64) *int64 { return &v }
func TestPromptMigrationAndCAS(t *testing.T) {
	ctx := context.Background()
	schema := pgtest.NewSchema(t)
	st, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.MigrateUpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	defaults, err := st.EnsureDefaults(ctx, "旧组织", "旧项目")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	original := "原始Prompt\n必须保留空白\n"
	if _, err = st.Pool.Exec(ctx, `INSERT INTO agents(id,project_id,name,model_provider,model_id,prompt,config_version)VALUES($1,$2,'旧Agent','fixture','model',$3,8)`, id, defaults.Project.ID, original); err != nil {
		t.Fatal(err)
	}
	if err = st.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetAgent(ctx, id)
	if err != nil || a.Prompt != original || a.PromptVersion != 1 || a.ConfigVersion != 8 {
		t.Fatal("009 changed legacy Agent", err)
	}
	project, err := st.GetProjectPrompt(ctx, defaults.Project.ID)
	if err != nil || project.Prompt != "" || project.PromptVersion != 1 {
		t.Fatal("009 project default", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, text := range []string{"P1", "P2"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.PutProjectPrompt(ctx, defaults.Project.ID, text, 1)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if apperr.Is(err, apperr.CodePreconditionFailed) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent project CAS", successes, conflicts)
	}
	p, err := st.GetProjectPrompt(ctx, defaults.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutProjectPrompt(ctx, defaults.Project.ID, p.Prompt, 1); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("same-text old version bypassed CAS", err)
	}
	same, err := st.PutProjectPrompt(ctx, defaults.Project.ID, p.Prompt, p.PromptVersion)
	if err != nil || same.PromptVersion != p.PromptVersion {
		t.Fatal("same-text bumped version", err)
	}
	nextModel := "different"
	a, err = st.UpdateAgent(ctx, id, store.AgentPatch{ModelID: &nextModel})
	if err != nil || a.PromptVersion != 1 || a.ConfigVersion != 9 {
		t.Fatal("model change impacted Prompt version", err)
	}
	text := "changed"
	name := "not-saved"
	if _, err = st.UpdateAgent(ctx, id, store.AgentPatch{Name: &name, Prompt: &text}); !apperr.Is(err, apperr.CodeValidationFailed) {
		t.Fatal("missing version accepted", err)
	}
	a, err = st.UpdateAgent(ctx, id, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: promptExpected(1)})
	if err != nil || a.PromptVersion != 2 || a.ConfigVersion != 10 {
		t.Fatal(err)
	}
	if _, err = st.UpdateAgent(ctx, id, store.AgentPatch{Name: &name, Prompt: &text, ExpectedPromptVersion: promptExpected(1)}); !apperr.Is(err, apperr.CodePreconditionFailed) {
		t.Fatal("stale mixed update accepted", err)
	}
	unchanged, err := st.GetAgent(ctx, id)
	if err != nil || unchanged.Name != "旧Agent" || unchanged.PromptVersion != 2 || unchanged.ConfigVersion != 10 {
		t.Fatal("mixed PATCH partially changed fields", err)
	}

	// Agent CAS also serializes two independent editors without losing either version.
	concurrent, err := st.CreateAgent(ctx, store.ProjectScope(defaults.Project.ID), "并发编辑", "fixture", "model", "")
	if err != nil {
		t.Fatal(err)
	}
	agentResults := make(chan error, 2)
	for _, value := range []string{"one", "two"} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			_, err := st.UpdateAgent(ctx, concurrent.ID, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: promptExpected(1)})
			agentResults <- err
		}(value)
	}
	wg.Wait()
	close(agentResults)
	successes, conflicts = 0, 0
	for err := range agentResults {
		if err == nil {
			successes++
		} else if apperr.Is(err, apperr.CodePreconditionFailed) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("Agent CAS lost concurrent editor", successes, conflicts)
	}
	if _, err = st.Pool.Exec(ctx, `CREATE FUNCTION reject_project_prompt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic project fault'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Pool.Exec(ctx, `CREATE TRIGGER reject_project_prompt BEFORE UPDATE ON projects FOR EACH ROW EXECUTE FUNCTION reject_project_prompt()`); err != nil {
		t.Fatal(err)
	}
	if _, err = st.PutProjectPrompt(ctx, defaults.Project.ID, "failed", p.PromptVersion); err == nil {
		t.Fatal("project fault accepted")
	}
	after, err := st.GetProjectPrompt(ctx, defaults.Project.ID)
	if err != nil || after != p {
		t.Fatal("project failure partially committed", err)
	}
	// A DB failure after computing a successful CAS cannot commit text or either version.
	for _, q := range []string{`CREATE FUNCTION reject_prompt_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic prompt write failure'; END $$`, `CREATE TRIGGER reject_prompt_update BEFORE UPDATE ON agents FOR EACH ROW EXECUTE FUNCTION reject_prompt_update()`} {
		if _, err = st.Pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	text = "failed"
	if _, err = st.UpdateAgent(ctx, id, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: promptExpected(2)}); err == nil {
		t.Fatal("fault injected update succeeded")
	}
	unchanged, err = st.GetAgent(ctx, id)
	if err != nil || unchanged.Prompt != "changed" || unchanged.PromptVersion != 2 || unchanged.ConfigVersion != 10 {
		t.Fatal("failed CAS partially committed", err)
	}
}

func TestPromptCaptureScopes(t *testing.T) {
	ctx := context.Background()
	st := pgtest.NewStore(t)
	dc, err := st.EnsureDefaults(ctx, "组织", "项目")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateProject(ctx, dc.Organization.ID, "另一个项目")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		id   uuid.UUID
		text string
	}{{dc.Project.ID, "rules-first"}, {other.ID, "rules-second"}} {
		if _, err = st.PutProjectPrompt(ctx, p.id, p.text, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		scope store.Scope
		want  string
	}{{store.ProjectScope(dc.Project.ID), "rules-first"}, {store.ProjectScope(other.ID), "rules-second"}, {store.PlatformScope(), ""}} {
		a, err := st.CreateAgent(ctx, tc.scope, "same-name", "fixture", "model", "own")
		if err != nil {
			t.Fatal(err)
		}
		snap, err := st.CaptureExecutionIdentity(ctx, a.ID)
		if err != nil || snap.Prompts.ProjectPrompt != tc.want || snap.Prompts.AgentPrompt != "own" || snap.Prompts.AgentVersion != 1 {
			t.Fatal("rules crossed scope", err)
		}
		if tc.want == "" && (snap.Prompts.ProjectID != nil || snap.Prompts.ProjectVersion != 0) {
			t.Fatal("platform inherited project identity")
		}
	}
}
