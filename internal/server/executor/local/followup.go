package local

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
)

// followUp is a reply the runtime's CLI gives by itself, with no message from the platform (after background tasks it
// started have ended). What it says, which tools it runs and its usage are kept until a run takes it: the run of origin
// background the host made for it (runID), or a reply of the platform's that was starting when the CLI began it, which
// records it first (the follow-up is then merged). It is the runtime client's runturn.SelfTurn.
type followUp struct {
	e        *Executor
	agentID  uuid.UUID
	runID    uuid.UUID // the run of origin background made for it
	triggers []executor.BackgroundTask
	sel      executor.Selection
	version  int64
	done     chan struct{}

	mu     sync.Mutex
	buf    []func(runturn.Sink)
	usage  []executor.Usage
	sink   runturn.Sink // set once a run took it
	taker  *uuid.UUID   // the run that took it
	ended  bool
	result runturn.Result
	spoke  bool // its first text came (blank lines before it are left out)
}

var _ runturn.SelfTurn = (*followUp)(nil)

func (f *followUp) do(fn func(runturn.Sink)) {
	f.mu.Lock()
	if f.sink == nil {
		f.buf = append(f.buf, fn)
		f.mu.Unlock()
		return
	}
	k := f.sink
	f.mu.Unlock()
	fn(k)
}

// TextDelta passes on the reply's text, without the blank lines it may start with (agy starts its own replies so).
func (f *followUp) TextDelta(s string) {
	f.mu.Lock()
	if !f.spoke {
		s = strings.TrimLeft(s, " \t\r\n")
		f.spoke = s != ""
	}
	f.mu.Unlock()
	if s != "" {
		f.do(func(k runturn.Sink) { k.TextDelta(s) })
	}
}
func (f *followUp) ToolStarted(id, name string) {
	f.do(func(k runturn.Sink) { k.ToolStarted(id, name) })
}
func (f *followUp) ToolEnded(id, name string, failed bool) {
	f.do(func(k runturn.Sink) { k.ToolEnded(id, name, failed) })
}
func (f *followUp) ToolCommand(id, name, command string) {
	f.do(func(k runturn.Sink) {
		if c, ok := k.(interface {
			ToolCommand(id, name, command string)
		}); ok {
			c.ToolCommand(id, name, command)
		}
	})
}
func (f *followUp) ToolFiles(id, name string, paths []string) {
	f.do(func(k runturn.Sink) {
		if c, ok := k.(interface {
			ToolFiles(id, name string, paths []string)
		}); ok {
			c.ToolFiles(id, name, paths)
		}
	})
}
func (f *followUp) ToolQuery(id, name, query string) {
	f.do(func(k runturn.Sink) {
		if c, ok := k.(interface{ ToolQuery(id, name, query string) }); ok {
			c.ToolQuery(id, name, query)
		}
	})
}
func (f *followUp) ToolNoResult(id, name string) {
	f.do(func(k runturn.Sink) {
		if c, ok := k.(interface{ ToolNoResult(id, name string) }); ok {
			c.ToolNoResult(id, name)
		}
	})
}

// End implements runturn.SelfTurn.
func (f *followUp) End(r runturn.Result) {
	f.mu.Lock()
	if f.ended {
		f.mu.Unlock()
		return
	}
	r.Text = strings.TrimLeft(r.Text, " \t\r\n")
	f.ended, f.result = true, r
	f.mu.Unlock()
	close(f.done)
	f.e.followUpEnded(f)
}

func (f *followUp) isEnded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

// run is the run its usage and background tasks go to: the one that took it, else the one made for it.
func (f *followUp) run() uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.taker != nil {
		return *f.taker
	}
	return f.runID
}

// addUsage records usage of the reply on the run that took it, or keeps it until one does.
func (f *followUp) addUsage(u executor.Usage) {
	f.mu.Lock()
	if f.taker == nil {
		f.usage = append(f.usage, u)
		f.mu.Unlock()
		return
	}
	run := *f.taker
	f.mu.Unlock()
	f.e.obs.Usage(f.agentID, &run, u)
}

// take gives the reply to the run runID: what came so far goes to sink in order, then the rest as it comes.
func (f *followUp) take(runID uuid.UUID, sink runturn.Sink) {
	f.mu.Lock()
	for len(f.buf) > 0 {
		buf := f.buf
		f.buf = nil
		f.mu.Unlock()
		for _, fn := range buf {
			fn(sink)
		}
		f.mu.Lock()
	}
	f.sink, f.taker = sink, &runID
	usage := f.usage
	f.usage = nil
	f.mu.Unlock()
	for _, u := range usage {
		f.e.obs.Usage(f.agentID, &runID, u)
	}
}

// selfTurnBegan is called by a runtime client when its CLI begins a turn by itself: the reply becomes a follow-up of
// the tasks that ended since the platform last sent a message, and of the task about (the one the CLI says the turn is
// about, which may end just after the turn began; empty when it says none), and the host makes a run of origin
// background for it.
// A task instance gets no runs of that kind: its turn waits for its background work, and a reply after that wait is
// not recorded.
func (e *Executor) selfTurnBegan(p *proc, about string) runturn.SelfTurn {
	f := &followUp{e: e, agentID: p.agentID, runID: uuid.Must(uuid.NewV7()), triggers: e.bgTakeEnded(p), done: make(chan struct{}),
		sel: executor.Selection{Runtime: p.drv.runtime(), Provider: p.provider, ModelID: p.model, ThinkingLevel: p.thinking}, version: p.version}
	host := e.followUps.Load()
	e.mu.Lock()
	p.selfTurn = f
	if !p.task && host == nil {
		e.mu.Unlock()
		p.log.Warn("the runtime replied by itself; the reply is not recorded")
		f.take(uuid.Nil, discardSink{})
		return f
	}
	// A task instance's reply goes into the turn that waits for it (or the instance's next turn): no run is made.
	e.followUpQueue[p.agentID] = append(e.followUpQueue[p.agentID], f)
	e.mu.Unlock()
	p.bgNotify()
	if !p.task {
		triggers := slices.Clone(f.triggers)
		go func() {
			if about != "" && !slices.ContainsFunc(triggers, func(t executor.BackgroundTask) bool { return t.ID == about }) {
				if t, ok := e.bgAwaitEnd(p, about, aboutWait); ok {
					triggers = append(triggers, t)
				}
			}
			(*host).FollowUp(p.agentID, f.runID, triggers)
		}()
	}
	return f
}

// aboutWait is how long a follow-up waits for the end of the task it is about (Grok announces the turn first).
const aboutWait = 10 * time.Second

// followUpEnded forgets the reply as the one the process is giving.
func (e *Executor) followUpEnded(f *followUp) {
	var ended []*proc
	e.mu.Lock()
	for p := range e.live {
		if p.selfTurn == f {
			p.selfTurn = nil
			ended = append(ended, p)
		}
	}
	e.mu.Unlock()
	for _, p := range ended {
		p.bgNotify()
	}
}

// takeFollowUp takes the follow-up made for the run runID; nil when there is none (another run took it, or the process
// that gave it is gone without a trace).
func (e *Executor) takeFollowUp(agentID, runID uuid.UUID) *followUp {
	e.mu.Lock()
	defer e.mu.Unlock()
	queue := e.followUpQueue[agentID]
	for i, f := range queue {
		if f.runID == runID {
			e.followUpQueue[agentID] = slices.Delete(queue, i, i+1)
			return f
		}
	}
	return nil
}

// takeFollowUps takes every follow-up the Agent's CLI began that no run has taken yet, oldest first.
func (e *Executor) takeFollowUps(agentID uuid.UUID) []*followUp {
	e.mu.Lock()
	defer e.mu.Unlock()
	queue := e.followUpQueue[agentID]
	delete(e.followUpQueue, agentID)
	return queue
}

// absorbFollowUps records, as part of the run runID that is about to send its message, the replies the CLI began by
// itself that no run has taken yet: they came first. It waits for them to end and returns their text.
func (e *Executor) absorbFollowUps(ctx context.Context, p *proc, runID uuid.UUID, sink runturn.Sink) string {
	var text []string
	for _, f := range e.takeFollowUps(p.agentID) {
		f.take(runID, sink)
		select {
		case <-f.done:
		case <-ctx.Done():
			return strings.Join(text, "\n\n")
		}
		if t := strings.TrimSpace(f.result.Text); t != "" {
			text = append(text, t)
		}
	}
	if len(text) > 0 {
		sink.TextDelta("\n\n")
	}
	return strings.Join(text, "\n\n")
}

// executeFollowUp is Execute for a run of origin background: it records the reply the CLI is giving (or gave) by itself
// that the run was made for. Nothing is sent to the runtime. A run whose reply another run took ends as merged.
func (e *Executor) executeFollowUp(ctx context.Context, r *run, req executor.Request, sink executor.Sink) (executor.Result, error) {
	f := e.takeFollowUp(req.AgentID, req.RunID)
	if f == nil {
		return executor.Result{Outcome: executor.OutcomeMerged}, nil
	}
	p := e.liveProc(req.AgentID)
	if p != nil {
		e.mu.Lock()
		p.busy = true
		if p.idleTimer != nil {
			p.idleTimer.Stop()
		}
		e.mu.Unlock()
		r.mu.Lock()
		r.proc, r.prompting = p, true
		r.mu.Unlock()
		runID := req.RunID
		p.curRun.Store(&runID)
	}
	sink.Running(f.version, f.sel)
	cwd := ""
	if p != nil {
		cwd = p.cwd
	}
	f.take(req.RunID, sinkAdapter{s: sink, cwd: cwd, command: func(id, name, command string) {
		e.toolCommand(req.AgentID, req.RunID, id, name, command)
	}, event: func(typ string, data map[string]any) { e.event(req.AgentID, &req.RunID, typ, data) }})
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	if p != nil {
		e.turnFinished(p)
	}
	if ctx.Err() != nil && !f.isEnded() {
		return executor.Result{}, ctx.Err()
	}
	tr := f.result
	res := executor.Result{Text: tr.Text, ConfigVersion: f.version}
	switch {
	case tr.Outcome == runturn.OutcomeCompleted:
		res.Outcome = executor.OutcomeCompleted
	case tr.Outcome == runturn.OutcomeAborted, r.interrupted.Load():
		res.Outcome = executor.OutcomeInterrupted
	default:
		res.Outcome, res.ErrorCode, res.ErrorMessage = executor.OutcomeFailed, tr.ErrorCode, tr.ErrorMessage
	}
	return res, nil
}

// usage records usage the runtime reported during a turn: on the run replying, else on the reply the CLI is giving by
// itself; outside any turn it belongs to no run.
func (e *Executor) usage(p *proc, u executor.Usage, inTurn bool) {
	if inTurn {
		if run := p.curRun.Load(); run != nil {
			e.obs.Usage(p.agentID, run, u)
			return
		}
		e.mu.Lock()
		f := p.selfTurn
		e.mu.Unlock()
		if f != nil {
			f.addUsage(u)
			return
		}
	}
	e.obs.Usage(p.agentID, nil, u)
}

// replyRun is the run a background task that starts now belongs to: the run replying, else the one made for the reply
// the CLI is giving by itself; nil when there is neither.
func (e *Executor) replyRun(p *proc) *uuid.UUID {
	if run := p.curRun.Load(); run != nil {
		return run
	}
	e.mu.Lock()
	f := p.selfTurn
	e.mu.Unlock()
	if f == nil {
		return nil
	}
	run := f.run()
	return &run
}

// endFollowUps ends the replies the process was giving by itself when it is gone (the client normally ends them as
// the process exits).
func (e *Executor) endFollowUps(p *proc) {
	e.mu.Lock()
	f := p.selfTurn
	p.selfTurn = nil
	e.mu.Unlock()
	if f != nil {
		f.End(runturn.Result{Outcome: runturn.OutcomeFailed, ErrorCode: executor.ErrRuntimeExited, ErrorMessage: "The runtime's process ended."})
	}
}

// discardSink drops a reply nobody records.
type discardSink struct{}

func (discardSink) TextDelta(string)               {}
func (discardSink) ToolStarted(string, string)     {}
func (discardSink) ToolEnded(string, string, bool) {}

// quietRuntime is a driver whose CLI does not reply by itself after its background work ended (Codex).
type quietRuntime interface {
	noFollowUp()
}

// holdForBackground keeps a task instance's turn (the run runID) open after its prompt while background tasks of the
// process run, and until the CLI's own reply after one ended has come and ended; that reply goes into the turn, and
// its text is returned. When nothing comes from the runtime for the background wait (Options.ACPBackgroundWait) the
// tasks still running are stopped (task_limit) and the turn ends, saying why. When the run is stopped meanwhile, the
// tasks are stopped with it and stopped reports true.
func (e *Executor) holdForBackground(ctx context.Context, p *proc, r *run, runID uuid.UUID, sink sinkAdapter) (string, bool) {
	limit := e.o.ACPBackgroundWait
	if limit <= 0 {
		limit = defaultACPBackgroundWait
	}
	replyWait := min(acpReplyQuiet, limit)
	_, quiet := p.drv.(quietRuntime)
	var texts []string
	last, expect := time.Now(), false
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	stop := func(reason string) {
		var ids []string
		for _, t := range e.bgRunning(p) {
			ids = append(ids, t.ID)
		}
		if len(ids) > 0 {
			e.stopTasks(ctx, p, ids, reason)
		}
	}
	for {
		changed := p.bgChanged()
		for _, f := range e.takeFollowUps(p.agentID) {
			f.take(runID, &followOnSink{sinkAdapter: sink})
			for done := false; !done; {
				select {
				case <-f.done:
					done = true
				case <-poll.C:
					if r.interrupted.Load() {
						stop(executor.StopReplyStopped)
						return strings.Join(texts, "\n\n"), true
					}
				case <-ctx.Done():
					return strings.Join(texts, "\n\n"), false
				}
			}
			if text := strings.TrimSpace(f.result.Text); text != "" {
				texts = append(texts, text)
			}
			last, expect = time.Now(), false
		}
		running := len(e.bgRunning(p))
		e.mu.Lock()
		replying := p.selfTurn != nil
		e.mu.Unlock()
		if running == 0 && !expect && !replying {
			return strings.Join(texts, "\n\n"), false
		}
		wait := limit
		if running == 0 {
			wait = replyWait
		}
		timer := time.NewTimer(time.Until(last.Add(wait)))
		select {
		case <-changed:
			timer.Stop()
			if len(e.bgRunning(p)) < running && !quiet {
				expect = true // the CLI replies by itself after a task ended
			}
			last = time.Now()
		case <-timer.C:
			if running > 0 {
				stop(executor.StopTaskLimit)
				sink.BackgroundWaitTimedOut(limit)
			}
			return strings.Join(texts, "\n\n"), false
		case <-poll.C:
			timer.Stop()
			if r.interrupted.Load() {
				stop(executor.StopReplyStopped)
				return strings.Join(texts, "\n\n"), true
			}
		case <-ctx.Done():
			timer.Stop()
			return strings.Join(texts, "\n\n"), false
		}
	}
}

// followOnSink passes on a reply that follows another in the same turn: its text starts after a blank line, without
// the reply's own leading blank lines, as the turn's final text has it.
type followOnSink struct {
	sinkAdapter
	started bool
}

func (s *followOnSink) TextDelta(d string) {
	if !s.started {
		d = strings.TrimLeft(d, " \t\r\n")
		if d == "" {
			return
		}
		s.started = true
		d = "\n\n" + d
	}
	s.sinkAdapter.TextDelta(d)
}
