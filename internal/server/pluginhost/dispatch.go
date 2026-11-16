package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"loop-now/internal/pluginmanifest"
	"loop-now/internal/server/store"
)

// Observe events: one delivery goroutine per loaded plugin, serial within a plugin and parallel across plugins. Due
// pending deliveries are taken in event time order, and only marked done once the plugin host sends ack. Failures
// (plugin error, timeout, host exit) are redelivered according to Backoff and marked failed once it is used up;
// while a redelivery is waiting, later events of the same plugin are delivered as usual, so one failed event never
// holds up the ones after it.

// Notify wakes the delivery goroutines; it is called after new pending deliveries are committed.
func (s *Supervisor) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, lp := range s.loaded {
		select {
		case lp.wake <- struct{}{}:
		default:
		}
	}
}

// startDispatcher starts the delivery goroutine for a newly loaded plugin, plus the schedule goroutine when it has
// scheduled jobs; lp.done is closed once both have finished.
func (s *Supervisor) startDispatcher(c *conn, spec Spec) *loadedPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	lp := &loadedPlugin{spec: spec, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{})}
	var wg sync.WaitGroup
	wg.Go(func() { s.dispatch(ctx, c, lp) })
	if len(spec.Schedules) > 0 {
		wg.Go(func() { s.schedule(ctx, c, lp, &wg) })
	}
	go func() {
		wg.Wait()
		close(lp.done)
	}()
	return lp
}

func (s *Supervisor) dispatch(ctx context.Context, c *conn, lp *loadedPlugin) {
	id := lp.spec.ID
	// The lease covers the time spent waiting for ack: if the server exits while waiting, the next run redelivers
	// once the lease expires.
	lease := 2 * s.o.EventTimeout
	poll := time.NewTicker(s.o.PollInterval)
	defer poll.Stop()
	for ctx.Err() == nil {
		d, err := s.o.Store.ClaimPluginDelivery(ctx, id, lease)
		if err != nil {
			if ctx.Err() == nil {
				s.o.Log.Warn("读取待投递的插件事件失败", "plugin", id, "err", err)
			}
		}
		if d == nil {
			select {
			case <-lp.wake:
			case <-poll.C:
			case <-ctx.Done():
			}
			continue
		}
		payload := map[string]any{"event": EventObject(d.PluginEvent), "attempt": d.Attempts, "timeoutMs": s.o.EventTimeout.Milliseconds()}
		sent := time.Now()
		_, err = c.request(ctx, TypeEvent, id, payload, s.o.EventTimeout, inflight{EventID: d.ID, EventType: d.Type})
		took := time.Since(sent)
		// Writing the result to the database is not cancelled by an unload or a plugin host exit.
		wctx := context.WithoutCancel(ctx)
		switch {
		case err == nil:
			err = s.o.Store.AckPluginDelivery(wctx, id, d.ID, took)
		case ctx.Err() != nil:
			// Unloaded or plugin host stopped: not the plugin's fault, so put it back in the queue right away and
			// redeliver after the next load.
			now := time.Now()
			err = s.o.Store.FailPluginDelivery(wctx, id, d.ID, "投递中断："+err.Error(), &now)
		default:
			reason := err.Error()
			var pe *PluginError
			if errors.As(err, &pe) {
				reason = pe.Message
			}
			retry := s.retryAt(d.Attempts)
			if retry == nil {
				s.enqueueLog(id, "error", "事件 "+d.Type+" "+d.ID+" 投递失败，不再重试："+reason)
			}
			err = s.o.Store.FailPluginDelivery(wctx, id, d.ID, reason, retry)
		}
		if err != nil {
			s.o.Log.Warn("记录插件事件投递结果失败", "plugin", id, "event", d.ID, "err", err)
		}
	}
}

// schedule fires schedule requests according to the manifest's schedule[] (plugins/SPEC.md §11): on each tick it
// looks at the current minute and fires when cron matches and this minute has not fired yet. When the previous run of
// the same entry has not responded yet, this run is skipped; runs missed before loading or while the plugin host was
// stopped are not made up, and the minute in which the plugin was loaded does not fire either (so a reload never
// fires twice).
func (s *Supervisor) schedule(ctx context.Context, c *conn, lp *loadedPlugin, wg *sync.WaitGroup) {
	type entry struct {
		pluginmanifest.Schedule
		cron    pluginmanifest.Cron
		last    time.Time
		running atomic.Bool
	}
	id := lp.spec.ID
	var entries []*entry
	for _, sc := range lp.spec.Schedules {
		cron, err := pluginmanifest.ParseCron(sc.Cron)
		if err != nil {
			s.enqueueLog(id, "error", "定时任务 "+sc.ID+" 的 cron 不合法："+err.Error())
			continue
		}
		entries = append(entries, &entry{Schedule: sc, cron: cron, last: s.o.Now().Truncate(time.Minute)})
	}
	tick := time.NewTicker(s.o.ScheduleTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		minute := s.o.Now().Truncate(time.Minute)
		for _, e := range entries {
			if !minute.After(e.last) || !e.cron.Match(minute) {
				continue
			}
			e.last = minute
			if !e.running.CompareAndSwap(false, true) {
				s.enqueueLog(id, "warn", "定时任务 "+e.ID+" 上一次还没有结束，跳过 "+minute.Format("2006-01-02 15:04"))
				continue
			}
			wg.Go(func() {
				defer e.running.Store(false)
				payload := map[string]any{"scheduleId": e.ID, "cron": e.Cron, "at": minute, "timeoutMs": s.o.ScheduleTimeout.Milliseconds()}
				_, err := c.request(ctx, TypeSchedule, id, payload, s.o.ScheduleTimeout, inflight{})
				if err != nil && ctx.Err() == nil {
					reason := err.Error()
					var pe *PluginError
					if errors.As(err, &pe) {
						reason = pe.Message
					}
					s.enqueueLog(id, "error", "定时任务 "+e.ID+" 失败："+reason)
				}
			})
		}
	}
}

// retryAt returns the redelivery time after failure number attempts; nil when the attempts are used up.
func (s *Supervisor) retryAt(attempts int) *time.Time {
	if attempts > len(s.o.Backoff) {
		return nil
	}
	at := time.Now().Add(s.o.Backoff[attempts-1])
	return &at
}

// EventObject expands an event record into the object sent to the plugin: the fixed fields plus the fields of that
// event type; the fixed fields take precedence.
func EventObject(ev store.PluginEvent) map[string]any {
	obj := map[string]any{}
	if len(ev.Data) > 0 {
		_ = json.Unmarshal(ev.Data, &obj)
	}
	obj["id"] = ev.ID
	obj["type"] = ev.Type
	obj["at"] = ev.At.UTC().Format(time.RFC3339Nano)
	obj["projectId"] = ev.ProjectID
	obj["agentId"] = ev.AgentID
	obj["taskId"] = ev.TaskID
	obj["runId"] = ev.RunID
	return obj
}
