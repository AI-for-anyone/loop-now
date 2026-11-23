package store_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// /copy takes the latest successful reply that has text: replies without text (commands Pi handled directly) and
// failed replies are skipped; nil when there is none.
func TestLatestCompletedAssistantSkipsEmptyReplies(t *testing.T) {
	e := newRecEnv(t)
	reply := func(status, text string) {
		t.Helper()
		r := e.send(true, "")
		if _, _, ok, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: status, Text: text}); err != nil || !ok {
			t.Fatalf("finish run: %v %v", ok, err)
		}
	}
	latest := func() *store.Message {
		t.Helper()
		m, err := e.s.LatestCompletedAssistant(e.ctx, e.agent)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	reply("completed", "")
	reply("completed", " \n\t")
	if m := latest(); m != nil {
		t.Fatalf("should be nil when there are only empty replies: %+v", m)
	}
	reply("completed", "a reply with text")
	reply("completed", "")
	reply("failed", "partial reply of a failed run")
	if m := latest(); m == nil || m.Text != "a reply with text" {
		t.Fatalf("should take the latest successful reply with text: %+v", m)
	}
}

// compactRun inserts one manual compaction and ends it with end.
func (e *recEnv) compactRun(instructions string, end store.RunEnd) store.Run {
	e.t.Helper()
	mustExec(e.t, e.s, `UPDATE agent_sessions SET state = 'active', session_file = 'session.jsonl' WHERE agent_id = $1`, e.agent)
	r, err := e.s.InsertConsoleCompactRun(e.ctx, e.agent, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), instructions)
	if err != nil {
		e.t.Fatal(err)
	}
	if c := store.ParseCompaction(r.Compaction); c.Instructions != instructions || c.Outcome != "" {
		e.t.Fatalf("only the instructions should be recorded at the start: %+v", c)
	}
	ended, msg, ok, err := e.s.FinishRun(e.ctx, r.ID, end)
	if err != nil || !ok || msg != nil {
		e.t.Fatalf("finish compaction: %v %v %+v", ok, err, msg)
	}
	return ended
}

// The instructions of a manual compaction are recorded when it starts and the result is merged in when it ends, so
// both can always be read from the run record; a failed or cancelled compaction has no result.
func TestCompactionRecordedOnRun(t *testing.T) {
	e := newRecEnv(t)
	before, after := int64(4200), int64(350)
	done := e.compactRun("keep the decisions", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionCompacted, TokensBefore: &before, EstimatedTokensAfter: &after}})
	c := store.ParseCompaction(done.Compaction)
	if c.Instructions != "keep the decisions" || c.Outcome != store.CompactionCompacted || *c.TokensBefore != 4200 || *c.EstimatedTokensAfter != 350 {
		t.Fatalf("compaction result: %+v", c)
	}
	skipped := e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "too_small"}})
	if c := store.ParseCompaction(skipped.Compaction); c.Outcome != store.CompactionSkipped || c.SkipReason != "too_small" || c.TokensBefore != nil {
		t.Fatalf("not compacted: %+v", c)
	}
	failed := e.compactRun("x", store.RunEnd{Status: "failed", ErrorCode: "pi_error", ErrorMessage: "model error"})
	if c := store.ParseCompaction(failed.Compaction); c.Instructions != "x" || c.Outcome != "" {
		t.Fatalf("a failed compaction should have no result: %+v", c)
	}
	last, err := e.s.LatestCompactRun(e.ctx, e.agent)
	if err != nil || last == nil || last.ID != failed.ID {
		t.Fatalf("latest manual compaction: %+v %v", last, err)
	}
}

// A manual compaction is not a reply: it does not count as the "previous reply" in the overview; a cancelled one is
// not an anomaly, while a failed one is listed as usual.
func TestCompactionInOverviewAndAnomalies(t *testing.T) {
	e := newRecEnv(t)
	normal := e.send(true, "")
	if _, _, _, err := e.s.FinishRun(e.ctx, normal.ID, store.RunEnd{Status: "failed", ErrorCode: "pi_error"}); err != nil {
		t.Fatal(err)
	}
	cancelled := e.compactRun("", store.RunEnd{Status: "interrupted", EndReason: "user_interrupt"})
	failed := e.compactRun("", store.RunEnd{Status: "failed", ErrorCode: "pi_error", ErrorMessage: "model error"})
	e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "already_compacted"}})
	last, err := e.s.LastEndedRuns(e.ctx, []uuid.UUID{e.agent})
	if err != nil || last[e.agent].ID != normal.ID {
		t.Fatalf("the previous reply should skip manual compactions: %+v %v", last[e.agent], err)
	}
	rows, total, _, err := e.s.Anomalies(e.ctx, store.ScopeFilter{}, nil, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for _, r := range rows {
		ids = append(ids, r.Run.ID)
	}
	if total != 2 || !slices.Contains(ids, normal.ID) || !slices.Contains(ids, failed.ID) || slices.Contains(ids, cancelled.ID) {
		t.Fatalf("anomaly list: %d %v", total, ids)
	}
}

// Locating a manual compaction: it has no conversation messages, so it is centred on its position at the time it
// started; when there are no messages after it, there is only the part before it.
func TestAroundCompactionRun(t *testing.T) {
	e := newRecEnv(t)
	reply := func() store.Run {
		r := e.send(true, "")
		if _, _, _, err := e.s.FinishRun(e.ctx, r.ID, store.RunEnd{Status: "completed", Text: "reply"}); err != nil {
			t.Fatal(err)
		}
		return r
	}
	reply()
	a := reply()
	compact := e.compactRun("", store.RunEnd{Status: "completed", Compaction: &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: "too_small"}})
	tail, err := e.s.ListMessages(e.ctx, e.agent, store.MessageQuery{AroundRun: &compact.ID, Limit: 3})
	if err != nil || len(tail.Items) != 3 || tail.HasMoreAfter || !tail.HasMore || tail.Items[2].Run.ID != a.ID {
		t.Fatalf("with no messages after the compaction, the part before it should be returned: %+v %v", tail, err)
	}
	b := reply()
	page, err := e.s.ListMessages(e.ctx, e.agent, store.MessageQuery{AroundRun: &compact.ID, Limit: 3})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("locate compaction: %+v %v", page, err)
	}
	if page.Items[0].Run.ID != a.ID || page.Items[0].Role != "assistant" || page.Items[1].Run.ID != b.ID || page.Items[1].Role != "user" {
		t.Fatalf("should be centred on where the compaction started: %+v", page.Items)
	}
	other, err := e.s.CreateAgent(e.ctx, store.ProjectScope(e.dc.Project.ID), "B", "anthropic", "claude-x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.ListMessages(e.ctx, other.ID, store.MessageQuery{AroundRun: &compact.ID, Limit: 3}); !apperr.Is(err, apperr.CodeNotFound) {
		t.Fatalf("locating this compaction from another Agent should be not_found: %v", err)
	}
}
