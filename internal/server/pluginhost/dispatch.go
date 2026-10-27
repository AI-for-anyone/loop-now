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

// 观察类事件：每个已加载的插件一个投递协程，同一插件串行、不同插件并行；按事件时间取到期的待投递记录，
// 宿主回 ack 才标为完成。失败（插件报错、超时、宿主退出）按 Backoff 重投，用尽后标为 failed；
// 重投等待期间同一插件更晚的事件照常投递，一条失败的事件不会卡住后面的。

// Notify 唤醒投递协程，在新的待投递记录提交后调用。
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

// startDispatcher 为刚加载的插件启动投递协程，有定时任务时再启动定时协程；两者都结束后 lp.done 关闭。
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
	// 租约覆盖等待 ack 的时长：服务端在等待期间退出时，租约到期后由下次运行重投。
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
		// 结果写库不随卸载或宿主退出取消。
		wctx := context.WithoutCancel(ctx)
		switch {
		case err == nil:
			err = s.o.Store.AckPluginDelivery(wctx, id, d.ID, took)
		case ctx.Err() != nil:
			// 卸载或宿主停止：不是插件的问题，立即放回队列，下次加载后重投。
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

// schedule 按清单 schedule[] 触发 schedule 请求（plugins/SPEC.md §11）：每个刻度看当前这一分钟，
// cron 匹配且这一分钟还没有触发过时触发。同一条目上一次还没有回应时跳过这一次；加载之前与宿主停止期间错过的不补，
// 加载时所在的那一分钟也不触发（重新加载不会重复触发）。
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

// retryAt 返回第 attempts 次失败后的重投时间；次数用尽时返回 nil。
func (s *Supervisor) retryAt(attempts int) *time.Time {
	if attempts > len(s.o.Backoff) {
		return nil
	}
	at := time.Now().Add(s.o.Backoff[attempts-1])
	return &at
}

// EventObject 把事件记录展开成发给插件的对象：固定字段加该类型自己的字段，固定字段优先。
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
