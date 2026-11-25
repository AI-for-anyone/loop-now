package local

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// procBackground holds the background tasks of one runtime process: work its CLI started during a reply that outlives
// the reply. They live only as long as the process; each is reported to the observer when it starts, when a stop is
// asked for and when it ends (once: the first end counts).
type procBackground struct {
	mu      sync.Mutex
	running map[string]*executor.BackgroundTask // by the runtime's id
	// ended are the tasks that ended since the platform last sent the runtime a message, in the order they ended: the
	// CLI tells about them in its next reply, its own or the one to that message.
	ended []executor.BackgroundTask
	// done are the tasks that have ended, by the runtime's id (the latest), so a stop can say how one ended.
	done map[string]executor.BackgroundTask
	// stopReasons are the reasons stops were asked for with, by the runtime's id: a driver records a task the runtime
	// reports as stopped with it.
	stopReasons map[string]string
	// changed is closed and replaced on every change, for those that wait for one.
	changed chan struct{}
}

func (b *procBackground) notifyLocked() {
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
}

// bgChanged returns a channel closed at the next change of the process's background tasks.
func (p *proc) bgChanged() <-chan struct{} {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	if p.bg.changed == nil {
		p.bg.changed = make(chan struct{})
	}
	return p.bg.changed
}

// reportBackground hands a task's state to the observer, when it keeps background tasks.
func (e *Executor) reportBackground(p *proc, t executor.BackgroundTask) {
	if o, ok := e.obs.(executor.BackgroundObserver); ok {
		o.BackgroundTask(p.agentID, t)
	}
}

// bgStarted records a task the runtime started in the background; it belongs to the run replying now unless t.RunID
// says otherwise. A task already running with the same id is not started again.
func (e *Executor) bgStarted(p *proc, t executor.BackgroundTask) {
	if t.RunID == uuid.Nil {
		run := e.replyRun(p)
		if run == nil {
			p.log.Warn("a background task started with no reply to tie it to; it is not listed", "taskId", t.ID)
			return
		}
		t.RunID = *run
	}
	t.Key, t.Runtime, t.Status = uuid.Must(uuid.NewV7()), p.drv.runtime(), executor.BackgroundRunning
	t.Label = executor.ClipLabel(t.Label)
	t.EndedAt, t.ExitCode, t.StopReason, t.StopRequestedAt, t.Output = nil, nil, "", nil, nil
	if t.StartedAt.IsZero() {
		t.StartedAt = time.Now()
	}
	p.bg.mu.Lock()
	if _, ok := p.bg.running[t.ID]; ok {
		p.bg.mu.Unlock()
		return
	}
	if p.bg.running == nil {
		p.bg.running = map[string]*executor.BackgroundTask{}
	}
	p.bg.running[t.ID] = &t
	e.reportBackground(p, t)
	p.bg.notifyLocked()
	p.bg.mu.Unlock()
}

// bgEnded records the end of a running task and reports whether it counted: a task that is not running (unknown, or it
// has ended already) is left as it is.
func (e *Executor) bgEnded(p *proc, id string, end executor.BackgroundEnd) bool {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	t := p.bg.running[id]
	if t == nil {
		return false
	}
	delete(p.bg.running, id)
	e.endTaskLocked(p, t, end, time.Now())
	return true
}

func (e *Executor) endTaskLocked(p *proc, t *executor.BackgroundTask, end executor.BackgroundEnd, at time.Time) {
	t.Status, t.EndedAt, t.ExitCode = end.Status, &at, end.ExitCode
	if end.Status == executor.BackgroundStopped {
		t.StopReason = end.StopReason
	}
	if end.Output != nil {
		tail := executor.TailOutput(*end.Output)
		t.Output = &tail
	}
	e.reportBackground(p, *t)
	p.bg.ended = append(p.bg.ended, *t)
	if p.bg.done == nil {
		p.bg.done = map[string]executor.BackgroundTask{}
	}
	p.bg.done[t.ID] = *t
	p.bg.notifyLocked()
}

// bgStopRequested records that a stop was asked for a running task, and reports whether it was (only once per task).
func (e *Executor) bgStopRequested(p *proc, id string, at time.Time) bool {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	t := p.bg.running[id]
	if t == nil || t.StopRequestedAt != nil {
		return false
	}
	t.StopRequestedAt = &at
	e.reportBackground(p, *t)
	p.bg.notifyLocked()
	return true
}

// bgRunning returns the running tasks, oldest first.
func (e *Executor) bgRunning(p *proc) []executor.BackgroundTask {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	return e.bgRunningLocked(p)
}

func (e *Executor) bgRunningLocked(p *proc) []executor.BackgroundTask {
	out := make([]executor.BackgroundTask, 0, len(p.bg.running))
	for _, t := range p.bg.running {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b executor.BackgroundTask) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Key.String(), b.Key.String()) // uuid v7: in the order they were made
	})
	return out
}

// bgTakeEnded returns the tasks that ended since it was last called, and forgets them.
func (e *Executor) bgTakeEnded(p *proc) []executor.BackgroundTask {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	out := p.bg.ended
	p.bg.ended = nil
	return out
}

// backgroundStopper is a driver that can ask its runtime to stop background tasks (each by the runtime's id). It returns
// once the request is made; the ends come as the runtime reports them.
type backgroundStopper interface {
	stopBackground(ctx context.Context, p *proc, ids []string) error
}

var (
	_ executor.BackgroundStopper = (*Executor)(nil)
	_ executor.RestartChecker    = (*Executor)(nil)
)

// StopBackground implements executor.BackgroundStopper.
func (e *Executor) StopBackground(ctx context.Context, agentID uuid.UUID, id, reason string) (executor.BackgroundTask, error) {
	p := e.liveProc(agentID)
	if p == nil {
		return executor.BackgroundTask{}, executor.ErrBackgroundNotFound
	}
	p.bg.mu.Lock()
	_, ok := p.bg.running[id]
	p.bg.mu.Unlock()
	if !ok {
		return executor.BackgroundTask{}, executor.ErrBackgroundNotFound
	}
	return e.stopTasks(ctx, p, []string{id}, reason)[0], nil
}

// StopAllBackground implements executor.BackgroundStopper.
func (e *Executor) StopAllBackground(ctx context.Context, agentID uuid.UUID, reason string) ([]executor.BackgroundTask, error) {
	p := e.liveProc(agentID)
	if p == nil {
		return []executor.BackgroundTask{}, nil
	}
	running := e.bgRunning(p)
	ids := make([]string, 0, len(running))
	for _, t := range running {
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		return []executor.BackgroundTask{}, nil
	}
	return e.stopTasks(ctx, p, ids, reason), nil
}

func (e *Executor) liveProc(agentID uuid.UUID) *proc {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.procs[agentID]
}

// stopTasks asks the runtime to stop the tasks ids, waits up to BackgroundStopWait for them to end and returns each as
// it is then. A task that ends on its own meanwhile keeps that end. reason is what a stopped task is recorded with.
func (e *Executor) stopTasks(ctx context.Context, p *proc, ids []string, reason string) []executor.BackgroundTask {
	now := time.Now()
	for _, id := range ids {
		e.bgStopRequested(p, id, now)
	}
	p.bg.mu.Lock()
	if p.bg.stopReasons == nil {
		p.bg.stopReasons = map[string]string{}
	}
	for _, id := range ids {
		p.bg.stopReasons[id] = reason
	}
	p.bg.mu.Unlock()
	if s, ok := p.drv.(backgroundStopper); ok {
		if err := s.stopBackground(ctx, p, ids); err != nil {
			p.log.Warn("the runtime could not be asked to stop background tasks", "tasks", ids, "err", err)
		}
	}
	wait := e.o.BackgroundStopWait
	if wait <= 0 {
		wait = 3 * time.Second
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		changed := p.bgChanged()
		out, pending := e.tasksNow(p, ids)
		if !pending {
			return out
		}
		select {
		case <-changed:
		case <-deadline.C:
			return out
		case <-ctx.Done():
			return out
		}
	}
}

// tasksNow returns the tasks ids as they are now (running or ended), and whether any of them still runs.
func (e *Executor) tasksNow(p *proc, ids []string) ([]executor.BackgroundTask, bool) {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	out := make([]executor.BackgroundTask, 0, len(ids))
	pending := false
	for _, id := range ids {
		if t := p.bg.running[id]; t != nil {
			out = append(out, *t)
			pending = true
		} else {
			out = append(out, p.bg.done[id])
		}
	}
	return out, pending
}

// bgStopReason is the reason a stop of the task id was asked for with; empty when none was.
func (p *proc) bgStopReason(id string) string {
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	return p.bg.stopReasons[id]
}

// RestartOnNextMessage reports whether the Agent's next message restarts its runtime process: its settings changed
// since the process started. With no process there is nothing to restart.
func (e *Executor) RestartOnNextMessage(ctx context.Context, agentID uuid.UUID) (bool, error) {
	p := e.liveProc(agentID)
	if p == nil {
		return false, nil
	}
	snap, err := e.src.ExecutionSnapshot(ctx, agentID)
	if errors.Is(err, agentconfig.ErrCredentialUnavailable) {
		return false, nil // the next message fails before it reaches the process
	}
	if err != nil {
		return false, err
	}
	return !e.reusable(p, snap), nil
}

// bgBusyLocked reports whether the process has background work going on: tasks running (they would end with it) or a
// reply its CLI is giving by itself. Executor.mu is held.
func (p *proc) bgBusyLocked() bool {
	if p.selfTurn != nil {
		return true
	}
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	return len(p.bg.running) > 0
}

// bgEndAll ends every running task as the process ends: stopped for reason, or unknown when there is none (the
// process exited by itself).
func (e *Executor) bgEndAll(p *proc, reason string) {
	end := executor.BackgroundEnd{Status: executor.BackgroundStopped, StopReason: reason}
	if reason == "" {
		end = executor.BackgroundEnd{Status: executor.BackgroundUnknown}
	}
	now := time.Now()
	p.bg.mu.Lock()
	defer p.bg.mu.Unlock()
	for _, t := range e.bgRunningLocked(p) {
		delete(p.bg.running, t.ID)
		e.endTaskLocked(p, &t, end, now)
	}
}

// backgroundStopReason is why the process's background tasks stop when it is stopped for why: the platform's reason
// for stopping it, or, when it ended by itself, a reply being stopped (the runtime's stop ends the process); empty when
// it exited unexpectedly.
func backgroundStopReason(p *proc, why stopCause) string {
	if why.bg != "" {
		return why.bg
	}
	switch why.reason {
	case stopConfigChanged:
		if p.sessionStale.Load() {
			return executor.StopRuntimeSwitched
		}
		return executor.StopSettingsChanged
	case stopConsoleReload:
		return executor.StopSettingsChanged
	case stopReleased:
		return executor.StopAgentDeleted
	case stopShutdown:
		return executor.StopShutdown
	case stopAbortTimeout:
		return executor.StopReplyStopped
	case stopForkSnapshot:
		return executor.StopTaskStarted
	case stopSessionReset, stopSessionResumed:
		return executor.StopSessionReset
	}
	if p.replyStopping.Load() {
		return executor.StopReplyStopped
	}
	return ""
}
