//go:build sqliteport

// SQLITE-PORT(G11): 依赖 PostgreSQL 专有机制（行锁/咨询锁/plpgsql/自建连接池/schema），SQLite 下需改写。
// 从 question_test.go 移出，默认不编译（go test -tags sqliteport 可单独查看）；改写后移回原文件并删掉本文件。
// 移出的函数：pauseInboxWrite.TraceQueryStart、pauseInboxWrite.TraceQueryEnd、TestInboxQuestionManagerCommitBoundaryAndNoticeIdempotency、TestInboxQuestionExpirySkipsLockedRowAndRetries

package httpapi_test

import (
	"context"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func (p *pauseInboxWrite) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if len(d.Args) > 0 {
		if key, ok := d.Args[0].(string); ok && strings.HasPrefix(key, "inbox-write:") {
			p.once.Do(func() {
				close(p.entered)
				select {
				case <-p.release:
				case <-ctx.Done():
				}
			})
		}
	}
	return ctx
}

func (*pauseInboxWrite) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestInboxQuestionManagerCommitBoundaryAndNoticeIdempotency(t *testing.T) {
	for _, mode := range []string{"ordinary-reply", "closed-question-reply", "notice"} {
		t.Run(mode, func(t *testing.T) {
			e, _, d := taskEnv(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			e.api.Tasks.Source = &tasks.Source{CloseQuestion: store.CloseInboxQuestion, ViewQuestion: e.api.Inbox.ViewQuestion}
			node := d.Nodes[0]
			principal := auth.Principal{Kind: auth.KindAgent, ID: node.AgentID, TaskID: d.Task.ID, RuntimeID: node.RuntimeID}
			input := inbox.QuestionInput{}
			if mode == "closed-question-reply" {
				input = inbox.QuestionInput{Options: []string{"yes"}, NodeRunID: node.ID}
			}
			q, err := e.api.Inbox.Send(ctx, principal, "@console", "old thread", "queue", input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.api.Tasks.Cancel(ctx, auth.Console(), d.Task.ID, taskmodel.CancelInput{ActionID: uuid.New()}); err != nil {
				t.Fatal(err)
			}
			next, err := e.st.CreateAgent(ctx, store.ProjectScope(e.dc.Project.ID), "NewManager", "anthropic", "claude-x", "")
			if err != nil {
				t.Fatal(err)
			}
			// No background reader may race replacing the isolated test pool tracer.
			if err = e.conv.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			original := e.st.Pool
			config := original.Config()
			trace := &pauseInboxWrite{entered: make(chan struct{}), release: make(chan struct{})}
			config.ConnConfig.Tracer = trace
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			e.st.Pool = pool
			defer func() { e.st.Pool = original; pool.Close() }()
			var once sync.Once
			release := func() { once.Do(func() { close(trace.release) }) }
			defer release()
			var messageID uuid.UUID
			if mode == "notice" {
				if err = e.st.Pool.QueryRow(ctx, `SELECT message_id FROM task_notifications WHERE task_id=$1 LIMIT 1`, d.Task.ID).Scan(&messageID); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			saved := make(chan store.InboxMessage, 1)
			go func() {
				if mode == "notice" {
					done <- e.api.Inbox.SystemNotice(ctx, node.AgentID, messageID, "")
					return
				}
				msg, err := e.api.Inbox.Reply(ctx, auth.Console(), q.ID, "late ordinary", "queue")
				saved <- msg
				done <- err
			}()
			select {
			case <-trace.entered:
			case <-ctx.Done():
				t.Fatal("routing barrier not reached")
			}
			if _, err = e.st.SetProjectManager(ctx, e.dc.Project.ID, next.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = e.st.RenameProject(ctx, e.dc.Project.ID, "改名后"); err != nil {
				t.Fatal(err)
			}
			release()
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if mode == "notice" {
				msg, err := e.st.GetInboxMessage(ctx, messageID, 200)
				if err != nil || msg.To.ID != next.ID || msg.From.Kind != "platform" || !strings.Contains(msg.Text, "/p/"+url.PathEscape("改名后")+"/") {
					t.Fatal("notice used stale Manager/link", err)
				}
				for i := 0; i < 3; i++ {
					if err = e.api.Inbox.SystemNotice(ctx, node.AgentID, messageID, ""); err != nil {
						t.Fatal(err)
					}
				}
				var messages, turns int
				e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_messages WHERE id=$1`, messageID).Scan(&messages)
				e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE trigger_inbox_message_id=$1`, messageID).Scan(&turns)
				if messages != 1 || turns != 1 {
					t.Fatal("notice retry dispatched twice", messages, turns)
				}
			} else {
				msg := <-saved
				if msg.To.ID != next.ID || msg.AnswerTo != nil {
					t.Fatal("late reply used stale Manager or became answer")
				}
			}
		})
	}
}

func TestInboxQuestionExpirySkipsLockedRowAndRetries(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.platformAgent("到期锁测试")
	sender := e.agentCaller(a.ID)
	create := func(text string) uuid.UUID {
		return decode[apitypes.InboxMessage](t, sender.expect(201, "POST", "/inbox/messages", map[string]any{"to": "@console", "text": text, "options": []string{"yes"}})).ID
	}
	locked, other := create("locked"), create("other")
	if _, err := e.st.Pool.Exec(ctx, `UPDATE inbox_questions SET expires_at=now()-interval '1 second' WHERE message_id=ANY($1)`, []uuid.UUID{locked, other}); err != nil {
		t.Fatal(err)
	}
	tx, err := e.st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT message_id FROM inbox_questions WHERE message_id=$1 FOR UPDATE`, locked); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err = e.st.ExpireInboxQuestions(bounded); err != nil {
		t.Fatal("locked question blocked independent expiry", err)
	}
	assertState := func(id uuid.UUID, want string, events int) {
		t.Helper()
		var state string
		var count int
		if err := e.st.Pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM inbox_question_outbox WHERE question_message_id=$1 AND state='expired') FROM inbox_questions WHERE message_id=$1`, id).Scan(&state, &count); err != nil || state != want || count != events {
			t.Fatalf("state=%s events=%d want=%s/%d: %v", state, count, want, events, err)
		}
	}
	assertState(locked, "open", 0)
	assertState(other, "expired", 1)
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = e.st.ExpireInboxQuestions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertState(locked, "expired", 1)
	assertState(other, "expired", 1)
}
