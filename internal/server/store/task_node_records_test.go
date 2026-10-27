package store_test

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/runrecord"
	"loop-now/internal/server/store"
	"testing"
	"time"
)

func TestTaskNodeToolFinalStateAcrossProjectedPages(t *testing.T) {
	s := taskConsoleStore(t)
	task, node, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	if _, e := s.Pool.Exec(ctx, `DELETE FROM lifecycle_events WHERE task_turn_id=$1`, turn.ID); e != nil {
		t.Fatal(e)
	}
	at := time.Now().UTC()
	for i := 0; i <= 231; i++ {
		kind := "run.phase"
		data := map[string]any{"phase": "launching"}
		if i == 0 || i == 231 {
			kind = "tool.changed"
			state := "started"
			if i == 231 {
				state = "succeeded"
			}
			data = map[string]any{"toolCallId": "single-call", "toolName": "bash", "state": state}
		}
		raw, _ := json.Marshal(data)
		if _, e := s.Pool.Exec(ctx, `INSERT INTO lifecycle_events(id,at,agent_id,type,data,task_id,workflow_node,task_turn_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.Must(uuid.NewV7()), at.Add(time.Duration(i)*time.Millisecond), agent, kind, raw, task, node.String(), turn.ID); e != nil {
			t.Fatal(e)
		}
	}
	cursor, tools, pages, entries := "", 0, 0, 0
	for {
		page, e := (&runrecord.Service{Store: s}).TaskNodeTimeline(ctx, task, node, turn.ID, cursor, 100)
		if e != nil {
			t.Fatal(e)
		}
		pages++
		entries += len(page.Entries)
		for _, entry := range page.Entries {
			if tool, ok := entry.(apitypes.TimelineToolEntry); ok {
				tools++
				if tool.State != "succeeded" || tool.DurationMs == nil || *tool.DurationMs != 231 {
					t.Fatal("first projected page lacks final tool facts", tool)
				}
			}
		}
		if page.NextCursor == nil {
			break
		}
		if pages > 3 || *page.NextCursor == cursor {
			t.Fatal("projected cursor did not advance")
		}
		cursor = *page.NextCursor
	}
	if tools != 1 || pages != 3 || entries != 231 {
		t.Fatal("tool was duplicated or omitted across semantic pages", tools, pages, entries)
	}
}

func TestTaskNodeRecordScopeAndHistory(t *testing.T) {
	s := taskConsoleStore(t)
	task, node, agent, turn := taskConsoleRecord(t, s)
	ctx := context.Background()
	got, a, e := s.TaskNodeRecord(ctx, task, node, turn.ID)
	if e != nil || a != agent || got.ID != turn.ID {
		t.Fatal("turn authority", e)
	}
	for _, scope := range [][3]uuid.UUID{{uuid.New(), node, turn.ID}, {task, uuid.New(), turn.ID}, {task, node, uuid.New()}} {
		if _, _, e = s.TaskNodeRecord(ctx, scope[0], scope[1], scope[2]); !store.IsNotFound(e) {
			t.Fatal("scope not rejected", e)
		}
	}
	// At collisions must remain deterministic and fully pageable.
	_, e = s.Pool.Exec(ctx, `DELETE FROM lifecycle_events WHERE task_turn_id=$1`, turn.ID)
	if e != nil {
		t.Fatal(e)
	}
	at := time.Now().UTC()
	for _, kind := range []string{"tool.changed", "run.phase", "run.ended"} {
		_, e = s.Pool.Exec(ctx, `INSERT INTO lifecycle_events(id,at,agent_id,type,data,task_id,workflow_node,task_turn_id) VALUES($1,$2,$3,$4,'{}',$5,$6,$7)`, uuid.Must(uuid.NewV7()), at, agent, kind, task, node.String(), turn.ID)
		if e != nil {
			t.Fatal(e)
		}
	}
	events, next, e := s.TaskNodeEvents(ctx, turn, "", 2)
	if e != nil || len(events) != 2 || next == "" {
		t.Fatal("first page", len(events), next, e)
	}
	last, end, e := s.TaskNodeEvents(ctx, turn, next, 2)
	if e != nil || len(last) != 1 || end != "" || events[1].ID == last[0].ID {
		t.Fatal("last page", e)
	}
	if _, _, e = s.TaskNodeEvents(ctx, turn, "bad", 2); e == nil {
		t.Fatal("bad cursor")
	}
	// A retired instance still owns its history; do not require live Agent config.
	_, e = s.Pool.Exec(ctx, `UPDATE task_instances SET retired=true,session_state='missing' WHERE id=$1`, turn.RuntimeID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.TaskNodeRecord(ctx, task, node, turn.ID); e != nil {
		t.Fatal("cleanup lost history", e)
	}
	totals, e := s.TaskNodeUsage(ctx, turn)
	if e != nil || totals.Total != 0 || totals.Calls != 0 {
		t.Fatal("empty usage", e)
	}
	t.Log("actual TEST schema: scoped historical authority, equal-time paging, cleanup history, empty usage")
	// Model-call entries must come from task usage rows, not normal run history.
	_, e = s.Pool.Exec(ctx, `INSERT INTO usage_records(id,agent_id,task_id,task_turn_id,kind,provider,model,input,output,cache_read,cache_write) VALUES($1,$2,$3,$4,'assistant','fixture-provider','captured-model',7,11,0,0)`, uuid.New(), agent, task, turn.ID)
	if e != nil {
		t.Fatal(e)
	}
	timeline, e := (&runrecord.Service{Store: s}).TaskNodeTimeline(ctx, task, node, turn.ID, "", 100)
	if e != nil || timeline.Usage.Total != 18 || timeline.Usage.Calls != 1 {
		t.Fatal("task usage summary", e)
	}
	found := false
	for _, entry := range timeline.Entries {
		if model, ok := entry.(apitypes.TimelineModelCallEntry); ok {
			found = true
			if model.Model == nil || *model.Model != "captured-model" || model.Input != 7 || model.Output != 11 {
				t.Fatal("task model facts", model)
			}
		}
	}
	if !found {
		t.Fatal("task model call omitted despite recorded usage")
	}
	if _, e = (&runrecord.Service{Store: s}).TaskNodeTimeline(ctx, task, node, turn.ID, "bad", 100); e == nil {
		t.Fatal("invalid projected cursor")
	}
}
