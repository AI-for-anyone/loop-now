// Package runrecord 负责运行记录与用量：接收执行器回报的事件与用量并写入数据库（记录失败只写日志，
// 不影响执行），以及时间线、用量统计与异常列表的查询。
package runrecord

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// writeTimeout 是单条记录写入的最长时间，避免数据库异常时拖住执行器的读协程。
const writeTimeout = 5 * time.Second

// Recorder 实现 executor.Observer。
type Recorder struct {
	Store *store.Store
	Log   *slog.Logger
	// StatusChanged 在 Agent 的进程状态变化后调用，用于推送概览；可为空。
	StatusChanged func(agentID uuid.UUID)
}

var _ executor.Observer = (*Recorder)(nil)

// Event 实现 executor.Observer。
func (r *Recorder) Event(agentID uuid.UUID, runID *uuid.UUID, e executor.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.Store.AddEvent(ctx, store.LifecycleEvent{AgentID: agentID, RunID: runID, Type: e.Type, At: e.At, Data: e.Data})
	if err != nil {
		r.Log.Warn("写入运行记录失败", "agentId", agentID, "runId", runID, "type", e.Type, "err", err)
	}
}

// Usage 实现 executor.Observer。
func (r *Recorder) Usage(agentID uuid.UUID, runID *uuid.UUID, u executor.Usage) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.Store.AddUsage(ctx, store.UsageRecord{AgentID: agentID, RunID: runID, Kind: u.Kind, Provider: u.Provider, Model: u.Model,
		ResponseModel: u.ResponseModel, StopReason: u.StopReason, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead,
		CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, DurationMs: u.DurationMs})
	if err != nil {
		r.Log.Warn("写入用量失败", "agentId", agentID, "runId", runID, "kind", u.Kind, "err", err)
	}
}

// ProcessChanged 实现 executor.Observer。
func (r *Recorder) ProcessChanged(agentID uuid.UUID) {
	if r.StatusChanged != nil {
		r.StatusChanged(agentID)
	}
}
