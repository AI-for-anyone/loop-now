package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"loop-now/internal/server/coord"
	"loop-now/internal/testutil/redistest"
	"testing"
	"time"

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
				if _, err := m.Tx.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at,state,closed_at,closed_reason) VALUES($1,'[{"id":"1","label":"continue"}]',now()+interval '1 hour',$2,CASE WHEN $2='closed' THEN now() ELSE NULL END,CASE WHEN $2='closed' THEN 'task_cancel' ELSE NULL END)`, q, before); err != nil {
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

func TestTaskLarkQuestionFirstAnswerRacesPublication(t *testing.T) {
	for _, winner := range []string{"publish", "answer"} {
		t.Run(winner, func(t *testing.T) {
			s, in := larkSwitchReadyFixture(t, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			q := uuid.New()
			publisher := in.Members[0]
			_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
				if e := store.InsertInboxMessage(ctx, m.Tx, store.NewInboxMessage{ID: q, From: store.Party{Kind: "agent", ID: publisher.AgentID, RuntimeID: publisher.RuntimeID}, To: store.ConsoleParty, Text: "original question"}); e != nil {
					return nil, e
				}
				if _, e := m.Tx.Exec(ctx, `INSERT INTO inbox_questions(message_id,options,expires_at) VALUES($1,'[{"id":"1","label":"continue"}]',now()+interval '1 hour')`, q); e != nil {
					return nil, e
				}
				if _, e := m.Tx.Exec(ctx, `INSERT INTO task_questions(question_message_id,task_id,runtime_id,node_run_id) VALUES($1,$2,$3,$4)`, q, in.TaskID, publisher.RuntimeID, in.NodeRunID); e != nil {
					return nil, e
				}
				m.Record.Current().Question = &taskmodel.Question{ID: q, MessageID: q, ThreadID: q, Text: "original question", Status: "open"}
				return nil, nil
			})
			if e != nil {
				t.Fatal(e)
			}
			original, e := s.GetTask(ctx, in.TaskID)
			if e != nil {
				t.Fatal(e)
			}
			in.ExpectedTaskVersion = original.View.Task.Version
			if _, e = reserveLarkFence(ctx, s, in); e != nil {
				t.Fatal(e)
			}
			current, e := s.GetTask(ctx, in.TaskID)
			if e != nil {
				t.Fatal(e)
			}
			next := *current.Current()
			next.ID = uuid.New()
			next.Attempt++
			next.Status = "queued"
			next.Output = nil
			next.Question = nil
			next.EndedAt = nil
			run := uuid.New()
			next.RunIDs = []uuid.UUID{run}
			svc := tasks.New(s, nil, nil, tasks.Options{}, nil)
			defer svc.Stop(context.Background())
			opts, e := redis.ParseURL(redistest.URL(t))
			if e != nil {
				t.Fatal(e)
			}
			rdb := redis.NewClient(opts)
			prefix := "loopnow:test:lark-question-race:" + uuid.NewString() + ":"
			defer func() { redistest.DeletePrefix(context.Background(), rdb, prefix); rdb.Close() }()
			is := &inbox.Service{St: s, Tasks: svc, Co: coord.New(rdb, coord.Options{Prefix: prefix, StreamMaxLen: 100}), Log: quietLogger(), O: inbox.Options{MaxMessage: 65536, PreviewChars: 100}}
			answer := func() error {
				option := "1"
				_, e := is.Reply(ctx, auth.Console(), q, "first actual answer", "queue", inbox.QuestionInput{OptionID: &option})
				return e
			}
			publish := func(held chan<- uint32, release <-chan struct{}) error {
				_, e := s.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
					m.Unchanged()
					if held != nil {
						held <- m.Tx.Conn().PgConn().PID()
						select {
						case <-release:
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					}
					_, _, e := s.PublishTaskLarkSwitchTx(ctx, m, in.ActorKey, in.ActionID, next, run, "QA only", json.RawMessage(`{"status":"succeeded"}`))
					return nil, e
				})
				return e
			}
			waitBlocker := func(pid uint32) {
				t.Helper()
				for ctx.Err() == nil {
					var blocked bool
					e := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked)
					if e != nil {
						t.Fatal(e)
					}
					if blocked {
						return
					}
					time.Sleep(time.Millisecond * 5)
				}
				t.Fatal("expected real lock waiter not reached")
			}
			pubDone, answerDone := make(chan error, 1), make(chan error, 1)
			if winner == "publish" {
				held, release := make(chan uint32, 1), make(chan struct{})
				go func() { pubDone <- publish(held, release) }()
				pid := <-held
				go func() { answerDone <- answer() }()
				waitBlocker(pid)
				close(release)
			} else {
				// Pause the real answer transaction after it owns the aggregate/question
				// locks; C then genuinely waits for the first-answer commit.
				conn, e := s.Pool.Acquire(ctx)
				if e != nil {
					t.Fatal(e)
				}
				defer conn.Release()
				var key int64
				if e = conn.QueryRow(ctx, `SELECT floor(random()*1000000000)::bigint+1000000000`).Scan(&key); e != nil {
					t.Fatal(e)
				}
				if _, e = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, key); e != nil {
					t.Fatal(e)
				}
				defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
				sql := fmt.Sprintf(`CREATE FUNCTION pause_lark_first_answer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d::bigint); RETURN NEW; END $$; CREATE TRIGGER pause_lark_first_answer BEFORE UPDATE OF state ON inbox_questions FOR EACH ROW WHEN (NEW.state='answered') EXECUTE FUNCTION pause_lark_first_answer()`, key)
				if _, e = s.Pool.Exec(ctx, sql); e != nil {
					t.Fatal(e)
				}
				go func() { answerDone <- answer() }()
				waitBlocker(conn.Conn().PgConn().PID())
				go func() { pubDone <- publish(nil, nil) }()
				// The answer's xact lock is proven held above. Releasing its own fixture
				// barrier lets it commit before C can acquire the task lock.
				if _, e = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key); e != nil {
					t.Fatal(e)
				}
			}
			pe, ae := <-pubDone, <-answerDone
			var state string
			var groups, answers int
			if e = s.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM task_lark_credential_bindings WHERE task_id=$2),(SELECT count(*) FROM inbox_messages WHERE answers_message_id=$1) FROM inbox_questions WHERE message_id=$1`, q, in.TaskID).Scan(&state, &groups, &answers); e != nil {
				t.Fatal(e)
			}
			if winner == "publish" {
				if pe != nil || !apperr.Is(ae, "question_closed") || state != "closed" || groups != 1 || answers != 0 {
					t.Fatal("C-first did not preserve close winner", pe, ae, state, groups, answers)
				}
			} else {
				if !apperr.Is(pe, "task_conflict") || ae != nil || state != "answered" || groups != 0 || answers != 1 {
					t.Fatal("answer-first was overwritten", pe, ae, state, groups, answers)
				}
			}
		})
	}
}
