package conversation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/store"
)

// consoleCommandRun 让发起手动压缩的请求等到这次执行结束。指令与结果记在执行上（runs.compaction），不在这里。
type consoleCommandRun struct {
	done chan executor.Result
}

func (s *Service) acquireConsoleCommand(ctx context.Context, agentID uuid.UUID) (uuid.UUID, string, error) {
	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.Acquire(ctx, agentID.String(), owner)
	if err != nil {
		return uuid.Nil, "", err
	}
	if !acquired {
		return uuid.Nil, "", errNotIdle()
	}
	// 锁只表示没有进行中的执行；排队由数据库判断，有可开始的排队执行时不做维护，放开锁后接着处理它们。
	_, queued, err := s.st.NextQueuedRun(ctx, agentID)
	if err != nil || queued {
		s.releaseConsoleCommand(agentID, owner)
		if err != nil {
			return uuid.Nil, "", err
		}
		return uuid.Nil, "", errNotIdle()
	}
	return runID, owner, nil
}

// errNotIdle 是平台命令因 Agent 不空闲而拒绝时的错误：不论是忙闲锁被占、数据库里有进行中或排队的执行，
// 还是运行时在做别的维护，都是 409 agent_not_idle，不带 details（agent_busy 是普通消息“选择打断或排队”用的）。
func errNotIdle() error {
	return apperr.Conflict(apperr.CodeAgentNotIdle, "Agent 正在回复或还有排队消息，请等它空闲后再操作")
}

// consoleCommandError 把执行器维护操作里可以预期的失败转成 409；其余错误照旧（服务端内部错误）。
func consoleCommandError(err error) error {
	switch {
	case errors.Is(err, executor.ErrMaintenanceBusy):
		return errNotIdle()
	case errors.Is(err, executor.ErrNoSession):
		return apperr.Conflict(apperr.CodeSessionNone, "当前还没有可操作的会话")
	case errors.Is(err, executor.ErrUnsupportedRuntime):
		return apperr.Conflict(apperr.CodeConsoleCommandFailed, "当前运行时不支持这个命令")
	}
	return err
}

// unsupportedExecutor 是执行器没有实现某项维护操作时的错误（只有测试替身会这样）。
func unsupportedExecutor(what string) error {
	return apperr.Conflict(apperr.CodeConsoleCommandFailed, "当前执行器不支持"+what)
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
	info := apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}
	s.publishSession(ctx, agentID, info, true)
	s.EmitAgentStatus(agentID)
	s.remindUnread(ctx, agentID)
	return info
}

// Compact 创建真实压缩执行并等到它结束，不创建用户或助手消息。指令与结果记在执行上（runs.compaction）：
// 请求在压缩结束前断开（刷新页面、代理超时）时压缩照常进行，结果事后从执行读取（run 事件、时间线、/session 的 lastCompaction）。
// 正常完成（包括 Pi 没有压缩：会话太短、已经压缩过、扩展取消了压缩）返回结束后的执行；被取消或失败时返回 409 console_command_failed。
func (s *Service) Compact(ctx context.Context, agentID uuid.UUID, instructions string) (store.Run, error) {
	if err := s.RequireCommand(ctx, agentID, "compact"); err != nil {
		return store.Run{}, err
	}
	runID, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return store.Run{}, err
	}
	command := &consoleCommandRun{done: make(chan executor.Result, 1)}
	s.commands.Store(runID, command)
	defer s.commands.Delete(runID)
	// 插入时在同一写事务里确认空闲（ensureIdle），不会撞上另一次进行中的执行。
	run, err := s.st.InsertConsoleCompactRun(ctx, agentID, runID, s.o.InstanceID, instructions)
	if err != nil {
		s.releaseConsoleCommand(agentID, owner)
		return store.Run{}, err
	}
	s.emitRun(ctx, agentID, run.ID)
	s.startDriver(agentID, run, owner)
	select {
	case <-command.done:
	case <-ctx.Done():
		return store.Run{}, ctx.Err()
	}
	ended, err := s.st.GetRun(ctx, runID)
	if err != nil {
		return store.Run{}, err
	}
	switch ended.Status {
	case "completed":
		return ended, nil
	case "interrupted":
		return store.Run{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, "压缩已取消")
	}
	message := ended.ErrorMessage
	if message == "" {
		message = "压缩未能完成"
	}
	return store.Run{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, message)
}

// LastCompaction 返回 Agent 最近一次手动压缩的执行（任何状态）；没有时为 nil。
func (s *Service) LastCompaction(ctx context.Context, agentID uuid.UUID) (*store.Run, error) {
	return s.st.LatestCompactRun(ctx, agentID)
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
		return apitypes.SessionInfo{}, unsupportedExecutor("停止空闲进程")
	}
	if _, err := stopper.StopIdle(ctx, agentID, "session_reset"); err != nil {
		return apitypes.SessionInfo{}, consoleCommandError(err)
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
		return false, unsupportedExecutor("重载")
	}
	stopped, err := stopper.StopIdle(ctx, agentID, "console_reload")
	return stopped, consoleCommandError(err)
}

// Export 下载当前受管 Pi 会话，格式只接受 html 或 jsonl。
func (s *Service) Export(ctx context.Context, agentID uuid.UUID, format string) (executor.SessionExport, error) {
	if format != "html" && format != "jsonl" {
		return executor.SessionExport{}, apperr.Invalid("format", "只能是 html 或 jsonl")
	}
	if err := s.RequireCommand(ctx, agentID, "export"); err != nil {
		return executor.SessionExport{}, err
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
		return executor.SessionExport{}, unsupportedExecutor("会话导出")
	}
	out, err := exporter.ExportSession(ctx, agentID, sess.SessionFile, format)
	if err != nil {
		if mapped := consoleCommandError(err); mapped != err {
			return executor.SessionExport{}, mapped
		}
		return executor.SessionExport{}, fmt.Errorf("导出会话失败：%w", err)
	}
	return out, nil
}

// ListResumeSessions 列出可恢复的受管 Pi 会话，不启动进程或调用模型。只在 Agent 空闲时列出（与恢复的条件一致），
// 但读文件期间不占忙闲锁：列表只读，期间开始的执行不受影响。
func (s *Service) ListResumeSessions(ctx context.Context, agentID uuid.UUID) ([]executor.ManagedSession, error) {
	if err := s.RequireCommand(ctx, agentID, "resume"); err != nil {
		return nil, err
	}
	sess, err := s.st.IdleSession(ctx, agentID)
	if err != nil {
		return nil, err
	}
	lister, ok := s.ex.(executor.SessionLister)
	if !ok {
		return nil, unsupportedExecutor("列出会话")
	}
	sessions, err := lister.ListSessions(ctx, agentID, sess.SessionFile)
	return sessions, consoleCommandError(err)
}

// ResumeSession 停止旧空闲 Pi 后恢复已验证的受管会话，只切换模型上下文。
func (s *Service) ResumeSession(ctx context.Context, agentID uuid.UUID, sessionID string) (apitypes.SessionInfo, error) {
	if err := s.RequireCommand(ctx, agentID, "resume"); err != nil {
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
		return apitypes.SessionInfo{}, unsupportedExecutor("恢复会话")
	}
	target, err := lister.ResolveSession(ctx, agentID, sessionID, sess.SessionFile)
	if err != nil {
		return apitypes.SessionInfo{}, consoleCommandError(err)
	}
	if target == nil {
		return apitypes.SessionInfo{}, apperr.Invalid("text", "未知或无效的会话 ID")
	}
	if target.Current {
		return apitypes.SessionInfo{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, "这已经是当前会话，无需恢复")
	}
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return apitypes.SessionInfo{}, unsupportedExecutor("停止空闲进程")
	}
	if _, err := stopper.StopIdle(ctx, agentID, "session_resumed"); err != nil {
		return apitypes.SessionInfo{}, consoleCommandError(err)
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

// platformCommands 是平台内置命令（对话页 "/" 菜单与 POST /messages 共用这一份）。piOnly：只适用于 Pi 会话
// （Claude Code 的会话由它自己管理，平台不能压缩、导出或恢复）；needsSession：要有可用的会话。
type platformCommandSpec struct {
	name, usage          string
	piOnly, needsSession bool
}

var platformCommands = []platformCommandSpec{
	{name: "session", usage: "/session"},
	{name: "new", usage: "/new"},
	{name: "compact", usage: "/compact [instructions]", piOnly: true, needsSession: true},
	{name: "reload", usage: "/reload"},
	{name: "copy", usage: "/copy"},
	{name: "export", usage: "/export [html|jsonl]", piOnly: true, needsSession: true},
	{name: "resume", usage: "/resume [sessionId]", piOnly: true},
}

// IsPlatformCommand 报告 name（不带 "/"）是不是平台内置命令。
func IsPlatformCommand(name string) bool {
	return slices.ContainsFunc(platformCommands, func(c platformCommandSpec) bool { return c.name == name })
}

var piOnlyCommands = func() map[string]bool {
	out := map[string]bool{}
	for _, c := range platformCommands {
		if c.piOnly {
			out[c.name] = true
		}
	}
	return out
}()

// PlatformCommand 是一条平台内置命令及它此刻能不能用。UnavailableReason：runtime_unsupported（运行时不支持）、
// session_none（还没有会话）、session_missing（会话文件丢失）；能用时为空。Agent 正忙这类随时变化的情况不在这里判断。
type PlatformCommand struct {
	Name, Usage       string
	Available         bool
	UnavailableReason string
}

// PlatformCommands 按 Agent 的运行时与会话状态列出平台内置命令。
func (s *Service) PlatformCommands(ctx context.Context, agentID uuid.UUID) ([]PlatformCommand, error) {
	agent, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	sess, err := s.st.GetAgentSession(ctx, agentID)
	if err != nil {
		return nil, err
	}
	claude := agentruntime.ForProvider(agent.ModelProvider) == agentruntime.ClaudeCode
	out := make([]PlatformCommand, 0, len(platformCommands))
	for _, c := range platformCommands {
		reason := ""
		switch {
		case c.piOnly && claude:
			reason = "runtime_unsupported"
		case c.needsSession && sess.State == "missing":
			reason = "session_missing"
		case c.needsSession && (sess.State != "active" || sess.SessionFile == ""):
			reason = "session_none"
		}
		out = append(out, PlatformCommand{Name: c.name, Usage: c.usage, Available: reason == "", UnavailableReason: reason})
	}
	return out, nil
}

// RequireCommand 确认 Agent 的运行时支持这个平台命令（名字不带 "/"）；不支持时返回 409 console_command_failed，
// 调用方在取得忙闲锁、创建执行之前检查。
func (s *Service) RequireCommand(ctx context.Context, agentID uuid.UUID, name string) error {
	if !piOnlyCommands[name] {
		return nil
	}
	agent, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agentruntime.ForProvider(agent.ModelProvider) == agentruntime.ClaudeCode {
		return apperr.Conflict(apperr.CodeConsoleCommandFailed, "Claude Code 的 Agent 不支持 /"+name+"：这个命令只适用于 Pi 会话")
	}
	return nil
}
