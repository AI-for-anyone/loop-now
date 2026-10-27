package runrecord

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"loop-now/internal/server/sleepmodel"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Service 提供时间线、用量统计与异常列表的查询；用量与异常可按项目或平台筛选。
type Service struct {
	Store *store.Store
}

// ---- 用量统计 ----

// Usage 统计范围内 [from, to) 的用量；from、to 为空表示不限。
func (s *Service) Usage(ctx context.Context, f store.ScopeFilter, from, to *time.Time) (apitypes.UsagePage, error) {
	if from != nil && to != nil && !from.Before(*to) {
		return apitypes.UsagePage{}, apperr.Invalid("from", "开始时间必须早于结束时间")
	}
	rows, err := s.Store.UsageSummary(ctx, f, from, to)
	if err != nil {
		return apitypes.UsagePage{}, err
	}
	out := apitypes.UsagePage{From: from, To: to, Rows: make([]apitypes.UsageRow, 0, len(rows))}
	for _, r := range rows {
		out.Rows = append(out.Rows, apitypes.UsageRow{AgentID: r.AgentID, AgentName: r.AgentName, AgentDeleted: r.AgentDeleted,
			Scope: apitypes.ScopeOf(r.ProjectID), ProjectID: r.ProjectID,
			Provider: strPtr(r.Provider), Model: strPtr(r.Model), Calls: r.Calls, Input: r.Input, Output: r.Output,
			CacheRead: r.CacheRead, CacheWrite: r.CacheWrite, Total: r.Total})
	}
	return out, nil
}

// ---- 统计页 ----

// statsSteps 是统计报表可用的桶宽。
var statsSteps = map[string]time.Duration{"hour": time.Hour, "day": 24 * time.Hour}

// Stats 返回范围内 [from, to) 的统计报表，按 bucket（hour 或 day）分桶，最多 1000 桶。
func (s *Service) Stats(ctx context.Context, f store.ScopeFilter, from, to time.Time, bucket string) (store.StatsReport, error) {
	if !from.Before(to) {
		return store.StatsReport{}, apperr.Invalid("from", "开始时间必须早于结束时间")
	}
	step, ok := statsSteps[bucket]
	if !ok {
		return store.StatsReport{}, apperr.Invalid("bucket", "必须是 hour 或 day")
	}
	if (to.Sub(from)+step-1)/step > 1000 {
		return store.StatsReport{}, apperr.Invalid("bucket", "桶数不能超过 1000，请缩短时间范围或改用更宽的桶")
	}
	out, err := s.Store.Stats(ctx, f, from, to, step)
	out.Bucket = bucket
	return out, err
}

// ---- 异常与失败 ----

// Anomalies 返回 from 之后结束的失败与被打断的执行，按结束时间倒序分页。
func (s *Service) Anomalies(ctx context.Context, f store.ScopeFilter, from *time.Time, cursor string, limit int) (apitypes.AnomalyPage, error) {
	var c *store.AnomalyCursor
	if cursor != "" {
		parsed, ok := decodeCursor(cursor)
		if !ok {
			return apitypes.AnomalyPage{}, apperr.Invalid("cursor", "游标无效")
		}
		c = &parsed
	}
	rows, total, next, err := s.Store.Anomalies(ctx, f, from, c, limit)
	if err != nil {
		return apitypes.AnomalyPage{}, err
	}
	out := apitypes.AnomalyPage{Items: make([]apitypes.Anomaly, 0, len(rows)), Total: total}
	for _, r := range rows {
		kind := apitypes.AnomalyRunFailed
		if r.Run.Status == "interrupted" {
			kind = apitypes.AnomalyRunInterrupted
			if r.Run.EndReason == "server_restart" {
				kind = apitypes.AnomalyServerRestart
			}
		}
		out.Items = append(out.Items, apitypes.Anomaly{RunID: r.Run.ID, Kind: kind, At: *r.Run.EndedAt, AgentID: r.Run.AgentID, AgentName: r.AgentName,
			Scope: apitypes.ScopeOf(r.ProjectID), ProjectID: r.ProjectID,
			ErrorCode: strPtr(r.Run.ErrorCode), ErrorMessage: strPtr(r.Run.ErrorMessage), EndReason: strPtr(r.Run.EndReason)})
	}
	if next != nil {
		enc := encodeCursor(*next)
		out.NextCursor = &enc
	}
	return out, nil
}

func encodeCursor(c store.AnomalyCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(c.EndedAt.UTC().Format(time.RFC3339Nano) + "|" + c.RunID.String()))
}

func decodeCursor(s string) (store.AnomalyCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return store.AnomalyCursor{}, false
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return store.AnomalyCursor{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return store.AnomalyCursor{}, false
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return store.AnomalyCursor{}, false
	}
	return store.AnomalyCursor{EndedAt: t, RunID: u}, true
}

// ---- 时间线 ----

// Timeline 返回某个 Agent 的某次执行的时间线。执行不属于该 Agent、或 Agent 不存在或已删除时返回不存在。
func (s *Service) Timeline(ctx context.Context, agentID, runID uuid.UUID) (apitypes.RunTimeline, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return apitypes.RunTimeline{}, err
	}
	run, err := s.Store.GetRun(ctx, runID)
	if err != nil {
		return apitypes.RunTimeline{}, err
	}
	if run.AgentID != agentID {
		return apitypes.RunTimeline{}, apperr.NotFound("执行")
	}
	events, err := s.Store.RunEvents(ctx, runID)
	if err != nil {
		return apitypes.RunTimeline{}, err
	}
	usage, err := s.Store.RunUsage(ctx, runID)
	if err != nil {
		return apitypes.RunTimeline{}, err
	}
	var entries []timed
	if len(events) == 0 {
		entries = legacyEntries(run)
	}
	for _, ev := range events {
		if e, ok := eventEntry(ev); ok {
			entries = append(entries, timed{at: ev.At, v: e})
		}
	}
	entries = append(entries, toolEntries(run.Tools)...)
	commands := toolCommands(events)
	for i := range entries {
		entries[i].v = withToolCommand(entries[i].v, commands)
	}
	for _, u := range usage {
		entries = append(entries, timed{at: u.At, v: apitypes.TimelineModelCallEntry{
			TimelineBase: base(apitypes.TimelineModelCall, u.At), Kind: u.Kind, Provider: strPtr(u.Provider), Model: strPtr(u.Model),
			ResponseModel: strPtr(u.ResponseModel), StopReason: strPtr(u.StopReason), Input: u.Input, Output: u.Output,
			CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, Total: u.Total, DurationMs: u.DurationMs}})
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	out := apitypes.RunTimeline{Run: apitypes.FromRun(run), Entries: make([]any, 0, len(entries)), Usage: apitypes.FromUsageTotals(run.Usage)}
	for _, e := range entries {
		out.Entries = append(out.Entries, e.v)
	}
	return out, nil
}

type timed struct {
	at time.Time
	v  any
}

func base(typ string, at time.Time) apitypes.TimelineBase {
	return apitypes.TimelineBase{Type: typ, At: at}
}

// legacyEntries 为本变更之前、没有生命周期事件的执行按 runs 的列合成条目。
func legacyEntries(r store.Run) []timed {
	var out []timed
	if r.WhenBusy != "" {
		out = append(out, timed{at: r.QueuedAt, v: apitypes.TimelineQueuedEntry{TimelineBase: base(apitypes.TimelineQueued, r.QueuedAt), WhenBusy: r.WhenBusy}})
	}
	if r.StartedAt != nil {
		out = append(out, timed{at: *r.StartedAt, v: base(apitypes.TimelineDispatched, *r.StartedAt)})
	}
	if r.RunningAt != nil {
		out = append(out, timed{at: *r.RunningAt, v: base(apitypes.TimelineRunning, *r.RunningAt)})
	}
	if r.AbortRequestedAt != nil {
		out = append(out, timed{at: *r.AbortRequestedAt, v: apitypes.TimelineAbortRequestedEntry{TimelineBase: base(apitypes.TimelineAbortRequested, *r.AbortRequestedAt)}})
	}
	if r.EndedAt != nil {
		out = append(out, timed{at: *r.EndedAt, v: apitypes.TimelineEndedEntry{TimelineBase: base(apitypes.TimelineEnded, *r.EndedAt),
			Status: r.Status, EndReason: strPtr(r.EndReason), ErrorCode: strPtr(r.ErrorCode)}})
	}
	return out
}

// toolEntries 按 toolCallId 把开始与结束合并为一条，开始时间为条目时间。
func toolEntries(tools []store.ToolEvent) []timed {
	var out []timed
	index := map[string]int{}
	for _, t := range tools {
		if t.State == "started" {
			index[t.ToolCallID] = len(out)
			out = append(out, timed{at: t.At, v: apitypes.TimelineToolEntry{TimelineBase: base(apitypes.TimelineTool, t.At),
				ToolCallID: t.ToolCallID, ToolName: t.ToolName, State: t.State}})
			continue
		}
		i, ok := index[t.ToolCallID]
		if !ok {
			continue
		}
		e := out[i].v.(apitypes.TimelineToolEntry)
		ended := t.At
		d := ended.Sub(e.At).Milliseconds()
		e.State, e.EndedAt, e.DurationMs = t.State, &ended, &d
		out[i].v = e
	}
	return out
}

// eventEntry 把一条生命周期事件转为时间线条目。
func eventEntry(ev store.LifecycleEvent) (any, bool) {
	d := ev.Data
	b := func(typ string) apitypes.TimelineBase { return base(typ, ev.At) }
	switch ev.Type {
	case "sleep.requested", "sleep.sleeping", "sleep.woken", "sleep.cancelled":
		raw, err := json.Marshal(d["sleepRecord"])
		if err != nil {
			return nil, false
		}
		var rec sleepmodel.Record
		if json.Unmarshal(raw, &rec) != nil || rec.ID == uuid.Nil {
			return nil, false
		}
		return apitypes.TimelineSleepEntry{TimelineBase: b("sleep"), Sleep: rec}, true
	case store.EventRunQueued:
		return apitypes.TimelineQueuedEntry{TimelineBase: b(apitypes.TimelineQueued), WhenBusy: str(d, "whenBusy")}, true
	case store.EventRunPromoted:
		e := apitypes.TimelinePromotedEntry{TimelineBase: b(apitypes.TimelinePromoted), From: str(d, "from"), To: str(d, "to")}
		if id, err := uuid.Parse(str(d, "messageId")); err == nil {
			e.MessageID = &id
		}
		return e, true
	case store.EventMessageCancelled:
		id, err := uuid.Parse(str(d, "messageId"))
		return apitypes.TimelineMessageCancelledEntry{TimelineBase: b(apitypes.TimelineMessageCancelled), MessageID: id}, err == nil
	case store.EventRunDispatched:
		return b(apitypes.TimelineDispatched), true
	case store.EventRunPhase:
		return apitypes.TimelinePhaseEntry{TimelineBase: b(apitypes.TimelinePhase), Phase: str(d, "phase")}, true
	case store.EventCLIInstall:
		return apitypes.TimelineCLIInstallEntry{TimelineBase: b(apitypes.TimelineCLIInstall), CLI: str(d, "cli"), Version: str(d, "version"),
			Result: str(d, "result"), DurationMs: num(d, "durationMs"), Error: optStr(d, "error")}, true
	case store.EventWorkspaceSync:
		e := apitypes.TimelineWorkspaceSyncEntry{TimelineBase: b(apitypes.TimelineWorkspaceSync), Result: str(d, "result"),
			DurationMs: num(d, "durationMs"), Error: optStr(d, "error"), Sources: []apitypes.TimelineSyncSource{}}
		if list, ok := d["sources"].([]any); ok {
			for _, item := range list {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				e.Sources = append(e.Sources, apitypes.TimelineSyncSource{Path: str(m, "path"), Kind: str(m, "kind"), Outcome: str(m, "outcome"),
					FilesWritten: optNum(m, "filesWritten"), FilesDeleted: optNum(m, "filesDeleted"), Error: optStr(m, "error")})
			}
		}
		return e, true
	case store.EventPiStarted:
		return apitypes.TimelinePiStartedEntry{TimelineBase: b(apitypes.TimelinePiStarted), PID: num(d, "pid"), StartupMs: num(d, "startupMs"),
			Reason: str(d, "reason"), ConfigVersion: num(d, "configVersion")}, true
	case store.EventPiReused:
		return apitypes.TimelinePiReusedEntry{TimelineBase: b(apitypes.TimelinePiReused), PID: num(d, "pid"), ProcessStartedAt: optTime(d, "startedAt")}, true
	case store.EventPiStopped:
		return apitypes.TimelinePiStoppedEntry{TimelineBase: b(apitypes.TimelinePiStopped), PID: num(d, "pid"), Reason: str(d, "reason"),
			UptimeMs: num(d, "uptimeMs")}, true
	case store.EventPiExited:
		return apitypes.TimelinePiExitedEntry{TimelineBase: b(apitypes.TimelinePiExited), PID: num(d, "pid"), UptimeMs: num(d, "uptimeMs"),
			Exit: str(d, "exit"), StderrTail: str(d, "stderrTail")}, true
	case store.EventRunRunning:
		return b(apitypes.TimelineRunning), true
	case store.EventModelRetryStarted:
		return apitypes.TimelineRetryStartedEntry{TimelineBase: b(apitypes.TimelineModelRetryStarted), Attempt: num(d, "attempt"),
			MaxAttempts: num(d, "maxAttempts"), DelayMs: num(d, "delayMs"), ErrorMessage: str(d, "errorMessage")}, true
	case store.EventModelRetryEnded:
		ok, _ := d["success"].(bool)
		return apitypes.TimelineRetryEndedEntry{TimelineBase: b(apitypes.TimelineModelRetryEnded), Success: ok, Attempt: num(d, "attempt"),
			FinalError: optStr(d, "finalError")}, true
	case store.EventContextCompaction:
		return apitypes.TimelineCompactionEntry{TimelineBase: b(apitypes.TimelineCompaction), Reason: str(d, "reason"), Result: str(d, "result"),
			TokensBefore: optNum(d, "tokensBefore"), EstimatedTokensAfter: optNum(d, "estimatedTokensAfter"),
			ErrorMessage: optStr(d, "errorMessage"), DurationMs: optNum(d, "durationMs")}, true
	case store.EventRunAbortRequested:
		e := apitypes.TimelineAbortRequestedEntry{TimelineBase: b(apitypes.TimelineAbortRequested)}
		if id, err := uuid.Parse(str(d, "byRunId")); err == nil {
			e.ByRunID = &id
		}
		e.Stop, _ = d["stop"].(bool)
		return e, true
	case store.EventRunEnded:
		return apitypes.TimelineEndedEntry{TimelineBase: b(apitypes.TimelineEnded), Status: str(d, "status"), EndReason: optStr(d, "endReason"),
			ErrorCode: optStr(d, "errorCode")}, true
	case store.EventSessionMissing:
		return apitypes.TimelineSessionMissingEntry{TimelineBase: b(apitypes.TimelineSessionMissing), SessionFile: optStr(d, "sessionFile")}, true
	case store.EventExtensionUI:
		return apitypes.TimelineExtensionUIEntry{TimelineBase: b(apitypes.TimelineExtensionUI), Method: str(d, "method"), Message: optStr(d, "message"),
			Level: optStr(d, "level"), StatusKey: optStr(d, "statusKey"), Answered: optStr(d, "answered"), QuestionID: optUUID(d, "questionId")}, true
	}
	return nil, false
}

// optUUID 读出 UUID 字段；没有或不合法时为空。
func optUUID(d map[string]any, key string) *uuid.UUID {
	s, _ := d[key].(string)
	id, err := uuid.Parse(s)
	if err != nil {
		return nil
	}
	return &id
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func str(d map[string]any, k string) string {
	s, _ := d[k].(string)
	return s
}

func optStr(d map[string]any, k string) *string { return strPtr(str(d, k)) }

func num(d map[string]any, k string) int64 {
	if n := optNum(d, k); n != nil {
		return *n
	}
	return 0
}

func optNum(d map[string]any, k string) *int64 {
	f, ok := d[k].(float64)
	if !ok {
		return nil
	}
	n := int64(f)
	return &n
}

func optTime(d map[string]any, k string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, str(d, k))
	if err != nil {
		return nil
	}
	return &t
}
