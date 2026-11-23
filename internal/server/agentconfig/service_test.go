package agentconfig_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/modelcatalog"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/sqlitetest"
)

type fakeReleaser struct {
	mu       sync.Mutex
	released []uuid.UUID
}

func (f *fakeReleaser) Release(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, id)
	return nil
}

type env struct {
	svc      *agentconfig.Service
	st       *store.Store
	releaser *fakeReleaser
	scope    store.Scope // default project
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := sqlitetest.NewStore(t)
	dc, err := st.EnsureDefaults(context.Background(), "Default org", "Default project")
	if err != nil {
		t.Fatal(err)
	}
	tmpls, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	rel := &fakeReleaser{}
	models := modelcatalog.New("1.1.0", nil, []modelcatalog.Provider{
		{ID: "anthropic", AuthMethods: []string{"api_key"}, APIKeyEnvVars: []string{"ANTHROPIC_API_KEY"}, Models: []modelcatalog.Model{{ID: "claude-x", Name: "Claude X"}, {ID: "claude-y", Name: "Claude Y"}}},
	})
	svc := &agentconfig.Service{
		Store: st, Models: models, Templates: tmpls, Releaser: rel, OrgID: dc.Organization.ID,
		Limits: agentconfig.Limits{
			MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096,
			Text: content.TextRules{Extensions: []string{".md", ".txt"}, MaxBytes: 1024},
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	return &env{svc: svc, st: st, releaser: rel, scope: store.ProjectScope(dc.Project.ID)}
}

func (e *env) agent(t *testing.T) store.Agent {
	t.Helper()
	a, err := e.svc.CreateAgent(context.Background(), e.scope, "assistant-"+uuid.NewString()[:8], "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("want error code %s, got %v", code, err)
	}
}

func TestCreateAndQueryAgent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	_, err := e.svc.CreateAgent(ctx, e.scope, a.Name, "anthropic", "claude-y", "Prompt")
	wantCode(t, err, apperr.CodeNameConflict)
	b, err := e.svc.CreateAgent(ctx, e.scope, "Assistant two", "anthropic", "claude-y", "Prompt")
	if err != nil || a.ID == b.ID || a.ConfigVersion != 1 {
		t.Fatalf("an Agent with a different name should be created: %v %v", b, err)
	}
	// Renaming to an existing name is rejected.
	dup := a.Name
	_, err = e.svc.UpdateAgent(ctx, b.ID, agentconfig.AgentPatch{Name: &dup})
	wantCode(t, err, apperr.CodeNameConflict)
	_, err = e.svc.CreateAgent(ctx, e.scope, "", "anthropic", "no-such", "")
	ae, _ := apperr.As(err)
	if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 2 {
		t.Fatalf("should report both the name and the model: %+v", ae)
	}
	items, err := e.st.ListAgents(ctx, e.scope)
	if err != nil || len(items) != 2 || items[0].Busy || items[0].SessionState != "none" {
		t.Fatalf("unexpected list: %+v %v", items, err)
	}
}

// Names form the Inbox address: both create and rename validate by the naming rules, the field-level error points to
// name, and nothing is changed.
func TestAgentNameRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	for _, name := range []string{"frontend/assistant", "assistant#1", "@assistant", " assistant", "assistant\u3000", "a\tb", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} { // check-english: zh (a trailing ideographic space U+3000 counts as whitespace)
		_, err := e.svc.CreateAgent(ctx, e.scope, name, "anthropic", "claude-x", "")
		ae, _ := apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 || ae.Fields[0].Field != "name" {
			t.Errorf("creating %q should be rejected with a name field error: %+v", name, ae)
		}
		n := name
		_, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Name: &n})
		ae, _ = apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || ae.Fields[0].Field != "name" {
			t.Errorf("renaming to %q should be rejected with a name field error: %+v", name, ae)
		}
	}
	got, err := e.st.GetAgent(ctx, a.ID)
	if err != nil || got.Name != a.Name || got.ConfigVersion != a.ConfigVersion {
		t.Fatalf("a rejected rename should not change the Agent: %+v %v", got, err)
	}
	// Spaces in the middle and @ are allowed.
	if _, err := e.svc.CreateAgent(ctx, e.scope, "dev assistant@frontend", "anthropic", "claude-x", ""); err != nil {
		t.Fatalf("a valid name was rejected: %v", err)
	}
}

func TestUpdateAgentBumpsVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	name := "New name"
	got, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Name: &name})
	if err != nil || got.ConfigVersion != 1 {
		t.Fatalf("changing only the display name should not bump the config version: %v %v", got.ConfigVersion, err)
	}
	prompt := "New Prompt"
	got, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Prompt: &prompt, ExpectedPromptVersion: func() *int64 { v := int64(1); return &v }()})
	if err != nil || got.ConfigVersion != 2 || got.Prompt != prompt {
		t.Fatalf("changing the Prompt should bump the config version: %+v %v", got, err)
	}
	p := "anthropic"
	_, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Provider: &p})
	wantCode(t, err, apperr.CodeValidationFailed)
}

func TestDeleteAgent(t *testing.T) {
	e := newEnv(t)
	e.scope = store.PlatformScope()
	ctx := context.Background()
	a := e.agent(t)
	if _, err := e.svc.AddCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PutCLICredential(ctx, a.ID, "gh", map[string]string{"token": "ghp_example"}); err != nil {
		t.Fatal(err)
	}
	// Deletion is refused while there are queued messages.
	runID := uuid.Must(uuid.NewV7())
	if _, err := e.st.Pool.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, queue_seq, status) VALUES ($1, $2, $3, 1, 1, 1, 'queued')`,
		runID, a.ID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.svc.DeleteAgent(ctx, a.ID), apperr.CodeAgentNotIdle)
	if _, err := e.st.Pool.Exec(ctx, `UPDATE runs SET status = 'completed' WHERE id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	// A soft-deleted Agent does not hold on to its name.
	if _, err := e.svc.CreateAgent(ctx, e.scope, a.Name, "anthropic", "claude-x", ""); err != nil {
		t.Fatalf("a deleted Agent's name should be reusable: %v", err)
	}
	if _, err := e.svc.GetAgentDetail(ctx, a.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("should be treated as missing after deletion: %v", err)
	}
	var creds, runs int
	_ = e.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_credentials WHERE agent_id = $1), (SELECT count(*) FROM runs WHERE agent_id = $1)`, a.ID).Scan(&creds, &runs)
	if creds != 0 || runs != 1 {
		t.Fatalf("should delete the credential secrets and keep the run records: creds=%d runs=%d", creds, runs)
	}
	if len(e.releaser.released) != 1 || e.releaser.released[0] != a.ID {
		t.Fatal("deletion should tell the executor to release")
	}
}

func skillZip(t *testing.T, name string, extra map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name + "/SKILL.md")
	_, _ = w.Write([]byte("---\nname: " + name + "\ndescription: Test Skill\n---\nBody\n"))
	for p, d := range extra {
		w, _ := zw.Create(name + "/" + p)
		_, _ = w.Write([]byte(d))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func TestSkillTemplatesAndCopies(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tmpl, err := e.svc.CreateSkillFromZip(ctx, skillZip(t, "pdf-tools", map[string]string{"scripts/run.sh": "echo hi"}))
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.Name != "pdf-tools" || len(tmpl.Files) != 2 || !strings.Contains(tmpl.SkillMD, "name: pdf-tools") {
		t.Fatalf("unexpected template detail: %+v", tmpl)
	}
	_, err = e.svc.CreateSkillFromZip(ctx, skillZip(t, "pdf-tools", nil))
	wantCode(t, err, apperr.CodeNameConflict)
	md, err := e.svc.CreateSkillFromMarkdown(ctx, "---\nname: notes\ndescription: Take notes\n---\n")
	if err != nil || len(md.Files) != 1 {
		t.Fatalf("creating a template from SKILL.md alone failed: %v", err)
	}
	_, err = e.svc.UpdateSkillTemplateMarkdown(ctx, tmpl.ID, "---\nname: pdf-tools\n---\n")
	wantCode(t, err, apperr.CodeValidationFailed)

	a := e.agent(t)
	copyA, err := e.svc.AddAgentSkill(ctx, a.ID, tmpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.AddAgentSkill(ctx, a.ID, tmpl.ID)
	wantCode(t, err, apperr.CodeNameConflict)

	// Editing a template does not affect existing copies and keeps the other files.
	newMD := "---\nname: pdf-tools\ndescription: New description\n---\nNew body\n"
	edited, err := e.svc.UpdateSkillTemplateMarkdown(ctx, tmpl.ID, newMD)
	if err != nil || len(edited.Files) != 2 || edited.Description != "New description" {
		t.Fatalf("editing the template SKILL.md should keep the other files: %+v %v", edited, err)
	}
	gotA, _ := e.svc.GetAgentSkill(ctx, a.ID, copyA.ID)
	if gotA.Description != "Test Skill" {
		t.Fatalf("editing the template should not affect existing copies: %s", gotA.Description)
	}
	// Edit a copy on its own, then recopy it from the latest template to overwrite it.
	if _, err := e.svc.UpdateAgentSkillMarkdown(ctx, a.ID, copyA.ID, "---\nname: pdf-tools\ndescription: Edited by me\n---\n"); err != nil {
		t.Fatal(err)
	}
	re, err := e.svc.RecopyAgentSkill(ctx, a.ID, copyA.ID)
	if err != nil || re.Description != "New description" || re.SkillMD != newMD {
		t.Fatalf("a recopy should overwrite everything: %+v %v", re, err)
	}
	// After the template is deleted the copy remains, its source is empty, and it cannot be recopied.
	if err := e.svc.DeleteSkillTemplate(ctx, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	gotA, err = e.svc.GetAgentSkill(ctx, a.ID, copyA.ID)
	if err != nil || gotA.SourceTemplateID != nil || len(gotA.Files) != 2 {
		t.Fatalf("after the template is deleted the copy should remain with an empty source: %+v %v", gotA, err)
	}
	_, err = e.svc.RecopyAgentSkill(ctx, a.ID, copyA.ID)
	wantCode(t, err, apperr.CodeTemplateDeleted)
}

func TestCLIsAndCredentials(t *testing.T) {
	e := newEnv(t)
	e.scope = store.PlatformScope()
	ctx := context.Background()
	a := e.agent(t)
	b := e.agent(t)

	v, err := e.svc.AddCLI(ctx, a.ID, "jq")
	if err != nil || v.HasTemplate {
		t.Fatalf("a CLI without a template is only a marker: %+v %v", v, err)
	}
	_, err = e.svc.PutCLICredential(ctx, a.ID, "jq", map[string]string{"x": "y"})
	wantCode(t, err, apperr.CodeValidationFailed)
	_, err = e.svc.PutCLICredential(ctx, a.ID, "lark-cli", map[string]string{"appId": "a", "appSecret": "s"})
	wantCode(t, err, apperr.CodeValidationFailed) // not in the available list
	// After adding it to the available list, a lark-cli credential can be entered: injected as a config file, with no
	// plaintext in the metadata and no risk warning.
	c := e.agent(t)
	if _, err := e.svc.AddCLI(ctx, c.ID, "lark-cli"); err != nil {
		t.Fatal(err)
	}
	lc, err := e.svc.PutCLICredential(ctx, c.ID, "lark-cli", map[string]string{"appId": "cli_a", "appSecret": "lark-secret"})
	if err != nil || lc.Status != "set" || !slices.Contains(lc.FilePaths, ".lark-cli/config.json") || len(lc.Warnings) != 0 ||
		strings.Join(lc.FieldNames, ",") != "appId,appSecret,brand" {
		t.Fatalf("entering the lark-cli credential failed: %+v %v", lc, err)
	}

	if _, err := e.svc.AddCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddCLI(ctx, b.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	cred, err := e.svc.PutCLICredential(ctx, a.ID, "gh", map[string]string{"token": "ghp_example"})
	if err != nil || cred.Status != "set" || cred.EnvNames[0] != "GH_TOKEN" {
		t.Fatalf("entering the gh credential failed: %+v %v", cred, err)
	}
	if _, err := e.svc.PutModelCredential(ctx, a.ID, map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret"}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.PutModelCredential(ctx, a.ID, map[string]string{"1BAD": "x"})
	wantCode(t, err, apperr.CodeValidationFailed)
	// Variables read by CLI templates cannot be used as model account credentials, and the field-level error points to
	// env.<variable name>; generic variables such as proxies are allowed as usual.
	for _, name := range []string{"GITHUB_TOKEN", "GH_HOST", "LARKSUITE_CLI_APP_SECRET", "LARK_CHANNEL"} {
		_, err = e.svc.PutModelCredential(ctx, b.ID, map[string]string{"ANTHROPIC_API_KEY": "k", name: "x"})
		ae, _ := apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 || ae.Fields[0].Field != "env."+name {
			t.Errorf("%s should be rejected as env.%s: %+v", name, name, ae)
		}
	}
	if _, err := e.svc.PutModelCredential(ctx, b.ID, map[string]string{"ANTHROPIC_API_KEY": "k", "HTTPS_PROXY": "http://127.0.0.1:7890"}); err != nil {
		t.Fatalf("proxy variables should be allowed: %v", err)
	}

	// The credential is stored as plaintext in the secret column.
	var stored bool
	_ = e.st.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_credentials WHERE agent_id = $1 AND cli_name = 'gh' AND instr(secret, CAST('ghp_example' AS BLOB)) > 0)`, a.ID).Scan(&stored)
	if !stored {
		t.Fatal("no plaintext credential in the secret column")
	}

	// The detail returns only metadata.
	d, err := e.svc.GetAgentDetail(ctx, a.ID)
	if err != nil || len(d.CLIs) != 2 || d.ModelCredential == nil || d.ModelCredential.EnvNames[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("unexpected detail: %+v %v", d, err)
	}
	// Removing a CLI also deletes its credential.
	if err := e.svc.RemoveCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.st.ListCredentials(ctx, a.ID)
	if len(rows) != 1 || rows[0].Purpose != store.PurposeModel {
		t.Fatalf("removing a CLI should delete its credential: %+v", rows)
	}
}

// Deleting a project: each of its Agents is handed to the executor for release (stop Pi, delete credential files) and
// the overview is told to remove it; the project name is validated by the naming rules.
func TestDeleteProjectReleasesAgents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var removed []uuid.UUID
	e.svc.AgentRemoved = func(id uuid.UUID) { removed = append(removed, id) }
	_, err := e.svc.CreateProject(ctx, "a/b")
	wantCode(t, err, apperr.CodeValidationFailed)
	p, err := e.svc.CreateProject(ctx, "Temporary")
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.svc.CreateAgent(ctx, store.ProjectScope(p.ID), "Alpha", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.CreateAgent(ctx, store.ProjectScope(p.ID), "Beta", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	keep, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "Platform", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	e.releaser.mu.Lock()
	released := slices.Clone(e.releaser.released)
	e.releaser.mu.Unlock()
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		if !slices.Contains(released, id) || !slices.Contains(removed, id) {
			t.Fatalf("%s should be released and reported removed: released=%v removed=%v", id, released, removed)
		}
	}
	if slices.Contains(released, keep.ID) {
		t.Fatal("Agents in other scopes should not be released")
	}
}
