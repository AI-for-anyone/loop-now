package app_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/workflow"
	"testing"
)

// Real App wiring + PG/Redis + fakePi RPC. No callback substitutes or providers.
func TestTaskQuestionAppAnswerAndExpiryBeforeSettled(t *testing.T) {
	for _, mode := range []string{"answer", "expiry", "blocked-expiry"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t)
			s := w.start(nil)
			defer s.stop()
			ctx := context.Background()
			_, manager := s.createAgent("Manager")
			_, agent := s.createAgent("Worker")
			dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
			s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
			description := " !sleep=1s !text=long_first_round"
			if mode == "blocked-expiry" {
				description += " !error=fixture_error"
			}
			body, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "真实问题组件", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", Description: description, AgentID: agent.ID, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
			if err != nil {
				t.Fatal(err)
			}
			var created taskmodel.Detail
			if err = json.Unmarshal(body, &created); err != nil {
				t.Fatal(err)
			}
			running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
				return d.Task.Status == "running" && s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy"
			})
			node := running.Nodes[0]
			principal := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
			q, err := s.a.API.Inbox.Send(ctx, principal, "@console", "是否继续", "queue", inbox.QuestionInput{Options: []string{"是", "否"}, NodeRunID: node.ID})
			if err != nil {
				t.Fatal(err)
			}
			view := decode[taskmodel.Detail](t, s.must(200, "GET", "/tasks/"+created.Task.ID.String(), nil))
			if !view.Nodes[0].Question.CanAnswer {
				t.Fatal("App Source.ViewQuestion not wired")
			}
			if mode == "answer" {
				s.must(201, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
			} else {
				if mode == "blocked-expiry" {
					taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
				}
				if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, q.ID); err != nil {
					t.Fatal(err)
				}
				if err = s.a.API.Inbox.ReconcileQuestions(ctx); err != nil {
					t.Fatal(err)
				}
			}
			final := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool {
				if d.Task.Status != "blocked" {
					return false
				}
				if mode == "blocked-expiry" {
					var n int
					_ = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='inbox_reminder' AND status='completed'`, created.Task.ID).Scan(&n)
					return n == 1
				}
				return true
			})
			if final.Nodes[0].Question.Status != map[string]string{"answer": "answered", "expiry": "expired", "blocked-expiry": "expired"}[mode] {
				t.Fatal("task reference lost terminal question")
			}
			var turns, reminders int
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE origin='inbox_reminder') FROM task_turns WHERE task_id=$1`, created.Task.ID).Scan(&turns, &reminders); err != nil || turns != 2 || reminders != 1 {
				t.Fatal("lost or duplicate recovery round", turns, reminders, err)
			}
			// Platform task status notice is real durable Inbox, not a fake callback.
			var notices int
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_notifications WHERE task_id=$1 AND sent_at IS NOT NULL`, created.Task.ID).Scan(&notices); err != nil {
				t.Fatal(err)
			}
			if notices == 0 {
				if err = s.a.API.Inbox.SystemNotice(ctx, manager.ID, func() uuid.UUID {
					var id uuid.UUID
					if err = s.a.Store.Pool.QueryRow(ctx, `SELECT message_id FROM task_notifications WHERE task_id=$1 LIMIT 1`, created.Task.ID).Scan(&id); err != nil {
						t.Fatal(err)
					}
					return id
				}(), ""); err != nil {
					t.Fatal(err)
				}
			}
			var msgID uuid.UUID
			if err = s.a.Store.Pool.QueryRow(ctx, `SELECT message_id FROM task_notifications WHERE task_id=$1 LIMIT 1`, created.Task.ID).Scan(&msgID); err != nil {
				t.Fatal(err)
			}
			message, err := s.a.Store.GetInboxMessage(ctx, msgID, 200)
			if err != nil || message.From.Kind != "platform" || message.To.ID != manager.ID {
				t.Fatal("Manager platform notice absent", err)
			}
			if _, err = s.a.Tasks.Cancel(ctx, auth.Console(), created.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
			taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "cancelled" && d.Cleanup == "archived" })
		})
	}
}

func TestTaskQuestionAppRestartRecoversPendingOutbox(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	ctx := context.Background()
	path, agent := s.createAgent("ExpiryOwner")
	q, err := s.a.API.Inbox.Send(ctx, auth.Principal{Kind: auth.KindAgent, ID: agent.ID}, "@console", "restart expiry", "queue", inbox.QuestionInput{Options: []string{"yes"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{`CREATE FUNCTION fail_platform_before_restart() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.from_kind='platform' THEN RAISE EXCEPTION 'synthetic delivery outage'; END IF; RETURN NEW; END $$`, `CREATE TRIGGER fail_platform_before_restart BEFORE INSERT ON inbox_messages FOR EACH ROW EXECUTE FUNCTION fail_platform_before_restart()`} {
		if _, err = s.a.Store.Pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.a.Store.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=$1`, q.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.a.API.Inbox.ReconcileQuestions(ctx); err == nil {
		t.Fatal("pending fixture did not fail")
	}
	s.stop()
	second := w.start(nil)
	defer second.stop()
	if _, err = second.a.Store.Pool.Exec(ctx, `DROP TRIGGER fail_platform_before_restart ON inbox_messages`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err = second.a.API.Inbox.ReconcileQuestions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	second.waitIdle(path)
	var messages, turns, pending int
	if err = second.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE from_kind='platform' AND in_reply_to=$1`, q.ID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err = second.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE trigger_inbox_message_id IN(SELECT id FROM inbox_messages WHERE in_reply_to=$1 AND from_kind='platform')`, q.ID).Scan(&turns); err != nil {
		t.Fatal(err)
	}
	if err = second.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND delivered_at IS NULL`, q.ID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || turns != 1 || pending != 0 {
		t.Fatal("restart lost or duplicated durable delivery", messages, turns, pending)
	}
}

func TestTaskQuestionAppCompletionClosesPendingOriginal(t *testing.T) {
	w := newWorld(t)
	s := w.start(nil)
	defer s.stop()
	ctx := context.Background()
	_, manager := s.createAgent("CompletionManager")
	_, agent := s.createAgent("CompletionWorker")
	dc := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil))
	s.must(200, "PUT", "/projects/"+dc.Project.ID.String()+"/manager", map[string]any{"agentId": manager.ID})
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: manager.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "complete closes original", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "work", AgentID: agent.ID, Description: " !sleep=500ms !text=original_round", InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{}}})
	if err != nil {
		t.Fatal(err)
	}
	var created taskmodel.Detail
	if err = json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	running := taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return s.a.Executor.Process(d.Nodes[0].RuntimeID).State == "busy" })
	node := running.Nodes[0]
	p := auth.Principal{Kind: auth.KindAgent, ID: agent.ID, TaskID: created.Task.ID, RuntimeID: node.RuntimeID}
	q, err := s.a.API.Inbox.Send(ctx, p, "@console", "仍未回答", "queue", inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.a.Tasks.Submit(ctx, p, created.Task.ID, node.ID, taskmodel.SubmitInput{ActionID: uuid.New(), Output: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	taskWait(t, s, created.Task.ID, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
	var state, reason string
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT state,closed_reason FROM inbox_questions WHERE message_id=$1`, q.ID).Scan(&state, &reason); err != nil || state != "closed" || reason != "node_completed" {
		t.Fatal("successful settled did not persist explicit close reason", state, reason, err)
	}
	s.must(409, "POST", "/inbox/messages/"+q.ID.String()+"/reply", map[string]any{"optionId": "1"})
}
