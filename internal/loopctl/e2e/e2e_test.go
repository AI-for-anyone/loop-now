package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
)

// 9.1 Agent 身份：服务端为 Pi 注入地址与令牌；Agent 用 loopctl 查询身份、列出、读取、回复、主动发信。
func TestAgentIdentity(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	dev := s.createAgent("开发助手")
	reviewer := s.createAgent("审查助手")

	// 控制台给开发助手发 Inbox 消息：Agent 被唤醒，Pi 启动时注入 Agent 令牌。
	sent := s.console(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/开发助手", "text": "请看一下构建"})
	s.waitIdle(dev)
	ag := newLoopctl(t, w.agentEnv())

	who := ag.run("whoami")
	if d := who.ok(t); d["kind"] != "agent" || d["address"] != "默认组织/默认项目/开发助手" || who.Meta.Identity != "agent" {
		t.Fatalf("%s", who.Stdout)
	}

	// 列出摘要两次都不改变已读状态。
	for i := 0; i < 2; i++ {
		msgs := ag.run("inbox", "list", "--unread").ok(t)["messages"].([]any)
		if len(msgs) != 1 || msgs[0].(map[string]any)["id"] != sent["id"] {
			t.Fatalf("第 %d 次列出未读：%v", i+1, msgs)
		}
	}
	// 读取后标为已读；重复读取结果相同、已读时间不变。
	first := ag.run("inbox", "read", sent["id"].(string)).ok(t)
	if first["text"] != "请看一下构建" || first["from"].(map[string]any)["address"] != "@console" || first["readAt"] == nil {
		t.Fatalf("%v", first)
	}
	if again := ag.run("inbox", "read", sent["id"].(string)).ok(t); again["readAt"] != first["readAt"] {
		t.Fatalf("重复读取时已读时间应不变：%v → %v", first["readAt"], again["readAt"])
	}
	if msgs := ag.run("inbox", "list", "--unread").ok(t)["messages"].([]any); len(msgs) != 0 {
		t.Fatalf("读取后不应再是未读：%v", msgs)
	}
	if msgs := ag.run("inbox", "list").ok(t)["messages"].([]any); len(msgs) != 1 || msgs[0].(map[string]any)["readAt"] == nil {
		t.Fatalf("默认列出全部，并标出已读：%v", msgs)
	}

	// 回复留在原线索中，收件人是控制台操作者。
	ag.stdin = "构建正常\n"
	reply := ag.run("inbox", "reply", sent["id"].(string), "--text-file", "-").ok(t)
	if reply["threadId"] != sent["threadId"] || reply["to"].(map[string]any)["kind"] != "console" || reply["whenBusy"] != nil {
		t.Fatalf("%v", reply)
	}
	consoleInbox := s.console(200, "GET", "/inbox/messages?unread=true", nil)["messages"].([]any)
	if len(consoleInbox) != 1 || consoleInbox[0].(map[string]any)["id"] != reply["id"] {
		t.Fatalf("控制台应收到回复：%v", consoleInbox)
	}

	// 主动发给控制台操作者：开启新线索。
	ag.stdin = ""
	toConsole := ag.run("inbox", "send", "@console", "--text", "构建完成").ok(t)
	if toConsole["threadId"] == sent["threadId"] || toConsole["whenBusy"] != nil {
		t.Fatalf("%v", toConsole)
	}
	// 发给另一个 Agent：不指定时排队，指定时打断。
	if m := ag.run("inbox", "send", "默认项目/审查助手", "--text", "请审查").ok(t); m["whenBusy"] != "queue" {
		t.Fatalf("%v", m)
	}
	if m := ag.run("inbox", "send", "默认项目/审查助手", "--when-busy", "interrupt", "--text", "加急").ok(t); m["whenBusy"] != "interrupt" {
		t.Fatalf("%v", m)
	}
	s.waitIdle(reviewer)
	// 地址解析不到，以及带 #任务ID 的地址（第一版不支持）。
	ag.run("inbox", "send", "默认项目/不存在", "--text", "x").fails(t, "not_found", 7)
	ag.run("inbox", "send", "默认项目/审查助手#0192f1a2", "--text", "x").fails(t, "not_found", 7)
	// Agent 身份不能用管理命令，也不能按线索查看。
	ag.run("agent", "list", "--platform").fails(t, "command_not_allowed", 2)
	ag.run("inbox", "threads").fails(t, "command_not_allowed", 2)
}

// 9.2 外部身份经 TLS 对外端口管理 Agent、录入凭证、管理 Skill、查看状态与历史、确认重开会话、与 Agent 往来；
// 9.3 每个写操作都能按 loopctl 输出的请求 ID 找到审计记录，操作者是该外部身份。
func TestExternalIdentity(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	identity, _, secret := s.issueToken("", "ci-bot", nil)

	ext := newLoopctl(t, nil)
	ext.stdin = secret + "\n"
	cfg := ext.run("config", "set", "default", "--server", s.ext, "--ca-file", w.caFile, "--token-stdin")
	if cfg.Code != 0 || strings.Contains(cfg.Stdout, secret) {
		t.Fatalf("%s", cfg.Stdout)
	}
	if info, _ := os.Stat(filepath.Join(ext.home, ".config", "loopctl", "config.json")); info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限 %o", info.Mode().Perm())
	}
	ext.stdin = ""
	if d := ext.run("whoami").ok(t); d["kind"] != "external" || d["address"] != "@ci-bot" {
		t.Fatalf("%v", d)
	}

	var writes []string // 写操作的请求 ID
	write := func(args ...string) map[string]any {
		t.Helper()
		r := ext.run(args...)
		d := r.ok(t)
		if r.Meta.RequestID == "" {
			t.Fatalf("%v 没有请求 ID", args)
		}
		writes = append(writes, r.Meta.RequestID)
		return d
	}

	provider, model := cliKeyProvider(t, ext)
	ext.run("agent", "list").fails(t, "missing_argument", 2)
	ext.stdin = "你是外部创建的助手"
	created := write("agent", "create", "--project", "默认项目", "--name", "外部助手", "--provider", provider, "--model", model, "--prompt-file", "-")
	ext.stdin = ""
	agentID := created["id"].(string)
	const addr = "默认项目/外部助手"
	if created["address"] != "默认组织/默认项目/外部助手" || created["scope"] != "project" {
		t.Fatalf("%v", created)
	}
	if list := ext.run("agent", "list", "--project", "默认项目").ok(t)["agents"].([]any); len(list) != 1 {
		t.Fatalf("%v", list)
	}
	// 用地址引用 Agent（GET /agents?address=）。
	if d := ext.run("agent", "get", addr).ok(t); d["id"] != agentID {
		t.Fatalf("%v", d)
	}

	// 上传资料并下载回来。
	src := write("agent", "source", "add", addr, "--path", "docs", "--files")["id"].(string)
	local := filepath.Join(t.TempDir(), "a.md")
	os.WriteFile(local, []byte("# 说明\n资料内容\n"), 0o600)
	put := write("agent", "file", "put", addr, src, "notes/a.md", "--from", local)
	out := filepath.Join(t.TempDir(), "back.md")
	got := ext.run("agent", "file", "get", addr, src, "notes/a.md", "--out", out).ok(t)
	if b, _ := os.ReadFile(out); string(b) != "# 说明\n资料内容\n" || got["sha256"] != put["sha256"] {
		t.Fatalf("下载的内容与上传的不同：%v %v", got, put)
	}

	// 项目账号只写不读，唯一账号由项目 Agent 自动取用。
	ext.stdin = `{"type":"api_key","key":"model-secret-value"}`
	cred := ext.run("project", "model-account", "create", "默认项目", "--provider", provider, "--name", "外部助手账号", "--from-file", "-")
	ext.stdin = ""
	if cred.Code != 0 || strings.Contains(cred.Stdout, "model-secret-value") {
		t.Fatalf("%s", cred.Stdout)
	}
	writes = append(writes, cred.Meta.RequestID)
	detail := ext.run("agent", "get", agentID)
	if detail.Code != 0 || strings.Contains(detail.Stdout, "model-secret-value") || !strings.Contains(detail.Stdout, provider) {
		t.Fatalf("Agent 详情只应有凭证的元数据：%s", detail.Stdout)
	}

	// 上传 Skill 并复制给 Agent。
	skillDir := t.TempDir()
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: e2e-review\ndescription: 端到端测试用的 Skill\n---\n正文\n"), 0o644)
	skill := write("skill", "create", "--dir", skillDir)
	copied := write("agent", "skill", "add", addr, "--template", skill["id"].(string))
	if copied["name"] != "e2e-review" {
		t.Fatalf("%v", copied)
	}

	// 状态、确认重开会话。
	if st := ext.run("agent", "status", addr).ok(t); st["busy"] != false {
		t.Fatalf("%v", st)
	}
	write("agent", "session", "reset", addr, "--yes")

	// 给 Agent 发 Inbox 消息；Agent 被唤醒后用自己的令牌回复；外部身份按线索查看往来。
	sent := write("inbox", "send", addr, "--text", "请检查构建")
	s.waitIdle(agentID)
	ag := newLoopctl(t, w.agentEnv())
	ag.run("inbox", "read", sent["id"].(string)).ok(t)
	ag.run("inbox", "reply", sent["id"].(string), "--text", "构建正常").ok(t)
	threads := ext.run("inbox", "threads").ok(t)
	if threads["unreadTotal"] != float64(1) {
		t.Fatalf("%v", threads)
	}
	thread := ext.run("inbox", "thread", sent["threadId"].(string)).ok(t)
	msgs := thread["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["text"] != "构建正常" || msgs[1].(map[string]any)["readAt"] != nil {
		t.Fatalf("线索中应有 Agent 的回复且仍为未读：%v", msgs)
	}
	if unread := ext.run("inbox", "list", "--unread").ok(t)["messages"].([]any); len(unread) != 1 {
		t.Fatalf("查看线索不应改变已读状态：%v", unread)
	}

	// 对话历史、运行记录、用量、异常与健康状态。
	history := ext.run("agent", "history", addr, "--limit", "10").ok(t)["messages"].([]any)
	if len(history) == 0 {
		t.Fatal("提醒那一轮应出现在对话历史中")
	}
	runID := history[len(history)-1].(map[string]any)["run"].(map[string]any)["id"].(string)
	if tl := ext.run("agent", "timeline", addr, runID).ok(t); len(tl["entries"].([]any)) == 0 {
		t.Fatalf("%v", tl)
	}
	ext.run("usage", "list", "--project", "默认项目").ok(t)
	ext.run("anomaly", "list", "--platform").ok(t)
	h := ext.run("health")
	if d := h.ok(t); d["ok"] != true || strings.Contains(h.Stdout, `"error"`) {
		t.Fatalf("对外端口的健康状态不应带错误字段：%s", h.Stdout)
	}
	// 只限控制台的操作在对外端口上被拒绝；回环上的控制台接口仍免登录。
	s.console(200, "GET", "/context", nil)

	// 9.3 审计：每个写操作都能按请求 ID 找到记录，操作者是这个外部身份；记录中没有令牌与凭证。
	for _, id := range writes {
		var kind, actor string
		var status int
		err := s.a.Store.Pool.QueryRow(context.Background(),
			`SELECT actor_kind, actor_id::text, status FROM audit_log WHERE request_id = $1`, id).Scan(&kind, &actor, &status)
		if err != nil || kind != "external" || actor != identity || status >= 300 {
			t.Errorf("请求 %s 的审计记录：%s %s %d %v", id, kind, actor, status, err)
		}
	}
	var leaked int
	s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE audit_log::text LIKE '%' || $1 || '%' OR audit_log::text LIKE '%model-secret-value%'`,
		secret[strings.LastIndex(secret, "_")+1:]).Scan(&leaked)
	if leaked != 0 {
		t.Errorf("审计记录中出现了令牌或凭证")
	}
}

// 10.1 服务端在命令执行中途停止：读请求按配置重试后返回退出码 4，写请求不重试；服务端恢复后照常执行。
// 10.3 外部令牌被吊销或过期后返回退出码 5，同一身份的其他令牌不受影响。
func TestExternalRecovery(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	identity, revokeID, revoked := s.issueToken("", "ci-bot", nil)
	_, _, keep := s.issueToken(identity, "", nil)
	_, _, short := s.issueToken(identity, "", map[string]any{"expiresAt": time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339)})
	conn := func(token string) *loopctl {
		return newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile,
			"LOOPCTL_RETRY_INITIAL": "20ms", "LOOPCTL_TIMEOUT": "5s"})
	}
	ext := conn(keep)
	ext.run("skill", "list").ok(t)

	s.stop()
	r := ext.run("skill", "list")
	r.fails(t, "connection_failed", 4)
	if r.Meta.Attempts != 3 {
		t.Fatalf("读请求应按配置重试 2 次：%s", r.Stdout)
	}
	r = ext.run("inbox", "send", "@console", "--text", "停机期间")
	r.fails(t, "connection_failed", 4)
	if r.Meta.Attempts != 1 {
		t.Fatalf("写请求不应重试：%s", r.Stdout)
	}
	s = w.start(nil)
	defer s.stop()
	ext.run("skill", "list").ok(t)
	if d := ext.run("inbox", "send", "@console", "--text", "恢复后").ok(t); d["to"].(map[string]any)["kind"] != "console" {
		t.Fatalf("%v", d)
	}

	// 吊销一个令牌：它返回 401，同一身份的其他令牌照常可用。
	conn(revoked).run("whoami").ok(t)
	s.console(204, "DELETE", "/external-identities/"+identity+"/tokens/"+revokeID, nil)
	conn(revoked).run("whoami").fails(t, "unauthenticated", 5)
	conn(keep).run("whoami").ok(t)
	// 过期的令牌返回 401。
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if conn(short).run("whoami").Code == 5 {
			conn(keep).run("whoami").ok(t)
			return
		}
	}
	t.Fatal("过期的令牌应返回 401")
}

// 10.2 Pi 进程重启后，旧的 Agent 令牌返回退出码 5，新注入的令牌可以使用。
func TestAgentTokenAfterPiRestart(t *testing.T) {
	w := newWorld(t, false)
	s := w.start(nil)
	defer s.stop()
	dev := s.createAgent("开发助手")
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "你好"})
	s.waitIdle(dev)
	old := w.agentEnv()
	newLoopctl(t, old).run("whoami").ok(t)

	// Pi 在回复中途异常退出（fakepi 的 !crash），进程被丢弃，令牌随之作废。
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "!crash 这一轮会崩溃"})
	s.waitIdle(dev)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if s.a.Executor.Process(uuid.MustParse(dev)).State == executor.ProcessStopped {
			break
		}
	}
	var r result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if r = newLoopctl(t, old).run("whoami"); r.Code == 5 {
			break
		}
	}
	r.fails(t, "unauthenticated", 5)

	// 下一条消息重新启动 Pi，注入新令牌；新令牌可用，旧令牌仍然无效。
	s.console(202, "POST", "/agents/"+dev+"/messages", map[string]any{"text": "再来一次"})
	s.waitIdle(dev)
	fresh := w.agentEnv()
	if fresh["LOOPCTL_AGENT_TOKEN"] == old["LOOPCTL_AGENT_TOKEN"] {
		t.Fatal("Pi 重启后应注入新令牌")
	}
	if d := newLoopctl(t, fresh).run("whoami").ok(t); d["address"] != "默认组织/默认项目/开发助手" {
		t.Fatalf("%v", d)
	}
	newLoopctl(t, old).run("whoami").fails(t, "unauthenticated", 5)
}
