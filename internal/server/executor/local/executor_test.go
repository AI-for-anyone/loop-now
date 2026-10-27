package local

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/loopctl/guide"
	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/clitemplate"
	"loop-now/internal/server/content"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/store"
	"loop-now/internal/testutil/fakepibin"
)

// ---- 测试替身 ----

type fakeSource struct {
	mu          sync.Mutex
	snaps       map[uuid.UUID]*agentconfig.Snapshot
	blobs       map[string][]byte
	files       map[uuid.UUID][]store.SyncFile
	synced      map[uuid.UUID]bool
	recorded    map[uuid.UUID]string
	missing     map[uuid.UUID]bool
	deleted     []uuid.UUID
	unavailable bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{snaps: map[uuid.UUID]*agentconfig.Snapshot{}, blobs: map[string][]byte{}, files: map[uuid.UUID][]store.SyncFile{},
		synced: map[uuid.UUID]bool{}, recorded: map[uuid.UUID]string{}, missing: map[uuid.UUID]bool{}}
}

func (f *fakeSource) blob(data string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sum := content.SHA256Hex([]byte(data))
	f.blobs[sum] = []byte(data)
	return sum
}

func (f *fakeSource) ExecutionSnapshot(_ context.Context, id uuid.UUID) (agentconfig.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return agentconfig.Snapshot{}, agentconfig.ErrCredentialUnavailable
	}
	s := *f.snaps[id]
	s.Sources = slices.Clone(s.Sources)
	now := time.Now()
	for i := range s.Sources {
		if f.synced[s.Sources[i].ID] {
			s.Sources[i].SyncedAt = &now
		}
	}
	switch {
	case f.missing[id]:
		s.Session = store.AgentSession{State: "missing", SessionFile: f.recorded[id], Epoch: 1}
	case f.recorded[id] != "":
		s.Session = store.AgentSession{State: "active", SessionFile: f.recorded[id], Epoch: 1}
	default:
		s.Session = store.AgentSession{State: "none", Epoch: 1}
	}
	return s, nil
}

func (f *fakeSource) ReadBlob(_ context.Context, sha string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.blobs[sha], nil
}

func (f *fakeSource) SourceFilesForSync(_ context.Context, id uuid.UUID) ([]store.SyncFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.files[id]), nil
}

func (f *fakeSource) MarkSourceSynced(_ context.Context, id uuid.UUID, processed []store.SyncFile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.synced[id] = true
	var keep []store.SyncFile
	for _, cur := range f.files[id] {
		done := slices.ContainsFunc(processed, func(p store.SyncFile) bool { return p.Path == cur.Path && p.UpdatedAt.Equal(cur.UpdatedAt) })
		switch {
		case done && cur.Pending == "delete":
			continue
		case done:
			cur.Pending = "none"
		}
		keep = append(keep, cur)
	}
	f.files[id] = keep
	return nil
}

func (f *fakeSource) RecordSessionFile(_ context.Context, id uuid.UUID, p string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded[id] = p
	return nil
}

func (f *fakeSource) MarkSessionMissing(_ context.Context, id uuid.UUID, _ *uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing[id] = true
	return nil
}

func (f *fakeSource) DeletedAgentIDs(context.Context) ([]uuid.UUID, error) { return f.deleted, nil }

func (f *fakeSource) update(id uuid.UUID, fn func(s *agentconfig.Snapshot)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.snaps[id])
	f.snaps[id].ConfigVersion++
}

type recSink struct {
	mu      sync.Mutex
	phases  []string
	running int
	text    strings.Builder
	tools   []string
	gotText chan struct{}
}

func newSink() *recSink { return &recSink{gotText: make(chan struct{}, 100)} }

func (s *recSink) Phase(p string) { s.mu.Lock(); s.phases = append(s.phases, p); s.mu.Unlock() }
func (s *recSink) Running(int64)  { s.mu.Lock(); s.running++; s.mu.Unlock() }
func (s *recSink) TextDelta(d string) {
	s.mu.Lock()
	s.text.WriteString(d)
	s.mu.Unlock()
	s.gotText <- struct{}{}
}
func (s *recSink) Tool(id, name, state string) {
	s.mu.Lock()
	s.tools = append(s.tools, name+":"+state)
	s.mu.Unlock()
}

type harness struct {
	t    *testing.T
	e    *Executor
	src  *fakeSource
	obs  *recObs
	root string
	env  map[string]string
}

func newHarness(t *testing.T, mutate func(o *Options), tmplDir string) *harness {
	t.Helper()
	root := t.TempDir()
	tmpls, err := clitemplate.Load(tmplDir)
	if err != nil {
		t.Fatal(err)
	}
	git, _ := exec.LookPath("git")
	h := &harness{t: t, src: newFakeSource(), obs: &recObs{}, root: root, env: map[string]string{}}
	o := Options{
		WorkRoot: root, PiBin: fakepibin.Path(t), GitBin: git, ExecBasePath: "/usr/bin:/bin:/usr/sbin:/sbin",
		Passthrough: []string{"FAKEPI_DUMP", "LANG"}, Getenv: func(k string) string { return h.env[k] }, Username: "tester",
		MaxProcesses: 4, IdleTimeout: time.Minute, StartTimeout: 10 * time.Second, StopGrace: 2 * time.Second,
		AbortTimeout: 5 * time.Second, CloneTimeout: 30 * time.Second, InstanceID: uuid.Must(uuid.NewV7()), Hostname: "test-host",
		Observer: h.obs,
	}
	if mutate != nil {
		mutate(&o)
	}
	h.e = New(o, h.src, tmpls, clitemplate.NewInstaller(root, time.Minute, nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = h.e.Shutdown(context.Background()) })
	return h
}

func (h *harness) agent(fn func(s *agentconfig.Snapshot)) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	s := &agentconfig.Snapshot{AgentID: id, ConfigVersion: 1, Provider: "anthropic", ModelID: "claude-x",
		ModelEnv: map[string]string{}, CLIPayloads: map[string]clitemplate.Payload{}}
	if fn != nil {
		fn(s)
	}
	h.src.mu.Lock()
	h.src.snaps[id] = s
	h.src.mu.Unlock()
	return id
}

func (h *harness) exec(id uuid.UUID, msg string) (executor.Result, *recSink) {
	h.t.Helper()
	sink := newSink()
	res, err := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.Must(uuid.NewV7()), Message: msg}, sink)
	if err != nil {
		h.t.Fatalf("Execute: %v", err)
	}
	return res, sink
}

func (h *harness) pid(id uuid.UUID) int {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	if p := h.e.procs[id]; p != nil {
		return p.client.Pid()
	}
	return 0
}

type dump struct {
	SystemPrompt string            `json:"systemPrompt"`
	Args         []string          `json:"args"`
	Env          map[string]string `json:"env"`
	Cwd          string            `json:"cwd"`
}

func readDump(t *testing.T, p string) dump {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var d dump
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func preinstall(t *testing.T, root, name, version, binary string) string {
	dir := filepath.Join(root, "tools", name, version, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, binary), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// ---- 6.3 环境与参数 ----

func TestEnvAndArgs(t *testing.T) {
	h := newHarness(t, nil, "")
	t.Setenv("LOOP_NOW_MASTER_KEY", "server-secret")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	h.env["LANG"] = "zh_CN.UTF-8"
	h.env["LOOP_NOW_MASTER_KEY"] = "server-secret"
	ghDir := preinstall(t, h.root, "gh", "2.100.0", "gh")
	preinstall(t, h.root, "lark-cli", "1.0.94", "lark-cli")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Prompt = "你是助手"
		s.ModelCredential, _ = modelcredential.APIKey("sk-test")
		s.CLIs = []string{"gh", "jq"}
		s.ModelEnv = map[string]string{"ANTHROPIC_API_KEY": "sk-test", "PATH": "/evil", "GITHUB_TOKEN": "leak", "GH_HOST": "evil.example", "HTTPS_PROXY": "http://127.0.0.1:7890"}
		s.CLIPayloads["gh"] = clitemplate.Payload{Env: map[string]string{"GH_TOKEN": "ghp_test"}}
	})
	res, _ := h.exec(id, "你好")
	if res.Outcome != executor.OutcomeCompleted || !strings.Contains(res.Text, "你好") {
		t.Fatalf("执行结果不对：%+v", res)
	}
	d := readDump(t, dumpPath)
	agentRoot := filepath.Join(h.root, "agents", id.String())
	want := map[string]string{
		"HOME": filepath.Join(agentRoot, "home"), "TMPDIR": filepath.Join(agentRoot, "tmp"), "PI_CODING_AGENT_DIR": filepath.Join(agentRoot, "pi"),
		"PI_OFFLINE": "1", "GH_TOKEN": "ghp_test", "LANG": "zh_CN.UTF-8", "USER": "tester",
	}
	for k, v := range want {
		if d.Env[k] != v {
			t.Errorf("环境变量 %s = %q，期望 %q", k, d.Env[k], v)
		}
	}
	for k := range d.Env {
		if strings.HasPrefix(k, "LOOP_NOW_") {
			t.Errorf("不应把服务端变量 %s 传给 Pi", k)
		}
	}
	// Legacy snapshot data cannot inject model or proxy variables into the process.
	for _, name := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "GH_HOST", "HTTPS_PROXY"} {
		if _, exists := d.Env[name]; exists {
			t.Errorf("model variable leaked: %s", name)
		}
	}
	authPath := filepath.Join(agentRoot, "pi", "auth.json")
	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}
	var auth map[string]map[string]string
	if err := json.Unmarshal(data, &auth); err != nil || auth["anthropic"]["key"] != "sk-test" {
		t.Fatal("private auth file missing credential", err)
	}
	for path, mode := range map[string]os.FileMode{authPath: 0600, filepath.Dir(authPath): 0700} {
		stat, err := os.Stat(path)
		if err != nil || stat.Mode().Perm() != mode {
			t.Fatal("invalid private credential file permissions", err)
		}
	}
	if !strings.HasPrefix(d.Env["PATH"], ghDir+":") || !strings.HasSuffix(d.Env["PATH"], "/usr/bin:/bin:/usr/sbin:/sbin") || strings.Contains(d.Env["PATH"], "lark-cli") {
		t.Errorf("PATH 顺序不对：%s", d.Env["PATH"])
	}
	if d.Cwd != filepath.Join(agentRoot, "workspace") && !strings.HasSuffix(d.Cwd, filepath.Join(id.String(), "workspace")) {
		t.Errorf("工作目录不对：%s", d.Cwd)
	}
	for _, f := range []string{"--no-approve", "--offline", "--no-skills"} {
		if !slices.Contains(d.Args, f) {
			t.Errorf("缺少参数 %s：%v", f, d.Args)
		}
	}
	if argValue(d.Args, "--provider") != "anthropic" || argValue(d.Args, "--model") != "claude-x" || argValue(d.Args, "--append-system-prompt") == "" || slices.Contains(d.Args, "--session") {
		t.Errorf("参数不对：%v", d.Args)
	}
}

// ---- 6.4 同步 ----

func TestSyncSkillsAndCredentialFiles(t *testing.T) {
	tmplDir := t.TempDir()
	// 当前平台要有安装包才算支持；工具已预装，不会下载。
	demo := `{"name":"demo","version":"1","injection":"files","fields":[{"name":"token","label":"t","type":"secret","required":true}],
	"credential":{"files":[{"path":".demo/cred","content":"{{token}}"}]},"binary":"demo",
	"install":{"` + clitemplate.Platform() + `":{"url":"https://example.invalid/demo.zip","sha256":"` + strings.Repeat("0", 64) + `","format":"zip","path":"demo"}}}`
	_ = os.WriteFile(filepath.Join(tmplDir, "demo.json"), []byte(demo), 0o600)
	h := newHarness(t, nil, tmplDir)
	preinstall(t, h.root, "demo", "1", "demo")
	md := h.src.blob("---\nname: pdf-tools\ndescription: d\n---\n")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Skills = []agentconfig.SkillSnapshot{{Name: "pdf-tools", Files: []store.SkillFile{{Path: "SKILL.md", SHA256: md}}}}
		s.CLIs = []string{"demo"}
		s.CLIPayloads["demo"] = clitemplate.Payload{Files: []clitemplate.PayloadFile{{Path: ".demo/cred", Content: []byte("secret-1")}}}
	})
	agentRoot := filepath.Join(h.root, "agents", id.String())
	_ = os.MkdirAll(filepath.Join(agentRoot, "skills", "old"), 0o700)
	_ = os.WriteFile(filepath.Join(agentRoot, "skills", "old", "SKILL.md"), []byte("x"), 0o600)

	h.exec(id, "hi")
	if b, _ := os.ReadFile(filepath.Join(agentRoot, "home", ".demo", "cred")); string(b) != "secret-1" {
		t.Fatalf("凭证文件未写入：%q", b)
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "skills", "old")); !os.IsNotExist(err) {
		t.Fatal("不在配置中的 Skill 目录应被删除")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "skills", "pdf-tools", "SKILL.md")); err != nil {
		t.Fatal("配置中的 Skill 应被写入")
	}
	// 从可用列表移除 demo：重启后凭证文件被删除。
	pid := h.pid(id)
	h.src.update(id, func(s *agentconfig.Snapshot) { s.CLIs = nil; delete(s.CLIPayloads, "demo") })
	h.exec(id, "hi")
	if h.pid(id) == pid {
		t.Fatal("配置变化后应重启进程")
	}
	if _, err := os.Stat(filepath.Join(agentRoot, "home", ".demo", "cred")); !os.IsNotExist(err) {
		t.Fatal("移除 CLI 后其凭证文件应被删除")
	}
}

// 5.5：用内置 lark-cli 模板渲染的凭证写入独立 HOME，权限收紧；Pi 环境中没有 lark-cli 的凭证变量；移除后清理。
func TestLarkCLICredentialFiles(t *testing.T) {
	h := newHarness(t, nil, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "lark-cli", "1.0.94", "lark-cli")
	builtin, err := clitemplate.Load("")
	if err != nil {
		t.Fatal(err)
	}
	lark, ok := builtin.Get("lark-cli")
	if !ok {
		t.Fatal("缺少内置 lark-cli 模板")
	}
	payload, _, err := lark.Render(map[string]string{"appId": "cli_test", "appSecret": "lark-secret-1"})
	if err != nil {
		t.Fatal(err)
	}
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.CLIs = []string{"lark-cli"}
		s.CLIPayloads["lark-cli"] = payload
	})
	h.exec(id, "hi")
	home := filepath.Join(h.root, "agents", id.String(), "home")
	mode := func(rel string) os.FileMode {
		fi, err := os.Stat(filepath.Join(home, rel))
		if err != nil {
			t.Fatalf("%s 不存在：%v", rel, err)
		}
		return fi.Mode().Perm()
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".lark-cli", "config.json")); !strings.Contains(string(b), `"appSecret": "lark-secret-1"`) || !strings.Contains(string(b), `"strictMode": "bot"`) {
		t.Fatalf("配置文件内容不对：%s", b)
	}
	if mode(".lark-cli") != 0o700 || mode(".lark-cli/config.json") != 0o600 {
		t.Fatalf("权限不对：目录 %v，文件 %v", mode(".lark-cli"), mode(".lark-cli/config.json"))
	}
	keyPath := "Library/Application Support/lark-cli/master.key.file"
	if runtime.GOOS == "darwin" {
		if b, _ := os.ReadFile(filepath.Join(home, keyPath)); len(b) != 32 || mode(keyPath) != 0o600 || mode("Library/Application Support/lark-cli") != 0o700 {
			t.Fatalf("macOS 主密钥文件不对：%d 字节，%v", len(b), mode(keyPath))
		}
	}
	d := readDump(t, dumpPath)
	if d.Env["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] != "1" || d.Env["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] != "1" || d.Env["LARKSUITE_CLI_REMOTE_META"] != "off" {
		t.Errorf("缺少关闭后台联网的变量：%v", d.Env)
	}
	for k, v := range d.Env {
		if strings.Contains(v, "lark-secret-1") || k == "LARKSUITE_CLI_APP_ID" || k == "LARKSUITE_CLI_APP_SECRET" {
			t.Errorf("凭证不应出现在 Pi 环境中：%s", k)
		}
	}
	// 删除凭证但保留 lark-cli：凭证文件被清理，固定变量仍按模板注入。
	h.src.update(id, func(s *agentconfig.Snapshot) { delete(s.CLIPayloads, "lark-cli") })
	h.exec(id, "hi")
	if _, err := os.Stat(filepath.Join(home, ".lark-cli", "config.json")); !os.IsNotExist(err) {
		t.Error("删除凭证后配置文件应被删除")
	}
	if d := readDump(t, dumpPath); d.Env["LARKSUITE_CLI_REMOTE_META"] != "off" {
		t.Errorf("没有凭证时也应注入固定变量：%v", d.Env)
	}
	// 从可用列表移除 lark-cli：固定变量不再注入。
	h.src.update(id, func(s *agentconfig.Snapshot) { s.CLIs = nil })
	h.exec(id, "hi")
	for _, rel := range []string{".lark-cli/config.json", keyPath} {
		if _, err := os.Stat(filepath.Join(home, rel)); !os.IsNotExist(err) {
			t.Errorf("移除 lark-cli 后 %s 应被删除", rel)
		}
	}
	if d := readDump(t, dumpPath); d.Env["LARKSUITE_CLI_REMOTE_META"] != "" {
		t.Errorf("移除 lark-cli 后不应再注入固定变量：%v", d.Env)
	}
}

func TestSyncFilesSourceRules(t *testing.T) {
	h := newHarness(t, nil, "")
	srcID, oldID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	t0 := time.Now()
	file := func(p, data, pending string) store.SyncFile {
		return store.SyncFile{Path: p, SHA256: h.src.blob(data), Pending: pending, UpdatedAt: t0}
	}
	h.src.files[srcID] = []store.SyncFile{file("a.md", "a1", "none"), file("c.md", "c1", "none"), file("d.md", "d1", "none"), file("e.md", "e1", "none")}
	h.src.files[oldID] = []store.SyncFile{file("x.md", "x", "none")}
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Sources = []store.FolderSource{{ID: srcID, Path: "docs", Kind: "files"}, {ID: oldID, Path: "old", Kind: "files"}}
	})
	ws := filepath.Join(h.root, "agents", id.String(), "workspace")
	h.exec(id, "hi")
	for _, f := range []string{"docs/a.md", "docs/c.md", "docs/d.md", "docs/e.md", "old/x.md"} {
		if _, err := os.Stat(filepath.Join(ws, f)); err != nil {
			t.Fatalf("首次同步应写入 %s", f)
		}
	}
	// Agent 改了 a、c，删了 e，自己建了 b。
	_ = os.WriteFile(filepath.Join(ws, "docs/a.md"), []byte("agent-a"), 0o644)
	_ = os.WriteFile(filepath.Join(ws, "docs/c.md"), []byte("agent-c"), 0o644)
	_ = os.Remove(filepath.Join(ws, "docs/e.md"))
	_ = os.WriteFile(filepath.Join(ws, "docs/b.md"), []byte("agent-b"), 0o644)
	// 人编辑了 a、上传了 b、删除了 d，并从配置中删掉了来源 old。
	changed := []store.SyncFile{file("a.md", "human-a", "write"), file("b.md", "human-b", "write"), file("c.md", "c1", "none"),
		file("d.md", "d1", "delete"), file("e.md", "e1", "none")}
	h.src.mu.Lock()
	h.src.files[srcID] = changed
	h.src.mu.Unlock()
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Sources = s.Sources[:1] })
	h.exec(id, "hi")

	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(ws, p)); return string(b) }
	if read("docs/a.md") != "human-a" || read("docs/b.md") != "human-b" {
		t.Errorf("人工修改应覆盖磁盘：a=%q b=%q", read("docs/a.md"), read("docs/b.md"))
	}
	if read("docs/c.md") != "agent-c" {
		t.Errorf("没被人改过的已有文件不应覆盖：%q", read("docs/c.md"))
	}
	if read("docs/e.md") != "e1" {
		t.Errorf("缺少的文件应补写：%q", read("docs/e.md"))
	}
	if _, err := os.Stat(filepath.Join(ws, "docs/d.md")); !os.IsNotExist(err) {
		t.Error("人删除的文件应从磁盘删除")
	}
	if _, err := os.Stat(filepath.Join(ws, "old/x.md")); err != nil {
		t.Error("配置中删掉的来源，磁盘目录应保留")
	}
	for _, f := range h.src.files[srcID] {
		if f.Pending != "none" {
			t.Errorf("同步后人工修改记录应清除：%+v", f)
		}
	}
}

func TestSyncRefusesSymlinkEscape(t *testing.T) {
	h := newHarness(t, nil, "")
	srcID := uuid.Must(uuid.NewV7())
	h.src.files[srcID] = []store.SyncFile{{Path: "a.md", SHA256: h.src.blob("x"), Pending: "none", UpdatedAt: time.Now()}}
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Sources = []store.FolderSource{{ID: srcID, Path: "docs", Kind: "files"}}
	})
	ws := filepath.Join(h.root, "agents", id.String(), "workspace")
	outside := t.TempDir()
	_ = os.MkdirAll(ws, 0o700)
	if err := os.Symlink(outside, filepath.Join(ws, "docs")); err != nil {
		t.Fatal(err)
	}
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrWorkspaceSyncFailed {
		t.Fatalf("经符号链接写到工作区之外应失败：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(outside, "a.md")); !os.IsNotExist(err) {
		t.Fatal("不应写到工作区之外")
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	work, bare := filepath.Join(dir, "work"), filepath.Join(dir, "repo.git")
	run := func(d string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	_ = os.MkdirAll(work, 0o755)
	run(work, "init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(work, "README.md"), []byte("hello"), 0o644)
	run(work, "add", ".")
	run(work, "commit", "-q", "-m", "init")
	run(dir, "clone", "-q", "--bare", work, bare)
	return bare
}

func TestCloneRepo(t *testing.T) {
	h := newHarness(t, nil, "")
	bare := gitRepo(t)
	srcID := uuid.Must(uuid.NewV7())
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.Sources = []store.FolderSource{{ID: srcID, Path: "app", Kind: "git", GitURL: "file:///nonexistent/repo.git", GitBranch: "main"}}
	})
	ws := filepath.Join(h.root, "agents", id.String(), "workspace")
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrWorkspaceSyncFailed {
		t.Fatalf("克隆失败应以 workspace_sync_failed 失败：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(ws, "app")); !os.IsNotExist(err) {
		t.Fatal("克隆失败后不应留下来源目录")
	}
	entries, _ := os.ReadDir(ws)
	if len(entries) != 0 {
		t.Fatalf("克隆失败后不应留下临时目录：%v", entries)
	}
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Sources[0].GitURL = "file://" + bare })
	if res, _ := h.exec(id, "hi"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("修正地址后应克隆成功：%+v", res)
	}
	_ = os.WriteFile(filepath.Join(ws, "app", "README.md"), []byte("agent change"), 0o644)
	h.src.update(id, func(s *agentconfig.Snapshot) { s.Prompt = "changed" })
	h.exec(id, "hi")
	if b, _ := os.ReadFile(filepath.Join(ws, "app", "README.md")); string(b) != "agent change" {
		t.Fatalf("已有仓库不应被重置：%q", b)
	}
}

// ---- 6.5 进程生命周期与会话 ----

func TestReuseIdleAndResume(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.IdleTimeout = 300 * time.Millisecond }, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(nil)
	h.exec(id, "one")
	pid := h.pid(id)
	h.exec(id, "two")
	if h.pid(id) != pid {
		t.Fatal("配置未变、未超时时应复用同一进程")
	}
	recorded := h.src.recorded[id]
	if recorded == "" {
		t.Fatal("会话文件写盘后应被记录")
	}
	time.Sleep(time.Second)
	if st := h.e.Process(id).State; st != executor.ProcessStopped {
		t.Fatalf("空闲超时后进程应退出：%s", st)
	}
	if res, _ := h.exec(id, "three"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("重新启动后应正常回复：%+v", res)
	}
	if argValue(readDump(t, dumpPath).Args, "--session") != recorded {
		t.Fatal("重新启动时应用记录的会话文件接续")
	}
}

func TestSessionMissing(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.exec(id, "one")
	recorded := h.src.recorded[id]
	_ = h.e.Release(context.Background(), id) // 停进程
	if err := os.Remove(recorded); err != nil {
		t.Fatal(err)
	}
	res, _ := h.exec(id, "two")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrSessionMissing || !h.src.missing[id] {
		t.Fatalf("记录的会话文件被删除后应判为丢失：%+v", res)
	}
	res, _ = h.exec(id, "three")
	if res.ErrorCode != executor.ErrSessionMissing {
		t.Fatalf("标记解除前后续消息也应失败：%+v", res)
	}
}

func TestFirstTurnRejectedIsNotMissing(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.IdleTimeout = 200 * time.Millisecond }, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	id := h.agent(nil)
	res, _ := h.exec(id, "!rejectprompt !nosession")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrPiError {
		t.Fatalf("首轮被拒应失败：%+v", res)
	}
	if h.src.recorded[id] != "" {
		t.Fatal("未写盘的会话文件不应被记录")
	}
	time.Sleep(700 * time.Millisecond)
	res, _ = h.exec(id, "again")
	if res.Outcome != executor.OutcomeCompleted || h.src.missing[id] || slices.Contains(readDump(t, dumpPath).Args, "--session") {
		t.Fatalf("应从新会话开始，不判为丢失：%+v missing=%v", res, h.src.missing[id])
	}
}

func TestCredentialUnavailable(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.src.unavailable = true
	res, _ := h.exec(id, "hi")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrCredentialUnavailable {
		t.Fatalf("凭证不可用时应失败：%+v", res)
	}
}

// ---- 6.6 进程数上限 ----

func TestEvictIdleAtLimit(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 1 }, "")
	a, b := h.agent(nil), h.agent(nil)
	h.exec(a, "hi")
	h.exec(b, "hi")
	if h.pid(a) != 0 || h.pid(b) == 0 {
		t.Fatal("到上限时应关闭最久未用的空闲进程")
	}
}

func TestWaitForSlotAndInterruptWhileWaiting(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.MaxProcesses = 1 }, "")
	a, b, c := h.agent(nil), h.agent(nil), h.agent(nil)
	var wg sync.WaitGroup
	var aDone, bDone time.Time
	sinkB := newSink()
	wg.Go(func() { h.exec(a, "!sleep=400ms 慢回复"); aDone = time.Now() })
	time.Sleep(200 * time.Millisecond)
	wg.Go(func() {
		res, err := h.e.Execute(context.Background(), executor.Request{AgentID: b, Message: "hi"}, sinkB)
		if err != nil || res.Outcome != executor.OutcomeCompleted {
			t.Errorf("B 应在等待后完成：%+v %v", res, err)
		}
		bDone = time.Now()
	})
	time.Sleep(200 * time.Millisecond)
	cRes := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: c, Message: "hi"}, newSink())
		cRes <- res
	}()
	time.Sleep(100 * time.Millisecond)
	_ = h.e.Interrupt(context.Background(), c, uuid.Nil)
	select {
	case res := <-cRes:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Errorf("等待名额时被打断应直接放弃：%+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Error("等待名额时被打断应立即返回")
	}
	wg.Wait()
	if !bDone.After(aDone) {
		t.Fatal("B 应在 A 结束后才开始")
	}
	if !slices.Contains(sinkB.phases, executor.PhaseWaitingSlot) {
		t.Fatalf("B 应报告等待名额：%v", sinkB.phases)
	}
}

// ---- 6.7 打断、异常与清理 ----

func TestInterruptKeepsProcess(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	sink := newSink()
	resCh := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, Message: "!sleep=2s !text=第一段_第二段_第三段"}, sink)
		resCh <- res
	}()
	<-sink.gotText
	pid := h.pid(id)
	_ = h.e.Interrupt(context.Background(), id, uuid.Nil)
	select {
	case res := <-resCh:
		if res.Outcome != executor.OutcomeInterrupted || res.Text == "" {
			t.Fatalf("打断后应为已打断并保留已产生的文字：%+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("打断后应很快结束")
	}
	if res, _ := h.exec(id, "next"); res.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
		t.Fatalf("打断后同一进程应继续处理下一条：%+v", res)
	}
}

func TestInterruptTimeoutKillsProcess(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.AbortTimeout = 500 * time.Millisecond }, "")
	id := h.agent(nil)
	sink := newSink()
	resCh := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, Message: "!sleep=10s !noabort"}, sink)
		resCh <- res
	}()
	<-sink.gotText
	pid := h.pid(id)
	_ = h.e.Interrupt(context.Background(), id, uuid.Nil)
	select {
	case res := <-resCh:
		if res.Outcome != executor.OutcomeInterrupted {
			t.Fatalf("打断超时后应记为已打断：%+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("打断超时后应强制结束进程")
	}
	if res, _ := h.exec(id, "next"); res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatalf("强制结束后应由新进程接着处理：%+v", res)
	}
}

func TestCrashThenRestart(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.exec(id, "first")
	res, _ := h.exec(id, "!crash")
	if res.Outcome != executor.OutcomeFailed || res.ErrorCode != executor.ErrPiExited || res.Text == "" {
		t.Fatalf("进程崩溃应以 pi_exited 失败并保留文字：%+v", res)
	}
	if res, _ := h.exec(id, "after"); res.Outcome != executor.OutcomeCompleted {
		t.Fatalf("崩溃后下一条消息应由新进程处理：%+v", res)
	}
}

func TestCleanupStale(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	d := h.e.dirs(id)
	_ = d.ensure()
	cmd := exec.Command(fakepibin.Path(t), "--mode", "rpc", "--session-dir", d.sessions)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, _ := cmd.StdinPipe()
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	b, _ := json.Marshal(pidRecord{PID: cmd.Process.Pid, InstanceID: uuid.NewString(), Hostname: "test-host", SessionDir: d.sessions})
	_ = os.WriteFile(d.pidFile, b, 0o600)
	deletedID := uuid.Must(uuid.NewV7())
	_ = os.MkdirAll(h.e.dirs(deletedID).home, 0o700)
	h.src.deleted = []uuid.UUID{deletedID}

	if err := h.e.CleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("遗留的 Pi 进程应被结束")
	}
	if _, err := os.Stat(d.pidFile); !os.IsNotExist(err) {
		t.Fatal("pi.pid 应被删除")
	}
	if _, err := os.Stat(h.e.dirs(deletedID).home); !os.IsNotExist(err) {
		t.Fatal("已删除 Agent 的独立 HOME 应被删除")
	}
}

func TestReleaseRemovesHome(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(nil)
	h.exec(id, "hi")
	d := h.e.dirs(id)
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.home); !os.IsNotExist(err) {
		t.Fatal("释放后独立 HOME 应被删除")
	}
	if _, err := os.Stat(d.workspace); err != nil {
		t.Fatal("释放后工作区应保留")
	}
	if h.e.Process(id).State != executor.ProcessStopped {
		t.Fatal("释放后进程应停止")
	}
}

// ---- Agent 令牌 ----

type fakeTokens struct {
	mu      sync.Mutex
	issued  map[uuid.UUID]string // 令牌 ID → 明文
	revoked map[uuid.UUID]bool
}

func (f *fakeTokens) IssueAgentToken(_ context.Context, agentID uuid.UUID) (uuid.UUID, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.Must(uuid.NewV7())
	f.issued[id] = "lna_" + id.String()
	return id, f.issued[id], nil
}

func (f *fakeTokens) RevokeAgentToken(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[id] = true
	return nil
}

// live 返回未作废的令牌明文。
func (f *fakeTokens) live() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for id, tok := range f.issued {
		if !f.revoked[id] {
			out = append(out, tok)
		}
	}
	return out
}

func TestAgentTokens(t *testing.T) {
	tokens := &fakeTokens{issued: map[uuid.UUID]string{}, revoked: map[uuid.UUID]bool{}}
	h := newHarness(t, func(o *Options) {
		o.Tokens, o.AgentURL = tokens, "http://127.0.0.1:8780/agent/v1"
		o.Passthrough = append(o.Passthrough, "LOOPCTL_URL")
	}, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	h.env["LOOPCTL_URL"] = "https://evil.example"
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.ModelEnv = map[string]string{"ANTHROPIC_API_KEY": "sk-test", "LOOPCTL_AGENT_TOKEN": "forged", "LOOPCTL_TOKEN": "forged"}
	})
	h.exec(id, "你好")
	d := readDump(t, dumpPath)
	live := tokens.live()
	if len(live) != 1 || d.Env["LOOPCTL_AGENT_TOKEN"] != live[0] || d.Env["LOOPCTL_AGENT_URL"] != "http://127.0.0.1:8780/agent/v1" {
		t.Fatalf("应注入本进程的令牌与接口地址：%v %v", live, d.Env)
	}
	for _, k := range []string{"LOOPCTL_TOKEN", "LOOPCTL_URL"} {
		if _, ok := d.Env[k]; ok {
			t.Errorf("LOOPCTL_ 开头的变量不能由凭证或透传设置：%s", k)
		}
	}

	// 配置变化导致 Pi 重启：旧令牌作废，新进程是新令牌。
	first := live[0]
	h.src.mu.Lock()
	h.src.snaps[id].ConfigVersion = 2
	h.src.mu.Unlock()
	h.exec(id, "再来")
	live = tokens.live()
	if len(live) != 1 || live[0] == first || readDump(t, dumpPath).Env["LOOPCTL_AGENT_TOKEN"] != live[0] {
		t.Fatalf("Pi 重启后旧令牌应作废、换成新令牌：%v", live)
	}

	// 进程被强制结束：令牌立即作废。
	if err := syscall.Kill(h.pid(id), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(tokens.live()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(tokens.live()); n != 0 {
		t.Fatalf("进程被强制结束后令牌应作废：%d", n)
	}

	// 释放 Agent（删除时）：进程停止，令牌作废。
	h.exec(id, "重新启动")
	if len(tokens.live()) != 1 {
		t.Fatal("重新启动后应有一个有效令牌")
	}
	if err := h.e.Release(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if n := len(tokens.live()); n != 0 {
		t.Fatalf("释放后令牌应作废：%d", n)
	}
}

// ---- loopctl 与内置 Skill ----

func TestLoopctlInPathAndBuiltinSkill(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "tools", "loopctl", "v1", "bin")
	h := newHarness(t, func(o *Options) { o.LoopctlBinDir = binDir }, "")
	dumpPath := filepath.Join(t.TempDir(), "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	preinstall(t, h.root, "gh", "2.100.0", "gh")
	id := h.agent(func(s *agentconfig.Snapshot) { s.CLIs = []string{"gh"} })
	h.exec(id, "你好")
	d := readDump(t, dumpPath)
	if first := strings.Split(d.Env["PATH"], ":")[0]; first != binDir {
		t.Fatalf("loopctl 应在 PATH 的最前面：%s", d.Env["PATH"])
	}
	skillDir := filepath.Join(h.root, "agents", id.String(), "skills", guide.SkillName)
	b, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil || !bytes.Equal(b, guide.Files()["SKILL.md"]) {
		t.Fatalf("内置 Skill 应写入同版本 loopctl 生成的内容：%v", err)
	}
	found := false
	for i, a := range d.Args {
		if a == "--skill" && i+1 < len(d.Args) && d.Args[i+1] == skillDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("Pi 应加载内置 Skill：%v", d.Args)
	}
}

// fakepi验证RPC分叉参数与目录隔离；不等同于真实Pi/模型验收。
func TestTaskForkUsesIndependentSessionAndWorkspace(t *testing.T) {
	h := newHarness(t, nil, "")
	normal := h.agent(nil)
	res, _ := h.exec(normal, "normal baseline")
	if res.Outcome != "completed" {
		t.Fatal(res)
	}
	if err := h.e.FreezeForFork(context.Background(), normal); err != nil {
		t.Fatal(err)
	}
	h.src.mu.Lock()
	baseline := h.src.recorded[normal]
	h.src.mu.Unlock()
	before, err := os.ReadFile(baseline)
	if err != nil {
		t.Fatal(err)
	}
	normalDirs := h.e.dirs(normal)
	if err = os.WriteFile(filepath.Join(normalDirs.workspace, "accumulated.txt"), []byte("normal only"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := h.agent(func(s *agentconfig.Snapshot) { s.ForkSessionFile = baseline })
	second := h.agent(func(s *agentconfig.Snapshot) { s.ForkSessionFile = baseline })
	for _, id := range []uuid.UUID{first, second} {
		res, _ = h.exec(id, "task independent")
		if res.Outcome != "completed" {
			t.Fatal(res)
		}
		if _, err = os.Stat(filepath.Join(h.e.dirs(id).workspace, "accumulated.txt")); !os.IsNotExist(err) {
			t.Fatal("normal accumulated file copied", err)
		}
	}
	h.src.mu.Lock()
	a, b := h.src.recorded[first], h.src.recorded[second]
	h.src.mu.Unlock()
	if a == b || a == baseline || b == baseline {
		t.Fatal("fork reused source session")
	}
	if err = h.e.FreezeForFork(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	fork, err := os.ReadFile(a)
	if err != nil || !bytes.Contains(fork, []byte("normal baseline")) || !bytes.Contains(fork, []byte("task independent")) {
		t.Fatal("fork lost baseline or new turn", err)
	}
	after, _ := os.ReadFile(baseline)
	if !bytes.Equal(before, after) {
		t.Fatal("fork modified normal session")
	}
	res, _ = h.exec(first, "task next node")
	if res.Outcome != "completed" {
		t.Fatal(res)
	}
	h.src.mu.Lock()
	again := h.src.recorded[first]
	h.src.mu.Unlock()
	if again != a {
		t.Fatal("same instance did not resume task session")
	}
}

func TestTaskProcessLogsUseConfigurationAgentAndRuntime(t *testing.T) {
	h := newHarness(t, nil, "")
	var buf bytes.Buffer
	h.e.log = slog.New(slog.NewJSONHandler(&buf, nil))
	agent, task := uuid.New(), uuid.New()
	runtime := h.agent(func(s *agentconfig.Snapshot) { s.ConfigurationAgentID = agent; s.TaskID = task })
	res, _ := h.exec(runtime, "hello")
	if res.Outcome != "completed" {
		t.Fatal(res)
	}
	if err := h.e.Release(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		msg, _ := row["msg"].(string)
		if msg != "Pi 进程启动" && msg != "Pi 进程停止" {
			continue
		}
		seen[msg] = true
		if row["agentId"] != agent.String() || row["runtimeId"] != runtime.String() || row["taskId"] != task.String() {
			t.Fatalf("log scope mismatch: %v", row)
		}
	}
	if !seen["Pi 进程启动"] || !seen["Pi 进程停止"] {
		t.Fatal("missing process logs", buf.String())
	}
}

func TestAuthCacheDamageRepairsWithoutReplacingSessionOrReplayingTurn(t *testing.T) {
	h := newHarness(t, nil, "")
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.ModelCredential, _ = modelcredential.APIKey("synthetic-original-account")
	})
	first, _ := h.exec(id, "initial")
	if first.Outcome != executor.OutcomeCompleted {
		t.Fatal("initial turn failed")
	}
	pid := h.pid(id)
	h.src.mu.Lock()
	session := h.src.recorded[id]
	h.src.mu.Unlock()
	if session == "" {
		t.Fatal("missing persisted session")
	}
	cache := filepath.Join(h.root, "agents", id.String(), "pi", "auth.json")
	for _, damage := range []string{"truncate", "remove"} {
		if damage == "truncate" {
			if err := os.WriteFile(cache, []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(cache); err != nil {
			t.Fatal(err)
		}
		result, sink := h.exec(id, "explicit-next-"+damage)
		if result.Outcome != executor.OutcomeCompleted || h.pid(id) != pid {
			t.Fatal("cache repair replaced process or failed")
		}
		h.src.mu.Lock()
		actualSession := h.src.recorded[id]
		h.src.mu.Unlock()
		if actualSession != session {
			t.Fatal("cache repair replaced session")
		}
		var auth map[string]map[string]string
		data, err := os.ReadFile(cache)
		if err != nil || json.Unmarshal(data, &auth) != nil || auth["anthropic"]["key"] != "synthetic-original-account" {
			t.Fatal("same-account cache was not rebuilt", err)
		}
		if sink.running != 1 {
			t.Fatal("cache repair replayed execution")
		}
	}
	data, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var entry struct {
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
			for _, content := range entry.Message.Content {
				counts[content.Text]++
			}
		}
	}
	for _, message := range []string{"initial", "explicit-next-truncate", "explicit-next-remove"} {
		if counts[message] != 1 {
			t.Fatal("session replayed or lost a user turn")
		}
	}
}

func TestKnownLegacyAuthUsesOnlyControlledExtensionAndPrivateFile(t *testing.T) {
	root := t.TempDir()
	entry := filepath.Join(root, "legacy-anthropic.mjs")
	if err := os.WriteFile(entry, []byte("export default ()=>{};"), 0600); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.LegacyAuthExtension = entry; o.PiPackageRoot = root }, "")
	dumpPath := filepath.Join(root, "dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	input := map[string]string{"ANTHROPIC_AUTH_TOKEN": "synthetic-A-bearer", "ANTHROPIC_OAUTH_TOKEN": "synthetic-A-oauth", "ANTHROPIC_API_KEY": "synthetic-A-key"}
	id := h.agent(func(s *agentconfig.Snapshot) {
		var err error
		s.ModelCredential, err = modelcredential.LegacySingleKey("anthropic", input)
		if err != nil {
			t.Fatal(err)
		}
	})
	result, _ := h.exec(id, "legacy fixture")
	if result.Outcome != executor.OutcomeCompleted {
		t.Fatal("legacy fixture did not execute")
	}
	dump := readDump(t, dumpPath)
	if argValue(dump.Args, "-e") != entry || !slices.Contains(dump.Args, "--no-extensions") {
		t.Fatal("legacy platform extension not explicitly selected")
	}
	for name := range input {
		if _, ok := dump.Env[name]; ok {
			t.Fatal("legacy secret entered process env")
		}
	}
	if dump.Env["LOOP_NOW_PI_PACKAGE_ROOT"] != root {
		t.Fatal("pinned installed source not selected")
	}
	data, err := os.ReadFile(filepath.Join(h.root, "agents", id.String(), "pi", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range input {
		if !bytes.Contains(data, []byte(value)) {
			t.Fatal("legacy auth file silently dropped a value")
		}
	}
}

func TestProjectPromptNextDispatchReplacesProcessKeepsSession(t *testing.T) {
	h := newHarness(t, nil, "")
	dumpPath := filepath.Join(t.TempDir(), "prompt-dump.json")
	h.env["FAKEPI_DUMP"] = dumpPath
	project := uuid.New()
	id := h.agent(func(s *agentconfig.Snapshot) {
		s.PromptLayers = &prompts.Layers{Format: prompts.FormatV1, ProjectID: &project, ProjectPrompt: "project-before", ProjectVersion: 1, AgentPrompt: "agent-own", AgentVersion: 1}
		s.Prompt, _ = s.PromptLayers.Compose()
	})
	sink := newSink()
	done := make(chan executor.Result, 1)
	go func() {
		res, _ := h.e.Execute(context.Background(), executor.Request{AgentID: id, RunID: uuid.New(), Message: "!sleep=300ms !text=first_reply"}, sink)
		done <- res
	}()
	select {
	case <-sink.gotText:
	case <-time.After(10 * time.Second):
		t.Fatal("no first text")
	}
	pid := h.pid(id)
	old := readDump(t, dumpPath)
	if !strings.Contains(old.SystemPrompt, "project-before") || !strings.Contains(old.SystemPrompt, "agent-own") {
		t.Fatal("fakePi did not read composed file")
	}
	h.src.mu.Lock()
	snapshot := h.src.snaps[id]
	version := snapshot.ConfigVersion
	next := prompts.Clone(snapshot.PromptLayers)
	next.ProjectPrompt = "project-after"
	next.ProjectVersion++
	snapshot.PromptLayers = next
	snapshot.Prompt, _ = next.Compose()
	h.src.mu.Unlock()
	if h.pid(id) != pid || readDump(t, dumpPath).SystemPrompt != old.SystemPrompt {
		t.Fatal("project update changed running reply")
	}
	if res := <-done; res.Outcome != executor.OutcomeCompleted {
		t.Fatal(res)
	}
	h.src.mu.Lock()
	session := h.src.recorded[id]
	h.src.mu.Unlock()
	res, _ := h.exec(id, "second")
	if res.Outcome != executor.OutcomeCompleted || h.pid(id) == pid {
		t.Fatal("project-only edit did not replace process", res)
	}
	got := readDump(t, dumpPath)
	if !strings.Contains(got.SystemPrompt, "project-after") || strings.Contains(got.SystemPrompt, "project-before") || argValue(got.Args, "--session") != session {
		t.Fatal("next dispatch lost Prompt or original session")
	}
	h.src.mu.Lock()
	defer h.src.mu.Unlock()
	if h.src.snaps[id].ConfigVersion != version || h.src.recorded[id] != session {
		t.Fatal("project edit mutated Agent version/session")
	}
}
