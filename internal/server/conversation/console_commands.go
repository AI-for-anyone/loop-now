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

// consoleCommandRun lets the request that started a manual compaction wait until that run ends. The instructions and
// the result are recorded on the run (runs.compaction), not here.
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
	// The lock only means there is no in-flight run; the queue is judged by the database: when there is a queued run
	// that can start, no maintenance is done, and after the lock is released those runs are processed.
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

// errNotIdle is the error for a platform command rejected because the Agent is not idle: whether the busy lock is
// taken, the database has an in-flight or queued run, or the runtime is doing other maintenance, it is always 409
// agent_not_idle without details (agent_busy is for regular messages, where the caller must "choose interrupt or
// queue").
func errNotIdle() error {
	return apperr.Conflict(apperr.CodeAgentNotIdle, "The Agent is replying or has queued messages. Wait until it is idle, then try again.")
}

// consoleCommandError turns the expected failures of executor maintenance operations into 409; other errors are
// returned as they are (internal server errors).
func consoleCommandError(err error) error {
	switch {
	case errors.Is(err, executor.ErrMaintenanceBusy):
		return errNotIdle()
	case errors.Is(err, executor.ErrNoSession):
		return apperr.Conflict(apperr.CodeSessionNone, "There is no session to act on yet.")
	case errors.Is(err, executor.ErrUnsupportedRuntime):
		return apperr.Conflict(apperr.CodeConsoleCommandFailed, "The current runtime does not support this command.")
	}
	return err
}

// unsupportedExecutor is the error when the executor does not implement a maintenance operation (only test doubles do
// that).
func unsupportedExecutor(what string) error {
	return apperr.Conflict(apperr.CodeConsoleCommandFailed, "The current executor does not support "+what)
}

// releaseConsoleCommand releases the busy lock held by a platform command and goes on to process the messages and
// reminders queued during the command: sends during that time could not get the lock and could only queue, and their
// kick came to nothing because the lock was taken; if they are not processed here they wait for the next
// reconciliation.
func (s *Service) releaseConsoleCommand(agentID uuid.UUID, owner string) {
	ctx := context.Background()
	if _, err := s.co.Release(ctx, agentID.String(), owner); err != nil {
		s.log.Warn("failed to release the busy lock of a platform command; waiting for reconciliation", "agentId", agentID, "err", err)
	}
	s.kick(ctx, agentID)
}

// holdConsoleCommand returns a function that releases the lock; it can be called many times but releases only once: a
// command releases the lock first and then does the cleanup that needs the lock to be free (scheduling reminders).
func (s *Service) holdConsoleCommand(agentID uuid.UUID, owner string) func() {
	return sync.OnceFunc(func() { s.releaseConsoleCommand(agentID, owner) })
}

// sessionChanged notifies the console after the session was reopened or switched, and adds reminders for Inbox messages
// received while the session was unavailable. It must not be called while holding the busy lock: a reminder has to
// acquire the lock to start right away.
func (s *Service) sessionChanged(ctx context.Context, agentID uuid.UUID, sess store.AgentSession) apitypes.SessionInfo {
	info := apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}
	s.publishSession(ctx, agentID, info, true)
	s.EmitAgentStatus(agentID)
	s.remindUnread(ctx, agentID)
	return info
}

// Compact creates a real compaction run and waits for it to end, without creating a user or assistant message. The
// instructions and the result are recorded on the run (runs.compaction): if the request disconnects before the
// compaction ends (page refresh, proxy timeout), the compaction still goes on and the result is read from the run
// afterwards (run events, the timeline, lastCompaction of /session). On normal completion (including when Pi did not
// compact: the session is too short, it was already compacted, or an extension cancelled the compaction) it returns the
// ended run; when cancelled or failed it returns 409 console_command_failed.
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
	// Inserting confirms idleness in the same write transaction (ensureIdle), so it cannot collide with another
	// in-flight run.
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
		return store.Run{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, "The compaction was cancelled.")
	}
	message := ended.ErrorMessage
	if message == "" {
		message = "The compaction did not finish."
	}
	return store.Run{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, message)
}

// LastCompaction returns the Agent's most recent manual compaction run (in any status); nil when there is none.
func (s *Service) LastCompaction(ctx context.Context, agentID uuid.UUID) (*store.Run, error) {
	return s.st.LatestCompactRun(ctx, agentID)
}

// NewSession stops the old idle process and reopens the session, keeping the workspace and the web conversation
// history.
func (s *Service) NewSession(ctx context.Context, agentID uuid.UUID) (apitypes.SessionInfo, error) {
	_, owner, err := s.acquireConsoleCommand(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	release := s.holdConsoleCommand(agentID, owner)
	defer release()
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return apitypes.SessionInfo{}, unsupportedExecutor("stopping idle processes.")
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

// Reload stops the old idle Pi; when there is no runtime there is nothing to reload.
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
		return false, unsupportedExecutor("reloading.")
	}
	stopped, err := stopper.StopIdle(ctx, agentID, "console_reload")
	return stopped, consoleCommandError(err)
}

// Export downloads the current managed Pi session; format must be html or jsonl.
func (s *Service) Export(ctx context.Context, agentID uuid.UUID, format string) (executor.SessionExport, error) {
	if format != "html" && format != "jsonl" {
		return executor.SessionExport{}, apperr.Invalid("format", "Must be html or jsonl.")
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
		return executor.SessionExport{}, apperr.Conflict(apperr.CodeSessionMissing, "The session file is missing. Confirm reopening the session in the console first.")
	}
	if sess.State != "active" || sess.SessionFile == "" {
		return executor.SessionExport{}, apperr.Conflict(apperr.CodeSessionNone, "There is no session to export yet.")
	}
	exporter, ok := s.ex.(executor.SessionExporter)
	if !ok {
		return executor.SessionExport{}, unsupportedExecutor("exporting sessions.")
	}
	out, err := exporter.ExportSession(ctx, agentID, sess.SessionFile, format)
	if err != nil {
		if mapped := consoleCommandError(err); mapped != err {
			return executor.SessionExport{}, mapped
		}
		return executor.SessionExport{}, fmt.Errorf("export the session: %w", err)
	}
	return out, nil
}

// ListResumeSessions lists the managed Pi sessions that can be resumed, without starting a process or calling the
// model. It only lists while the Agent is idle (the same condition as resuming), but does not hold the busy lock while
// reading files: the list is read-only, and runs that start meanwhile are not affected.
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
		return nil, unsupportedExecutor("listing sessions.")
	}
	sessions, err := lister.ListSessions(ctx, agentID, sess.SessionFile)
	return sessions, consoleCommandError(err)
}

// ResumeSession stops the old idle Pi and then resumes a verified managed session; it only switches the model context.
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
		return apitypes.SessionInfo{}, unsupportedExecutor("resuming sessions.")
	}
	target, err := lister.ResolveSession(ctx, agentID, sessionID, sess.SessionFile)
	if err != nil {
		return apitypes.SessionInfo{}, consoleCommandError(err)
	}
	if target == nil {
		return apitypes.SessionInfo{}, apperr.Invalid("text", "The session ID is unknown or invalid.")
	}
	if target.Current {
		return apitypes.SessionInfo{}, apperr.Conflict(apperr.CodeConsoleCommandFailed, "This is already the current session. There is nothing to resume.")
	}
	stopper, ok := s.ex.(executor.IdleProcessStopper)
	if !ok {
		return apitypes.SessionInfo{}, unsupportedExecutor("stopping idle processes.")
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

// LatestReply returns the most recent successful reply; nil when there is none. It does not call the model.
func (s *Service) LatestReply(ctx context.Context, agentID uuid.UUID) (*store.Message, error) {
	return s.st.LatestCompletedAssistant(ctx, agentID)
}

// platformCommandSpec describes one platform built-in command; platformCommands is the one list shared by the
// conversation page's "/" menu and POST /messages. piOnly: only applies to Pi sessions (Claude Code manages its own
// sessions; the platform cannot compact, export or resume them); needsSession: requires a usable session.
type platformCommandSpec struct {
	name, usage          string
	piOnly, needsSession bool
	zh, en               string // Chinese and English descriptions; usage placeholders (instructions, html|jsonl, session-id) are spelled the same in them
}

// platformCommands is the list of platform commands.
// check-english: zh (the console shows the zh descriptions in the Chinese interface)
var platformCommands = []platformCommandSpec{
	{name: "session", usage: "/session",
		zh: "查看当前会话、模型和进程状态", en: "Show the current session, model, and process"},
	{name: "new", usage: "/new",
		zh: "重开会话，保留页面里的对话历史", en: "Start a new session and keep the page's history"},
	{name: "compact", usage: "/compact [instructions]", piOnly: true, needsSession: true,
		zh: "压缩当前会话的上下文；instructions 是给这次压缩的附加说明，可省略", en: "Compact the current context; instructions are optional guidance for this compaction"},
	{name: "reload", usage: "/reload",
		zh: "停止当前进程，下一条消息时重新加载", en: "Stop the process; it reloads on the next message"},
	{name: "copy", usage: "/copy",
		zh: "复制最近一条有文字的成功回复", en: "Copy the latest successful reply that has text"},
	{name: "export", usage: "/export [html|jsonl]", piOnly: true, needsSession: true,
		zh: "导出当前会话，格式为 html（默认）或 jsonl", en: "Export the current session as html (default) or jsonl"},
	{name: "resume", usage: "/resume [session-id]", piOnly: true,
		zh: "不带 session-id 时列出可恢复的会话；带上完整的 session-id 时恢复那个会话", en: "Without session-id, list resumable sessions; with a full session-id, resume that session"},
}

// IsPlatformCommand reports whether name (without "/") is a platform built-in command.
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

// PlatformCommand is one platform built-in command and whether it can be used right now. UnavailableReason:
// runtime_unsupported (the runtime does not support it), session_none (there is no session yet), session_missing (the
// session file is lost); empty when it can be used. Conditions that change from moment to moment, such as the Agent
// being busy, are not judged here.
type PlatformCommand struct {
	Name, Usage       string
	Description       LocalizedDescription
	Available         bool
	UnavailableReason string
}

// LocalizedDescription holds a platform command's Chinese and English descriptions (the LocalizedText object of the
// API, zh-CN first).
type LocalizedDescription struct {
	ZhCN string `json:"zh-CN"`
	EnUS string `json:"en-US"`
}

// PlatformCommands lists the platform built-in commands according to the Agent's runtime and session state.
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
		out = append(out, PlatformCommand{Name: c.name, Usage: c.usage, Description: LocalizedDescription{ZhCN: c.zh, EnUS: c.en},
			Available: reason == "", UnavailableReason: reason})
	}
	return out, nil
}

// RequireCommand confirms that the Agent's runtime supports this platform command (name without "/"); when it does not,
// it returns 409 console_command_failed. Callers check this before acquiring the busy lock and creating a run.
func (s *Service) RequireCommand(ctx context.Context, agentID uuid.UUID, name string) error {
	if !piOnlyCommands[name] {
		return nil
	}
	agent, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agentruntime.ForProvider(agent.ModelProvider) == agentruntime.ClaudeCode {
		return apperr.Conflict(apperr.CodeConsoleCommandFailed, "Claude Code Agents do not support /"+name+". This command only works in Pi sessions.")
	}
	return nil
}
