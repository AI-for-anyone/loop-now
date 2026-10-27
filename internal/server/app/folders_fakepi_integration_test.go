package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
)

type piDump struct {
	Args         []string `json:"args"`
	Cwd          string   `json:"cwd"`
	SystemPrompt string   `json:"systemPrompt"`
}

// real 解析软链接（macOS 的临时目录 /var 指向 /private/var，Pi 报告的工作目录是解析后的路径）。
func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readDump(t *testing.T, path string) piDump {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d piDump
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

// Pi 启动时，追加的系统提示带上项目（平台 Agent 为全局）上下文入口与两个文件夹的绝对路径；Pi 的工作目录就是 Agent 文件夹；
// 入口修改要等 Pi 重启才生效；平台自有目录中的入口文件在启动时删除。
func TestFolderContextInjectionActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	dump := filepath.Join(t.TempDir(), "fake-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
	w.cfg.ContextDir = filepath.Join(t.TempDir(), "loop_now")
	s := w.start(nil)
	defer s.stop()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	path, agent := s.createAgent("Ctx")
	s.must(200, "PUT", "/projects/"+project.String()+"/context-folder/content?path=AGENTS.md", []byte("项目约定 v1"))
	s.must(200, "PUT", path+"/folder/content?path=AGENTS.md", []byte("自己的笔记"))
	root := filepath.Join(w.cfg.WorkRoot, "agents", agent.ID.String())
	for _, p := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "pi", "CLAUDE.md")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("不该被读入"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=ok"})
	s.waitIdle(path)
	workspace := filepath.Join(root, "workspace")
	projectDir := filepath.Join(w.cfg.ContextDir, "projects", project.String())
	d := readDump(t, dump)
	if d.Cwd != real(t, workspace) || !slices.Contains(d.Args, "--append-system-prompt") {
		t.Fatalf("Pi 的工作目录应是 Agent 文件夹，并追加系统提示：%s %v", d.Cwd, d.Args)
	}
	for _, want := range []string{"项目约定 v1", projectDir, workspace, filepath.Join(projectDir, "AGENTS.md"), "你是测试助手"} {
		if !strings.Contains(d.SystemPrompt, want) {
			t.Fatalf("追加的系统提示应含 %q：%s", want, d.SystemPrompt)
		}
	}
	if strings.Contains(d.SystemPrompt, "自己的笔记") {
		t.Fatal("自己的入口由 Pi 从工作目录读取，不应重复放进追加的提示")
	}
	for _, p := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "pi", "CLAUDE.md")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("平台目录中的入口文件应在启动时删除：%s", p)
		}
	}
	// 入口修改不触发重启：复用中的 Pi 仍是旧提示。
	s.must(200, "PUT", "/projects/"+project.String()+"/context-folder/content?path=AGENTS.md", []byte("项目约定 v2"))
	if err := os.Remove(dump); err != nil {
		t.Fatal(err)
	}
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=again"})
	s.waitIdle(path)
	if _, err := os.Stat(dump); !os.IsNotExist(err) {
		t.Fatal("入口修改不应让 Pi 重启")
	}
	// 配置变化导致重启后读到新入口。
	s.must(200, "PATCH", path, map[string]any{"prompt": "新的提示", "expectedPromptVersion": agent.PromptVersion})
	s.must(202, "POST", path+"/messages", map[string]any{"text": "!text=third"})
	s.waitIdle(path)
	if d = readDump(t, dump); !strings.Contains(d.SystemPrompt, "项目约定 v2") || !strings.Contains(d.SystemPrompt, "新的提示") {
		t.Fatalf("重启后应读到新入口：%s", d.SystemPrompt)
	}

	// 平台 Agent：全局上下文文件夹，看不到项目的。
	ppath, _ := s.createPlatformAgent("PlatformCtx")
	s.must(200, "PUT", ppath+"/model-credential", map[string]any{"provider": "anthropic", "key": "sk-test"})
	s.must(200, "PUT", "/context-folder/content?path=AGENTS.md", []byte("全局约定"))
	s.must(202, "POST", ppath+"/messages", map[string]any{"text": "!text=ok"})
	s.waitIdle(ppath)
	d = readDump(t, dump)
	if !strings.Contains(d.SystemPrompt, "全局约定") || !strings.Contains(d.SystemPrompt, filepath.Join(w.cfg.ContextDir, "global")) || strings.Contains(d.SystemPrompt, "项目约定") {
		t.Fatalf("平台 Agent 只看到全局上下文：%s", d.SystemPrompt)
	}
}

// 新任务（快照 v9）开始时复制基础 Agent 的整个文件夹（APFS 克隆）：实例在副本中工作，改动不影响原文件夹；不再冻结文件夹来源。
func TestTaskCopiesAgentFolderActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	dump := filepath.Join(t.TempDir(), "fake-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
	logs := &syncBuffer{}
	w.logTo = logs
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	_, manager := s.createAgent("Manager")
	wpath, worker := s.createAgent("Worker")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	s.must(200, "PUT", wpath+"/folder/content?path=AGENTS.md", []byte("Worker 的笔记"))
	s.must(200, "PUT", wpath+"/folder/content?path=node_modules/pkg/index.js", []byte("module.exports = 1"))
	base := filepath.Join(w.cfg.WorkRoot, "agents", worker.ID.String(), "workspace")
	if err := os.Symlink("node_modules/pkg", filepath.Join(base, "pkg")); err != nil {
		t.Fatal(err)
	}
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "copy", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=30s !noabort !text=working", AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	task := decode[taskmodel.Detail](t, raw)
	runtime := task.Nodes[0].RuntimeID
	waitFor(t, "节点进入运行", func() bool {
		var n int
		return s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND status='running'`, runtime).Scan(&n) == nil && n == 1
	})
	copyDir := filepath.Join(w.cfg.WorkRoot, "agents", runtime.String(), "workspace")
	if b, err := os.ReadFile(filepath.Join(copyDir, "node_modules/pkg/index.js")); err != nil || string(b) != "module.exports = 1" {
		t.Fatalf("实例工作区应是基础文件夹的副本：%q %v", b, err)
	}
	if l, _ := os.Readlink(filepath.Join(copyDir, "pkg")); l != "node_modules/pkg" {
		t.Fatalf("软链接应原样复制：%q", l)
	}
	d := readDump(t, dump)
	if d.Cwd != real(t, copyDir) || !strings.Contains(d.SystemPrompt, "任务结束后回收") || !strings.Contains(d.SystemPrompt, filepath.Join("projects", project.String())) {
		t.Fatalf("任务实例在副本中工作，并看到项目上下文：%s %s", d.Cwd, d.SystemPrompt)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "AGENTS.md"), []byte("任务里改了"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(base, "AGENTS.md")); string(b) != "Worker 的笔记" {
		t.Fatal("改副本不应影响基础 Agent 的文件夹")
	}
	var snapshot []byte
	var sources string
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT snapshot, sources::text FROM task_instances WHERE id=$1`, runtime).Scan(&snapshot, &sources); err != nil {
		t.Fatal(err)
	}
	var env struct {
		SchemaVersion int `json:"schemaVersion"`
	}
	if err := json.Unmarshal(snapshot, &env); err != nil || env.SchemaVersion != 9 || sources != "[]" {
		t.Fatalf("新任务应是快照 v9、没有冻结来源：%d %s %v", env.SchemaVersion, sources, err)
	}
	if !strings.Contains(logs.String(), "任务工作目录已复制") || !strings.Contains(logs.String(), "clonefile") {
		t.Fatalf("应记录复制方式（APFS 克隆）：%s", logs.String())
	}
}
