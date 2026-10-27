package runrecord

import (
	"context"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"sort"
	"time"
)

// TaskNodeTimeline is separate from Timeline's normal runs authority. It stays
// available after task cleanup and never resolves current Agent configuration.
func (s *Service) TaskNodeTimeline(ctx context.Context, task, node, run uuid.UUID, cursor string, limit int) (apitypes.TaskNodeRunTimeline, error) {
	var out apitypes.TaskNodeRunTimeline
	t, agent, e := s.Store.TaskNodeRecord(ctx, task, node, run)
	if e != nil {
		return out, e
	}
	if limit < 1 || limit > 200 {
		return out, apperr.Invalid("limit", "必须为1..200")
	}
	var after time.Time
	var afterID uuid.UUID
	if cursor != "" {
		var e error
		after, afterID, e = store.ParseTaskNodeRecordCursor(cursor)
		if e != nil {
			return out, e
		}
	}
	events, e := s.Store.TaskNodeAllEvents(ctx, t)
	if e != nil {
		return out, e
	}
	totals, e := s.Store.TaskNodeUsage(ctx, t)
	if e != nil {
		return out, e
	}
	usage, e := s.Store.TaskNodeUsageRecords(ctx, t)
	if e != nil {
		return out, e
	}
	out = apitypes.TaskNodeRunTimeline{TaskID: task, NodeRunID: node, AgentID: agent, RuntimeID: t.RuntimeID, Run: apitypes.TaskNodeRunSummary{ID: t.ID, Origin: t.Origin, ExecutionMode: t.ExecutionMode, Status: t.Status, QueuedAt: t.QueuedAt, StartedAt: t.StartedAt, EndedAt: t.EndedAt, ErrorCode: t.ErrorCode, ErrorMessage: taskmodel.PublicError(t.ErrorCode, t.ErrorMessage)}, Entries: []any{}, Usage: apitypes.FromUsageTotals(totals)}
	for i := range events {
		if events[i].Type == store.EventRunQueued {
			if events[i].Data == nil {
				events[i].Data = map[string]any{}
			}
			busy := "queue"
			if t.Priority == 0 {
				busy = "interrupt"
			}
			events[i].Data["whenBusy"] = busy
		}
	}
	projected := taskNodeEntries(events)
	commands := toolCommands(events)
	for i := range projected {
		projected[i].value = withToolCommand(projected[i].value, commands)
	}
	for _, u := range usage {
		projected = append(projected, taskNodeTimelineItem{id: u.ID, at: u.At, value: apitypes.TimelineModelCallEntry{TimelineBase: base(apitypes.TimelineModelCall, u.At), Kind: u.Kind, Provider: strPtr(u.Provider), Model: strPtr(u.Model), ResponseModel: strPtr(u.ResponseModel), StopReason: strPtr(u.StopReason), Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, Total: u.Total, DurationMs: u.DurationMs}})
	}
	sort.Slice(projected, func(i, j int) bool {
		if projected[i].at.Equal(projected[j].at) {
			return projected[i].id.String() < projected[j].id.String()
		}
		return projected[i].at.Before(projected[j].at)
	})
	if cursor != "" {
		keep := projected[:0]
		for _, v := range projected {
			if v.at.After(after) || v.at.Equal(after) && v.id.String() > afterID.String() {
				keep = append(keep, v)
			}
		}
		projected = keep
	}
	if len(projected) > limit {
		projected = projected[:limit]
		last := projected[len(projected)-1]
		next := store.TaskNodeRecordCursor(last.at, last.id)
		out.NextCursor = &next
	}
	for _, v := range projected {
		out.Entries = append(out.Entries, v.value)
	}
	return out, nil
}

func taskNodeEventEntry(ev store.LifecycleEvent) (any, bool) {
	if ev.Type == "tool.changed" {
		id, name, state := str(ev.Data, "toolCallId"), str(ev.Data, "toolName"), str(ev.Data, "state")
		if id == "" || name == "" || (state != "started" && state != "succeeded" && state != "failed") {
			return nil, false
		}
		item := apitypes.TimelineToolEntry{TimelineBase: base(apitypes.TimelineTool, ev.At), ToolCallID: id, ToolName: name, State: state}
		if state != "started" {
			item.EndedAt = &ev.At
		}
		// No fabricated start or duration when only an end observation exists.
		return item, true
	}
	if ev.Type == store.EventRunEnded {
		code := optStr(ev.Data, "errorCode")
		if code != nil {
			// TimelineEnded shares the closed RunErrorCode contract. Task-only
			// codes remain available in the independent run summary, not here.
			switch *code {
			case "credential_unavailable", "cli_install_failed", "workspace_sync_failed", "session_missing", "pi_start_failed", "pi_exited", "pi_error", "internal_error":
			default:
				code = nil
			}
		}
		return apitypes.TimelineEndedEntry{TimelineBase: base(apitypes.TimelineEnded, ev.At), Status: str(ev.Data, "outcome"), ErrorCode: code}, true
	}
	return eventEntry(ev)
}

type taskNodeTimelineItem struct {
	id    uuid.UUID
	at    time.Time
	value any
}

func taskNodeEntries(events []store.LifecycleEvent) []taskNodeTimelineItem {
	out := []taskNodeTimelineItem{}
	tools := map[string]int{}
	starts := map[string]time.Time{}
	for _, ev := range events {
		value, ok := taskNodeEventEntry(ev)
		if !ok {
			continue
		}
		tool, isTool := value.(apitypes.TimelineToolEntry)
		if !isTool {
			out = append(out, taskNodeTimelineItem{id: ev.ID, at: ev.At, value: value})
			continue
		}
		idx, exists := tools[tool.ToolCallID]
		if !exists {
			idx = len(out)
			tools[tool.ToolCallID] = idx
			out = append(out, taskNodeTimelineItem{id: ev.ID, at: ev.At, value: tool})
		}
		old := out[idx].value.(apitypes.TimelineToolEntry)
		if tool.State == "started" {
			if _, known := starts[tool.ToolCallID]; !known {
				starts[tool.ToolCallID] = ev.At
				old.At = ev.At
				out[idx].at = ev.At
				out[idx].id = ev.ID
			}
		} else {
			old.State = tool.State
			old.EndedAt = &ev.At
		}
		if start, known := starts[tool.ToolCallID]; known && old.EndedAt != nil && !old.EndedAt.Before(start) {
			d := old.EndedAt.Sub(start).Milliseconds()
			old.DurationMs = &d
		}
		out[idx].value = old
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].at.Equal(out[j].at) {
			return out[i].id.String() < out[j].id.String()
		}
		return out[i].at.Before(out[j].at)
	})
	return out
}
