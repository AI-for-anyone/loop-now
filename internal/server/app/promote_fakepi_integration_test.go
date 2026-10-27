package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// promoteWorld is newWorld against the installed Pi 1.4 model catalog, with
// fakePi wrapped to report that version. Real App, TEST PG/Redis and fakePi.
func promoteWorld(t *testing.T) *world {
	t.Helper()
	real, err := preflight.Resolve(context.Background(), preflight.Options{PiBin: "pi", PiVersion: "1.4.0", NodeBin: "node",
		WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if err != nil {
		t.Skipf("本机没有可用的 Pi 1.4.0（读取模型目录需要）：%v", err)
	}
	wrapper := filepath.Join(t.TempDir(), "pi")
	if err = os.WriteFile(wrapper, []byte("#!/bin/sh\nif [ \"$1\" = --version ] || [ \"$1\" = -v ]; then echo 1.4.0; exit 0; fi\nexec "+fakepibin.Path(t)+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, prefix := redistest.New(t)
	return &world{t: t, schema: pgtest.NewSchema(t), piAIDir: real.PiAIDir, cfg: config.Config{
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 50 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second,
		DatabaseURL: pgtest.URL(t), RedisURL: redistest.URL(t),
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"},
		WorkRoot: filepath.Join(t.TempDir(), "work"), RedisKeyPrefix: prefix, DefaultOrgName: "默认组织", DefaultProjectName: "默认项目",
		PiBin: wrapper, PiVersion: "1.4.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second,
		PiStopGrace: 2 * time.Second, AbortTimeout: 2 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, LockTTL: 5 * time.Second, LockRenewInterval: time.Second, ReconcileInterval: 500 * time.Millisecond,
		SSEKeepalive: time.Second, EventStreamMaxLen: 1000, RunTextTTL: time.Hour, ShutdownTimeout: 10 * time.Second,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16,
		TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100,
		LoopctlDistDir: loopctlfake.Dir(t), SleepScanInterval: 50 * time.Millisecond, SleepScanBatch: 100,
	}}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for until := time.Now().Add(20 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("等待超时：" + what)
}

// firstSSESnapshot reads the first event of an SSE stream, which must be a snapshot.
func firstSSESnapshot(t *testing.T, url string, out any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	event := ""
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			if event != "snapshot" {
				t.Fatalf("第一条事件应为 snapshot：%s", event)
			}
			if err = json.Unmarshal([]byte(v), out); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("没有读到 snapshot")
}

func TestPromoteToInterruptActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	path, agent := s.createAgent("Promoter")
	send := func(text, whenBusy string) apitypes.Run {
		body := map[string]any{"text": text}
		if whenBusy != "" {
			body["whenBusy"] = whenBusy
		}
		return decode[apitypes.SendMessageResult](t, s.must(202, "POST", path+"/messages", body)).Run
	}
	// 当前回复不理会中止，执行器在 AbortTimeout 后强制结束：留出观察排队的窗口。
	first := send("!sleep=30s !noabort !text=first", "")
	waitFor(t, "第一条进入运行", func() bool {
		r, e := s.a.Store.GetRun(ctx, first.ID)
		return e == nil && r.Status == "running"
	})
	n1 := send("!text=n1", "queue")
	n2 := send("!text=n2", "queue")
	got := decode[apitypes.AgentQueueActionReceipt](t, s.must(200, "POST", path+"/runs/"+n2.ID.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New()}))
	ids := func(runs []apitypes.Run) []uuid.UUID {
		out := []uuid.UUID{}
		for _, r := range runs {
			out = append(out, r.ID)
		}
		return out
	}
	itemRuns := func(items []apitypes.QueueItem) []uuid.UUID {
		out := []uuid.UUID{}
		for _, it := range items {
			out = append(out, it.RunID)
		}
		return out
	}
	if !slices.Equal(itemRuns(got.Queue), []uuid.UUID{n2.ID, n1.ID}) || got.Run.WhenBusy == nil || *got.Run.WhenBusy != "interrupt" || got.Queue[0].WhenBusy != "interrupt" {
		t.Fatalf("转换结果：%+v", got)
	}
	var snap apitypes.SseSnapshot
	firstSSESnapshot(t, s.url+path+"/events", &snap)
	if !slices.Equal(ids(snap.QueuedRuns), []uuid.UUID{n2.ID, n1.ID}) || *snap.QueuedRuns[0].WhenBusy != "interrupt" || *snap.QueuedRuns[1].WhenBusy != "queue" {
		t.Fatalf("SSE 快照的排队顺序或 whenBusy 不对：%+v", snap.QueuedRuns)
	}
	if code, body := s.call("POST", path+"/runs/"+first.ID.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New()}); code != 409 || !strings.Contains(string(body), "run_not_queued") {
		t.Fatalf("进行中的执行应 409 run_not_queued：%d %s", code, body)
	}
	if code, _ := s.call("POST", path+"/runs/"+n2.ID.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New()}); code != 200 {
		t.Fatalf("重复转换应幂等：%d", code)
	}
	waitFor(t, "全部执行结束", func() bool {
		for _, id := range []uuid.UUID{first.ID, n1.ID, n2.ID} {
			r, e := s.a.Store.GetRun(ctx, id)
			if e != nil || r.EndedAt == nil {
				return false
			}
		}
		return true
	})
	a, _ := s.a.Store.GetRun(ctx, first.ID)
	r1, _ := s.a.Store.GetRun(ctx, n1.ID)
	r2, _ := s.a.Store.GetRun(ctx, n2.ID)
	if a.Status != "interrupted" || a.EndReason != "user_interrupt" {
		t.Fatalf("当前回复应被打断：%s %s", a.Status, a.EndReason)
	}
	if r1.Status != "completed" || r2.Status != "completed" || !r2.StartedAt.Before(*r1.StartedAt) {
		t.Fatalf("转换的消息应先于普通排队执行：n2=%s@%v n1=%s@%v", r2.Status, r2.StartedAt, r1.Status, r1.StartedAt)
	}
	_ = agent
}

func TestPromoteTaskNodeInboxActualAppFakePi(t *testing.T) {
	w := promoteWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	s.must(201, "POST", "/projects/"+project.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	_, manager := s.createAgent("Manager")
	_, worker := s.createAgent("Worker")
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": manager.ID})
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "promote", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=30s !noabort !text=working", AgentID: worker.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	task := decode[taskmodel.Detail](t, raw)
	node, runtime := task.Nodes[0].ID, task.Nodes[0].RuntimeID
	var active uuid.UUID
	waitFor(t, "节点进入运行", func() bool {
		return s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, runtime).Scan(&active) == nil
	})
	base := "/tasks/" + task.Task.ID.String() + "/nodes/" + node.String()
	send := func(text string) uuid.UUID {
		r := decode[taskmodel.TaskNodeInboxReceipt](t, s.must(201, "POST", base+"/inbox-messages", map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": runtime, "text": text, "whenBusy": "queue"}))
		var m struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(r.Message, &m); err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	m1, m2 := send("第一条排队"), send("第二条排队")
	q := decode[taskmodel.TaskNodeQueue](t, s.must(200, "GET", base+"/queue", nil))
	if len(q.Items) != 2 || q.Items[0].WhenBusy != "queue" || *q.Items[0].MessageID != m1 || *q.Items[1].MessageID != m2 || q.Items[0].RunID != q.Items[1].RunID || q.Items[0].Text != "第一条排队" {
		t.Fatalf("排队列表：%+v", q)
	}
	normal := q.Items[0].RunID
	receipt := decode[taskmodel.TaskNodeQueueActionReceipt](t, s.must(200, "POST", base+"/inbox-messages/"+m2.String()+"/promote-to-interrupt", map[string]any{"actionId": uuid.New(), "expectedAgentId": worker.ID, "expectedRuntimeId": runtime}))
	q = decode[taskmodel.TaskNodeQueue](t, s.must(200, "GET", base+"/queue", nil))
	if receipt.Status != "accepted" || len(q.Items) != 2 || q.Items[0].RunID != receipt.RunID || q.Items[0].WhenBusy != "interrupt" || *q.Items[0].MessageID != m2 || q.Items[1].RunID != normal || *q.Items[1].MessageID != m1 {
		t.Fatalf("转换后的排队列表：%+v %+v", receipt, q)
	}
	var queueEvents int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_events WHERE task_id=$1 AND type='task.node.queue.updated'`, task.Task.ID).Scan(&queueEvents); err != nil || queueEvents < 3 {
		t.Fatalf("应推送排队列表事件：%d %v", queueEvents, err)
	}
	defer func() {
		if !t.Failed() {
			return
		}
		rows, _ := s.a.Store.Pool.Query(ctx, `SELECT id::text,status,priority,origin,coalesce(execution_mode,''),started_at IS NOT NULL,coalesce(error_code,'') FROM task_turns WHERE runtime_id=$1 ORDER BY queue_seq`, runtime)
		for rows.Next() {
			var id, st, origin, mode, code string
			var p int
			var started bool
			_ = rows.Scan(&id, &st, &p, &origin, &mode, &started, &code)
			t.Logf("turn %s %s p=%d %s %s started=%v err=%s", id[:8], st, p, origin, mode, started, code)
		}
		rows.Close()
		d, _ := s.a.Tasks.Get(ctx, auth.Console(), task.Task.ID)
		t.Logf("task %s node %+v", d.Task.Status, d.Nodes[len(d.Nodes)-1].Status)
	}()
	waitFor(t, "转换的打断组被领取", func() bool {
		var started bool
		return s.a.Store.Pool.QueryRow(ctx, `SELECT started_at IS NOT NULL FROM task_turns WHERE id=$1`, receipt.RunID).Scan(&started) == nil && started
	})
	// fakePi ends that round without submitting, so the node blocks and the
	// remaining task_node-mode group expires by the existing rule; it must not
	// have been claimed before the promoted group either way.
	var activeStatus string
	var promotedStart time.Time
	var normalStart *time.Time
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT status FROM task_turns WHERE id=$1),(SELECT started_at FROM task_turns WHERE id=$2),(SELECT started_at FROM task_turns WHERE id=$3)`, active, receipt.RunID, normal).Scan(&activeStatus, &promotedStart, &normalStart); err != nil {
		t.Fatal(err)
	}
	if activeStatus != "interrupted" || normalStart != nil && !promotedStart.Before(*normalStart) {
		t.Fatalf("当前轮应被打断，转换的消息应先被领取：%s %v %v", activeStatus, promotedStart, normalStart)
	}
	var links int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_node_inbox_run_links WHERE message_id=$1 AND run_id=$2 AND role='notification'`, m2, receipt.RunID).Scan(&links); err != nil || links != 1 {
		t.Fatalf("转换的消息应由打断组通知：%d %v", links, err)
	}
}
