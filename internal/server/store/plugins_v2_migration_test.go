package store_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
	"loop-now/internal/testutil/pgtest"
)

// legacyPluginTables 是 031 删除、回退时重建的旧插件表（005 与 022）。
var legacyPluginTables = []string{"plugins", "plugin_instances", "plugin_template_names", "platform_plugins", "platform_plugin_versions",
	"project_platform_plugins", "platform_plugin_config_versions", "agent_plugin_grants", "platform_plugin_accounts",
	"platform_plugin_account_versions", "task_plugin_versions", "platform_plugin_state",
	"inbox_channel_threads", "inbox_channel_deliveries", "inbox_channel_dedupe", "inbox_messages"}

// legacyPluginShape 记录旧插件表、006 通道表与 inbox_messages 的列（不含列序）、约束与索引，以及 task_records 上的旧列。
func legacyPluginShape(t *testing.T, s *store.Store) []string {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `
		SELECT 'col '||table_name||'.'||column_name||' '||data_type||' '||is_nullable||' '||coalesce(column_default,'')
		FROM information_schema.columns WHERE table_schema=current_schema() AND (table_name = ANY($1) OR (table_name='task_records' AND column_name='platform_plugins_captured'))
		UNION ALL
		SELECT 'con '||c.conrelid::regclass::text||' '||c.conname||' '||pg_get_constraintdef(c.oid)
		FROM pg_constraint c WHERE c.connamespace=current_schema()::regnamespace
		 AND (c.conrelid::regclass::text = ANY($1) OR c.confrelid::regclass::text = ANY($1))
		UNION ALL
		SELECT 'idx '||indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename = ANY($1)
		ORDER BY 1`, legacyPluginTables)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		out = append(out, line)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	return out
}

// 031 删除旧插件表（有内容也直接删），保留 agent_credentials.template_source，建好第二版的表；
// 有第二版插件数据时拒绝回退，没有时回退到 030 的结构。
func TestPluginsV2MigrationReplacesLegacyTables(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, pgtest.URL(t), store.OpenOptions{SearchPath: pgtest.NewSchema(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.MigrateUpTo(ctx, 30); err != nil {
		t.Fatal(err)
	}
	before := legacyPluginShape(t, s)
	if len(before) == 0 {
		t.Fatal("030 应有旧插件表")
	}
	must := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s：%v", sql, err)
		}
	}
	defaults, err := s.EnsureDefaults(ctx, "org", "project")
	if err != nil {
		t.Fatal(err)
	}
	legacy := uuid.New()
	must(`INSERT INTO platform_plugins(id,name,display_name,source,version,manifest) VALUES($1,'old','Old','{}','1','{}')`, legacy)
	must(`INSERT INTO project_platform_plugins(project_id,plugin_id,enabled) VALUES($1,$2,true)`, defaults.Project.ID, legacy)
	must(`INSERT INTO plugins(id,source,status) VALUES($1,'{}','installed')`, legacy)
	must(`INSERT INTO plugin_instances(id,plugin_id,project_id) VALUES($1,$1,$2)`, legacy, defaults.Project.ID)

	if err := s.MigrateUpTo(ctx, 31); err != nil {
		t.Fatalf("旧插件表有内容时也应直接删除：%v", err)
	}
	var gone, captured, templateSource bool
	if err := s.Pool.QueryRow(ctx, `SELECT to_regclass('platform_plugins') IS NULL AND to_regclass('plugin_instances') IS NULL AND to_regclass('plugin_template_names') IS NULL
		AND to_regclass('inbox_channel_threads') IS NULL AND NOT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='inbox_messages' AND column_name LIKE '%channel_id'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='task_records' AND column_name='platform_plugins_captured'),
		EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='agent_credentials' AND column_name='template_source')`).Scan(&gone, &captured, &templateSource); err != nil {
		t.Fatal(err)
	}
	if !gone || captured || !templateSource {
		t.Fatalf("旧表已删=%v，platform_plugins_captured 仍在=%v，template_source 保留=%v", gone, captured, templateSource)
	}
	// 去掉通道列后，收发双方的约束照旧生效，channel 不再是合法的一方。
	agent, err := s.CreateAgent(ctx, store.ProjectScope(defaults.Project.ID), "收件", "anthropic", "m", "")
	if err != nil {
		t.Fatal(err)
	}
	message := uuid.New()
	must(`INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,to_agent_id,text,when_busy) VALUES($1,$1,'console','agent',$2,'x','queue')`, message, agent.ID)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO inbox_messages(id,thread_id,from_kind,to_kind,text) VALUES($1,$1,'channel','console','x')`, uuid.New()); err == nil {
		t.Fatal("channel 不应再是合法的发件方")
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO inbox_messages(id,thread_id,from_kind,from_agent_id,to_kind,text) VALUES($1,$1,'console',$2,'console','x')`, uuid.New(), agent.ID); err == nil {
		t.Fatal("控制台发件不应带 Agent")
	}
	must(`DELETE FROM inbox_messages WHERE id=$1`, message)
	var disabled bool
	if err := s.Pool.QueryRow(ctx, `SELECT disabled FROM plugin_settings`).Scan(&disabled); err != nil || disabled {
		t.Fatalf("全局设置应有一行且未停用：%v %v", disabled, err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES('Bad_ID','x','{}','1','{}','/x','installed')`); err == nil {
		t.Fatal("插件 ID 只允许小写字母、数字与连字符")
	}
	must(`INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES('webhook','Webhook','{}','1.0.0','{}','/x','installed')`)
	must(`INSERT INTO plugin_state(plugin_id,project_id,key,value) VALUES('webhook',NULL,'k','1')`)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO plugin_state(plugin_id,project_id,key,value) VALUES('webhook',NULL,'k','2')`); err == nil {
		t.Fatal("全局状态键应唯一（project_id 为空也算同一行）")
	}
	if err := s.MigrateDownTo(ctx, 30); err == nil || !strings.Contains(err.Error(), "plugins_v2_down_refused") {
		t.Fatalf("有插件数据时应拒绝回退：%v", err)
	}
	must(`DELETE FROM plugins`)
	if err := s.MigrateDownTo(ctx, 30); err != nil {
		t.Fatalf("没有插件数据时可以回退：%v", err)
	}
	if after := legacyPluginShape(t, s); !reflect.DeepEqual(before, after) {
		t.Fatalf("回退后的旧表结构与 030 不同：\n030：%q\n回退后：%q", before, after)
	}
	if err := s.MigrateUpTo(ctx, 31); err != nil {
		t.Fatal(err)
	}
}
