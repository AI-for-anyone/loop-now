package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// ---- 迁移 00004 ----

func TestMigration00004(t *testing.T) {
	schema := pgtest.NewSchema(t)
	ctx := context.Background()
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: schema})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.MigrateUpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) error {
		_, err := s.Pool.Exec(ctx, sql, args...)
		return err
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if err := exec(sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	reject := func(what, sql string, args ...any) {
		t.Helper()
		if err := exec(sql, args...); err == nil {
			t.Fatalf("%s 应被约束拒绝", what)
		}
	}
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }

	// 第一版与项目层的数据：一个 Agent 与它的一次执行。
	org, proj, agent, run := id(), id(), id(), id()
	must(`INSERT INTO organizations (id, name, is_default) VALUES ($1, '默认组织', true)`, org)
	must(`INSERT INTO projects (id, organization_id, name, is_default) VALUES ($1, $2, '默认项目', true)`, proj, org)
	must(`INSERT INTO agents (id, project_id, name, model_provider, model_id) VALUES ($1, $2, '开发助手', 'anthropic', 'claude-x')`, agent, proj)
	must(`INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status) VALUES ($1, $2, $3, 1, 0, 'completed')`, run, agent, id())

	if err := s.MigrateUpTo(ctx, 4); err != nil {
		t.Fatalf("迁移到 00004 应成功：%v", err)
	}
	var origin string
	var trigger *uuid.UUID
	if err := s.Pool.QueryRow(ctx, `SELECT origin, trigger_inbox_message_id FROM runs WHERE id = $1`, run).Scan(&origin, &trigger); err != nil || origin != "console" || trigger != nil {
		t.Fatalf("已有执行的来源应为 console、没有触发消息：%q %v %v", origin, trigger, err)
	}

	// 外部身份：名称规则、不能叫 console、未删除的身份中唯一，软删除后名称可以再用。
	ci := id()
	must(`INSERT INTO external_identities (id, name) VALUES ($1, 'ci-bot')`, ci)
	reject("外部身份重名", `INSERT INTO external_identities (id, name) VALUES ($1, 'ci-bot')`, id())
	reject("外部身份叫 console", `INSERT INTO external_identities (id, name) VALUES ($1, 'console')`, id())
	reject("外部身份名含大写", `INSERT INTO external_identities (id, name) VALUES ($1, 'CI')`, id())
	reject("外部身份名以点开头", `INSERT INTO external_identities (id, name) VALUES ($1, '.ci')`, id())
	must(`UPDATE external_identities SET deleted_at = now() WHERE id = $1`, ci)
	ci2 := id()
	must(`INSERT INTO external_identities (id, name) VALUES ($1, 'ci-bot')`, ci2)

	// 令牌：摘要固定 32 字节，显示前缀有固定格式。
	hash := make([]byte, 32)
	must(`INSERT INTO external_tokens (id, identity_id, secret_hash, display_prefix) VALUES ($1, $2, $3, 'lne_0123abcd')`, id(), ci2, hash)
	reject("令牌摘要长度不对", `INSERT INTO external_tokens (id, identity_id, secret_hash, display_prefix) VALUES ($1, $2, $3, 'lne_0123abcd')`, id(), ci2, hash[:16])
	reject("显示前缀格式不对", `INSERT INTO external_tokens (id, identity_id, secret_hash, display_prefix) VALUES ($1, $2, $3, 'lne_xyz')`, id(), ci2, hash)
	must(`INSERT INTO agent_tokens (id, agent_id, instance_id, secret_hash) VALUES ($1, $2, $3, $4)`, id(), agent, id(), hash)

	// Inbox 消息：新线索的第一条 thread_id = id；回复沿用线索并记下回复的消息。
	m1 := id()
	must(`INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, to_agent_id, text, when_busy) VALUES ($1, $1, 'console', 'agent', $2, '你好', 'queue')`, m1, agent)
	must(`INSERT INTO inbox_messages (id, thread_id, in_reply_to, from_kind, from_agent_id, to_kind, text) VALUES ($1, $2, $2, 'agent', $3, 'console', '收到')`, id(), m1, agent)
	reject("回复却没有沿用线索", `INSERT INTO inbox_messages (id, thread_id, in_reply_to, from_kind, to_kind, to_agent_id, text, when_busy) VALUES ($1, $1, $2, 'console', 'agent', $3, 'x', 'queue')`, id(), m1, agent)
	reject("新线索的 thread_id 不是自己", `INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, to_agent_id, text, when_busy) VALUES ($1, $2, 'console', 'agent', $3, 'x', 'queue')`, id(), m1, agent)
	reject("发给 Agent 却没有 whenBusy", `INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, to_agent_id, text) VALUES ($1, $1, 'console', 'agent', $2, 'x')`, id(), agent)
	reject("发给人却带 whenBusy", `INSERT INTO inbox_messages (id, thread_id, from_kind, from_agent_id, to_kind, text, when_busy) VALUES ($1, $1, 'agent', $2, 'console', 'x', 'queue')`, id(), agent)
	reject("发件人类别与引用不符", `INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, text) VALUES ($1, $1, 'agent', 'console', 'x')`, id())
	reject("收件人同时指向 Agent 与外部身份", `INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, to_agent_id, to_identity_id, text, when_busy) VALUES ($1, $1, 'console', 'agent', $2, $3, 'x', 'queue')`, id(), agent, ci2)
	reject("正文为空", `INSERT INTO inbox_messages (id, thread_id, from_kind, to_kind, to_identity_id, text) VALUES ($1, $1, 'console', 'external', $2, '')`, id(), ci2)

	// 提醒执行必须记下触发的消息；同一 Agent 每个优先级最多一条排队中的提醒。
	reject("提醒执行没有触发消息", `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, origin) VALUES ($1, $2, $3, 1, 0, 'queued', 'inbox_reminder')`, id(), agent, id())
	reject("普通执行带了触发消息", `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, trigger_inbox_message_id) VALUES ($1, $2, $3, 1, 0, 'queued', $4)`, id(), agent, id(), m1)
	must(`INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, origin, trigger_inbox_message_id) VALUES ($1, $2, $3, 1, 0, 'queued', 'inbox_reminder', $4)`, id(), agent, id(), m1)
	reject("第二条排队中的普通提醒", `INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, origin, trigger_inbox_message_id) VALUES ($1, $2, $3, 1, 0, 'queued', 'inbox_reminder', $4)`, id(), agent, id(), m1)
	must(`INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, origin, trigger_inbox_message_id) VALUES ($1, $2, $3, 1, 1, 'queued', 'inbox_reminder', $4)`, id(), agent, id(), m1)
	must(`INSERT INTO runs (id, agent_id, user_message_id, config_version, priority, status, when_busy) VALUES ($1, $2, $3, 1, 0, 'queued', 'queue')`, id(), agent, id())

	must(`INSERT INTO audit_log (id, actor_kind, method, operation, status) VALUES ($1, 'cli', 'CLI', 'identity.create', 0)`, id())
	reject("审计的操作者类别不对", `INSERT INTO audit_log (id, actor_kind, method, operation, status) VALUES ($1, 'root', 'GET', 'x', 200)`, id())

	// 回退到 00003 再迁移回来。
	if err := s.MigrateDownTo(ctx, 3); err != nil {
		t.Fatalf("回退到 00003 应成功：%v", err)
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id = $1`, agent).Scan(&n); err != nil || n != 4 {
		t.Fatalf("回退后执行应保留：%d %v", n, err)
	}
	if err := s.MigrateUpTo(ctx, 4); err != nil {
		t.Fatalf("再次迁移应成功：%v", err)
	}
}
