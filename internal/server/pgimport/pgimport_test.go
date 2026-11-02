package pgimport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"loop-now/internal/server/db"
	"loop-now/internal/server/pgimport"
	"loop-now/internal/server/store"
	"loop-now/internal/server/store/pgschema"
	"loop-now/internal/testutil/pgtest"
)

// source 返回一个迁移到最新 PG 版本的独立 schema，以及指向它的连接串。
func source(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool := pgtest.NewMigratedPool(t)
	u := pgtest.URL(t)
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return pool, u + sep + "search_path=" + url.QueryEscape(pool.Config().ConnConfig.RuntimeParams["search_path"])
}

func exec(t *testing.T, pool *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

type fixture struct {
	org, project, agent, credential, event uuid.UUID
	at                                     time.Time
	secret                                 []byte
}

// seed 在源库写一组覆盖各种列类型的行：uuid、时刻、布尔、bytea、文本数组、jsonb，以及 events 表。
func seed(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	f := fixture{org: uuid.New(), project: uuid.New(), agent: uuid.New(), credential: uuid.New(), event: uuid.New(),
		at: time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC), secret: []byte{0, 0xff, 0x10, 'x'}}
	exec(t, pool, `INSERT INTO organizations(id,name,is_default,created_at,updated_at) VALUES($1,'组织',true,$2,$2)`, f.org, f.at)
	exec(t, pool, `INSERT INTO projects(id,organization_id,name,is_default) VALUES($1,$2,'项目',true)`, f.project, f.org)
	// 订阅 Agent 事件的插件：源库的触发器为下面的 Agent 写了插件事件，导入不能再补一份。
	exec(t, pool, `INSERT INTO plugins(id,display_name,source,version,manifest,install_dir,status) VALUES('watcher','Watcher','{}','1.0.0','{"events": ["agent.created"]}','/nonexistent','installed')`)
	exec(t, pool, `INSERT INTO project_plugins(project_id,plugin_id,enabled) VALUES($1,'watcher',true)`, f.project)
	exec(t, pool, `INSERT INTO agents(id,project_id,name,model_provider,model_id,created_at) VALUES($1,$2,'甲','anthropic','m',$3)`, f.agent, f.project, f.at)
	exec(t, pool, `INSERT INTO agent_credentials(id,agent_id,purpose,secret,field_names) VALUES($1,$2,'model',$3,ARRAY['KEY','引号"与,逗号'])`, f.credential, f.agent, f.secret)
	exec(t, pool, `INSERT INTO lifecycle_events(id,at,agent_id,type,data) VALUES($1,$2,$3,'pi.started','{"b": [1, true], "a": "文本"}')`, f.event, f.at, f.agent)
	exec(t, pool, `SELECT setval(pg_get_serial_sequence('runs','queue_seq'), 41)`)
	return f
}

func openTarget(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), db.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestImportConvertsValuesAndKeepsInvariants(t *testing.T) {
	ctx := context.Background()
	pool, src := source(t)
	f := seed(t, pool)
	dir := filepath.Join(t.TempDir(), "db")
	report, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != pgschema.Latest || len(report.Tables) == 0 {
		t.Fatalf("报告不对：%+v", report)
	}
	st := openTarget(t, dir)

	var orgID string
	var isDefault, createdAt int64
	if err := st.Pool.QueryRow(ctx, `SELECT id, is_default, created_at FROM organizations`).Scan(&orgID, &isDefault, &createdAt); err != nil {
		t.Fatal(err)
	}
	if orgID != f.org.String() || isDefault != 1 || createdAt != f.at.UnixMicro() {
		t.Fatalf("uuid/布尔/时刻换写不对：%s %d %d", orgID, isDefault, createdAt)
	}
	a, err := st.GetAgent(ctx, f.agent)
	if err != nil || a.Name != "甲" || !a.CreatedAt.Equal(f.at) {
		t.Fatalf("导入后按 store 读 Agent 不对：%+v %v", a, err)
	}
	var secret []byte
	var fields string
	if err := st.Pool.QueryRow(ctx, `SELECT secret, field_names FROM agent_credentials WHERE id=$1`, f.credential).Scan(&secret, &fields); err != nil {
		t.Fatal(err)
	}
	var names []string
	if !bytes.Equal(secret, f.secret) || json.Unmarshal([]byte(fields), &names) != nil || !reflect.DeepEqual(names, []string{"KEY", `引号"与,逗号`}) {
		t.Fatalf("bytea/文本数组换写不对：%q", fields)
	}
	var data string
	if err := st.Pool.QueryRow(ctx, `SELECT data FROM ev.lifecycle_events WHERE id=$1`, f.event).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(data), &decoded); err != nil || !reflect.DeepEqual(decoded, map[string]any{"a": "文本", "b": []any{1.0, true}}) {
		t.Fatalf("jsonb 换写不对：%s", data)
	}
	// 序列接着源库用到的位置；单例设置只有源库那一行；导入本身不产生插件变更。
	seqs := map[string]int64{}
	rows, err := st.Pool.Query(ctx, `SELECT name, value FROM sequences`)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	var value int64
	if _, err := db.ForEachRow(rows, []any{&name, &value}, func() error { seqs[name] = value; return nil }); err != nil {
		t.Fatal(err)
	}
	if want := map[string]int64{"runs.queue_seq": 41, "task_turns.queue_seq": 0, "conversation_messages.seq": 0}; !reflect.DeepEqual(seqs, want) {
		t.Fatalf("序列：%v", seqs)
	}
	var settings, models, changes, lifecycleChanges, pgEvents, events int64
	if err := st.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM plugin_settings),(SELECT count(*) FROM model_settings),(SELECT count(*) FROM plugin_changes),(SELECT count(*) FROM ev.plugin_lifecycle_changes),(SELECT count(*) FROM plugin_events)`).Scan(&settings, &models, &changes, &lifecycleChanges, &events); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM plugin_events`).Scan(&pgEvents); err != nil {
		t.Fatal(err)
	}
	if settings != 1 || models != 1 || changes != 0 || lifecycleChanges != 0 || pgEvents == 0 || events != pgEvents {
		t.Fatalf("单例/插件变更不对：%d %d %d %d %d/%d", settings, models, changes, lifecycleChanges, events, pgEvents)
	}
	// 每张表都在报告里，行数与源库一致。
	for _, tb := range report.Tables {
		var n int64
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+tb.Name).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != tb.Rows {
			t.Fatalf("%s：源库 %d 行，报告 %d 行", tb.Name, n, tb.Rows)
		}
	}
}

func dbFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), db.DataFile) || strings.HasPrefix(e.Name(), db.EventsFile) {
			out = append(out, e.Name())
		}
	}
	return out
}

// 不满足前提时拒绝，且不留下库文件；已有的库不被碰。
func TestImportRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("version", func(t *testing.T) {
		pool, src := source(t)
		exec(t, pool, `INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)`, pgschema.Latest+1)
		dir := filepath.Join(t.TempDir(), "db")
		if _, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir}); err == nil || !strings.Contains(err.Error(), "迁移版本") {
			t.Fatalf("版本不符应拒绝：%v", err)
		}
		if files := dbFiles(t, dir); len(files) != 0 {
			t.Fatalf("拒绝后留下了 %v", files)
		}
	})
	t.Run("existing", func(t *testing.T) {
		_, src := source(t)
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, db.DataFile), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir}); err == nil || !strings.Contains(err.Error(), "不覆盖") {
			t.Fatalf("已有库应拒绝：%v", err)
		}
		if b, err := os.ReadFile(filepath.Join(dir, db.DataFile)); err != nil || string(b) != "keep" {
			t.Fatal("已有库被改动", err)
		}
	})
	t.Run("running", func(t *testing.T) {
		_, src := source(t)
		dir := t.TempDir()
		lock, err := db.LockDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Release()
		if _, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir}); err == nil || !strings.Contains(err.Error(), "停掉服务") {
			t.Fatalf("服务占着目录时应拒绝：%v", err)
		}
	})
	t.Run("structure", func(t *testing.T) {
		pool, src := source(t)
		exec(t, pool, `ALTER TABLE agents ADD COLUMN extra text`)
		dir := filepath.Join(t.TempDir(), "db")
		if _, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir}); err == nil || !strings.Contains(err.Error(), "新库的 agents 没有列 extra") {
			t.Fatalf("结构不符应拒绝：%v", err)
		}
		if files := dbFiles(t, dir); len(files) != 0 {
			t.Fatalf("拒绝后留下了 %v", files)
		}
	})
	// 源库里不成立的外键在提交时被拒绝，整个导入回滚并删掉新建的库文件。
	t.Run("foreign key", func(t *testing.T) {
		pool, src := source(t)
		seed(t, pool)
		exec(t, pool, `ALTER TABLE agents DROP CONSTRAINT agents_project_id_fkey`)
		exec(t, pool, `INSERT INTO agents(id,project_id,name,model_provider,model_id) VALUES($1,$2,'孤儿','anthropic','m')`, uuid.New(), uuid.New())
		dir := filepath.Join(t.TempDir(), "db")
		if _, err := pgimport.Import(ctx, pgimport.Options{SourceURL: src, DBDir: dir}); db.Code(err) != db.CodeConstraintFK {
			t.Fatalf("外键不成立应失败：%v", err)
		}
		if files := dbFiles(t, dir); len(files) != 0 {
			t.Fatalf("失败后留下了 %v", files)
		}
	})
}

// 连接失败的错误里没有连接串里的用户名与密码。
func TestConnectErrorHidesCredentials(t *testing.T) {
	_, err := pgimport.Import(context.Background(), pgimport.Options{SourceURL: "postgres://fixture-user:fixture-secret@127.0.0.1:1/fixture-db?connect_timeout=2", DBDir: filepath.Join(t.TempDir(), "db")})
	if err == nil {
		t.Fatal("应连接失败")
	}
	for _, s := range []string{"fixture-user", "fixture-secret", "fixture-db"} {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("错误里带了连接参数：%v", err)
		}
	}
}
