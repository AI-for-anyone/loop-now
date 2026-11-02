package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
)

func TestTaskLarkPublishPreservesQuestionFourStatesAndRejectsLateAnswer(t *testing.T) {
	for _, before := range []string{"open", "answered", "expired", "closed"} {
		t.Run(before, func(t *testing.T) {
			s, in := larkSwitchReadyFixture(t, nil)
			ctx := context.Background()
			q := uuid.New()
			var publisher store.TaskLarkFenceMember
			for _, member := range in.Members {
				if member.Role == "publisher" {
					publisher = member
				}
			}
			_, err := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				if err := store.InsertInboxMessage(ctx, m.Tx, store.NewInboxMessage{ID: q, From: store.Party{Kind: "agent", ID: publisher.AgentID, RuntimeID: publisher.RuntimeID}, To: store.ConsoleParty, Text: "original question"}); err != nil {
					return nil, err
				}
				if _, err := m.Tx.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at,state,closed_at,closed_reason) VALUES($1,'[{"id":"1","label":"continue"}]',now()+3600000000,$2,CASE WHEN $2='closed' THEN now() ELSE NULL END,CASE WHEN $2='closed' THEN 'task_cancel' ELSE NULL END)`, q, before); err != nil {
					return nil, err
				}
				if _, err := m.Tx.Exec(ctx, `INSERT INTO task_questions(question_message_id,task_id,runtime_id,node_run_id) VALUES($1,$2,$3,$4)`, q, in.TaskID, publisher.RuntimeID, in.NodeRunID); err != nil {
					return nil, err
				}
				m.Record.Current().Question = &taskmodel.Question{ID: q, MessageID: q, ThreadID: q, Text: "original question", Status: before}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.GetTask(ctx, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			in.ExpectedTaskVersion = r.View.Task.Version
			if _, err = reserveLarkFence(ctx, s, in); err != nil {
				t.Fatal(err)
			}
			r, err = s.GetTask(ctx, in.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			next := *r.Current()
			next.ID = uuid.New()
			next.Attempt++
			next.Status = "queued"
			next.Output, next.Question, next.EndedAt = nil, nil, nil
			run := uuid.New()
			next.RunIDs = []uuid.UUID{run}
			_, err = s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				m.Unchanged()
				_, _, err := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, run, "remaining QA only", json.RawMessage(`{"status":"succeeded"}`))
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			var state string
			var reason *string
			var outbox int
			if err = s.Pool.QueryRow(ctx, `SELECT state,closed_reason,(SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1) FROM inbox_questions WHERE message_id=$1`, q).Scan(&state, &reason, &outbox); err != nil {
				t.Fatal(err)
			}
			want := before
			if before == "open" {
				want = "closed"
			}
			if state != want || before == "open" && (reason == nil || *reason != "task_retry" || outbox != 1) || before == "closed" && (reason == nil || *reason != "task_cancel") || before != "open" && outbox != 0 {
				t.Fatal("question state/history overwritten", before, state, reason, outbox)
			}
			if before == "open" {
				svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
				defer svc.Stop(ctx)
				is := &inbox.Service{St: s, Tasks: svc, O: inbox.Options{MaxMessage: 65536, PreviewChars: 100}}
				option := "1"
				if _, err = is.Reply(ctx, auth.Console(), q, "late answer", "queue", inbox.QuestionInput{OptionID: &option}); !apperr.Is(err, "question_closed") {
					t.Fatal("old question revived new attempt", err)
				}
				for i := 0; i < 2; i++ {
					if err = is.ReconcileQuestions(ctx); err != nil {
						t.Fatal("real question outbox reconcile", err)
					}
				}
				var messages int
				if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages`).Scan(&messages); err != nil || messages != 1 {
					t.Fatal("late option left message or fallback", messages, err)
				}
				var pending, turns int
				if err = s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND delivered_at IS NULL),(SELECT count(*) FROM task_turns WHERE task_id=$2)`, q, in.TaskID).Scan(&pending, &turns); err != nil || pending != 0 || turns != 2 {
					t.Fatal("closed notice woke new attempt or duplicated", pending, turns, err)
				}
			}
		})
	}
}
