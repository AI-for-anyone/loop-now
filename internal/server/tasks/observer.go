package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// Observer 将实例进程记录关联到真实Agent和任务轮次；常态记录保持原路径。
type Observer struct {
	Store   *store.Store
	Normal  executor.Observer
	Timeout time.Duration
	Log     *slog.Logger
}

func (o *Observer) Event(runtime uuid.UUID, run *uuid.UUID, e executor.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	inst, err := o.Store.TaskInstance(ctx, runtime)
	if store.IsNotFound(err) {
		o.Normal.Event(runtime, run, e)
		return
	}
	if err != nil {
		o.failed(runtime, run, err)
		return
	}
	var node *string
	var trigger *uuid.UUID
	if run != nil {
		turn, e := o.Store.TaskTurn(ctx, *run)
		if e != nil || turn.RuntimeID != runtime {
			o.failed(runtime, run, errors.New("任务轮次与运行实例不匹配或不可读取"))
			return
		}
		if turn.NodeRunID != uuid.Nil {
			value := turn.NodeRunID.String()
			node = &value
		}
		trigger = turn.TriggerInboxID
	}
	at := e.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		o.failed(runtime, run, err)
		return
	}
	_, err = o.Store.Pool.Exec(ctx, `INSERT INTO lifecycle_events(id,at,agent_id,type,data,instance_id,task_id,workflow_node,task_turn_id,inbox_message_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.Must(uuid.NewV7()), at, inst.AgentID, e.Type, encoded, o.Store.InstanceID, inst.TaskID, node, run, trigger)
	if err != nil {
		o.failed(runtime, run, err)
	}
}
func (o *Observer) Usage(runtime uuid.UUID, run *uuid.UUID, u executor.Usage) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	inst, err := o.Store.TaskInstance(ctx, runtime)
	if store.IsNotFound(err) {
		o.Normal.Usage(runtime, run, u)
		return
	}
	if err != nil {
		o.failed(runtime, run, err)
		return
	}
	if run != nil {
		turn, e := o.Store.TaskTurn(ctx, *run)
		if e != nil || turn.RuntimeID != runtime {
			o.failed(runtime, run, errors.New("任务轮次与运行实例不匹配或不可读取"))
			return
		}
	}
	nullable := func(v string) *string {
		if v == "" {
			return nil
		}
		return &v
	}
	_, err = o.Store.Pool.Exec(ctx, `INSERT INTO usage_records(id,agent_id,task_id,task_turn_id,kind,provider,model,response_model,stop_reason,input,output,cache_read,cache_write,reasoning,duration_ms) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, uuid.Must(uuid.NewV7()), inst.AgentID, inst.TaskID, run, u.Kind, nullable(u.Provider), nullable(u.Model), nullable(u.ResponseModel), nullable(u.StopReason), u.Input, u.Output, u.CacheRead, u.CacheWrite, u.Reasoning, u.DurationMs)
	if err != nil {
		o.failed(runtime, run, err)
	}
}
func (o *Observer) ProcessChanged(runtime uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	inst, err := o.Store.TaskInstance(ctx, runtime)
	if store.IsNotFound(err) {
		o.Normal.ProcessChanged(runtime)
		return
	}
	if err != nil {
		o.failed(runtime, nil, err)
		return
	}
	o.Normal.ProcessChanged(inst.AgentID)
}
func (o *Observer) failed(runtime uuid.UUID, run *uuid.UUID, err error) {
	if err == nil || o.Log == nil {
		return
	}
	attrs := []any{"runtimeId", runtime, "runId", run, "instanceId", o.Store.InstanceID, "error", err}
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	if inst, e := o.Store.TaskInstance(ctx, runtime); e == nil {
		attrs = append(attrs, "agentId", inst.AgentID, "taskId", inst.TaskID)
	}
	if run != nil {
		if t, e := o.Store.TaskTurn(ctx, *run); e == nil {
			attrs = append(attrs, "nodeRunId", taskNodeRef(t.NodeRunID), "inboxMessageId", t.TriggerInboxID, "requestId", t.RequestID)
		}
	}
	o.Log.Warn("写入任务运行记录失败", attrs...)
}

var _ executor.Observer = (*Observer)(nil)
