// Package runrecord handles run records and usage: it receives the events and usage reported by the executor and writes
// them to the database (a failed write is only logged and does not affect the run), and it serves the timeline, usage
// statistics and anomaly list queries.
package runrecord

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// writeTimeout is the maximum time for writing one record, so a database problem cannot hold up the executor's reader
// goroutine.
const writeTimeout = 5 * time.Second

// Recorder implements executor.Observer.
type Recorder struct {
	Store *store.Store
	Log   *slog.Logger
	// StatusChanged is called after the Agent's process state changes, to push the overview; may be nil.
	StatusChanged func(agentID uuid.UUID)
}

var _ executor.Observer = (*Recorder)(nil)

// Event implements executor.Observer.
func (r *Recorder) Event(agentID uuid.UUID, runID *uuid.UUID, e executor.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.Store.AddEvent(ctx, store.LifecycleEvent{AgentID: agentID, RunID: runID, Type: e.Type, At: e.At, Data: e.Data})
	if err != nil {
		r.Log.Warn("failed to write the run record", "agentId", agentID, "runId", runID, "type", e.Type, "err", err)
	}
}

// Usage implements executor.Observer.
func (r *Recorder) Usage(agentID uuid.UUID, runID *uuid.UUID, u executor.Usage) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.Store.AddUsage(ctx, store.UsageRecord{AgentID: agentID, RunID: runID, Kind: u.Kind, Provider: u.Provider, Model: u.Model,
		ResponseModel: u.ResponseModel, StopReason: u.StopReason, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead,
		CacheWrite: u.CacheWrite, Reasoning: u.Reasoning, DurationMs: u.DurationMs})
	if err != nil {
		r.Log.Warn("failed to write usage", "agentId", agentID, "runId", runID, "kind", u.Kind, "err", err)
	}
}

// ProcessChanged implements executor.Observer.
func (r *Recorder) ProcessChanged(agentID uuid.UUID) {
	if r.StatusChanged != nil {
		r.StatusChanged(agentID)
	}
}
