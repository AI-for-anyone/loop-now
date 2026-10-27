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
	"loop-now/internal/testutil/pgtest"
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
	scope    store.Scope // 默认项目
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st := pgtest.NewStore(t)
	dc, err := st.EnsureDefaults(context.Background(), "默认组织", "默认项目")
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
	a, err := e.svc.CreateAgent(context.Background(), e.scope, "助手-"+uuid.NewString()[:8], "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("期望错误码 %s，实际 %v", code, err)
	}
}

func TestCreateAndQueryAgent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	_, err := e.svc.CreateAgent(ctx, e.scope, a.Name, "anthropic", "claude-y", "提示")
	wantCode(t, err, apperr.CodeNameConflict)
	b, err := e.svc.CreateAgent(ctx, e.scope, "助手二", "anthropic", "claude-y", "提示")
	if err != nil || a.ID == b.ID || a.ConfigVersion != 1 {
		t.Fatalf("不同名的 Agent 应正常创建：%v %v", b, err)
	}
	// 改名为已有的名字被拒绝。
	dup := a.Name
	_, err = e.svc.UpdateAgent(ctx, b.ID, agentconfig.AgentPatch{Name: &dup})
	wantCode(t, err, apperr.CodeNameConflict)
	_, err = e.svc.CreateAgent(ctx, e.scope, "", "anthropic", "no-such", "")
	ae, _ := apperr.As(err)
	if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 2 {
		t.Fatalf("应同时指出名称与模型：%+v", ae)
	}
	items, err := e.st.ListAgents(ctx, e.scope)
	if err != nil || len(items) != 2 || items[0].Busy || items[0].SessionState != "none" {
		t.Fatalf("列表不对：%+v %v", items, err)
	}
}

// 名称会组成 Inbox 地址：创建与改名都按 naming 的规则校验，字段级错误指向 name，且不做任何修改。
func TestAgentNameRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	for _, name := range []string{"前端/助手", "助手#1", "@助手", " 助手", "助手\u3000", "a\tb", "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} {
		_, err := e.svc.CreateAgent(ctx, e.scope, name, "anthropic", "claude-x", "")
		ae, _ := apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 || ae.Fields[0].Field != "name" {
			t.Errorf("创建 %q 应以 name 字段错误拒绝：%+v", name, ae)
		}
		n := name
		_, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Name: &n})
		ae, _ = apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || ae.Fields[0].Field != "name" {
			t.Errorf("改名为 %q 应以 name 字段错误拒绝：%+v", name, ae)
		}
	}
	got, err := e.st.GetAgent(ctx, a.ID)
	if err != nil || got.Name != a.Name || got.ConfigVersion != a.ConfigVersion {
		t.Fatalf("被拒绝的改名不应改动 Agent：%+v %v", got, err)
	}
	// 中间的空格与 @ 是允许的。
	if _, err := e.svc.CreateAgent(ctx, e.scope, "dev assistant@前端", "anthropic", "claude-x", ""); err != nil {
		t.Fatalf("合法名称被拒绝：%v", err)
	}
}

func TestUpdateAgentBumpsVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.agent(t)
	name := "新名字"
	got, err := e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Name: &name})
	if err != nil || got.ConfigVersion != 1 {
		t.Fatalf("只改显示名称不应递增配置版本：%v %v", got.ConfigVersion, err)
	}
	prompt := "新的 Prompt"
	got, err = e.svc.UpdateAgent(ctx, a.ID, agentconfig.AgentPatch{Prompt: &prompt, ExpectedPromptVersion: func() *int64 { v := int64(1); return &v }()})
	if err != nil || got.ConfigVersion != 2 || got.Prompt != prompt {
		t.Fatalf("修改 Prompt 应递增配置版本：%+v %v", got, err)
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
	// 有排队消息时拒绝删除。
	runID := uuid.Must(uuid.NewV7())
	if _, err := e.st.Pool.Exec(ctx, `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status) VALUES ($1, $2, $3, 1, 1, 'queued')`,
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
	// 已软删除的 Agent 不占用名字。
	if _, err := e.svc.CreateAgent(ctx, e.scope, a.Name, "anthropic", "claude-x", ""); err != nil {
		t.Fatalf("已删除 Agent 的名字应可再用：%v", err)
	}
	if _, err := e.svc.GetAgentDetail(ctx, a.ID); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("删除后应按不存在处理：%v", err)
	}
	var creds, runs int
	_ = e.st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM agent_credentials WHERE agent_id = $1), (SELECT count(*) FROM runs WHERE agent_id = $1)`, a.ID).Scan(&creds, &runs)
	if creds != 0 || runs != 1 {
		t.Fatalf("应删除凭证密文、保留执行记录：creds=%d runs=%d", creds, runs)
	}
	if len(e.releaser.released) != 1 || e.releaser.released[0] != a.ID {
		t.Fatal("删除后应通知执行器释放")
	}
}

func skillZip(t *testing.T, name string, extra map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create(name + "/SKILL.md")
	_, _ = w.Write([]byte("---\nname: " + name + "\ndescription: 测试用 Skill\n---\n正文\n"))
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
		t.Fatalf("模板详情不对：%+v", tmpl)
	}
	_, err = e.svc.CreateSkillFromZip(ctx, skillZip(t, "pdf-tools", nil))
	wantCode(t, err, apperr.CodeNameConflict)
	md, err := e.svc.CreateSkillFromMarkdown(ctx, "---\nname: notes\ndescription: 记笔记\n---\n")
	if err != nil || len(md.Files) != 1 {
		t.Fatalf("只用 SKILL.md 新建模板失败：%v", err)
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

	// 编辑模板不影响已有副本，且保留其他文件。
	newMD := "---\nname: pdf-tools\ndescription: 新描述\n---\n新正文\n"
	edited, err := e.svc.UpdateSkillTemplateMarkdown(ctx, tmpl.ID, newMD)
	if err != nil || len(edited.Files) != 2 || edited.Description != "新描述" {
		t.Fatalf("编辑模板 SKILL.md 应保留其他文件：%+v %v", edited, err)
	}
	gotA, _ := e.svc.GetAgentSkill(ctx, a.ID, copyA.ID)
	if gotA.Description != "测试用 Skill" {
		t.Fatalf("编辑模板不应影响已有副本：%s", gotA.Description)
	}
	// 单独编辑副本，再用最新模板重新复制覆盖。
	if _, err := e.svc.UpdateAgentSkillMarkdown(ctx, a.ID, copyA.ID, "---\nname: pdf-tools\ndescription: 我改的\n---\n"); err != nil {
		t.Fatal(err)
	}
	re, err := e.svc.RecopyAgentSkill(ctx, a.ID, copyA.ID)
	if err != nil || re.Description != "新描述" || re.SkillMD != newMD {
		t.Fatalf("重新复制应整体覆盖：%+v %v", re, err)
	}
	// 删除模板后副本仍在，来源为空，不能重新复制。
	if err := e.svc.DeleteSkillTemplate(ctx, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	gotA, err = e.svc.GetAgentSkill(ctx, a.ID, copyA.ID)
	if err != nil || gotA.SourceTemplateID != nil || len(gotA.Files) != 2 {
		t.Fatalf("模板删除后副本应保留且来源为空：%+v %v", gotA, err)
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
		t.Fatalf("没有模板的 CLI 只作标记：%+v %v", v, err)
	}
	_, err = e.svc.PutCLICredential(ctx, a.ID, "jq", map[string]string{"x": "y"})
	wantCode(t, err, apperr.CodeValidationFailed)
	_, err = e.svc.PutCLICredential(ctx, a.ID, "lark-cli", map[string]string{"appId": "a", "appSecret": "s"})
	wantCode(t, err, apperr.CodeValidationFailed) // 不在可用列表中
	// 加入可用列表后可以录入 lark-cli 凭证：以配置文件注入，元数据中没有明文，也没有风险提示。
	c := e.agent(t)
	if _, err := e.svc.AddCLI(ctx, c.ID, "lark-cli"); err != nil {
		t.Fatal(err)
	}
	lc, err := e.svc.PutCLICredential(ctx, c.ID, "lark-cli", map[string]string{"appId": "cli_a", "appSecret": "lark-secret"})
	if err != nil || lc.Status != "set" || !slices.Contains(lc.FilePaths, ".lark-cli/config.json") || len(lc.Warnings) != 0 ||
		strings.Join(lc.FieldNames, ",") != "appId,appSecret,brand" {
		t.Fatalf("录入 lark-cli 凭证失败：%+v %v", lc, err)
	}

	if _, err := e.svc.AddCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddCLI(ctx, b.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	cred, err := e.svc.PutCLICredential(ctx, a.ID, "gh", map[string]string{"token": "ghp_example"})
	if err != nil || cred.Status != "set" || cred.EnvNames[0] != "GH_TOKEN" {
		t.Fatalf("录入 gh 凭证失败：%+v %v", cred, err)
	}
	if _, err := e.svc.PutModelCredential(ctx, a.ID, map[string]string{"ANTHROPIC_API_KEY": "sk-ant-secret"}); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.PutModelCredential(ctx, a.ID, map[string]string{"1BAD": "x"})
	wantCode(t, err, apperr.CodeValidationFailed)
	// CLI 模板读取的变量不能作为模型账号凭证，字段级错误指向 env.<变量名>；代理等通用变量照常允许。
	for _, name := range []string{"GITHUB_TOKEN", "GH_HOST", "LARKSUITE_CLI_APP_SECRET", "LARK_CHANNEL"} {
		_, err = e.svc.PutModelCredential(ctx, b.ID, map[string]string{"ANTHROPIC_API_KEY": "k", name: "x"})
		ae, _ := apperr.As(err)
		if ae == nil || ae.Code != apperr.CodeValidationFailed || len(ae.Fields) != 1 || ae.Fields[0].Field != "env."+name {
			t.Errorf("%s 应以 env.%s 拒绝：%+v", name, name, ae)
		}
	}
	if _, err := e.svc.PutModelCredential(ctx, b.ID, map[string]string{"ANTHROPIC_API_KEY": "k", "HTTPS_PROXY": "http://127.0.0.1:7890"}); err != nil {
		t.Fatalf("代理变量应允许录入：%v", err)
	}

	// 凭证以明文存入 secret 列。
	var stored bool
	_ = e.st.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_credentials WHERE agent_id = $1 AND cli_name = 'gh' AND position('ghp_example'::bytea in secret) > 0)`, a.ID).Scan(&stored)
	if !stored {
		t.Fatal("secret 列中没有明文凭证")
	}

	// 详情只返回元数据。
	d, err := e.svc.GetAgentDetail(ctx, a.ID)
	if err != nil || len(d.CLIs) != 2 || d.ModelCredential == nil || d.ModelCredential.EnvNames[0] != "ANTHROPIC_API_KEY" {
		t.Fatalf("详情不对：%+v %v", d, err)
	}
	// 移除 CLI 时一并删除凭证。
	if err := e.svc.RemoveCLI(ctx, a.ID, "gh"); err != nil {
		t.Fatal(err)
	}
	rows, _ := e.st.ListCredentials(ctx, a.ID)
	if len(rows) != 1 || rows[0].Purpose != store.PurposeModel {
		t.Fatalf("移除 CLI 后其凭证应被删除：%+v", rows)
	}
}

// 删除项目：其中每个 Agent 都交给执行器释放（停止 Pi、删除凭证文件），并通知概览移除；项目名按命名规则校验。
func TestDeleteProjectReleasesAgents(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var removed []uuid.UUID
	e.svc.AgentRemoved = func(id uuid.UUID) { removed = append(removed, id) }
	_, err := e.svc.CreateProject(ctx, "a/b")
	wantCode(t, err, apperr.CodeValidationFailed)
	p, err := e.svc.CreateProject(ctx, "临时")
	if err != nil {
		t.Fatal(err)
	}
	a, err := e.svc.CreateAgent(ctx, store.ProjectScope(p.ID), "甲", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.CreateAgent(ctx, store.ProjectScope(p.ID), "乙", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	keep, err := e.svc.CreateAgent(ctx, store.PlatformScope(), "平台", "anthropic", "claude-x", "")
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
			t.Fatalf("%s 应被释放并通知移除：released=%v removed=%v", id, released, removed)
		}
	}
	if slices.Contains(released, keep.ID) {
		t.Fatal("其他范围的 Agent 不应被释放")
	}
}
