package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/server/config"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/taskmodel"
)

// 手动驱动同版CLI通过实际App HTTP入口；fake Pi不自主调用CLI，不作为真实模型证明。
func sleepCLIState(t *testing.T, l *loopctl) sleepmodel.State {
	t.Helper()
	r := l.run("sleep", "status")
	r.ok(t)
	var body any
	if err := json.Unmarshal(r.Data, &body); err != nil {
		t.Fatal(err)
	}
	if err := clitest.Load(t, "agent-openapi.yaml").Components.Schemas["SleepState"].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal(err)
	}
	var state sleepmodel.State
	if err := json.Unmarshal(r.Data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func waitSleepCLI(t *testing.T, l *loopctl, fn func(sleepmodel.State) bool) sleepmodel.State {
	t.Helper()
	for until := time.Now().Add(8 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		state := sleepCLIState(t, l)
		if fn(state) {
			return state
		}
	}
	t.Fatal("sleep state timeout")
	return sleepmodel.State{}
}

// GET可以描述starting；fixture显式等running后再发送一次POST。
func waitRunningSleepCLI(t *testing.T, s *server, l *loopctl, task bool, exclude uuid.UUID) sleepmodel.State {
	t.Helper()
	return waitSleepCLI(t, l, func(st sleepmodel.State) bool {
		if st.CurrentRunID == nil || *st.CurrentRunID == exclude {
			return false
		}
		query := `SELECT status FROM runs WHERE id=$1`
		if task {
			query = `SELECT status FROM task_turns WHERE id=$1`
		}
		var status string
		if err := s.a.Store.Pool.QueryRow(context.Background(), query, *st.CurrentRunID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		return status == "running"
	})
}

func requestSleepCLI(t *testing.T, l *loopctl, run uuid.UUID, seconds int, reason string) ([]string, sleepmodel.Receipt) {
	t.Helper()
	a := []string{"sleep", "--run", run.String(), "--seconds", strconv.Itoa(seconds), "--action-id", uuid.NewString()}
	if reason != "" {
		a = append(a, "--reason", reason)
	}
	r := l.run(a...)
	r.ok(t)
	var body any
	json.Unmarshal(r.Data, &body)
	if err := clitest.Load(t, "agent-openapi.yaml").Components.Schemas["SleepReceipt"].Value.VisitJSON(body, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal(err)
	}
	var receipt sleepmodel.Receipt
	json.Unmarshal(r.Data, &receipt)
	if r.Meta.Attempts != 1 || !receipt.EndTurn || receipt.Sleep.State != "requested" || receipt.Sleep.WakeAt != nil {
		t.Fatalf("registration changed: %s", r.Stdout)
	}
	return a, receipt
}

func sleepFixtureConfig(c *config.Config) {
	c.SleepScanInterval = 50 * time.Millisecond
	c.SleepScanBatch = 100
}

func newSleepWorld(t *testing.T) *world {
	t.Helper()
	// Inbox摘要提醒不包含原正文directive；固定fake Pi本轮窗口，不改变生产超时。
	t.Setenv("FAKEPI_PROMPT_DELAY", "800ms")
	w := newWorld(t, false)
	w.cfg.ExecEnvPassthrough = append(w.cfg.ExecEnvPassthrough, "FAKEPI_PROMPT_DELAY")
	return w
}

func sleepNativeTurns(t *testing.T, path string, want int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	users := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var line struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if err = json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Message.Role == "user" {
			users++
		}
	}
	if err = sc.Err(); err != nil {
		t.Fatal(err)
	}
	if users != want {
		t.Fatalf("native fake Pi turns=%d want=%d", users, want)
	}
}

func TestSleepCLIRealAppRestart(t *testing.T) {
	for _, mode := range []string{"requested_invalidated", "sleeping_preserved"} {
		t.Run(mode, func(t *testing.T) {
			w := newSleepWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			s := w.start(func(c *config.Config) { c.PiIdleTimeout = 100 * time.Millisecond })
			defer func() {
				if s != nil {
					s.stop()
				}
			}()
			id := s.createAgent("sleep恢复")
			l := testTaskClient(t, s, id)
			s.console(202, "POST", "/agents/"+id+"/messages", map[string]any{"text": " !sleep=800ms !text=restore_source"})
			st := waitRunningSleepCLI(t, s, l, false, uuid.Nil)
			_, first := requestSleepCLI(t, l, *st.CurrentRunID, 3, " !sleep=800ms !text=restore_wake")
			var wakeAt *time.Time
			session := ""
			if mode == "sleeping_preserved" {
				st = waitSleepCLI(t, l, func(st sleepmodel.State) bool { return st.Sleep != nil && st.Sleep.State == "sleeping" })
				wakeAt = st.Sleep.WakeAt
				session = waitCLISessionPath(t, w, uuid.MustParse(id))
				for until := time.Now().Add(3 * time.Second); s.a.Executor.Process(uuid.MustParse(id)).State != "stopped" && time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
				}
				if s.a.Executor.Process(uuid.MustParse(id)).State != "stopped" {
					t.Fatal("fake Pi did not idle exit")
				}
			}
			s.stop()
			s = nil
			s = w.start(nil)
			l = testTaskClient(t, s, id)
			if mode == "sleeping_preserved" {
				st = sleepCLIState(t, l)
				if st.Sleep == nil || st.Sleep.ID != first.Sleep.ID || st.Sleep.WakeAt == nil || !st.Sleep.WakeAt.Equal(*wakeAt) {
					t.Fatal("restart reset sleep/wakeAt")
				}
				st = waitRunningSleepCLI(t, s, l, false, first.Sleep.SourceRunID)
				_, second := requestSleepCLI(t, l, *st.CurrentRunID, 120, "")
				waitSleepCLI(t, l, func(st sleepmodel.State) bool {
					return st.Sleep != nil && st.Sleep.ID == second.Sleep.ID && st.Sleep.State == "sleeping"
				})
				if got := waitCLISessionPath(t, w, uuid.MustParse(id)); got != session {
					t.Fatal("restart changed session")
				}
				sleepNativeTurns(t, session, 2)
			} else {
				s.waitIdle(id)
				if sleepCLIState(t, l).Sleep != nil {
					t.Fatal("requested activated after restart")
				}
			}
			var state string
			var timers, total int
			if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT state FROM agent_sleeps WHERE id=$1`, first.Sleep.ID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE origin='sleep_wake'),count(*) FROM runs WHERE agent_id=$1`, uuid.MustParse(id)).Scan(&timers, &total); err != nil {
				t.Fatal(err)
			}
			wantState, wantTimers, wantTotal := "cancelled", 0, 1
			if mode == "sleeping_preserved" {
				wantState, wantTimers, wantTotal = "woken", 1, 2
			}
			if state != wantState || timers != wantTimers || total != wantTotal {
				t.Fatalf("state=%s timers=%d total=%d", state, timers, total)
			}
			t.Logf("mode=%s sleep=%s originalWakeAt=%v timers=%d runs=%d schema=%s Redis=%s", mode, first.Sleep.ID, wakeAt, timers, total, w.schema, w.cfg.RedisKeyPrefix)
		})
	}
}

func TestSleepCLIRealAppNormal(t *testing.T) {
	for _, mode := range []string{"queued_before_settled", "early_queue", "deadline_resleep"} {
		t.Run(mode, func(t *testing.T) {
			w := newSleepWorld(t)
			s := w.start(sleepFixtureConfig)
			defer s.stop()
			id := s.createAgent("sleep常态")
			l := testTaskClient(t, s, id)
			s.console(202, "POST", "/agents/"+id+"/messages", map[string]any{"text": " !sleep=800ms !text=source"})
			st := waitRunningSleepCLI(t, s, l, false, uuid.Nil)
			seconds := 120
			if mode == "deadline_resleep" || mode == "early_queue" {
				seconds = 1
			}
			a, first := requestSleepCLI(t, l, *st.CurrentRunID, seconds, " !sleep=800ms !text=timer")
			if status := s.console(200, "GET", "/agents/"+id+"/status", nil); status["busy"] != true || status["sleep"] != nil {
				t.Fatal("requested falsely idle", status)
			}
			l.run(append(append([]string{}, a...), "--reason", "changed")...).fails(t, "invalid_argument", 2)
			changed := append([]string{}, a...)
			changed[4] = "121"
			l.run(changed...).fails(t, "sleep_conflict", 8)
			var originalWake *time.Time
			if mode == "queued_before_settled" {
				s.console(202, "POST", "/agents/"+id+"/messages", map[string]any{"text": "real queued work", "whenBusy": "queue"})
			} else {
				st = waitSleepCLI(t, l, func(st sleepmodel.State) bool { return st.Sleep != nil && st.Sleep.State == "sleeping" })
				if st.CurrentRunID != nil || st.Sleep.ID != first.Sleep.ID || st.Sleep.WakeAt == nil {
					t.Fatal(st)
				}
				originalWake = st.Sleep.WakeAt
				if mode == "early_queue" {
					s.console(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/sleep常态", "text": "real Inbox wakes", "whenBusy": "queue"})
				} else {
					st = waitRunningSleepCLI(t, s, l, false, first.Sleep.SourceRunID)
					_, second := requestSleepCLI(t, l, *st.CurrentRunID, 120, "")
					if second.Sleep.Revision <= first.Sleep.Revision {
						t.Fatal("scope revision reset")
					}
					waitSleepCLI(t, l, func(st sleepmodel.State) bool {
						return st.Sleep != nil && st.Sleep.ID == second.Sleep.ID && st.Sleep.State == "sleeping"
					})
				}
			}
			s.waitIdle(id)
			if mode == "early_queue" {
				if delay := time.Until(*originalWake) + 100*time.Millisecond; delay > 0 {
					time.Sleep(delay)
				}
			}
			sleepNativeTurns(t, waitCLISessionPath(t, w, uuid.MustParse(id)), 2)
			var replay sleepmodel.Receipt
			r := l.run(a...)
			r.ok(t)
			json.Unmarshal(r.Data, &replay)
			if !reflect.DeepEqual(replay, first) {
				t.Fatal("original receipt changed after source round")
			}
			l.run("sleep", "--run", first.Sleep.SourceRunID.String(), "--seconds", "1", "--action-id", uuid.NewString()).fails(t, "sleep_conflict", 8)
			var timers, total int
			var state, reason string
			var carrier *uuid.UUID
			if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT state,end_reason,wake_run_id FROM agent_sleeps WHERE id=$1`, first.Sleep.ID).Scan(&state, &reason, &carrier); err != nil {
				t.Fatal(err)
			}
			if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE origin='sleep_wake'),count(*) FROM runs WHERE agent_id=$1`, uuid.MustParse(id)).Scan(&timers, &total); err != nil {
				t.Fatal(err)
			}
			wantTimers, wantReason := 0, "queued_work"
			if mode == "deadline_resleep" {
				wantTimers, wantReason = 1, "deadline"
			}
			if state != "woken" || reason != wantReason || carrier == nil || timers != wantTimers || total != 2 {
				t.Fatalf("state=%s reason=%s carrier=%v timers=%d total=%d", state, reason, carrier, timers, total)
			}
			page := s.console(200, "GET", "/agents/"+id+"/messages", nil)
			if err := clitest.Load(t, "openapi.yaml").Components.Schemas["MessagePage"].Value.VisitJSON(page, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatal("actual message wire schema", err)
			}
			wakeMessages, linked := 0, 0
			for _, item := range page["messages"].([]any) {
				message := item.(map[string]any)
				if _, exists := message["wakeSleepId"]; exists {
					t.Fatal("unexpected top-level wakeSleepId")
				}
				if message["role"] == "user" && message["origin"] == "sleep_wake" {
					wakeMessages++
				}
				if run, ok := message["run"].(map[string]any); ok && run["id"] == carrier.String() && run["wakeSleepId"] == first.Sleep.ID.String() {
					linked++
				}
			}
			if wakeMessages != wantTimers || linked == 0 {
				t.Fatal("wake origin or Run wakeSleepId lost", wakeMessages, linked)
			}
			t.Logf("mode=%s sleep=%s wakeRun=%s end=%s timers=%d runs=%d schema=%s Redis=%s", mode, first.Sleep.ID, *carrier, reason, timers, total, w.schema, w.cfg.RedisKeyPrefix)
		})
	}
}

func TestSleepCLIRealAppTaskModes(t *testing.T) {
	for _, mode := range []string{"task_node", "blocked_conversation"} {
		t.Run(mode, func(t *testing.T) {
			w := newSleepWorld(t)
			s := w.start(sleepFixtureConfig)
			defer s.stop()
			_, manager := newTaskManager(t, s, "sleepManager")
			worker := s.createAgent("sleepWorker")
			description := "!sleep=800ms !text=task"
			if mode == "blocked_conversation" {
				description += " !error=fixture"
			}
			created := taskCLIInput(t, manager, taskTextRequest(worker, description), "task", "create")
			id := created["task"].(map[string]any)["id"].(string)
			node := currentCLINode(t, waitRunningCLITask(t, s, id))
			l := testTaskClient(t, s, node.RuntimeID.String())
			exclude := uuid.Nil
			if mode == "blocked_conversation" {
				exclude = node.RunIDs[len(node.RunIDs)-1]
				waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
				s.console(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/sleepWorker#" + id, "text": " !sleep=800ms !text=ordinary"})
			}
			st := waitRunningSleepCLI(t, s, l, true, exclude)
			questionID := uuid.Nil
			if mode == "task_node" {
				q := l.run("inbox", "send", "@console", "--text", "still open", "--option", "yes", "--node-run", node.ID.String()).ok(t)
				questionID = uuid.MustParse(q["id"].(string))
			}
			_, first := requestSleepCLI(t, l, *st.CurrentRunID, 1, " !sleep=800ms !text=task_timer")
			waitSleepCLI(t, l, func(st sleepmodel.State) bool { return st.Sleep != nil && st.Sleep.State == "sleeping" })
			d := waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
			wantStatus := "waiting"
			if mode == "blocked_conversation" {
				wantStatus = "blocked"
			}
			if d.Task.Status != wantStatus || (mode == "task_node") != (d.Nodes[0].Sleep != nil) {
				t.Fatal("sleep crossed workflow boundary", d.Task.Status)
			}
			inst, err := s.a.Store.TaskInstance(context.Background(), node.RuntimeID)
			if err != nil || inst.SessionFile == nil {
				t.Fatal("missing saved task session", err)
			}
			session := *inst.SessionFile
			st = waitRunningSleepCLI(t, s, l, true, first.Sleep.SourceRunID)
			_, second := requestSleepCLI(t, l, *st.CurrentRunID, 120, "")
			waitSleepCLI(t, l, func(st sleepmodel.State) bool {
				return st.Sleep != nil && st.Sleep.ID == second.Sleep.ID && st.Sleep.State == "sleeping"
			})
			inst, err = s.a.Store.TaskInstance(context.Background(), node.RuntimeID)
			if err != nil || inst.SessionFile == nil || *inst.SessionFile != session {
				t.Fatal("task session replaced", err)
			}
			d = waitCLITask(t, s, id, func(d taskmodel.Detail) bool {
				return len(d.SleepingInstances) == 1 && d.SleepingInstances[0].ID == second.Sleep.ID
			})
			if d.Task.Status != wantStatus || len(d.Nodes) != 1 || second.Sleep.Revision <= first.Sleep.Revision {
				t.Fatal("workflow advanced or revision reset")
			}
			wantTurns := 2
			if mode == "blocked_conversation" {
				wantTurns = 3
			}
			sleepNativeTurns(t, session, wantTurns)
			if mode == "task_node" {
				q, err := s.a.Store.GetInboxMessage(context.Background(), questionID, 200)
				if err != nil || q.Question == nil || q.Question.State != "open" {
					t.Fatal("sleep changed original question", err)
				}
			}
			history := l.run("task", "history", id, node.ID.String()).ok(t)
			if err := clitest.Load(t, "agent-openapi.yaml").Components.Schemas["TaskMessagePage"].Value.VisitJSON(history, openapi3.EnableJSONSchema2020()); err != nil {
				t.Fatal("actual task history wire schema", err)
			}
			var timers int
			if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, node.RuntimeID).Scan(&timers); err != nil || timers != 1 {
				t.Fatal("duplicate timer", timers, err)
			}
			cancelCLITask(t, s, manager, id)
			l.run("sleep", "status").fails(t, "unauthenticated", 5)
			var active int
			if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM agent_sleeps WHERE task_id=$1 AND state IN ('requested','sleeping')`, uuid.MustParse(id)).Scan(&active); err != nil || active != 0 {
				t.Fatal("cancel retained effective sleep", active, err)
			}
			t.Logf("mode=%s first=%s second=%s one timer; session retained; task cancelled; schema=%s Redis=%s", mode, first.Sleep.ID, second.Sleep.ID, w.schema, w.cfg.RedisKeyPrefix)
		})
	}
}
