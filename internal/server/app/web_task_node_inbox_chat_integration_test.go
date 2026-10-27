package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
	"loop-now/internal/testutil/testenv"
)

// Web owns this file only. Production and shared helpers remain read-only.
func TestWebTaskNodeInboxChatIntegration(t *testing.T) {
	if os.Getenv("LOOP_NOW_WEB_TASK_INBOX_INTEGRATION") != "1" {
		t.Skip("explicit isolated React/App/TEST PG/fakePi/controlled actual CLI")
	}
	db, e := url.Parse(pgtest.URL(t))
	if e != nil || !strings.HasSuffix(db.Path, "_test") {
		t.Fatal("TEST PG required")
	}
	ro, e := redis.ParseURL(redistest.URL(t))
	if e != nil || ro.DB != 10 {
		t.Fatal("TEST Redis DB10 required")
	}
	root := testenv.RepoRoot()
	evidence := filepath.Join(root, "web/test-results/add-task-node-inbox-chat/app")
	if e = os.MkdirAll(evidence, 0700); e != nil {
		t.Fatal("evidence directory unavailable")
	}
	audit := map[string]any{"boundary": "actual React -> isolated App/PG/Redis/fakePi; controlled actual runtime CLI, not autonomous model", "webOrigin": "http://127.0.0.1:4326"}
	var w *world
	var address string
	t.Cleanup(func() {
		if w == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		audit["schema"] = w.schema
		audit["redisPrefix"] = w.cfg.RedisKeyPrefix
		c, e := pgx.Connect(ctx, pgtest.URL(t))
		if e != nil {
			t.Error("cleanup PG read unavailable")
			return
		}
		defer c.Close(ctx)
		var exists bool
		e = c.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname=$1)`, w.schema).Scan(&exists)
		audit["schemaRemoved"] = e == nil && !exists
		if e != nil || exists {
			t.Error("private schema cleanup failed")
		}
		rc := redis.NewClient(ro)
		defer rc.Close()
		it := rc.Scan(ctx, 0, w.cfg.RedisKeyPrefix+"*", 100).Iterator()
		found := it.Next(ctx)
		audit["redisPrefixEmpty"] = !found && it.Err() == nil
		if found || it.Err() != nil {
			t.Error("private Redis cleanup failed")
		}
		_, e = os.Stat(w.cfg.WorkRoot)
		audit["workRootRemoved"] = os.IsNotExist(e)
		conn, e := net.DialTimeout("tcp", address, 150*time.Millisecond)
		audit["appListenerClosed"] = e != nil
		if e == nil {
			conn.Close()
			t.Error("temporary App listener remains")
		}
		webConn, webErr := net.DialTimeout("tcp", "127.0.0.1:4326", 150*time.Millisecond)
		audit["webListenerClosed"] = webErr != nil
		if webErr == nil {
			webConn.Close()
			t.Error("self-owned frontend listener remains")
		}
		raw, _ := json.MarshalIndent(audit, "", "  ")
		if os.WriteFile(filepath.Join(evidence, "driver-results.json"), raw, 0600) != nil {
			t.Error("audit write failed")
		}
	})
	bin := inboxActualCLI(t)
	w = taskNodeInboxWorld(t)
	w.cfg.ConsoleOrigins = []string{"http://127.0.0.1:4326"}
	w.cfg.PiMaxProcesses = 4
	dump := filepath.Join(t.TempDir(), "runtime-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	t.Setenv("FAKEPI_PROMPT_DELAY", "6s")
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP", "FAKEPI_PROMPT_DELAY"}
	s := w.start(nil)
	address = s.a.Addr()
	t.Cleanup(s.stop)
	t.Log("WEB_TASK_INBOX_APP_READY: compiled actual app.Build started; public App editor freeze released")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic Web Inbox model", "credential": map[string]string{"type": "api_key", "key": "synthetic-not-a-provider-key"}})
	d, p := sleepTask(t, s, " !sleep=1h !tool=bash !usage=1,2 !text=WEB_INBOX_WORK_OUTPUT")
	active := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	node := d.Nodes[0]
	t.Cleanup(func() {
		_, _ = s.a.Tasks.Cancel(context.Background(), auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()})
	})
	// Copy only this synthetic process identity into a private test temp file
	// before a separate Manager process may overwrite the shared dump. No token
	// is included in screenshots, logs, manifests or persisted evidence.
	for end := time.Now().Add(4 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, e = os.Stat(dump); e == nil {
			break
		}
		if time.Now().After(end) {
			t.Fatal("fake runtime environment unavailable")
		}
	}
	env := dumpedEnv(t, dump)
	private := t.TempDir()
	authPath := filepath.Join(private, "runtime-auth.json")
	raw, _ := json.Marshal(map[string]string{"url": env["LOOPCTL_AGENT_URL"], "token": env["LOOPCTL_AGENT_TOKEN"]})
	if os.WriteFile(authPath, raw, 0600) != nil {
		t.Fatal("private test identity unavailable")
	}
	question, e := s.a.API.Inbox.Send(context.Background(), p, "默认项目/Manager", "WEB_MANAGER_ONLY_ORIGINAL", "queue", inbox.QuestionInput{Options: []string{"yes", "no"}, NodeRunID: node.ID})
	if e != nil {
		t.Fatal("TEST Manager question failed", e)
	}
	manifest := map[string]any{"apiBase": s.url, "webOrigin": "http://127.0.0.1:4326", "projectName": dc.Project.Name, "taskId": d.Task.ID, "nodeRunId": node.ID, "agentId": p.ID, "runtimeId": p.RuntimeID, "initialRunId": active.ID, "questionId": question.ID, "cliBin": bin, "privateAuthPath": authPath, "privateRoot": private, "cliHome": filepath.Join(private, "home")}
	if os.MkdirAll(filepath.Join(private, "home"), 0700) != nil {
		t.Fatal("isolated CLI HOME unavailable")
	}
	manifestPath := filepath.Join(private, "manifest.json")
	raw, _ = json.Marshal(manifest)
	if os.WriteFile(manifestPath, raw, 0600) != nil {
		t.Fatal("manifest unavailable")
	}
	runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "/Users/ljl/.local/node-v24.21.0/bin/node", filepath.Join(root, "web/scripts/task-inbox-app.mjs"))
	cmd.Dir = root
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LOOP_NOW_WEB_TASK_INBOX_MANIFEST=" + manifestPath}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
	})
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	browserErr := cmd.Run()
	if browserErr != nil {
		if x, ok := browserErr.(*exec.ExitError); ok {
			audit["browserExitCode"] = x.ExitCode()
		}
		audit["browserErrorType"] = regexp.MustCompile(`[A-Za-z]+Error`).FindString(stderr.String())
		audit["browserErrorLine"] = regexp.MustCompile(`task-inbox-app\.mjs:[0-9]+:[0-9]+`).FindString(stderr.String())
		t.Log("safe browser exit diagnostic", audit["browserExitCode"], audit["browserErrorType"], audit["browserErrorLine"])
	}
	counts := map[string]int{}
	for name, query := range map[string]string{
		"roots":             `SELECT count(*) FROM task_node_inbox_messages WHERE task_id=$1 AND message_id=root_message_id`,
		"replies":           `SELECT count(*) FROM task_node_inbox_messages WHERE task_id=$1 AND message_id<>root_message_id`,
		"acceptedActions":   `SELECT count(*) FROM task_actions WHERE task_id=$1 AND operation='sendTaskNodeInboxMessage'`,
		"readLinks":         `SELECT count(*) FROM task_node_inbox_run_links l JOIN task_node_inbox_messages m ON m.message_id=l.message_id WHERE m.task_id=$1 AND l.role='read'`,
		"replyRootLinks":    `SELECT count(*) FROM task_node_inbox_run_links l JOIN task_node_inbox_messages m ON m.message_id=l.message_id WHERE m.task_id=$1 AND l.role='reply' AND m.message_id=m.root_message_id`,
		"replyMessageLinks": `SELECT count(*) FROM task_node_inbox_run_links l JOIN task_node_inbox_messages m ON m.message_id=l.message_id WHERE m.task_id=$1 AND l.role='reply' AND m.message_id<>m.root_message_id`,
		"replyLinks":        `SELECT count(*) FROM task_node_inbox_run_links l JOIN task_node_inbox_messages m ON m.message_id=l.message_id WHERE m.task_id=$1 AND l.role='reply'`,
		"consoleTurns":      `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='console_node'`,
		"readRoots":         `SELECT count(*) FROM task_node_inbox_messages m JOIN inbox_messages i ON i.id=m.message_id WHERE m.task_id=$1 AND m.message_id=m.root_message_id AND i.read_at IS NOT NULL`,
	} {
		var count int
		if e = s.a.Store.Pool.QueryRow(context.Background(), query, d.Task.ID).Scan(&count); e != nil {
			t.Fatal("scoped PG proof failed", name, e)
		}
		counts[name] = count
	}
	audit["pgCounts"] = counts
	audit["taskId"] = d.Task.ID
	audit["nodeRunId"] = node.ID
	audit["runtimeId"] = node.RuntimeID
	raw, _ = json.MarshalIndent(counts, "", "  ")
	_ = os.WriteFile(filepath.Join(evidence, "pg-readback.json"), raw, 0600)
	if browserErr != nil {
		t.Fatal("browser failed; safe browser/PG evidence retained")
	}
	if counts["roots"] != 2 || counts["replies"] != 2 || counts["acceptedActions"] != 2 || counts["readRoots"] != 2 || counts["readLinks"] != 2 || counts["replyLinks"] != 4 || counts["replyRootLinks"] != 2 || counts["replyMessageLinks"] != 2 || counts["consoleTurns"] != 0 {
		t.Fatal("real Inbox/turn mapping counts differ", counts)
	}
	final := taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool { return v.Task.Status == "cancelled" && v.Cleanup == "archived" })
	if len(final.Nodes) != 1 || final.Nodes[0].ID != node.ID || final.Nodes[0].Attempt != 1 {
		t.Fatal("conversation changed original execution")
	}
	var foreign int
	if e = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_node_inbox_messages WHERE task_id=$1 AND (node_run_id<>$2 OR runtime_id<>$3 OR agent_id<>$4)`, d.Task.ID, node.ID, p.RuntimeID, p.ID).Scan(&foreign); e != nil || foreign != 0 {
		t.Fatal("Inbox escaped original scope")
	}
	audit["foreignInboxMappings"] = foreign
	audit["passed"] = true
}
