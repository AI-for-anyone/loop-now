// Package conversation 协调控制台与常态 Agent 的对话：发送消息、忙闲锁、排队与打断、
// 每个 Agent 的驱动循环、服务端重启后的处理，以及 Redis 数据丢失后的对账。
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// Options 是对话服务的参数。
type Options struct {
	InstanceID        uuid.UUID
	MaxMessage        int64
	LockRenewInterval time.Duration
	ReconcileInterval time.Duration
}

// Service 是对话服务。
type Service struct {
	st  *store.Store
	co  *coord.Coord
	ex  executor.Executor
	o   Options
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	drivers map[uuid.UUID]bool

	overviewMu      sync.Mutex
	overviewWorkers map[uuid.UUID]*overviewWorker
	overviewWG      sync.WaitGroup
	sleepRevisions  sync.Map
	dispatchGates   sync.Map // AgentID -> chan struct{}；任务分叉只阻止新轮派发，不打断当前轮

	// InboxCancelled 在一条 Inbox 消息被人取消、事务提交后调用（追加收件箱事件）；为空时不推送。
	InboxCancelled func(ctx context.Context, m store.InboxMessage)
}

// New 创建对话服务；调用 Start 后才开始处理排队消息。
func New(st *store.Store, co *coord.Coord, ex executor.Executor, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{st: st, co: co, ex: ex, o: o, log: log, ctx: ctx, cancel: cancel, drivers: map[uuid.UUID]bool{},
		overviewWorkers: map[uuid.UUID]*overviewWorker{}}
}

// Start 处理上次运行遗留的进行中执行（标为“服务端重启中断”），然后启动对账协程。
func (s *Service) Start(ctx context.Context) error {
	if err := s.recoverAfterRestart(ctx); err != nil {
		return err
	}
	s.reconcile(ctx)
	s.wg.Go(s.reconcileLoop)
	return nil
}

// Stop 停止接收新的派发，等待驱动循环退出。进行中的执行保持原状态，下次启动时按重启中断处理。
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	// 取消后在锁内不会再启动新的概览推送协程，此后等待它们是安全的。
	s.overviewMu.Lock()
	s.overviewMu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); s.overviewWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- 发送消息 ----

// Send 接收控制台消息。Agent 空闲时立即开始处理；忙时必须选择打断或排队。
func (s *Service) Send(ctx context.Context, agentID uuid.UUID, text, whenBusy string, imageIDs ...uuid.UUID) (apitypes.Message, error) {
	if strings.TrimSpace(text) == "" && len(imageIDs) == 0 {
		return apitypes.Message{}, apperr.Invalid("text", "消息不能为空")
	}
	if int64(len(text)) > s.o.MaxMessage {
		return apitypes.Message{}, apperr.TooLarge("消息超过长度上限")
	}
	if whenBusy != "" && whenBusy != "interrupt" && whenBusy != "queue" {
		return apitypes.Message{}, apperr.Invalid("whenBusy", "只能是 interrupt 或 queue")
	}
	if s.ctx.Err() != nil {
		return apitypes.Message{}, errors.New("服务端正在停止")
	}

	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.AcquireIfIdle(ctx, agentID.String(), owner)
	if err != nil {
		return apitypes.Message{}, err
	}
	if acquired {
		msg, run, err := s.st.InsertMessageAndRun(ctx, store.NewRun{ImageIDs: imageIDs, MaxImages: s.st.ImageMaxCount, AgentID: agentID, RunID: runID, Text: text, Start: true, InstanceID: s.o.InstanceID,
			RequestID: reqid.From(ctx)})
		switch {
		case err == nil:
			s.emitMessage(ctx, agentID, msg, run)
			s.emitRun(ctx, agentID, run.ID)
			s.startDriver(agentID, run, owner)
			return apitypes.FromMessage(msg, run), nil
		case errors.Is(err, store.ErrActiveRunExists):
			// Redis 丢了锁：按数据库中的进行中执行恢复锁，然后按“忙”处理。
			_, _ = s.co.Release(ctx, agentID.String(), owner)
			s.restoreLock(ctx, agentID)
		default:
			_, _ = s.co.Release(ctx, agentID.String(), owner)
			return apitypes.Message{}, err
		}
	}

	if whenBusy == "" {
		return apitypes.Message{}, s.busyError(ctx, agentID)
	}
	msg, run, err := s.st.InsertMessageAndRun(ctx, store.NewRun{
		ImageIDs: imageIDs, MaxImages: s.st.ImageMaxCount, AgentID: agentID, RunID: runID, Text: text, WhenBusy: whenBusy, InstanceID: s.o.InstanceID, RequestID: reqid.From(ctx),
		Enqueue: func(r store.Run) error {
			if whenBusy == "interrupt" {
				return s.co.PushInterrupt(ctx, agentID.String(), r.ID.String())
			}
			return s.co.PushNormal(ctx, agentID.String(), r.ID.String())
		},
		OnEnqueueError: func(err error) {
			s.log.Warn("写入 Redis 队列失败，等待对账补回", "agentId", agentID, "runId", runID, "err", err)
		},
	})
	if err != nil {
		return apitypes.Message{}, err
	}
	s.emitMessage(ctx, agentID, msg, run)
	s.emitRun(ctx, agentID, run.ID)
	s.emitQueue(ctx, agentID)
	if whenBusy == "interrupt" {
		s.interruptActive(ctx, agentID, run.ID)
	}
	s.kick(ctx, agentID)
	return apitypes.FromMessage(msg, run), nil
}

func (s *Service) busyError(ctx context.Context, agentID uuid.UUID) error {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return err
	}
	counts, queued, err := s.queueCounts(ctx, agentID)
	if err != nil {
		return err
	}
	details := apitypes.AgentBusyDetails{Queued: counts}
	if active, err := s.st.ActiveRun(ctx, agentID); err != nil {
		return err
	} else if active != nil {
		details.CurrentRunID = active.ID
	} else if len(queued) > 0 {
		details.CurrentRunID = queued[0].ID
		s.kick(ctx, agentID)
	}
	e := apperr.Conflict(apperr.CodeAgentBusy, "Agent 正在回复，请选择打断或排队")
	e.Details = details
	return e
}

func (s *Service) queueCounts(ctx context.Context, agentID uuid.UUID) (apitypes.QueueCounts, []store.Run, error) {
	queued, err := s.st.QueuedRuns(ctx, agentID)
	if err != nil {
		return apitypes.QueueCounts{}, nil, err
	}
	var c apitypes.QueueCounts
	for _, r := range queued {
		if r.Priority == 0 {
			c.Interrupt++
		} else {
			c.Normal++
		}
	}
	return c, queued, nil
}

// interruptActive 把进行中的执行转为打断中（byRunID 为引发打断的消息所属的执行），并经 Redis 通知持有它的驱动循环。
func (s *Service) interruptActive(ctx context.Context, agentID, byRunID uuid.UUID) {
	if h := testHookBeforeMarkAborting.Load(); h != nil {
		(*h)(byRunID)
	}
	active, err := s.st.MarkAborting(ctx, agentID, byRunID)
	if err != nil {
		s.log.Error("标记打断失败", "agentId", agentID, "err", err)
		return
	}
	if active == nil {
		return
	}
	s.emitRun(ctx, agentID, active.ID)
	if err := s.co.PublishInterrupt(ctx, agentID.String(), active.ID.String()); err != nil {
		s.log.Warn("发布打断命令失败，等待驱动循环的定时检查", "agentId", agentID, "err", err)
	}
}

// restoreLock 按数据库中的进行中执行补设忙闲锁。
func (s *Service) restoreLock(ctx context.Context, agentID uuid.UUID) {
	active, err := s.st.ActiveRun(ctx, agentID)
	if err != nil || active == nil || active.InstanceID == nil {
		return
	}
	_, _ = s.co.Restore(ctx, agentID.String(), coord.LockValue(active.ID.String(), active.InstanceID.String()))
}

// ---- 驱动循环 ----

// kick 在本进程没有该 Agent 的驱动循环、且锁空闲时，取出队首消息开始处理。
func (s *Service) kick(ctx context.Context, agentID uuid.UUID) {
	if s.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	running := s.drivers[agentID]
	s.mu.Unlock()
	if running {
		return
	}
	next, ok, err := s.co.Handoff(ctx, agentID.String(), "", s.o.InstanceID.String())
	if err != nil || !ok || next == "" {
		return
	}
	s.startNext(ctx, agentID, next)
}

// startNext 从刚取得锁的执行开始；它已不在排队状态时继续交接下一条。
func (s *Service) startNext(ctx context.Context, agentID uuid.UUID, next string) {
	for next != "" {
		owner := coord.LockValue(next, s.o.InstanceID.String())
		runID, err := uuid.Parse(next)
		if err == nil {
			run, started, err := s.st.StartQueuedRun(ctx, runID, s.o.InstanceID)
			if err == nil && started {
				s.emitRun(ctx, agentID, run.ID)
				s.emitQueue(ctx, agentID)
				s.startDriver(agentID, run, owner)
				return
			}
			if err != nil {
				s.log.Error("开始排队执行失败", "agentId", agentID, "runId", next, "err", err)
				_, _ = s.co.Release(ctx, agentID.String(), owner)
				return
			}
		}
		var ok bool
		next, ok, err = s.co.Handoff(ctx, agentID.String(), owner, s.o.InstanceID.String())
		if err != nil || !ok {
			return
		}
	}
}

func (s *Service) startDriver(agentID uuid.UUID, run store.Run, owner string) {
	s.mu.Lock()
	if s.drivers[agentID] {
		s.mu.Unlock()
		s.log.Error("驱动循环已存在，放弃重复启动", "agentId", agentID, "runId", run.ID)
		return
	}
	s.drivers[agentID] = true
	s.mu.Unlock()
	s.wg.Go(func() { s.drive(agentID, run, owner) })
}

func (s *Service) drive(agentID uuid.UUID, run store.Run, owner string) {
	ctx := s.ctx
	defer func() {
		s.mu.Lock()
		delete(s.drivers, agentID)
		s.mu.Unlock()
		if ctx.Err() == nil {
			s.kick(context.Background(), agentID) // 退出期间若有新排队消息，接着处理
		}
	}()
	subCtx, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	var cur sync.Map // "run" -> uuid.UUID
	interrupts, err := s.co.SubscribeInterrupts(subCtx, agentID.String())
	if err != nil {
		s.log.Warn("订阅打断命令失败，改由定时检查发现打断", "agentId", agentID, "err", err)
	} else {
		go func() {
			for id := range interrupts {
				if v, ok := cur.Load("run"); ok && v.(uuid.UUID).String() == id {
					runID := v.(uuid.UUID)
					beforeInterrupt(runID)
					_ = s.ex.Interrupt(ctx, agentID, runID)
				}
			}
		}()
	}

	for {
		cur.Store("run", run.ID)
		res := s.execute(ctx, agentID, run, owner)
		if ctx.Err() != nil {
			return // 服务端停止：执行保持原状态，下次启动时标为重启中断
		}
		s.finish(ctx, run, res)

		next, ok, err := s.co.Handoff(ctx, agentID.String(), owner, s.o.InstanceID.String())
		if err == nil && !ok {
			// 锁已不是本执行持有（例如 Redis 数据丢失）：锁空闲时直接交接。
			next, ok, err = s.co.Handoff(ctx, agentID.String(), "", s.o.InstanceID.String())
		}
		if err != nil || !ok || next == "" {
			if err != nil {
				s.log.Warn("交接忙闲锁失败，等待对账", "agentId", agentID, "err", err)
			}
			return
		}
		started := false
		for next != "" && !started {
			owner = coord.LockValue(next, s.o.InstanceID.String())
			if runID, perr := uuid.Parse(next); perr == nil {
				var serr error
				run, started, serr = s.st.StartQueuedRun(ctx, runID, s.o.InstanceID)
				if serr != nil {
					s.log.Error("开始排队执行失败", "agentId", agentID, "runId", next, "err", serr)
					_, _ = s.co.Release(ctx, agentID.String(), owner)
					return
				}
			}
			if !started {
				if next, ok, err = s.co.Handoff(ctx, agentID.String(), owner, s.o.InstanceID.String()); err != nil || !ok {
					return
				}
			}
		}
		if !started {
			return
		}
		s.emitRun(ctx, agentID, run.ID)
		s.emitQueue(ctx, agentID)
	}
}

// testHookBeforeInterrupt 只供测试使用：确认某次执行处于打断中之后、调用执行器的 Interrupt 之前调用，
// 用来在这两步之间注入时序（例如让该执行先结束、下一次执行开始）。生产环境为空。
var testHookBeforeInterrupt atomic.Pointer[func(runID uuid.UUID)]

// testHookBeforeMarkAborting 只供测试使用：引发打断的执行确定之后、标记进行中执行为打断中之前调用，
// 用来让引发打断的那条执行先被出队并开始。生产环境为空。
var testHookBeforeMarkAborting atomic.Pointer[func(byRunID uuid.UUID)]

func beforeInterrupt(runID uuid.UUID) {
	if h := testHookBeforeInterrupt.Load(); h != nil {
		(*h)(runID)
	}
}

// execute 调用执行器处理一次执行，期间续期忙闲锁，并定时检查数据库中的打断标记（防止 Redis 通知丢失）。
func (s *Service) execute(ctx context.Context, agentID uuid.UUID, run store.Run, owner string) executor.Result {
	attrs := []any{"runId", run.ID, "agentId", agentID, "whenBusy", run.WhenBusy}
	if run.StartedAt != nil {
		attrs = append(attrs, "queuedMs", run.StartedAt.Sub(run.QueuedAt).Milliseconds())
	}
	s.log.Info("回复开始", attrs...)
	if st, err := s.st.RunStatus(ctx, run.ID); err == nil && st == "aborting" {
		return executor.Result{Outcome: executor.OutcomeInterrupted}
	}
	msg, err := s.st.GetMessage(ctx, run.UserMessageID)
	if err != nil {
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "读取消息失败：" + err.Error()}
	}
	if run.Origin == "sleep_wake" {
		s.emitMessage(ctx, agentID, msg, run)
	}
	if run.Origin == "inbox_reminder" {
		text, void, err := s.prepareReminder(ctx, agentID, run)
		if err != nil {
			return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "准备平台提醒失败：" + err.Error()}
		}
		if void {
			return executor.Result{Outcome: outcomeVoided}
		}
		msg.Text = text
	}
	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	go func() {
		t := time.NewTicker(s.o.LockRenewInterval)
		defer t.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-t.C:
				if ok, err := s.co.Renew(renewCtx, agentID.String(), owner); err != nil || !ok {
					s.log.Warn("续期忙闲锁失败", "agentId", agentID, "ok", ok, "err", err)
				}
				if st, err := s.st.RunStatus(renewCtx, run.ID); err == nil && st == "aborting" {
					beforeInterrupt(run.ID)
					_ = s.ex.Interrupt(renewCtx, agentID, run.ID)
				}
			}
		}
	}()
	sink := &runSink{s: s, agentID: agentID, runID: run.ID}
	release, gateErr := s.PauseDispatch(ctx, agentID)
	if gateErr != nil {
		return executor.Result{Outcome: executor.OutcomeInterrupted}
	}
	defer release()
	if st, err := s.st.RunStatus(ctx, run.ID); err == nil && st == "aborting" {
		return executor.Result{Outcome: executor.OutcomeInterrupted}
	}
	prompt := msg.Text
	if run.WakeSleepID != nil && run.Origin != "sleep_wake" {
		prompt = "平台通知：sleep已因新工作结束（sleepId=" + run.WakeSleepID.String() + "）。\n" + prompt
	}
	images, imageErr := s.st.ImageContents(ctx, msg.Images)
	if imageErr != nil {
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "图片内容不可用"}
	}
	trigger := run.UserMessageID
	if run.TriggerInboxID != nil {
		trigger = *run.TriggerInboxID
	}
	res, err := s.ex.Execute(ctx, executor.Request{Images: images, AgentID: agentID, RunID: run.ID, Message: fmt.Sprintf("本轮运行ID：%s。\n%s", run.ID, prompt),
		Origin: run.Origin, MessageID: trigger}, sink)
	if err != nil {
		if ctx.Err() != nil {
			return res
		}
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: err.Error()}
	}
	return res
}

func (s *Service) finish(ctx context.Context, run store.Run, res executor.Result) {
	end := store.RunEnd{Text: res.Text, ConfigVersion: res.ConfigVersion}
	switch res.Outcome {
	case executor.OutcomeCompleted:
		end.Status = "completed"
	case executor.OutcomeInterrupted:
		end.Status, end.EndReason = "interrupted", "user_interrupt"
	case outcomeVoided:
		end.Status, end.EndReason = "interrupted", "no_pending_messages"
	default:
		end.Status, end.ErrorCode, end.ErrorMessage = "failed", res.ErrorCode, res.ErrorMessage
	}
	if end.Text == "" && end.Status != "completed" {
		if t, err := s.co.RunText(ctx, run.ID.String()); err == nil {
			end.Text = t
		}
	}
	s.endRun(ctx, run.AgentID, run.ID, end)
	if res.ErrorCode == executor.ErrSessionMissing {
		s.emitSession(ctx, run.AgentID)
	}
}

func (s *Service) endRun(ctx context.Context, agentID, runID uuid.UUID, end store.RunEnd) {
	r, msg, finished, err := s.st.FinishRun(ctx, runID, end)
	if errors.Is(err, store.ErrSleepSessionNotSaved) {
		end.Status = "failed"
		end.ErrorCode = executor.ErrInternal
		end.ErrorMessage = "会话记录保存失败"
		r, msg, finished, err = s.st.FinishRun(ctx, runID, end)
	}
	if err != nil {
		s.log.Error("结束执行失败", "agentId", agentID, "runId", runID, "err", err)
		return
	}
	if !finished {
		return
	}
	attrs := []any{"runId", r.ID, "agentId", agentID, "status", r.Status, "endReason", r.EndReason, "errorCode", r.ErrorCode, "tokens", r.Usage.Total}
	if r.EndedAt != nil {
		begin := r.QueuedAt
		if r.StartedAt != nil {
			begin = *r.StartedAt
		}
		attrs = append(attrs, "durationMs", r.EndedAt.Sub(begin).Milliseconds())
	}
	s.log.Info("回复结束", attrs...)
	s.emit(ctx, agentID, apitypes.EventRunEnded, apitypes.SseRunEnded{Run: apitypes.FromRun(r), Message: apitypes.FromMessage(*msg, r)})
	_ = s.co.DeleteRunText(ctx, runID.String())
	s.EmitAgentStatus(agentID)
}

// runSink 把执行器事件写入数据库与事件流。
type runSink struct {
	s       *Service
	agentID uuid.UUID
	runID   uuid.UUID
}

func (k *runSink) Phase(phase string) {
	ctx := k.s.ctx
	if err := k.s.st.SetRunPhase(ctx, k.runID, phase); err != nil {
		k.s.log.Warn("更新执行阶段失败", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emitRun(ctx, k.agentID, k.runID)
}

func (k *runSink) Running(configVersion int64) {
	ctx := k.s.ctx
	if err := k.s.st.SetRunRunning(ctx, k.runID, configVersion); err != nil {
		k.s.log.Warn("更新执行状态失败", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emitRun(ctx, k.agentID, k.runID)
}

func (k *runSink) TextDelta(delta string) {
	data, _ := json.Marshal(apitypes.SseRunText{RunID: k.runID, Delta: delta})
	if _, err := k.s.co.AppendText(k.s.ctx, k.agentID.String(), k.runID.String(), delta, apitypes.EventRunText, data); err != nil {
		k.s.log.Warn("写入文字增量失败", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
}

func (k *runSink) Tool(callID, name, state string) {
	ctx := k.s.ctx
	ev := store.ToolEvent{ToolCallID: callID, ToolName: name, State: state, At: time.Now()}
	if err := k.s.st.AddToolEvent(ctx, k.runID, ev); err != nil {
		k.s.log.Warn("记录工具状态失败", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emit(ctx, k.agentID, apitypes.EventRunTool, apitypes.SseRunTool{RunID: k.runID, ToolCallID: callID, ToolName: name, State: state, At: ev.At})
}

// ---- 重启处理与对账 ----

func (s *Service) recoverAfterRestart(ctx context.Context) error {
	runs, err := s.st.ActiveRuns(ctx)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if r.InstanceID != nil && *r.InstanceID == s.o.InstanceID {
			continue
		}
		text, _ := s.co.RunText(ctx, r.ID.String())
		s.log.Warn("上次运行遗留的进行中执行，标为服务端重启中断", "agentId", r.AgentID, "runId", r.ID)
		s.endRun(ctx, r.AgentID, r.ID, store.RunEnd{Status: "interrupted", EndReason: "server_restart", Text: text})
		if r.InstanceID != nil {
			_, _ = s.co.Release(ctx, r.AgentID.String(), coord.LockValue(r.ID.String(), r.InstanceID.String()))
		}
	}
	return nil
}

func (s *Service) reconcileLoop() {
	t := time.NewTicker(s.o.ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.reconcile(s.ctx)
		}
	}
}

// reconcile 按数据库重建缺失的 Redis 队列，为进行中的执行补设锁，并为无人处理的排队消息启动驱动循环。
func (s *Service) reconcile(ctx context.Context) {
	active, err := s.st.ActiveRuns(ctx)
	if err != nil {
		s.log.Warn("对账：读取进行中执行失败", "err", err)
		return
	}
	for _, r := range active {
		if r.InstanceID != nil {
			_, _ = s.co.Restore(ctx, r.AgentID.String(), coord.LockValue(r.ID.String(), r.InstanceID.String()))
		}
	}
	agents, err := s.st.AgentsWithQueuedRuns(ctx)
	if err != nil {
		s.log.Warn("对账：读取排队执行失败", "err", err)
		return
	}
	for _, agentID := range agents {
		queued, err := s.st.QueuedRuns(ctx, agentID)
		if err != nil {
			continue
		}
		var wantI, wantN []string
		for _, r := range queued {
			if r.Priority == 0 {
				wantI = append(wantI, r.ID.String())
			} else {
				wantN = append(wantN, r.ID.String())
			}
		}
		gotI, gotN, err := s.co.QueueContents(ctx, agentID.String())
		if err != nil {
			continue
		}
		if !containsAll(gotI, wantI) || !containsAll(gotN, wantN) {
			s.log.Warn("对账：Redis 队列缺少排队执行，按数据库重建", "agentId", agentID)
			if err := s.co.RebuildQueues(ctx, agentID.String(), wantI, wantN); err != nil {
				continue
			}
		}
		if !slices.ContainsFunc(active, func(r store.Run) bool { return r.AgentID == agentID }) {
			s.kick(ctx, agentID)
		}
	}
}

func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// ---- 事件 ----

func (s *Service) emit(ctx context.Context, agentID uuid.UUID, typ string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("序列化事件失败", "type", typ, "err", err)
		return false
	}
	if _, err := s.co.AppendEvent(ctx, agentID.String(), typ, data); err != nil {
		s.log.Warn("写入事件流失败", "agentId", agentID, "type", typ, "err", err)
		return false
	}

	return true
}

func (s *Service) emitMessage(ctx context.Context, agentID uuid.UUID, m store.Message, r store.Run) {
	s.emit(ctx, agentID, apitypes.EventMessageCreated, apitypes.SseMessageCreated{Message: apitypes.FromMessage(m, r)})
	s.EmitAgentStatus(agentID)
}

func (s *Service) emitRun(ctx context.Context, agentID, runID uuid.UUID) {
	r, err := s.st.GetRun(ctx, runID)
	if err != nil {
		s.log.Warn("读取执行失败", "runId", runID, "err", err)
		return
	}
	s.emit(ctx, agentID, apitypes.EventRunUpdated, apitypes.SseRunUpdated{Run: apitypes.FromRun(r)})
	s.EmitAgentStatus(agentID)
}

// emitQueue 推送排队列表：全部排队执行与逐条排队项（与快照里的 queuedRuns、queueItems 同形同序）。
func (s *Service) emitQueue(ctx context.Context, agentID uuid.UUID) {
	runs, items, err := s.queueState(ctx, agentID)
	if err != nil {
		s.log.Warn("读取排队列表失败", "agentId", agentID, "err", err)
		return
	}
	s.emit(ctx, agentID, apitypes.EventQueueUpdated, apitypes.SseQueueUpdated{QueuedRuns: runs, Items: items})
}

func (s *Service) queueState(ctx context.Context, agentID uuid.UUID) ([]apitypes.Run, []apitypes.QueueItem, error) {
	queued, err := s.st.QueuedRuns(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	runs := make([]apitypes.Run, 0, len(queued))
	for _, r := range queued {
		runs = append(runs, apitypes.FromRun(r))
	}
	items, err := s.st.AgentQueue(ctx, agentID)
	if err != nil {
		return nil, nil, err
	}
	return runs, apitypes.FromQueueItems(items), nil
}

func (s *Service) emitSession(ctx context.Context, agentID uuid.UUID) {
	sess, err := s.st.GetAgentSession(ctx, agentID)
	if err != nil {
		return
	}
	s.emit(ctx, agentID, apitypes.EventSessionUpdated, apitypes.SseSessionUpdated{State: sess.State, Epoch: sess.Epoch})
	s.EmitAgentStatus(agentID)
}

// ---- 概览事件流 ----

// overviewWorker 记录某个 Agent 的概览推送协程是否在运行，以及运行期间是否又有变化。
type overviewWorker struct{ running, dirty bool }

// EmitAgentStatus 异步推送 Agent 当前的运行状态到概览事件流（Agent 已删除时推送移除事件），不阻塞调用方，
// 所以不会拉长执行结束到交接忙闲锁之间的时间。同一 Agent 至多一个推送协程：运行期间的新变化合并为再推一次，
// 最后推送的总是最新状态。服务停止后不再推送。失败只记日志。
func (s *Service) EmitAgentStatus(agentID uuid.UUID) {
	s.overviewMu.Lock()
	defer s.overviewMu.Unlock()
	if s.ctx.Err() != nil {
		return
	}
	w := s.overviewWorkers[agentID]
	if w == nil {
		w = &overviewWorker{}
		s.overviewWorkers[agentID] = w
	}
	if w.running {
		w.dirty = true
		return
	}
	w.running = true
	s.overviewWG.Go(func() {
		for {
			s.pushAgentStatus(agentID)
			s.overviewMu.Lock()
			if !w.dirty || s.ctx.Err() != nil {
				delete(s.overviewWorkers, agentID)
				s.overviewMu.Unlock()
				return
			}
			w.dirty = false
			s.overviewMu.Unlock()
		}
	})
}

func (s *Service) pushAgentStatus(agentID uuid.UUID) {
	s.emitSleep(s.ctx, agentID)
	ctx := context.Background()
	item, err := s.overviewItem(ctx, agentID)
	if apperr.Is(err, apperr.CodeNotFound) {
		// 已删除（含所在项目被删除）：带上原来的归属，按范围订阅的概览据此过滤。
		pid, perr := s.st.AgentProjectAny(ctx, agentID)
		if perr != nil && !apperr.Is(perr, apperr.CodeNotFound) {
			s.log.Warn("读取已删除 Agent 的归属失败", "agentId", agentID, "err", perr)
		}
		s.appendOverview(ctx, apitypes.EventAgentRemoved, apitypes.SseAgentRemoved{AgentID: agentID, Scope: apitypes.ScopeOf(pid), ProjectID: pid})
		return
	}
	if err != nil {
		s.log.Warn("计算概览状态失败", "agentId", agentID, "err", err)
		return
	}
	s.appendOverview(ctx, apitypes.EventAgentStatus, apitypes.SseAgentStatus{Agent: item})
}

func (s *Service) appendOverview(ctx context.Context, typ string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("序列化概览事件失败", "type", typ, "err", err)
		return
	}
	if _, err := s.co.AppendOverview(ctx, typ, data); err != nil {
		s.log.Warn("写入概览事件流失败", "type", typ, "err", err)
	}
}

// overviewItem 返回一个未删除 Agent 的概览状态。
func (s *Service) overviewItem(ctx context.Context, agentID uuid.UUID) (apitypes.AgentOverview, error) {
	a, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		return apitypes.AgentOverview{}, err
	}
	items, err := s.overviewItems(ctx, []store.Agent{a})
	if err != nil {
		return apitypes.AgentOverview{}, err
	}
	return items[0], nil
}

func (s *Service) overviewItems(ctx context.Context, agents []store.Agent) ([]apitypes.AgentOverview, error) {
	ids := make([]uuid.UUID, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	lastRuns, err := s.st.LastEndedRuns(ctx, ids)
	if err != nil {
		return nil, err
	}
	exits, err := s.st.LastProcessExits(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]apitypes.AgentOverview, 0, len(agents))
	for _, a := range agents {
		st, err := s.Status(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		item := apitypes.AgentOverview{AgentID: a.ID, Scope: apitypes.ScopeOf(a.ProjectID), ProjectID: a.ProjectID, Name: a.Name, Model: apitypes.ModelRef{Provider: a.ModelProvider, ModelID: a.ModelID}, Status: st}
		if r, ok := lastRuns[a.ID]; ok {
			run := apitypes.FromRun(r)
			item.LastRun = &run
		}
		if e, ok := exits[a.ID]; ok && st.ProcessState == executor.ProcessStopped {
			item.LastProcessExit = &apitypes.ProcessExit{At: e.At, Reason: e.Reason}
		}
		out = append(out, item)
	}
	return out, nil
}

// OverviewSnapshot 返回范围内的概览快照及其事件 ID。先取事件流的最新位置再读状态：
// 之后的事件可能与快照重叠（状态事件是完整状态，重复无害），但不会缺失。
func (s *Service) OverviewSnapshot(ctx context.Context, f store.ScopeFilter) (apitypes.SseOverviewSnapshot, string, error) {
	lastID, err := s.co.OverviewLastID(ctx)
	if err != nil {
		return apitypes.SseOverviewSnapshot{}, "", err
	}
	list, err := s.st.ListAgentsFiltered(ctx, f)
	if err != nil {
		return apitypes.SseOverviewSnapshot{}, "", err
	}
	agents := make([]store.Agent, 0, len(list))
	for _, it := range list {
		agents = append(agents, it.Agent)
	}
	items, err := s.overviewItems(ctx, agents)
	if err != nil {
		return apitypes.SseOverviewSnapshot{}, "", err
	}
	return apitypes.SseOverviewSnapshot{Agents: items}, lastID, nil
}

// ---- 查询 ----

// Status 返回 Agent 的状态。
func (s *Service) Status(ctx context.Context, agentID uuid.UUID) (apitypes.AgentStatus, error) {
	if _, err := s.st.GetAgent(ctx, agentID); err != nil {
		return apitypes.AgentStatus{}, err
	}
	proc := s.ex.Process(agentID)
	out := apitypes.AgentStatus{AgentID: agentID, ProcessState: proc.State, ProcessStartedAt: proc.StartedAt}
	if proc.PID > 0 {
		out.ProcessPID = &proc.PID
	}
	active, err := s.st.ActiveRun(ctx, agentID)
	if err != nil {
		return out, err
	}
	if active != nil {
		full, err := s.st.GetRun(ctx, active.ID)
		if err != nil {
			return out, err
		}
		r := apitypes.FromRun(full)
		out.Busy, out.CurrentRun = true, &r
	}
	if out.Queued, _, err = s.queueCounts(ctx, agentID); err != nil {
		return out, err
	}
	sess, err := s.st.GetAgentSession(ctx, agentID)
	if err != nil {
		return out, err
	}
	out.Session = apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}
	out.Sleep, out.SleepRevision, err = s.st.ActiveSleepProjection(ctx, agentID, uuid.Nil)
	if err != nil {
		return out, err
	}
	return out, nil
}

// Snapshot 返回 SSE 快照及其事件 ID：快照中的文字恰好包含到该 ID 为止的增量。
func (s *Service) Snapshot(ctx context.Context, agentID uuid.UUID) (apitypes.SseSnapshot, string, error) {
	st, err := s.Status(ctx, agentID)
	if err != nil {
		return apitypes.SseSnapshot{}, "", err
	}
	runID := ""
	if st.CurrentRun != nil {
		runID = st.CurrentRun.ID.String()
	}
	text, lastID, err := s.co.Snapshot(ctx, agentID.String(), runID)
	if err != nil {
		return apitypes.SseSnapshot{}, "", err
	}
	snap := apitypes.SseSnapshot{Status: st}
	if st.CurrentRun != nil {
		snap.CurrentText = &text
	}
	if snap.QueuedRuns, snap.QueueItems, err = s.queueState(ctx, agentID); err != nil {
		return snap, "", err
	}
	return snap, lastID, nil
}

// History 返回对话历史的一页。
func (s *Service) History(ctx context.Context, agentID uuid.UUID, q store.MessageQuery) (apitypes.MessagePage, error) {
	page, err := s.st.ListMessages(ctx, agentID, q)
	if err != nil {
		return apitypes.MessagePage{}, err
	}
	out := apitypes.MessagePage{Messages: make([]apitypes.Message, 0, len(page.Items)), HasMore: page.HasMore, HasMoreAfter: page.HasMoreAfter}
	for _, it := range page.Items {
		out.Messages = append(out.Messages, apitypes.FromMessage(it.Message, it.Run))
	}
	return out, nil
}

// ResetSession 确认重开会话。
func (s *Service) ResetSession(ctx context.Context, agentID uuid.UUID) (apitypes.SessionInfo, error) {
	sess, err := s.st.ResetSession(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	s.emit(ctx, agentID, apitypes.EventSessionUpdated, apitypes.SseSessionUpdated{State: sess.State, Epoch: sess.Epoch})
	s.EmitAgentStatus(agentID)
	s.remindUnread(ctx, agentID) // 会话丢失期间收到的 Inbox 消息没有安排提醒，重开后补上
	return apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}, nil
}
