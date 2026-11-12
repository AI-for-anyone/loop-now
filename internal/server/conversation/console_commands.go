package conversation

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

type consoleCommandRun struct {
	instructions string
	done         chan executor.Result
}

func (s *Service) acquireConsoleCommand(ctx context.Context, agentID uuid.UUID) (uuid.UUID, string, error) {
	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.Acquire(ctx, agentID.String(), owner)
	if err != nil {
		return uuid.Nil, "", err
	}
	if !acquired {
		return uuid.Nil, "", s.busyError(ctx, agentID)
	}
	// 锁只表示没有进行中的执行；排队由数据库判断，有可开始的排队执行时不做维护，放开锁后接着处理它们。
	_, queued, err := s.st.NextQueuedRun(ctx, agentID)
	if err != nil || queued {
		s.releaseConsoleCommand(agentID, owner)
		if err != nil {
			return uuid.Nil, "", err
		}
		return uuid.Nil, "", apperr.Conflict(apperr.CodeAgentNotIdle, "Agent 正在回复或还有排队消息，请等它空闲后再操作")
	}
	return runID, owner, nil
}

// releaseConsoleCommand 放开平台命令持有的忙闲锁，并接着处理命令期间排进来的消息与提醒：
// 那期间的发送拿不到锁只能排队，它们的 kick 也因锁被占而落空，不在这里接着处理就要等下一次对账。
func (s *Service) releaseConsoleCommand(agentID uuid.UUID, owner string) {
	ctx := context.Background()
	if _, err := s.co.Release(ctx, agentID.String(), owner); err != nil {
		s.log.Warn("放开平台命令的忙闲锁失败，等待对账", "agentId", agentID, "err", err)
	}
	s.kick(ctx, agentID)
}

// holdConsoleCommand 返回放开锁的函数，可以多次调用、只放开一次：命令先放开锁，再做需要锁空闲的收尾（安排提醒）。
func (s *Service) holdConsoleCommand(agentID uuid.UUID, owner string) func() {
	return sync.OnceFunc(func() { s.releaseConsoleCommand(agentID, owner) })
}

// sessionChanged 在会话重开或切换之后通知控制台，并为会话不可用期间收到的 Inbox 消息补上提醒。
// 调用时不能持有忙闲锁：提醒要取得锁才能立即开始。
func (s *Service) sessionChanged(ctx context.Context, agentID uuid.UUID, sess store.AgentSession) apitypes.SessionInfo {
	s.emit(ctx, agentID, apitypes.EventSessionUpdated, apitypes.SseSessionUpdated{State: sess.State, Epoch: sess.Epoch})
	s.EmitAgentStatus(agentID)
	s.remindUnread(ctx, agentID)
	return apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}
}

// Compact 创建真实压缩运行并等待 Pi 返回 Token 结果，不创建用户或助手消息。
func (s *Service) Compact(ctx context.Context, agentID uuid.UUID, instructions string) (executor.CompactionResult, error) {
	runID, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return executor.CompactionResult{}, err
	}
	command := &consoleCommandRun{instructions: instructions, done: make(chan executor.Result, 1)}
	s.commands.Store(runID, command)
	defer s.commands.Delete(runID)
	run, err := s.st.InsertConsoleCompactRun(ctx, agentID, runID, s.o.InstanceID)
	if err != nil {
		s.releaseConsoleCommand(agentID, owner)
		if errors.Is(err, store.ErrActiveRunExists) {
			s.restoreLock(ctx, agentID)
			return executor.CompactionResult{}, s.busyError(ctx, agentID)
		}
		return executor.CompactionResult{}, err
	}
	s.emitRun(ctx, agentID, run.ID)
	s.startDriver(agentID, run, owner)
	select {
	case res := <-command.done:
		if res.Outcome != executor.OutcomeCompleted || res.Compaction == nil {
			message := res.ErrorMessage
			if message == "" {
				message = "压缩已取消或未能完成"
			}
			return executor.CompactionResult{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, message)
		}
		return *res.Compaction, nil
	case <-ctx.Done():
		return executor.CompactionResult{}, ctx.Err()
	}
}

// NewSession 停止旧空闲进程并重开会话，保留工作区和 Web 对话历史。
func (s *Service) NewSession(ctx context.Context, agentID uuid.UUID) (apitypes.SessionInfo, error) {
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	release := s.holdConsoleCommand(agentID, owner)
	defer release()
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return apitypes.SessionInfo{}, errors.New("当前执行器不支持停止空闲进程")
	}
	if _, err := stopper.StopIdle(ctx, agentID, "session_reset"); err != nil {
		return apitypes.SessionInfo{}, err
	}
	sess, err := s.st.ResetSession(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	release()
	return s.sessionChanged(ctx, agentID, sess), nil
}

// Reload 停止旧空闲 Pi；没有运行时无需重载。
func (s *Service) Reload(ctx context.Context, agentID uuid.UUID) (bool, error) {
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return false, err
	}
	defer s.releaseConsoleCommand(agentID, owner)
	if _, err := s.st.IdleSession(ctx, agentID); err != nil {
		return false, err
	}
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return false, errors.New("当前执行器不支持重载")
	}
	return stopper.StopIdle(ctx, agentID, "console_reload")
}

// Export 下载当前受管 Pi 会话，格式只接受 html 或 jsonl。
func (s *Service) Export(ctx context.Context, agentID uuid.UUID, format string) (executor.SessionExport, error) {
	if format != "html" && format != "jsonl" {
		return executor.SessionExport{}, apperr.Invalid("format", "只能是 html 或 jsonl")
	}
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, err
	}
	defer s.releaseConsoleCommand(agentID, owner)
	sess, err := s.st.IdleSession(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, err
	}
	if sess.State == "missing" {
		return executor.SessionExport{}, apperr.Conflict(apperr.CodeSessionMissing, "会话文件丢失，请先在控制台确认重开会话")
	}
	if sess.State != "active" || sess.SessionFile == "" {
		return executor.SessionExport{}, apperr.Conflict(apperr.CodeSessionNone, "当前还没有可导出的会话")
	}
	exporter, ok := s.ex.(executor.SessionExporter)
	if !ok {
		return executor.SessionExport{}, errors.New("当前执行器不支持会话导出")
	}
	out, err := exporter.ExportSession(ctx, agentID, format)
	if err != nil {
		return executor.SessionExport{}, fmt.Errorf("导出会话失败：%w", err)
	}
	return out, nil
}

// ListResumeSessions 列出可恢复的受管 Pi 会话，不启动进程或调用模型。
func (s *Service) ListResumeSessions(ctx context.Context, agentID uuid.UUID) ([]executor.ManagedSession, error) {
	if err := s.requirePiSession(ctx, agentID); err != nil {
		return nil, err
	}
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return nil, err
	}
	defer s.releaseConsoleCommand(agentID, owner)
	sess, err := s.st.IdleSession(ctx, agentID)
	if err != nil {
		return nil, err
	}
	lister, ok := s.ex.(executor.SessionLister)
	if !ok {
		return nil, errors.New("当前执行器不支持列出会话")
	}
	return lister.ListSessions(ctx, agentID, sess.SessionFile)
}

// ResumeSession 停止旧空闲 Pi 后恢复已验证的受管会话，只切换模型上下文。
func (s *Service) ResumeSession(ctx context.Context, agentID uuid.UUID, sessionID string) (apitypes.SessionInfo, error) {
	if err := s.requirePiSession(ctx, agentID); err != nil {
		return apitypes.SessionInfo{}, err
	}
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	release := s.holdConsoleCommand(agentID, owner)
	defer release()
	sess, err := s.st.IdleSession(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	lister, ok := s.ex.(executor.SessionLister)
	if !ok {
		return apitypes.SessionInfo{}, errors.New("当前执行器不支持恢复会话")
	}
	sessions, err := lister.ListSessions(ctx, agentID, sess.SessionFile)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	var target *executor.ManagedSession
	for i := range sessions {
		if sessions[i].ID == sessionID {
			target = &sessions[i]
			break
		}
	}
	if target == nil {
		return apitypes.SessionInfo{}, apperr.Invalid("text", "未知或无效的会话 ID")
	}
	if target.Current {
		return apitypes.SessionInfo{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, "这已经是当前会话，无需恢复")
	}
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return apitypes.SessionInfo{}, errors.New("当前执行器不支持停止空闲进程")
	}
	if _, err := stopper.StopIdle(ctx, agentID, "session_resumed"); err != nil {
		return apitypes.SessionInfo{}, err
	}
	sess, err = s.st.ResumeSession(ctx, agentID, sess.SessionFile, target.Path)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	release()
	return s.sessionChanged(ctx, agentID, sess), nil
}

// LatestReply 返回最近一条成功回复；没有时返回 nil，不调用模型。
func (s *Service) LatestReply(ctx context.Context, agentID uuid.UUID) (*store.Message, error) {
	return s.st.LatestCompletedAssistant(ctx, agentID)
}

// Pi 的受管会话格式不能交给 Claude Code 恢复。
func (s *Service) requirePiSession(ctx context.Context, agentID uuid.UUID) error {
	agent, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agentruntime.ForProvider(agent.ModelProvider) == agentruntime.ClaudeCode {
		return apperr.Conflict(apperr.CodeConsoleCommandFailed, "当前运行时不支持 Pi 会话恢复")
	}
	return nil
}
