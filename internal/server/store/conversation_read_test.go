package store_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
	"loop-now/internal/server/store"
)

// turn sends a message, starts it and ends it with the given status and reply text.
func (e *recEnv) turn(text, status, reply string) store.Run {
	e.t.Helper()
	_, r, err := e.s.InsertMessageAndRun(e.ctx, store.NewRun{AgentID: e.agent, RunID: uuid.Must(uuid.NewV7()), Text: text, Start: true,
		InstanceID: uuid.Must(uuid.NewV7())})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.s.SetRunRunning(e.ctx, r.ID, 1); err != nil {
		e.t.Fatal(err)
	}
	end := store.RunEnd{Status: status, Text: reply}
	if status == "interrupted" {
		end.EndReason = "user_interrupt"
	}
	if status == "failed" {
		end.ErrorCode, end.ErrorMessage = "pi_error", "boom"
	}
	if _, _, ok, err := e.s.FinishRun(e.ctx, r.ID, end); err != nil || !ok {
		e.t.Fatalf("finishing the run failed: %v %v", ok, err)
	}
	return r
}

func (e *recEnv) tool(run uuid.UUID, call, name, state string) {
	e.t.Helper()
	if err := e.s.AddToolEvent(e.ctx, run, store.ToolEvent{ToolCallID: call, ToolName: name, State: state, At: time.Now()}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *recEnv) conversation(q store.ConversationQuery) store.ConversationPage {
	e.t.Helper()
	page, err := e.s.ReadConversation(e.ctx, e.agent, q)
	if err != nil {
		e.t.Fatal(err)
	}
	return page
}

func texts(page store.ConversationPage) []string {
	out := []string{}
	for _, m := range page.Entries {
		out = append(out, m.Role+":"+m.Text)
	}
	return out
}

// The conversation an Agent reads is what was saved since the user last started over: earlier sessions, queued
// messages it has not been given yet and messages withdrawn while queued are left out; failed and interrupted turns
// keep the text that was written, and each reply summarizes the turn's tools.
func TestReadConversationIsTheCurrentConversation(t *testing.T) {
	e := newRecEnv(t)
	e.turn("before the restart", "completed", "old answer")
	if _, err := e.s.ResetSession(e.ctx, e.agent); err != nil {
		t.Fatal(err)
	}
	first := e.turn("first", "completed", "answer one")
	e.tool(first.ID, "c1", "bash", "started")
	e.tool(first.ID, "c1", "bash", "succeeded")
	e.tool(first.ID, "c2", "bash", "started")
	e.tool(first.ID, "c2", "bash", "failed")
	e.tool(first.ID, "c3", "read", "started")
	e.tool(first.ID, "c3", "read", "succeeded")
	e.turn("second", "failed", "partial two")
	e.turn("third", "interrupted", "partial three")
	busy := e.send(true, "")
	queued := e.send(false, "queue")
	withdrawn := e.send(false, "queue")
	if err := e.s.Pool.InTx(e.ctx, func(tx *db.Tx) error {
		_, _, _, err := e.s.CancelQueuedRunTx(e.ctx, tx, e.agent, withdrawn.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	page := e.conversation(store.ConversationQuery{Limit: 20})
	want := []string{"user:first", "assistant:answer one", "user:second", "assistant:partial two", "user:third", "assistant:partial three", "user:hi"}
	if got := texts(page); !slices.Equal(got, want) {
		t.Fatalf("wrong conversation:\n got %v\nwant %v", got, want)
	}
	if page.Total != len(want) || page.HasMore || page.HasMoreAfter {
		t.Fatalf("wrong page flags: total %d hasMore %v hasMoreAfter %v", page.Total, page.HasMore, page.HasMoreAfter)
	}
	reply := page.Entries[1]
	if reply.RunID != first.ID || reply.RunStatus != "completed" || reply.Origin != "console" {
		t.Fatalf("wrong reply: %+v", reply)
	}
	if len(reply.Tools) != 2 || reply.Tools[0] != (store.ToolUse{Name: "bash", Calls: 2, Failed: 1}) || reply.Tools[1] != (store.ToolUse{Name: "read", Calls: 1}) {
		t.Fatalf("wrong tool summary: %+v", reply.Tools)
	}
	if len(page.Entries[0].Tools) != 0 {
		t.Fatalf("a request has no tool summary: %+v", page.Entries[0].Tools)
	}
	if last := page.Entries[len(page.Entries)-1]; last.RunID != busy.ID || last.RunStatus != "starting" {
		t.Fatalf("the turn in progress is part of the conversation: %+v", last)
	}
	for _, m := range page.Entries {
		if m.RunID == queued.ID || m.RunID == withdrawn.ID {
			t.Fatalf("a queued or withdrawn message must not be read: %+v", m)
		}
	}
}

// Pages are oldest first within the page; without a position the newest page is returned, and before/after move
// through the conversation.
func TestReadConversationPages(t *testing.T) {
	e := newRecEnv(t)
	for _, text := range []string{"1", "2", "3"} {
		e.turn(text, "completed", "r"+text)
	}
	newest := e.conversation(store.ConversationQuery{Limit: 4})
	if got := texts(newest); !slices.Equal(got, []string{"user:2", "assistant:r2", "user:3", "assistant:r3"}) || !newest.HasMore || newest.HasMoreAfter || newest.Total != 6 {
		t.Fatalf("wrong newest page: %v %+v", got, newest)
	}
	older := e.conversation(store.ConversationQuery{Before: &newest.Entries[0].ID, Limit: 4})
	if got := texts(older); !slices.Equal(got, []string{"user:1", "assistant:r1"}) || older.HasMore || !older.HasMoreAfter {
		t.Fatalf("wrong older page: %v %+v", got, older)
	}
	after := e.conversation(store.ConversationQuery{After: &older.Entries[1].ID, Limit: 1})
	if got := texts(after); !slices.Equal(got, []string{"user:2"}) || !after.HasMore || !after.HasMoreAfter {
		t.Fatalf("wrong page after a message: %v %+v", got, after)
	}
	if _, err := e.s.ResetSession(e.ctx, e.agent); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ReadConversation(e.ctx, e.agent, store.ConversationQuery{Before: &newest.Entries[0].ID, Limit: 4}); err == nil {
		t.Fatal("a message from before the restart is not a position in the current conversation")
	}
	if page := e.conversation(store.ConversationQuery{Limit: 4}); len(page.Entries) != 0 || page.Total != 0 {
		t.Fatalf("after /new the conversation is empty: %+v", page)
	}
}
