package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/config"
	"loop-now/internal/server/contracttest"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/sleepmodel"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSleepAppNormalQueueDeadlineAndRestart(t *testing.T) {
	for _, mode := range []string{"queued-before-settled", "sleeping-restart-before", "sleeping-restart-after", "requested-restart"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			s := w.start(func(c *config.Config) { c.PiIdleTimeout = 100 * time.Millisecond })
			defer func() {
				if s != nil {
					s.stop()
				}
			}()
			path, agent := s.createAgent("SleepAgent")
			s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=800ms !text=sleep_source"})
			run := s.waitRunning(path)
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID}
			seconds := int64(120)
			if mode == "sleeping-restart-before" {
				seconds = 2
			}
			if mode == "sleeping-restart-after" {
				seconds = 1
			}
			receipt, err := s.a.Store.RequestSleep(context.Background(), p, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: seconds}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if st := decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil)); !st.Busy || st.Sleep != nil {
				t.Fatal("requested projected as sleeping", st)
			}
			if mode == "queued-before-settled" {
				s.must(202, "POST", path+"/messages", map[string]any{"text": "real next", "whenBusy": "queue"})
			}
			if mode == "requested-restart" {
				s.stop()
				s = w.start(nil)
				st := s.waitIdle(path)
				if st.Sleep != nil {
					t.Fatal("requested activated after restart")
				}
				var state string
				if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT state FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state); err != nil || state != "cancelled" {
					t.Fatal(state, err)
				}
			} else {
				st := s.waitIdle(path)
				if mode == "queued-before-settled" {
					if st.Sleep != nil {
						t.Fatal("queued work delayed by sleep")
					}
				} else {
					if st.Sleep == nil || st.Sleep.ID != receipt.Sleep.ID || st.Busy {
						t.Fatal("not sleeping", st)
					}
					originalWake := *st.Sleep.WakeAt
					// Let the real idle reaper exit Pi before stopping this owned App.
					limit := time.Now().Add(3 * time.Second)
					for s.a.Executor.Process(agent.ID).State != "stopped" && time.Now().Before(limit) {
						time.Sleep(10 * time.Millisecond)
					}
					if s.a.Executor.Process(agent.ID).State != "stopped" {
						t.Fatal("idle Pi stayed resident")
					}
					s.stop()
					s = nil
					if mode == "sleeping-restart-after" {
						time.Sleep(time.Until(originalWake) + 20*time.Millisecond)
					}
					s = w.start(nil)
					if mode == "sleeping-restart-before" {
						st = decode[apitypes.AgentStatus](t, s.must(200, "GET", path+"/status", nil))
						if st.Sleep == nil || !st.Sleep.WakeAt.Equal(originalWake) {
							t.Fatal("restart reset wakeAt", st)
						}
					}
					limit = time.Now().Add(6 * time.Second)
					for time.Now().Before(limit) {
						var done int
						err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='sleep_wake' AND status='completed'`, agent.ID).Scan(&done)
						if err != nil {
							t.Fatal(err)
						}
						if done == 1 {
							break
						}
						time.Sleep(20 * time.Millisecond)
					}
					s.waitIdle(path)
				}
			}
			var timers, total int
			if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE origin='sleep_wake'),count(*) FROM runs WHERE agent_id=$1`, agent.ID).Scan(&timers, &total); err != nil {
				t.Fatal(err)
			}
			wantTimers, wantTotal := 1, 2
			if mode == "queued-before-settled" {
				wantTimers = 0
			}
			if mode == "requested-restart" {
				wantTimers, wantTotal = 0, 1
			}
			if timers != wantTimers || total != wantTotal {
				t.Fatalf("timer=%d total=%d want=%d/%d", timers, total, wantTimers, wantTotal)
			}
			var recordedWake *time.Time
			var carrier *uuid.UUID
			var state string
			if err = s.a.Store.Pool.QueryRow(context.Background(), `SELECT state,wake_at,wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state, &recordedWake, &carrier); err != nil {
				t.Fatal(err)
			}
			session, err := s.a.Store.GetAgentSession(context.Background(), agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			prompts := sleepNativePrompts(t, session.SessionFile)
			if prompts != wantTotal {
				t.Fatal("normal source/wake replayed", prompts, wantTotal)
			}
			t.Logf("case=normal-%s sleepId=%s originalWakeAt=%v wakeRunId=%v state=%s PiPrompts=%d timers=%d", mode, receipt.Sleep.ID, recordedWake, carrier, state, prompts, timers)
		})
	}
}

func TestSleepAppTaskNodeAndBlockedConversation(t *testing.T) {
	t.Setenv("FAKEPI_PROMPT_DELAY", "800ms")
	for _, mode := range []string{"task_node", "task_conversation"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			w.cfg.ExecEnvPassthrough = append(w.cfg.ExecEnvPassthrough, "FAKEPI_PROMPT_DELAY")
			s := w.start(nil)
			defer s.stop()
			ctx := context.Background()
			_, manager := s.createAgent("Manager")
			_, agent := s.createAgent("Worker")
			dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
			s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
			description := " !sleep=800ms !text=first"
			if mode == "task_conversation" {
				description += " !error=fixture"
			}
			raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "sleep task", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: description, AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
			if err != nil {
				t.Fatal(err)
			}
			created := decode[taskmodel.Detail](t, raw)
			node := created.Nodes[0]
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
			// Keep a genuine unanswered question throughout sleep and timer continuation.
			taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return s.a.Executor.Process(node.RuntimeID).State == "busy" })
			question, err := s.a.API.Inbox.Send(ctx, p, "@console", "still unanswered", "queue", inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "task_conversation" {
				taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
				_, err = s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/Worker#"+created.Task.ID.String(), " !sleep=800ms !text=ordinary", "queue")
				if err != nil {
					t.Fatal(err)
				}
			}
			waitRun := func(exclude uuid.UUID) uuid.UUID {
				limit := time.Now().Add(8 * time.Second)
				for time.Now().Before(limit) {
					var id uuid.UUID
					err := s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running' AND id<>$2 ORDER BY queue_seq DESC LIMIT 1`, node.RuntimeID, exclude).Scan(&id)
					if err == nil {
						return id
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("no running continuation")
				return uuid.Nil
			}
			run := waitRun(uuid.Nil)
			reason := " !sleep=800ms !text=timer"
			first, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: run, Seconds: 1, Reason: &reason}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			sleeping := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return len(d.SleepingInstances) == 1 })
			if mode == "task_node" && (sleeping.Task.Status != "waiting" || sleeping.Nodes[0].Sleep == nil) {
				t.Fatal("node not waiting")
			}
			if mode == "task_conversation" && (sleeping.Task.Status != "blocked" || sleeping.Nodes[0].Sleep != nil) {
				t.Fatal("ordinary sleep advanced blocked node")
			}
			inst, err := s.a.Store.TaskInstance(ctx, node.RuntimeID)
			if err != nil || inst.SessionFile == nil {
				t.Fatal("task session not saved", err)
			}
			session := *inst.SessionFile
			nextRun := waitRun(run)
			second, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: nextRun, Seconds: 120}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if second.Sleep.Revision <= first.Sleep.Revision {
				t.Fatal("scope revision reset")
			}
			taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
				return len(d.SleepingInstances) == 1 && d.SleepingInstances[0].ID == second.Sleep.ID
			})
			inst, err = s.a.Store.TaskInstance(ctx, node.RuntimeID)
			if err != nil || inst.SessionFile == nil || *inst.SessionFile != session {
				t.Fatal("task session changed", err)
			}
			q, err := s.a.Store.GetInboxMessage(ctx, question.ID, 200)
			if err != nil || q.Question.State != "open" {
				t.Fatal("timer answered/closed question", err)
			}
			var timers int
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, node.RuntimeID).Scan(&timers); err != nil || timers != 1 {
				t.Fatal("timer duplicate", timers, err)
			}
			if _, err = s.a.Tasks.Cancel(ctx, auth.Console(), created.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
			final := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
			if len(final.SleepingInstances) != 0 {
				t.Fatal("terminal sleep survived")
			}
		})
	}
}

func TestSleepAppWakeCommitExecutionAndSessionFailure(t *testing.T) {
	for _, mode := range []string{"wake-queued-restart", "wake-running-restart", "session-save-failure", "redis-cleared"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			s := w.start(nil)
			defer func() {
				if s != nil {
					s.stop()
				}
			}()
			ctx := context.Background()
			path, agent := s.createAgent("RecoveryAgent")
			s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=800ms !text=source"})
			run := s.waitRunning(path)
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID}
			reason := " !sleep=2s !text=continued"
			receipt, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120, Reason: &reason}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "session-save-failure" {
				if _, err = s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION reject_session_save() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'offline injected'; END $$; CREATE TRIGGER reject_session_save BEFORE UPDATE OF session_file ON agent_sessions FOR EACH ROW EXECUTE FUNCTION reject_session_save()`); err != nil {
					t.Fatal(err)
				}
				s.waitIdle(path)
				ended, err := s.a.Store.GetRun(ctx, run.ID)
				if err != nil || ended.Status != "failed" {
					t.Fatal("session fault presented as sleep", ended, err)
				}
				var state string
				if err = s.a.Store.Pool.QueryRow(ctx, `SELECT state FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state); err != nil || state != "cancelled" {
					t.Fatal(state, err)
				}
				t.Logf("case=%s sleepId=%s sourceRunId=%s wakeAt=null wakeRunId=null PiPrompts=1", mode, receipt.Sleep.ID, run.ID)
				return
			}
			st := s.waitIdle(path)
			if st.Sleep == nil {
				t.Fatal("sleep absent")
			}
			originalWake := *st.Sleep.WakeAt
			barrier := "offline-test-owner"
			if mode == "wake-queued-restart" {
				ok, e := s.a.Coord.AcquireIfIdle(ctx, agent.ID.String(), barrier)
				if e != nil || !ok {
					t.Fatal("barrier", e)
				}
			}
			if mode == "redis-cleared" {
				keys, err := s.a.Redis.Keys(ctx, w.cfg.RedisKeyPrefix+"*").Result()
				if err != nil {
					t.Fatal(err)
				}
				if len(keys) > 0 {
					if err = s.a.Redis.Del(ctx, keys...).Err(); err != nil {
						t.Fatal(err)
					}
				}
				current, err := s.a.Store.SleepState(ctx, p)
				if err != nil || current.Sleep == nil || !current.Sleep.WakeAt.Equal(originalWake) {
					t.Fatal("Redis loss reset durable sleep", err)
				}
			}
			if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.a.Store.ReconcileSleeps(ctx, 100); err != nil {
				t.Fatal(err)
			}
			var wakeID uuid.UUID
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&wakeID); err != nil {
				t.Fatal(err)
			}
			if mode == "wake-queued-restart" {
				wake, err := s.a.Store.GetRun(ctx, wakeID)
				if err != nil || wake.Status != "queued" {
					t.Fatal("barrier did not hold queued wake", err)
				}
				s.stop()
				s = nil
				// Redis client is closed by stop. Remove the owned stale barrier via the next App's PG reconciliation after TTL.
				s = w.start(nil)
				if _, err = s.a.Coord.Release(ctx, agent.ID.String(), barrier); err != nil {
					t.Fatal(err)
				}
			} else if mode == "wake-running-restart" {
				limit := time.Now().Add(4 * time.Second)
				for time.Now().Before(limit) {
					wake, e := s.a.Store.GetRun(ctx, wakeID)
					if e == nil && wake.Status == "running" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				wake, err := s.a.Store.GetRun(ctx, wakeID)
				if err != nil || wake.Status != "running" {
					t.Fatal("wake never ran", err)
				}
				s.stop()
				s = w.start(nil)
			}
			s.waitIdle(path)
			wake, err := s.a.Store.GetRun(ctx, wakeID)
			if err != nil {
				t.Fatal(err)
			}
			expected := "completed"
			if mode == "wake-running-restart" {
				expected = "interrupted"
			}
			if wake.Status != expected {
				t.Fatal("wake recovery semantics", wake.Status, expected)
			}
			var n int
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='sleep_wake'`, agent.ID).Scan(&n); err != nil || n != 1 {
				t.Fatal("wake replayed", n, err)
			}
			sess, err := s.a.Store.GetAgentSession(ctx, agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(sess.SessionFile)
			if err != nil {
				t.Fatal(err)
			}
			prompts := 0
			for _, line := range bytes.Split(data, []byte("\n")) {
				var entry struct {
					Message struct {
						Role string `json:"role"`
					} `json:"message"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
					prompts++
				}
			}
			if prompts != 2 {
				t.Fatal("unexpected Pi prompt count", prompts)
			}
			events, err := s.a.Coord.ReadEvents(ctx, agent.ID.String(), "0-0", 1000, -time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			timerUser := 0
			validator := contracttest.New(t, strings.TrimSuffix(s.url, "/api/v1"))
			for _, event := range events {
				if event.Type != "message.created" {
					continue
				}
				validator.ValidateSSE(event.Type, event.Data)
				var created apitypes.SseMessageCreated
				if json.Unmarshal(event.Data, &created) == nil && created.Message.Role == "user" && created.Message.Origin == "sleep_wake" && created.Message.Run.ID == wakeID {
					timerUser++
					if created.Message.Run.WakeSleepID == nil || *created.Message.Run.WakeSleepID != receipt.Sleep.ID {
						t.Fatal("timer SSE reference missing")
					}
				}
			}
			if timerUser != 1 {
				t.Fatal("actual dispatched timer user SSE missing/duplicated", timerUser)
			}
			t.Logf("case=%s sleepId=%s originalWakeAt=%s wakeRunId=%s PiPrompts=%d wakeStatus=%s", mode, receipt.Sleep.ID, originalWake.Format(time.RFC3339Nano), wakeID, prompts, wake.Status)
		})
	}
}

func TestSleepAppInboxReadBeforeDispatchStillContinues(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	path, agent := s.createAgent("UnreadWake")
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=600ms !text=source"})
	run := s.waitRunning(path)
	receipt, err := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if st := s.waitIdle(path); st.Sleep == nil {
		t.Fatal("source not sleeping")
	}
	// Deterministically reproduce a recipient read before the notification driver
	// can count unread mail; the real message and reminder transaction still runs.
	if _, err = s.a.Store.Pool.Exec(ctx, `CREATE FUNCTION read_before_dispatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.to_agent_id='`+agent.ID.String()+`'::uuid THEN NEW.read_at=now(); END IF; RETURN NEW; END $$; CREATE TRIGGER read_before_dispatch BEFORE INSERT ON inbox_messages FOR EACH ROW EXECUTE FUNCTION read_before_dispatch()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/UnreadWake", "valid real message", "queue"); err != nil {
		t.Fatal(err)
	}
	if st := s.waitIdle(path); st.Sleep != nil {
		t.Fatal("Inbox did not wake")
	}
	var reminders, read int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='inbox_reminder' AND status='completed' AND wake_sleep_id=$2`, agent.ID, receipt.Sleep.ID).Scan(&reminders); err != nil || reminders != 1 {
		t.Fatal(reminders, err)
	}
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE to_agent_id=$1 AND read_at IS NOT NULL`, agent.ID).Scan(&read); err != nil || read != 1 {
		t.Fatal(read, err)
	}
	sess, err := s.a.Store.GetAgentSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sess.SessionFile)
	if err != nil {
		t.Fatal(err)
	}
	prompts := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Message struct {
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
			prompts++
			if prompts == 2 && len(entry.Message.Content) > 0 && !strings.Contains(entry.Message.Content[0].Text, "sleep已因新工作结束") {
				t.Fatal("continuation omitted sleep-ended notice")
			}
		}
	}
	if prompts != 2 {
		t.Fatal("zero unread skipped real continuation", prompts)
	}
}

func TestSleepAppQuestionAnswerAndExpiryWake(t *testing.T) {
	for _, mode := range []string{"answer", "expiry"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			s := w.start(nil)
			defer s.stop()
			ctx := context.Background()
			_, manager := s.createAgent("Manager")
			_, agent := s.createAgent("Worker")
			dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
			s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
			raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "question wakes sleep", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=800ms !text=source", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
			if err != nil {
				t.Fatal(err)
			}
			d := decode[taskmodel.Detail](t, raw)
			node := d.Nodes[0]
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
			taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return s.a.Executor.Process(node.RuntimeID).State == "busy" })
			var source uuid.UUID
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, node.RuntimeID).Scan(&source); err != nil {
				t.Fatal(err)
			}
			q, err := s.a.API.Inbox.Send(ctx, p, "@console", "question", "queue", inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID})
			if err != nil {
				t.Fatal(err)
			}
			sleep, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source, Seconds: 120}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
			if mode == "answer" {
				s.must(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
			} else {
				if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, q.ID); err != nil {
					t.Fatal(err)
				}
				if err = s.a.API.Inbox.ReconcileQuestions(ctx); err != nil {
					t.Fatal(err)
				}
			}
			taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
			var turns, timers int
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE origin='inbox_reminder' AND wake_sleep_id=$2),count(*) FILTER(WHERE origin='sleep_wake') FROM task_turns WHERE runtime_id=$1`, node.RuntimeID, sleep.Sleep.ID).Scan(&turns, &timers); err != nil || turns != 1 || timers != 0 {
				t.Fatal("question wake duplicate/lost", turns, timers, err)
			}
			if state, err := s.a.Store.SleepState(ctx, p); err != nil || state.Sleep != nil {
				t.Fatal("question callback did not end sleep", err)
			}
			if _, err = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSleepAppTaskRequestedAndSleepingRestart(t *testing.T) {
	for _, mode := range []string{"requested", "sleeping-before", "sleeping-after"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			w.cfg.SleepScanInterval = 50 * time.Millisecond
			w.cfg.SleepScanBatch = 100
			s := w.start(nil)
			defer func() {
				if s != nil {
					s.stop()
				}
			}()
			ctx := context.Background()
			_, manager := s.createAgent("Manager")
			_, agent := s.createAgent("Worker")
			dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
			s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
			raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "task sleep restart", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=800ms !text=source", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
			if err != nil {
				t.Fatal(err)
			}
			d := decode[taskmodel.Detail](t, raw)
			node := d.Nodes[0]
			p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
			taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return s.a.Executor.Process(node.RuntimeID).State == "busy" })
			var source uuid.UUID
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, node.RuntimeID).Scan(&source); err != nil {
				t.Fatal(err)
			}
			seconds := int64(2)
			if mode == "sleeping-after" {
				seconds = 1
			}
			receipt, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: source, Seconds: seconds}, 1000)
			if err != nil {
				t.Fatal(err)
			}
			var wakeAt *time.Time
			if mode != "requested" {
				sleeping := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
				wakeAt = sleeping.Nodes[0].Sleep.WakeAt
			}
			s.stop()
			s = nil
			if mode == "sleeping-after" {
				time.Sleep(time.Until(*wakeAt) + 20*time.Millisecond)
			}
			s = w.start(nil)
			if mode == "requested" {
				after := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
				if after.Nodes[0].Sleep != nil {
					t.Fatal("unfinished intent activated")
				}
			} else {
				if mode == "sleeping-before" {
					after, err := s.a.Store.SleepState(ctx, p)
					if err != nil || after.Sleep == nil || !after.Sleep.WakeAt.Equal(*wakeAt) {
						t.Fatal("task wakeAt reset", err)
					}
				}
				limit := time.Now().Add(6 * time.Second)
				for time.Now().Before(limit) {
					var complete bool
					err = s.a.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake' AND status='completed')`, node.RuntimeID).Scan(&complete)
					if err != nil {
						t.Fatal(err)
					}
					if complete {
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			var timers int
			var wakeID *uuid.UUID
			var state string
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT state,wake_run_id FROM agent_sleeps WHERE id=$1`, receipt.Sleep.ID).Scan(&state, &wakeID); err != nil {
				t.Fatal(err)
			}
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, node.RuntimeID).Scan(&timers); err != nil {
				t.Fatal(err)
			}
			want := 1
			if mode == "requested" {
				want = 0
				if state != "cancelled" || wakeID != nil {
					t.Fatal("requested recovery", state, wakeID)
				}
			} else if state != "woken" || wakeID == nil {
				t.Fatal("sleeping recovery", state, wakeID)
			}
			if timers != want {
				t.Fatal("task timer duplicated/lost", timers, want)
			}
			inst, err := s.a.Store.TaskInstance(ctx, node.RuntimeID)
			if err != nil || inst.SessionFile == nil {
				t.Fatal("task session absent", err)
			}
			data, err := os.ReadFile(*inst.SessionFile)
			if err != nil {
				t.Fatal(err)
			}
			prompts := 0
			for _, line := range bytes.Split(data, []byte("\n")) {
				var entry struct {
					Message struct {
						Role string `json:"role"`
					} `json:"message"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
					prompts++
				}
			}
			if prompts != 1+want {
				t.Fatal("old task tools replayed", prompts)
			}
			t.Logf("case=task-%s sleepId=%s originalWakeAt=%v wakeRunId=%v PiPrompts=%d", mode, receipt.Sleep.ID, wakeAt, wakeID, prompts)
			if _, err = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSleepAppOldAttemptCannotAffectNewSleep(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	_, manager := s.createAgent("Manager")
	_, agent := s.createAgent("Worker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "sleep attempts", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: " !sleep=800ms !text=source", AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	d := decode[taskmodel.Detail](t, raw)
	node := d.Nodes[0]
	p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
	waitRun := func() uuid.UUID {
		limit := time.Now().Add(6 * time.Second)
		for time.Now().Before(limit) {
			var id uuid.UUID
			if err := s.a.Store.Pool.QueryRow(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status='running'`, node.RuntimeID).Scan(&id); err == nil {
				return id
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("no source running")
		return uuid.Nil
	}
	firstRun := waitRun()
	firstAction := uuid.New()
	firstInput := sleepmodel.Input{ActionID: firstAction, RunID: firstRun, Seconds: 120}
	first, err := s.a.Store.RequestSleep(ctx, p, firstInput, 1000)
	if err != nil {
		t.Fatal(err)
	}
	taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool { return d.Nodes[0].Sleep != nil })
	if _, err = s.a.Tasks.Fail(ctx, p, d.Task.ID, node.ID, taskmodel.FailInput{ActionID: uuid.New(), Reason: "explicit failure"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Tasks.Retry(ctx, auth.Console(), d.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: node.ID}); err != nil {
		t.Fatal(err)
	}
	secondRun := waitRun()
	second, err := s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: secondRun, Seconds: 120}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	current := taskWait(t, s, d.Task.ID, func(d taskmodel.Detail) bool {
		return len(d.SleepingInstances) == 1 && d.SleepingInstances[0].ID == second.Sleep.ID
	})
	if current.Task.CurrentNodeRunID == nil || *current.Task.CurrentNodeRunID == node.ID || second.Sleep.Revision <= first.Sleep.Revision {
		t.Fatal("retry did not create independent binding")
	}
	beforeRevision := current.SleepingInstances[0].Revision
	beforeWake := *current.SleepingInstances[0].WakeAt
	// Inject a late storage finalization for the old source, using its real IDs.
	// This is a Tx regression, not a claim that Pi itself sent a late RPC frame.
	if _, err = s.a.Store.MutateTask(ctx, d.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		waiting, e := s.a.Store.FinishSleepTx(ctx, m.Tx, m, firstRun, "completed")
		if waiting {
			t.Error("old source reactivated sleep")
		}
		return nil, e
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, first.Sleep.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	after, err := s.a.Store.GetTask(ctx, d.Task.ID)
	if err != nil || len(after.View.SleepingInstances) != 1 || after.View.SleepingInstances[0].ID != second.Sleep.ID || after.View.SleepingInstances[0].Revision != beforeRevision || !after.View.SleepingInstances[0].WakeAt.Equal(beforeWake) {
		t.Fatal("old attempt changed new sleep", err)
	}
	if _, err = s.a.Store.RequestSleep(ctx, p, sleepmodel.Input{ActionID: uuid.New(), RunID: firstRun, Seconds: 1}, 1000); err == nil {
		t.Fatal("late old-run request accepted")
	}
	replay, err := s.a.Store.RequestSleep(ctx, p, firstInput, 1000)
	if err != nil || replay.Sleep.ID != first.Sleep.ID || replay.Sleep.State != "requested" {
		t.Fatal("old immutable receipt lost", err)
	}
	after, err = s.a.Store.GetTask(ctx, d.Task.ID)
	if err != nil || len(after.View.SleepingInstances) != 1 || after.View.SleepingInstances[0].ID != second.Sleep.ID || after.View.SleepingInstances[0].Revision != beforeRevision || !after.View.SleepingInstances[0].WakeAt.Equal(beforeWake) {
		t.Fatal("late old registration changed new sleep", err)
	}
	inst, err := s.a.Store.TaskInstance(ctx, p.RuntimeID)
	if err != nil || inst.SessionFile == nil {
		t.Fatal(err)
	}
	if n := sleepNativePrompts(t, *inst.SessionFile); n != 2 {
		t.Fatal("old callback replayed source", n)
	}
	var timers int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE runtime_id=$1 AND origin='sleep_wake'`, p.RuntimeID).Scan(&timers); err != nil || timers != 0 {
		t.Fatal("old callback dispatched timer", timers, err)
	}
	t.Logf("case=old-attempt/late-callback/registration oldSleepId=%s newSleepId=%s originalWakeAt=%s currentRevision=%d wakeRunId=null PiPrompts=2 timers=0", first.Sleep.ID, second.Sleep.ID, beforeWake.Format(time.RFC3339Nano), beforeRevision)
	if _, err = s.a.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
		t.Fatal(err)
	}
}

type sleepMirrorFailure struct {
	key     string
	enabled atomic.Bool
	seen    atomic.Int32
}

func (h *sleepMirrorFailure) DialHook(next redis.DialHook) redis.DialHook          { return next }
func (h *sleepMirrorFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }
func (h *sleepMirrorFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			args := cmd.Args()
			if cmd.Name() == "rpush" && len(args) > 1 && args[1] == h.key && h.enabled.Load() {
				h.seen.Add(1)
				return errors.New("offline injected queue mirror failure")
			}
		}
		return next(ctx, cmds)
	}
}

func TestSleepAppRedisMirrorFailureRecoversOneCarrier(t *testing.T) {
	w := newWorld(t)
	w.cfg.SleepScanInterval = 50 * time.Millisecond
	w.cfg.SleepScanBatch = 100
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	path, agent := s.createAgent("MirrorRecovery")
	s.must(202, "POST", path+"/messages", map[string]any{"text": " !sleep=600ms !text=source"})
	run := s.waitRunning(path)
	receipt, err := s.a.Store.RequestSleep(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, sleepmodel.Input{ActionID: uuid.New(), RunID: run.ID, Seconds: 120}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	st := s.waitIdle(path)
	if st.Sleep == nil {
		t.Fatal("sleep absent")
	}
	originalWake := *st.Sleep.WakeAt
	hook := &sleepMirrorFailure{key: s.a.Coord.Keys().QueueNormal(agent.ID.String())}
	hook.enabled.Store(true)
	defer hook.enabled.Store(false)
	s.a.Redis.AddHook(hook)
	if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE agent_sleeps SET wake_at=now()-interval '1 second' WHERE id=$1`, receipt.Sleep.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	limit := time.Now().Add(4 * time.Second)
	for hook.seen.Load() == 0 && time.Now().Before(limit) {
		time.Sleep(10 * time.Millisecond)
	}
	if hook.seen.Load() == 0 {
		t.Fatal("mirror fault was not exercised")
	}
	queued, err := s.a.Store.QueuedRuns(ctx, agent.ID)
	if err != nil || len(queued) != 1 || queued[0].Origin != "sleep_wake" {
		t.Fatal("PG carrier lost on mirror fault", queued, err)
	}
	wakeID := queued[0].ID
	if err = s.a.Store.ReconcileSleeps(ctx, 100); err != nil {
		t.Fatal(err)
	}
	hook.enabled.Store(false)
	s.waitIdle(path)
	var count int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_id=$1 AND origin='sleep_wake'`, agent.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("mirror retry duplicated carrier", count, err)
	}
	wake, err := s.a.Store.GetRun(ctx, wakeID)
	if err != nil || wake.Status != "completed" {
		t.Fatal("original carrier not recovered", err)
	}
	sess, err := s.a.Store.GetAgentSession(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sess.SessionFile)
	if err != nil {
		t.Fatal(err)
	}
	prompts := 0
	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) == nil && entry.Message.Role == "user" {
			prompts++
		}
	}
	if prompts != 2 {
		t.Fatal("mirror retry replayed execution", prompts)
	}
	t.Logf("case=mirror-failure sleepId=%s originalWakeAt=%s wakeRunId=%s PiPrompts=%d failures=%d", receipt.Sleep.ID, originalWake.Format(time.RFC3339Nano), wakeID, prompts, hook.seen.Load())
}
