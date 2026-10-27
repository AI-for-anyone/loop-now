package e2e_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/server/taskmodel"
)

func questionExternal(t *testing.T, w *world, s *server) *loopctl {
	t.Helper()
	_, _, token := s.issueToken("", "question-recipient", nil)
	return newLoopctl(t, map[string]string{"LOOPCTL_URL": s.ext, "LOOPCTL_TOKEN": token, "LOOPCTL_CA_FILE": w.caFile})
}
func cliQuestionState(t *testing.T, l *loopctl, id string) string {
	t.Helper()
	return l.run("inbox", "read", id).ok(t)["question"].(map[string]any)["state"].(string)
}
func questionConflict(t *testing.T, l *loopctl, id, code, state string) {
	t.Helper()
	r := l.run("inbox", "reply", id, "--answer", "1")
	r.fails(t, code, 8)
	var detail map[string]any
	if err := json.Unmarshal(r.Error.Details, &detail); err != nil {
		t.Fatal(err)
	}
	if err := clitest.Load(t, "openapi.yaml").Components.Schemas["QuestionClosedDetails"].Value.VisitJSON(detail, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal("actual error details violate formal schema", err)
	}
	if detail["question"].(map[string]any)["state"] != state || r.Meta.Attempts != 1 {
		t.Fatal("actual QuestionClosedDetails or single-write attempt lost")
	}
}
func expireCLIQuestion(t *testing.T, s *server, id string) {
	t.Helper()
	if _, err := s.a.Store.Pool.Exec(context.Background(), `UPDATE inbox_questions SET expires_at=clock_timestamp()-interval '1 second' WHERE message_id=$1`, uuid.MustParse(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.a.API.Inbox.ReconcileQuestions(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func waitCLIInbox(t *testing.T, l *loopctl, predicate func(map[string]any) bool) map[string]any {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
		for _, item := range l.run("inbox", "list").ok(t)["messages"].([]any) {
			m := item.(map[string]any)
			if predicate(m) {
				return l.run("inbox", "read", m["id"].(string)).ok(t)
			}
		}
	}
	t.Fatal("Inbox evidence not delivered")
	return nil
}

func TestInboxQuestionCLIRealAppOrdinary(t *testing.T) {
	w := newWorld(t, true)
	s := w.start(nil)
	defer s.stop()
	senderID := s.createAgent("普通提问者")
	sender := testTaskClient(t, s, senderID)
	recipient := questionExternal(t, w, s)
	ask := func(text string) string {
		t.Helper()
		r := sender.run("inbox", "send", "@question-recipient", "--text", text, "--option", "继续", "--option", "停止")
		q := r.ok(t)
		if r.Meta.Attempts != 1 || q["question"].(map[string]any)["state"] != "open" {
			t.Fatal("actual question not created once")
		}
		return q["id"].(string)
	}
	option := ask("option question")
	answer := recipient.run("inbox", "reply", option, "--answer", "2", "--option", "继续追问", "--option", "停止追问").ok(t)
	if answer["text"] != "选择：停止" || answer["answerTo"].(map[string]any)["messageId"] != option || cliQuestionState(t, recipient, option) != "answered" {
		t.Fatal("actual option answer missing")
	}
	if answer["question"].(map[string]any)["state"] != "open" {
		t.Fatal("answer plus new question was not created")
	}
	sender.run("inbox", "reply", answer["id"].(string), "--answer", "1").ok(t)
	if cliQuestionState(t, sender, answer["id"].(string)) != "answered" {
		t.Fatal("Agent could not answer external sender's question")
	}
	questionConflict(t, recipient, option, "already_answered", "answered")
	if ordinary := recipient.run("inbox", "reply", option, "--text", "after answer").ok(t); ordinary["answerTo"] != nil {
		t.Fatal("terminal ordinary reply counted as answer")
	}
	text := ask("text question")
	supplement := sender.run("inbox", "reply", text, "--text", "supplement").ok(t)
	recipient.run("inbox", "reply", supplement["id"].(string), "--text", "ordinary thread message").ok(t)
	if cliQuestionState(t, recipient, text) != "open" {
		t.Fatal("ordinary thread message answered original")
	}
	textAnswer := recipient.run("inbox", "reply", text, "--text", "answer original in text").ok(t)
	if textAnswer["answerTo"].(map[string]any)["messageId"] != text || textAnswer["answerTo"].(map[string]any)["optionId"] != nil || cliQuestionState(t, recipient, text) != "answered" {
		t.Fatal("pure text original answer missing")
	}
	expired := ask("expiry question")
	expireCLIQuestion(t, s, expired)
	questionConflict(t, recipient, expired, "question_expired", "expired")
	if msg := recipient.run("inbox", "reply", expired, "--text", "ordinary after expiry").ok(t); msg["answerTo"] != nil {
		t.Fatal("expired text treated as answer")
	}
	notice := waitCLIInbox(t, sender, func(m map[string]any) bool {
		n, ok := m["notice"].(map[string]any)
		return ok && n["messageId"] == expired
	})
	if notice["from"].(map[string]any)["kind"] != "platform" || notice["notice"].(map[string]any)["type"] != "question_expired" {
		t.Fatal("actual expiry notice projection missing")
	}
	reasked := sender.run("inbox", "reply", notice["id"].(string), "--text", "ask again", "--option", "yes", "--option", "no").ok(t)
	if reasked["to"].(map[string]any)["address"] != "@question-recipient" || reasked["question"].(map[string]any)["state"] != "open" {
		t.Fatal("expiry notice did not reply to original other party")
	}
	// Console also participates through the actual App, without a plugin or browser fixture.
	consoleQuestion := sender.run("inbox", "send", "@console", "--text", "console question", "--option", "yes", "--option", "no").ok(t)
	consoleAnswer := s.console(201, "POST", "/inbox/messages/"+consoleQuestion["id"].(string)+"/reply", map[string]any{"optionId": "1"})
	if consoleAnswer["answerTo"].(map[string]any)["messageId"] != consoleQuestion["id"] {
		t.Fatal("actual console option answer missing")
	}
	fromConsole := s.console(201, "POST", "/inbox/messages", map[string]any{"to": "默认项目/普通提问者", "text": "Agent answer console", "options": []string{"yes", "no"}})
	agentAnswer := sender.run("inbox", "reply", fromConsole["id"].(string), "--answer", "2").ok(t)
	if agentAnswer["answerTo"].(map[string]any)["messageId"] != fromConsole["id"] || agentAnswer["to"].(map[string]any)["kind"] != "console" {
		t.Fatal("Agent CLI failed to answer actual console question")
	}
	t.Log("real App ordinary options/text answers, non-answer thread text, already_answered/expired details, expiry platform notice and explicit re-question")
}

func TestTaskCLIQuestionAnswerTimingAndExpiry(t *testing.T) {
	for _, mode := range []string{"early-option", "late-text", "expiry", "completed-close"} {
		t.Run(mode, func(t *testing.T) {
			w := newWorld(t, true)
			w.cfg.PiMaxProcesses = 3
			s := w.start(nil)
			defer s.stop()
			_, manager := newTaskManager(t, s, "问题Manager")
			worker := s.createAgent("问题执行者")
			recipient := questionExternal(t, w, s)
			created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=1s !text=first_round"), "task", "create")
			id := created["task"].(map[string]any)["id"].(string)
			node := currentCLINode(t, waitRunningCLITask(t, s, id))
			client := testTaskClient(t, s, node.RuntimeID.String())
			client.run("inbox", "send", "@question-recipient", "--text", "missing dispatch", "--option", "yes").fails(t, "validation_failed", 9)
			q := client.run("inbox", "send", "@question-recipient", "--text", "continue?", "--option", "yes", "--option", "no", "--node-run", node.ID.String()).ok(t)
			qid := q["id"].(string)
			client.run("inbox", "send", "@question-recipient", "--text", "second open", "--option", "yes", "--node-run", node.ID.String()).fails(t, "task_conflict", 8)
			if mode == "completed-close" {
				taskCLIInput(t, client, map[string]any{"actionId": uuid.NewString(), "output": map[string]any{"result": "submitted while question open"}, "edgeId": nil}, "task", "submit", id, node.ID.String())
				waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "completed" && d.Cleanup == "archived" })
				questionConflict(t, recipient, qid, "question_closed", "closed")
				assertCLIClosedReason(t, s, qid, "node_completed")
				plain := recipient.run("inbox", "reply", qid, "--text", "ordinary after completed").ok(t)
				if plain["answerTo"] != nil || manager.run("task", "get", id).ok(t)["task"].(map[string]any)["status"] != "completed" {
					t.Fatal("late completed message revived task or answered old question")
				}
				return
			}
			if mode == "late-text" {
				waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "waiting" })
				recipient.run("inbox", "send", "默认项目/问题执行者#"+id, "--text", "ordinary message").ok(t)
				if cliQuestionState(t, recipient, qid) != "open" {
					t.Fatal("ordinary Inbox answered task question")
				}
				recipient.run("inbox", "reply", qid, "--text", "original answer after settled").ok(t)
			} else if mode == "expiry" {
				expireCLIQuestion(t, s, qid)
				questionConflict(t, recipient, qid, "question_expired", "expired")
			} else {
				recipient.run("inbox", "reply", qid, "--answer", "1").ok(t)
				if current := manager.run("task", "get", id).ok(t)["task"].(map[string]any)["status"]; current != "running" {
					t.Fatal("early answer ended running round")
				}
				questionConflict(t, recipient, qid, "already_answered", "answered")
			}
			blocked := waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "blocked" })
			expected := "answered"
			if mode == "expiry" {
				expected = "expired"
			}
			if blocked.Nodes[0].Question == nil || blocked.Nodes[0].Question.Status != expected {
				t.Fatal("task question reference lost actual state")
			}
			var reminders int
			if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT count(*) FROM task_turns WHERE task_id=$1 AND origin='inbox_reminder' AND status='completed'`, uuid.MustParse(id)).Scan(&reminders); err != nil || reminders < 1 {
				t.Fatal("answer/expiry wakeup lost", err)
			}
			cancelCLITask(t, s, manager, id)
			status := waitCLIInbox(t, manager, func(m map[string]any) bool {
				return m["from"].(map[string]any)["kind"] == "platform" && m["notice"] == nil
			})
			manager.run("inbox", "reply", status["id"].(string), "--text", "should not reply to status").fails(t, "forbidden", 6)
			t.Log("real task question timing/expiry wakeup, no duplicate open question, durable status notice read-only")
		})
	}
}

func TestTaskCLIQuestionRetryCancelCloseAndLateMessages(t *testing.T) {
	w := newWorld(t, true)
	w.cfg.PiMaxProcesses = 3
	s := w.start(nil)
	defer s.stop()
	managerID, manager := newTaskManager(t, s, "关闭问题Manager")
	worker := s.createAgent("关闭问题执行者")
	recipient := questionExternal(t, w, s)
	created := taskCLIInput(t, manager, taskTextRequest(worker, "!sleep=1s !text=work"), "task", "create")
	id := created["task"].(map[string]any)["id"].(string)
	node := currentCLINode(t, waitRunningCLITask(t, s, id))
	client := testTaskClient(t, s, node.RuntimeID.String())
	ask := func(n taskmodel.NodeRun) string {
		t.Helper()
		return client.run("inbox", "send", "@question-recipient", "--text", "continue?", "--option", "yes", "--option", "no", "--node-run", n.ID.String()).ok(t)["id"].(string)
	}
	old := ask(node)
	waitCLITask(t, s, id, func(d taskmodel.Detail) bool { return d.Task.Status == "waiting" })
	client.stdin = "explicit block before retry"
	client.run("task", "fail", id, node.ID.String(), "--action-id", uuid.NewString(), "--reason-file", "-").ok(t)
	manager.run("task", "retry", id, "--action-id", uuid.NewString(), "--expected-node-run", node.ID.String(), "--yes").ok(t)
	next := currentCLINode(t, waitRunningCLITask(t, s, id))
	if next.ID == node.ID {
		t.Fatal("retry reused node dispatch")
	}
	assertCLIClosedReason(t, s, old, "task_retry")
	if cliQuestionState(t, recipient, old) != "closed" {
		t.Fatal("retry did not persist closed")
	}
	client.run("inbox", "send", "@question-recipient", "--text", "old dispatch", "--option", "yes", "--node-run", node.ID.String()).fails(t, "task_conflict", 8)
	fresh := ask(next)
	questionConflict(t, recipient, old, "question_closed", "closed")
	recipient.run("inbox", "reply", old, "--text", "late ordinary text after retry").ok(t)
	if cliQuestionState(t, recipient, fresh) != "open" {
		t.Fatal("late old text/options answered new question")
	}
	cancelCLITask(t, s, manager, id)
	assertCLIClosedReason(t, s, fresh, "task_cancel")
	if cliQuestionState(t, recipient, fresh) != "closed" {
		t.Fatal("cancel did not persist closed")
	}
	questionConflict(t, recipient, fresh, "question_closed", "closed")
	ordinary := recipient.run("inbox", "reply", fresh, "--text", "ordinary after cancel").ok(t)
	if ordinary["answerTo"] != nil || ordinary["to"].(map[string]any)["id"] != managerID {
		t.Fatal("terminal ordinary text not forwarded to Manager")
	}
	if manager.run("task", "get", id).ok(t)["task"].(map[string]any)["status"] != "cancelled" {
		t.Fatal("late answer/message revived cancelled task")
	}
	t.Log("real retry/cancel persistent closed, old dispatch/late choice rejected, new question unaffected, terminal text preserved/forwarded without revival")
}

func assertCLIClosedReason(t *testing.T, s *server, id, reason string) {
	t.Helper()
	var got, state string
	if err := s.a.Store.Pool.QueryRow(context.Background(), `SELECT state,closed_reason FROM inbox_questions WHERE message_id=$1`, uuid.MustParse(id)).Scan(&state, &got); err != nil || state != "closed" || got != reason {
		t.Fatal("persisted close state/reason incorrect", err)
	}
}
