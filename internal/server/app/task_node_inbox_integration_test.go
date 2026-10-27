package app_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/preflight"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/testutil/fakepibin"
	"loop-now/internal/testutil/loopctlfake"
	"loop-now/internal/testutil/pgtest"
	"loop-now/internal/testutil/redistest"
)

// Own schema/prefix only: unlike generic test factories this never sweeps other
// sessions' old namespaces. No provider, real Pi execution or shared listeners.
func taskNodeInboxWorld(t *testing.T) *world {
	t.Helper()
	ctx := context.Background()
	real, e := preflight.Resolve(ctx, preflight.Options{PiBin: "pi", PiVersion: "1.1.0", NodeBin: "node", WorkRoot: filepath.Join(t.TempDir(), "probe"), HomeDirs: []string{"/nonexistent-home"}})
	if e != nil {
		t.Fatal("offline catalog preflight unavailable", e)
	}
	c, e := pgx.Connect(ctx, pgtest.URL(t))
	if e != nil {
		t.Fatal("TEST connection unavailable")
	}
	schema := "task_node_inbox_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = c.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		defer c.Close(ctx)
		if _, e := c.Exec(ctx, `DROP SCHEMA `+pgx.Identifier{schema}.Sanitize()+` CASCADE`); e != nil {
			t.Error(e)
		}
	})
	opts, e := redis.ParseURL(redistest.URL(t))
	if e != nil {
		t.Fatal("TEST Redis URL invalid")
	}
	rc := redis.NewClient(opts)
	prefix := "loopnow:test:task-node-inbox:" + uuid.NewString() + ":"
	t.Cleanup(func() {
		if e := redistest.DeletePrefix(ctx, rc, prefix); e != nil {
			t.Error(e)
		}
		rc.Close()
	})
	w := &world{t: t, schema: schema, piAIDir: real.PiAIDir, cfg: config.Config{
		DatabaseURL: pgtest.URL(t), RedisURL: redistest.URL(t), RedisKeyPrefix: prefix,
		ListenAddr: "127.0.0.1:0", AllowedHosts: []string{"localhost", "127.0.0.1", "::1"}, ConsoleOrigins: []string{"http://localhost:5173"}, DefaultOrgName: "默认组织", DefaultProjectName: "默认项目",
		WorkRoot: filepath.Join(t.TempDir(), "work"), PiBin: fakepibin.Path(t), PiVersion: "1.1.0", PiMaxProcesses: 2, PiIdleTimeout: time.Minute, PiStartTimeout: 10 * time.Second, PiStopGrace: 2 * time.Second, AbortTimeout: 3 * time.Second, GitCloneTimeout: 30 * time.Second, CLIInstallTimeout: time.Minute,
		NodeBin: "node", PiAIDir: real.PiAIDir, LockTTL: 5 * time.Second, LockRenewInterval: time.Second, ReconcileInterval: 100 * time.Millisecond, SSEKeepalive: time.Second, EventStreamMaxLen: 1000, RunTextTTL: time.Hour, ShutdownTimeout: 10 * time.Second,
		TaskMaxSteps: 100, TaskMaxEdges: 300, TaskMaxVisits: 1000, TaskMaxRequest: 1 << 20, TaskMaxArtifact: 20 << 20, TaskMaxSession: 100 << 20, TaskReconcileInterval: 50 * time.Millisecond, TaskEventPoll: 10 * time.Millisecond, TaskRecordTimeout: time.Second, SleepScanInterval: 50 * time.Millisecond, SleepScanBatch: 100,
		MaxUploadFile: 1 << 20, MaxSkillArchive: 1 << 20, MaxSkillEntries: 50, MaxCredential: 4096, MaxMessage: 1 << 16, MaxInboxMessage: 1 << 16, InboxPreviewChars: 200, InboxPageMax: 100, TextEditExtensions: []string{".md"}, TextEditMax: 1 << 16, LoopctlDistDir: loopctlfake.Dir(t),
	}}
	t.Logf("actual TEST schema=%s RedisPrefix=%s ephemeral loopback App/fakePi", schema, prefix)
	return w
}

func inboxActualCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "loopctl")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "./cmd/loopctl")
	cmd.Dir = "../../.."
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off")
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("build test-only CLI: %v %s", e, out)
	}
	return bin
}

func TestTaskNodeInboxAppActualCLIAndSSE(t *testing.T) {
	bin := inboxActualCLI(t)
	doc := contracttest.Load(t)
	assertSchema := func(name string, value any) {
		t.Helper()
		raw, _ := json.Marshal(value)
		var wire any
		json.Unmarshal(raw, &wire)
		if e := doc.Components.Schemas[name].Value.VisitJSON(wire, openapi3.EnableJSONSchema2020()); e != nil {
			t.Fatalf("actual %s violates formal API: %v", name, e)
		}
	}
	w := taskNodeInboxWorld(t)
	dump := filepath.Join(t.TempDir(), "fake-env.json")
	t.Setenv("FAKEPI_DUMP", dump)
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_DUMP"}
	s := w.start(nil)
	defer s.stop()
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic offline", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	d, p := sleepTask(t, s, " !sleep=1h !tool=bash !text=WORKING_STREAM")
	active := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	node := d.Nodes[0]
	path := taskConsoleRoute(d.Task.ID, node.ID)
	for end := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, e := os.Stat(dump); e == nil {
			break
		}
		if time.Now().After(end) {
			t.Fatal("fake environment not recorded")
		}
	}
	env := dumpedEnv(t, dump)
	home := t.TempDir()
	runCLI := func(args ...string) map[string]any {
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LOOPCTL_AGENT_URL=" + env["LOOPCTL_AGENT_URL"], "LOOPCTL_AGENT_TOKEN=" + env["LOOPCTL_AGENT_TOKEN"]}
		raw, e := cmd.Output()
		if e != nil {
			t.Fatalf("actual CLI rejected %v: %v %s", args, e, raw)
		}
		var out struct {
			Data map[string]any `json:"data"`
		}
		if e = json.Unmarshal(raw, &out); e != nil {
			t.Fatal(e)
		}
		return out.Data
	}
	question, e := s.a.API.Inbox.Send(context.Background(), p, "默认项目/Manager", "MANAGER_ONLY_ORIGINAL_QUESTION", "queue", inbox.QuestionInput{Options: []string{"yes", "no"}, NodeRunID: node.ID})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", s.url+"/tasks/"+d.Task.ID.String()+"/events", nil)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	observed := make(chan string, 64)
	go func() {
		defer close(observed)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data:") {
				select {
				case observed <- sc.Text():
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	bad := taskConsoleInput(node, uuid.New(), "INVALID_TARGET_MUST_NOT_DISPATCH", "interrupt")
	bad["expectedRuntimeId"] = uuid.New()
	s.must(409, "POST", path+"/inbox-messages", bad)
	bad = taskConsoleInput(node, uuid.New(), "NOT_AN_ANSWER", "queue")
	bad["optionId"] = "yes"
	s.must(422, "POST", path+"/inbox-messages", bad)
	before := decode[taskmodel.TaskNodeInboxPage](t, s.must(200, "GET", path+"/inbox-messages", nil))
	if len(before.Items) != 0 {
		t.Fatal("invalid requests left Inbox rows")
	}
	original, e := s.a.Store.TaskTurn(ctx, active.ID)
	if e != nil || original.Status != "running" {
		t.Fatal("invalid target interrupted original execution", e)
	}
	input := taskConsoleInput(node, uuid.New(), "ACTUAL_INBOX_BODY", "queue")
	receipt := decode[taskmodel.TaskNodeInboxReceipt](t, s.must(201, "POST", path+"/inbox-messages", input))
	assertSchema("TaskNodeInboxReceipt", receipt)
	var msg apitypes.InboxMessage
	if e = json.Unmarshal(receipt.Message, &msg); e != nil {
		t.Fatal(e)
	}
	list := runCLI("inbox", "list")
	if len(list["messages"].([]any)) != 1 {
		t.Fatal("actual Agent list missing Inbox")
	}
	// Exercise the actual Agent thread routes, not direct service calls. A
	// console history view and an Agent thread GET never imply Agent reading.
	threadPath := "/inbox/threads/" + msg.ThreadID.String()
	agentRequest := func(method, suffix string, body any, want int) {
		t.Helper()
		var payload *bytes.Reader
		if body == nil {
			payload = bytes.NewReader(nil)
		} else {
			raw, _ := json.Marshal(body)
			payload = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(ctx, method, env["LOOPCTL_AGENT_URL"]+suffix, payload)
		if err != nil {
			t.Fatal("invalid isolated Agent request")
		}
		req.Header.Set("Authorization", "Bearer "+env["LOOPCTL_AGENT_TOKEN"])
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal("isolated Agent thread request failed")
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("Agent thread %s expected %d got %d", method, want, res.StatusCode)
		}
	}
	agentRequest("GET", "/inbox/threads", nil, 404)
	agentRequest("GET", threadPath, nil, 422)
	agentRequest("GET", threadPath+"?runId="+uuid.NewString(), nil, 409)
	agentRequest("GET", threadPath+"?runId="+active.ID.String(), nil, 200)
	unread, err := s.a.Store.GetInboxMessage(ctx, msg.ID, 160)
	if err != nil || unread.ReadAt != nil {
		t.Fatal("thread history GET marked Agent read", err)
	}
	agentRequest("POST", threadPath+"/read", nil, 422)
	agentRequest("POST", threadPath+"/read", map[string]any{"runId": nil}, 422)
	agentRequest("POST", threadPath+"/read", map[string]any{"runId": uuid.NewString()}, 409)
	agentRequest("POST", threadPath+"/read", map[string]any{"runId": active.ID.String()}, 200)
	agentRequest("POST", threadPath+"/read", map[string]any{"runId": active.ID.String()}, 200)
	var readLinks int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_node_inbox_run_links WHERE message_id=$1 AND run_id=$2 AND role='read'`, msg.ID, active.ID).Scan(&readLinks); err != nil || readLinks != 1 {
		t.Fatal("batch thread read did not persist one actual run association", readLinks, err)
	}
	read := runCLI("inbox", "read", msg.ID.String(), "--run", active.ID.String())
	if read["text"] != "ACTUAL_INBOX_BODY" || read["readAt"] == nil {
		t.Fatal("read not persisted")
	}
	reply := runCLI("inbox", "reply", msg.ID.String(), "--run", active.ID.String(), "--text", "ACTUAL_INBOX_REPLY")
	if reply["inReplyTo"] != msg.ID.String() {
		t.Fatal("reply not in actual thread")
	}
	page := decode[taskmodel.TaskNodeInboxPage](t, s.must(200, "GET", path+"/inbox-messages", nil))
	assertSchema("TaskNodeInboxPage", page)
	if len(page.Items) != 2 {
		t.Fatal("history lacks actual reply")
	}
	s.must(200, "GET", path+"/inbox-actions/"+receipt.ActionID.String(), nil)
	// Scope verification is against task turns, not the normal-Agent runs API.
	s.must(404, "GET", path+"/runs/"+uuid.NewString()+"/timeline", nil)
	timeline := decode[apitypes.TaskNodeRunTimeline](t, s.must(200, "GET", path+"/runs/"+active.ID.String()+"/timeline", nil))
	assertSchema("TaskNodeRunTimeline", timeline)
	if timeline.Run.ID != active.ID || timeline.NodeRunID != node.ID {
		t.Fatal("timeline scope")
	}
	gotInput, gotReply, gotRead := false, false, false
	for !(gotInput && gotReply && gotRead) {
		select {
		case line, ok := <-observed:
			if !ok {
				t.Fatal("SSE ended before Inbox projections")
			}
			var event struct {
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				RunLinks []taskmodel.TaskNodeInboxRunLink `json:"runLinks"`
			}
			if e := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); e != nil {
				t.Fatal("invalid SSE JSON", e)
			}
			gotInput = gotInput || event.Message.Text == "ACTUAL_INBOX_BODY"
			gotReply = gotReply || event.Message.Text == "ACTUAL_INBOX_REPLY"
			for _, link := range event.RunLinks {
				gotRead = gotRead || link.Role == "read"
			}
		case <-ctx.Done():
			t.Fatal("SSE missing actual Inbox state")
		}
	}
	detail := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if detail.Nodes[0].Question == nil || detail.Nodes[0].Question.CanAnswer {
		t.Fatal("ordinary Inbox expanded question answer permission")
	}
	q, e := s.a.Store.GetInboxMessage(ctx, question.ID, 160)
	if e != nil || q.Question == nil || q.Question.State != "open" || q.Question.Answer != nil {
		t.Fatal("ordinary strict Inbox answered Manager original question", e)
	}
	ordinary, e := s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/Worker#"+d.Task.ID.String(), "LEGACY_TASK_INBOX", "queue")
	if e != nil {
		t.Fatal("legacy task Inbox", e)
	}
	if _, e = s.a.API.Inbox.Read(ctx, p, ordinary.ID); e != nil {
		t.Fatal("legacy read now requires run", e)
	}
	legacy := decode[taskmodel.TaskNodeMessageReceipt](t, s.must(201, "POST", path+"/messages", taskConsoleInput(node, uuid.New(), "LEGACY_DIRECT_NODE", "queue")))
	if legacy.Message.Origin != "console_node" || legacy.Message.Text != "LEGACY_DIRECT_NODE" {
		t.Fatal("old messages endpoint reinterpreted")
	}
	if e = s.a.Executor.Interrupt(ctx, p.RuntimeID, active.ID); e != nil {
		t.Fatal("release held fake run", e)
	}
	taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool { return v.Task.Status == "blocked" })
	finalTimeline := decode[apitypes.TaskNodeRunTimeline](t, s.must(200, "GET", path+"/runs/"+active.ID.String()+"/timeline", nil))
	assertSchema("TaskNodeRunTimeline", finalTimeline)
	toolCalls := 0
	for _, entry := range finalTimeline.Entries {
		raw, _ := json.Marshal(entry)
		var item map[string]any
		json.Unmarshal(raw, &item)
		if item["type"] == "tool" {
			toolCalls++
			if item["state"] != "succeeded" {
				t.Fatal("actual tool outcome", item["state"])
			}
		}
	}
	if toolCalls != 1 {
		t.Fatal("actual tool start/end not coalesced", toolCalls)
	}
	t.Log("actual loopback App + newly-built loopctl + fakePi: real Inbox list/read/reply, task SSE state, action GET and scoped timeline; no providers")
}

func TestTaskNodeInboxAppBlockedRecoveryAndSleep(t *testing.T) {
	w := taskNodeInboxWorld(t)
	t.Setenv("FAKEPI_PROMPT_DELAY", "900ms")
	w.cfg.ExecEnvPassthrough = []string{"FAKEPI_PROMPT_DELAY"}
	s := w.start(nil)
	defer func() {
		if s != nil {
			s.stop()
		}
	}()
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(201, "POST", "/projects/"+dc.Project.ID.String()+"/credential-accounts", map[string]any{"purpose": "model", "provider": "anthropic", "name": "synthetic offline", "credential": map[string]string{"type": "api_key", "key": "synthetic-key"}})
	d, p := sleepTask(t, s, " !sleep=300ms !error=original_fixture_failure")
	blocked := taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool { return v.Task.Status == "blocked" })
	n := blocked.Nodes[0]
	path := taskConsoleRoute(d.Task.ID, n.ID)
	ctx := context.Background()
	if e := s.a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	receipt := decode[taskmodel.TaskNodeInboxReceipt](t, s.must(201, "POST", path+"/inbox-messages", taskConsoleInput(n, uuid.New(), "RECOVER_DURABLE_INBOX", "queue")))
	var message apitypes.InboxMessage
	if e := json.Unmarshal(receipt.Message, &message); e != nil {
		t.Fatal(e)
	}
	s.stop()
	s = nil
	s = w.start(nil)
	run := sleepWaitTurn(t, s, p.RuntimeID, uuid.Nil)
	if run.Origin != "inbox_reminder" || run.ExecutionMode != "task_conversation" || run.NodeRunID != n.ID {
		t.Fatal("recovered queued Inbox changed target/mode")
	}
	if _, e := s.a.API.Inbox.Read(ctx, p, message.ID, run.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.a.API.Inbox.Reply(ctx, p, message.ID, "actual service reply before sleep", "queue", inbox.QuestionInput{RunID: run.ID}); e != nil {
		t.Fatal(e)
	}
	first, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	sleeping := taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool {
		return len(v.SleepingInstances) == 1 && v.SleepingInstances[0].ID == first.Sleep.ID
	})
	wakeAt := *sleeping.SleepingInstances[0].WakeAt
	s.stop()
	s = nil
	s = w.start(nil)
	restored := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if restored.Task.Status != "blocked" || restored.Nodes[0].Attempt != 1 || len(restored.SleepingInstances) != 1 || !restored.SleepingInstances[0].WakeAt.Equal(wakeAt) {
		t.Fatal("restart changed original sleep state")
	}
	// Advance only the existing deadline in this test's private schema.
	if _, e = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=clock_timestamp()-interval '1 second' WHERE id=$1`, first.Sleep.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.a.Store.ReconcileSleeps(ctx, 100); e != nil {
		t.Fatal(e)
	}
	wake := sleepWaitTurn(t, s, p.RuntimeID, run.ID)
	if wake.NodeRunID != n.ID || wake.ExecutionMode != "task_conversation" || wake.Origin != "sleep_wake" {
		t.Fatal("wake lost strict binding")
	}
	second, e := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: wake.ID, Seconds: 120}, 1000)
	if e != nil {
		t.Fatal(e)
	}
	taskWait(t, s, d.Task.ID, func(v taskmodel.Detail) bool {
		return len(v.SleepingInstances) == 1 && v.SleepingInstances[0].ID == second.Sleep.ID
	})
	secondReceipt := decode[taskmodel.TaskNodeInboxReceipt](t, s.must(201, "POST", path+"/inbox-messages", taskConsoleInput(n, uuid.New(), "QUEUE_WAKES_ORIGINAL_INBOX", "queue")))
	if secondReceipt.NodeRunID != n.ID {
		t.Fatal("queue changed node")
	}
	carrier := sleepWaitTurn(t, s, p.RuntimeID, wake.ID)
	if carrier.NodeRunID != n.ID || carrier.ExecutionMode != "task_conversation" || carrier.Origin != "inbox_reminder" {
		t.Fatal("queue wake changed source")
	}
	var state string
	if e = s.a.Store.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, second.Sleep.ID).Scan(&state); e != nil || state != "woken" {
		t.Fatal("second sleep timer not cancelled", e)
	}
	final := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+d.Task.ID.String(), nil))
	if final.Task.Status != "blocked" || final.Nodes[0].Attempt != 1 {
		t.Fatal("Inbox resumed workflow")
	}
	page := decode[taskmodel.TaskNodeInboxPage](t, s.must(200, "GET", path+"/inbox-messages", nil))
	if len(page.Items) != 3 {
		t.Fatal("recovery lost original Inbox/reply")
	}
	t.Log("actual App normal shutdown/rebuild: queued Inbox survives, actual read/reply links, first sleep wakeAt unchanged, second sleep lineage and queue wake; fakePi, TEST-only deadline advancement, no SIGKILL/real models")
}
