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

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Service provides queries for the timeline, usage statistics and the anomaly list; usage and anomalies can be filtered
// by project or platform.
type Service struct {
	Store *store.Store
}

// ---- Usage statistics ----

// Usage sums the usage within [from, to) of the scope; a nil from or to means unbounded.
func (s *Service) Usage(ctx context.Context, f store.ScopeFilter, from, to *time.Time) (apitypes.UsagePage, error) {
	if from != nil && to != nil && !from.Before(*to) {
		return apitypes.UsagePage{}, apperr.Invalid("from", "Must be earlier than to.")
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

// ---- Stats page ----

// statsSteps are the bucket widths available for the stats report.
var statsSteps = map[string]time.Duration{"hour": time.Hour, "day": 24 * time.Hour}

// Stats returns the stats report within [from, to) of the scope, bucketed by bucket (hour or day), at most 1000
// buckets.
func (s *Service) Stats(ctx context.Context, f store.ScopeFilter, from, to time.Time, bucket string) (store.StatsReport, error) {
	if !from.Before(to) {
		return store.StatsReport{}, apperr.Invalid("from", "Must be earlier than to.")
	}
	step, ok := statsSteps[bucket]
	if !ok {
		return store.StatsReport{}, apperr.Invalid("bucket", "Must be hour or day.")
	}
	if (to.Sub(from)+step-1)/step > 1000 {
		return store.StatsReport{}, apperr.Invalid("bucket", "Must not produce more than 1000 buckets. Shorten the time range or use a wider bucket.")
	}
	out, err := s.Store.Stats(ctx, f, from, to, step)
	out.Bucket = bucket
	return out, err
}

// ---- Anomalies and failures ----

// Anomalies returns failed and interrupted runs that ended after from, paginated by end time, newest first.
func (s *Service) Anomalies(ctx context.Context, f store.ScopeFilter, from *time.Time, cursor string, limit int) (apitypes.AnomalyPage, error) {
	var c *store.AnomalyCursor
	if cursor != "" {
		parsed, ok := decodeCursor(cursor)
		if !ok {
			return apitypes.AnomalyPage{}, apperr.Invalid("cursor", "The cursor is invalid.")
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
		var runtime *string
		if sel := r.Run.SelectionInfo(); sel != nil && sel.Runtime != "" {
			runtime = &sel.Runtime
		}
		out.Items = append(out.Items, apitypes.Anomaly{RunID: r.Run.ID, Kind: kind, At: *r.Run.EndedAt, AgentID: r.Run.AgentID, AgentName: r.AgentName,
			Scope: apitypes.ScopeOf(r.ProjectID), ProjectID: r.ProjectID, Runtime: runtime,
			ErrorCode: strPtr(r.Run.ErrorCode), ErrorMessage: strPtr(r.Run.ErrorMessage), EndReason: strPtr(r.Run.EndReason), Compaction: apitypes.CompactionOf(r.Run)})
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

// ---- Timeline ----

// Timeline returns the timeline of one run of an Agent. When the run does not belong to that Agent, or the Agent does
// not exist or was deleted, it returns not found.
func (s *Service) Timeline(ctx context.Context, agentID, runID uuid.UUID) (apitypes.RunTimeline, error) {
	if _, err := s.Store.GetAgent(ctx, agentID); err != nil {
		return apitypes.RunTimeline{}, err
	}
	run, err := s.Store.GetRun(ctx, runID)
	if err != nil {
		return apitypes.RunTimeline{}, err
	}
	if run.AgentID != agentID {
		return apitypes.RunTimeline{}, apperr.NotFound("Run")
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
	for _, b := range backgroundEntries(run.BackgroundTasks) {
		entries = append(entries, timed{at: b.at, v: b.value})
	}
	requests, err := s.Store.RunPermissionRequests(ctx, runID)
	if err != nil {
		return apitypes.RunTimeline{}, err
	}
	for _, p := range permissionEntries(requests) {
		entries = append(entries, timed{at: p.at, v: p.value})
	}
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
	// On equal times the "ended" entry sorts last: with a coarse clock (Windows), the last tool call can have the same
	// time as the end.
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].at.Equal(entries[j].at) {
			return entries[i].at.Before(entries[j].at)
		}
		return !isEnded(entries[i].v) && isEnded(entries[j].v)
	})
	out := apitypes.RunTimeline{Run: apitypes.FromRun(run), Entries: make([]any, 0, len(entries)), Usage: apitypes.FromUsageTotals(run.Usage)}
	for _, e := range entries {
		out.Entries = append(out.Entries, e.v)
	}
	fillPhaseRuntimes(len(out.Entries), func(i int) any { return out.Entries[i] }, func(i int, v any) { out.Entries[i] = v })
	return out, nil
}

func isEnded(v any) bool {
	_, ok := v.(apitypes.TimelineEndedEntry)
	return ok
}

type timed struct {
	at time.Time
	v  any
}

func base(typ string, at time.Time) apitypes.TimelineBase {
	return apitypes.TimelineBase{Type: typ, At: at}
}

// legacyEntries synthesizes entries from the runs columns for runs from before this change, which have no lifecycle
// events.
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

// toolEntries merges the start and end of a tool call into one entry by toolCallId; the start time is the entry's time.
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

// recordedRuntime is the runtime recorded with an event; empty when the event has none (written before it was
// recorded) or names no registered runtime.
func recordedRuntime(d map[string]any) string {
	if v := str(d, "runtime"); agentruntime.Known(v) {
		return v
	}
	return ""
}

// runtimeOf is the runtime of a process event. Process events written before the runtime was recorded get pi, a rule
// for those old records only: every process was Pi then.
func runtimeOf(d map[string]any) string {
	if v := recordedRuntime(d); v != "" {
		return v
	}
	return agentruntime.Pi
}

// fillPhaseRuntimes gives every phase entry in a run's entries (in time order) its runtime. Phase events written before
// the runtime was recorded get it by a rule for those old records only, the one the console used to apply: the runtime
// of the first process entry (pi_started or pi_reused) after the phase and before the run ended; without one,
// claude-code when the run ended with a runtime_* error code (codes only Claude Code reported then); otherwise pi.
func fillPhaseRuntimes(n int, get func(int) any, set func(int, any)) {
	for i := 0; i < n; i++ {
		phase, ok := get(i).(apitypes.TimelinePhaseEntry)
		if !ok || phase.Runtime != "" {
			continue
		}
		phase.Runtime = agentruntime.Pi
	scan:
		for j := i + 1; j < n; j++ {
			switch e := get(j).(type) {
			case apitypes.TimelinePiStartedEntry:
				phase.Runtime = e.Runtime
				break scan
			case apitypes.TimelinePiReusedEntry:
				phase.Runtime = e.Runtime
				break scan
			case apitypes.TimelineEndedEntry:
				if e.ErrorCode != nil && strings.HasPrefix(*e.ErrorCode, "runtime_") {
					phase.Runtime = agentruntime.ClaudeCode
				}
				break scan
			}
		}
		set(i, phase)
	}
}

// eventEntry turns one lifecycle event into a timeline entry.
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
		return apitypes.TimelinePhaseEntry{TimelineBase: b(apitypes.TimelinePhase), Phase: str(d, "phase"), Runtime: recordedRuntime(d)}, true
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
			Reason: str(d, "reason"), ConfigVersion: num(d, "configVersion"), Runtime: runtimeOf(d)}, true
	case store.EventPiReused:
		return apitypes.TimelinePiReusedEntry{TimelineBase: b(apitypes.TimelinePiReused), PID: num(d, "pid"), ProcessStartedAt: optTime(d, "startedAt"), Runtime: runtimeOf(d)}, true
	case store.EventPiStopped:
		return apitypes.TimelinePiStoppedEntry{TimelineBase: b(apitypes.TimelinePiStopped), PID: num(d, "pid"), Reason: str(d, "reason"),
			UptimeMs: num(d, "uptimeMs"), Runtime: runtimeOf(d)}, true
	case store.EventPiExited:
		return apitypes.TimelinePiExitedEntry{TimelineBase: b(apitypes.TimelinePiExited), PID: num(d, "pid"), UptimeMs: num(d, "uptimeMs"),
			Exit: str(d, "exit"), StderrTail: str(d, "stderrTail"), Runtime: runtimeOf(d)}, true
	case store.EventModelRateLimit:
		info, ok := d["info"].(map[string]any)
		return apitypes.TimelineModelRateLimitEntry{TimelineBase: b(apitypes.TimelineModelRateLimit), Info: info}, ok
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
	case store.EventBackgroundWaitTimeout:
		return apitypes.TimelineBackgroundWaitTimeoutEntry{TimelineBase: b(apitypes.TimelineBackgroundWait), LimitMs: num(d, "limitMs")}, true
	case store.EventContextHandoff:
		reason := str(d, "reason")
		if reason == "" {
			reason = store.HandoffRuntimeChanged // records from before the reason was kept
		}
		return apitypes.TimelineContextHandoffEntry{TimelineBase: b(apitypes.TimelineContextHandoff), ContextHandoff: apitypes.ContextHandoff{
			Reason: reason, FromRuntime: str(d, "fromRuntime"), ToRuntime: str(d, "toRuntime"), IncludedMessages: int(num(d, "includedMessages")),
			OmittedMessages: int(num(d, "omittedMessages")), Images: int(num(d, "images")), Bytes: int(num(d, "bytes")), BudgetBytes: int(num(d, "budgetBytes"))}}, true
	case store.EventExtensionUI:
		return apitypes.TimelineExtensionUIEntry{TimelineBase: b(apitypes.TimelineExtensionUI), Method: str(d, "method"), Message: optStr(d, "message"),
			Level: optStr(d, "level"), StatusKey: optStr(d, "statusKey"), Answered: optStr(d, "answered"), QuestionID: optUUID(d, "questionId")}, true
	}
	return nil, false
}

// optUUID reads a UUID field; nil when missing or invalid.
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
