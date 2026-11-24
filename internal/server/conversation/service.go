// Package conversation coordinates conversations between the console and regular Agents: sending messages, the busy
// lock, queueing and interrupting, each Agent's driver loop, handling after a server restart, and periodic
// reconciliation. A queued message is simply a run with status='queued' in the database.
package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/contexthandoff"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/reqid"
	"loop-now/internal/server/store"
)

// Options holds the conversation service's parameters.
type Options struct {
	InstanceID uuid.UUID
	MaxMessage int64
	// AbortPollInterval is how often the database is checked for an interrupt flag while a reply is running (a fallback
	// for when the interrupt notification is lost); 0 means 10 seconds.
	AbortPollInterval time.Duration
	ReconcileInterval time.Duration
	// HandoffBudget is the budget of the conversation carried to a new runtime after a change of runtime (see
	// contexthandoff); 0 means the default.
	HandoffBudget int
}

func (o Options) abortPollInterval() time.Duration {
	if o.AbortPollInterval > 0 {
		return o.AbortPollInterval
	}
	return 10 * time.Second
}

type driverStart struct {
	run   store.Run
	owner string
}

// Service is the conversation service.
type Service struct {
	st  *store.Store
	co  *coord.Coord
	ex  executor.Executor
	o   Options
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu             sync.Mutex
	drivers        map[uuid.UUID]bool
	pendingDrivers map[uuid.UUID]driverStart

	overviewMu      sync.Mutex
	overviewWorkers map[uuid.UUID]*overviewWorker
	overviewWG      sync.WaitGroup
	sleepRevisions  sync.Map
	dispatchGates   sync.Map // AgentID -> chan struct{}; a task fork blocks dispatching new turns, not the current one
	commands        sync.Map // RunID -> *consoleCommandRun; a manual compaction request waits for its run to end
	sessions        sync.Map // AgentID -> sessionPush; the session.updated most recently pushed

	// InboxCancelled is called after an Inbox message has been cancelled by a person and the transaction has committed
	// (it appends an Inbox event); when nil, nothing is pushed.
	InboxCancelled func(ctx context.Context, m store.InboxMessage)
}

// New creates the conversation service; queued messages are only processed after Start is called.
func New(st *store.Store, co *coord.Coord, ex executor.Executor, o Options, log *slog.Logger) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{st: st, co: co, ex: ex, o: o, log: log, ctx: ctx, cancel: cancel, drivers: map[uuid.UUID]bool{},
		pendingDrivers: map[uuid.UUID]driverStart{}, overviewWorkers: map[uuid.UUID]*overviewWorker{}}
}

// Start handles in-flight runs left over from the previous process (marking them “服务端重启中断” ("Cut off by a server
// restart")), then starts the reconciliation goroutine.
func (s *Service) Start(ctx context.Context) error {
	if err := s.recoverAfterRestart(ctx); err != nil {
		return err
	}
	s.reconcile(ctx)
	s.wg.Go(s.reconcileLoop)
	return nil
}

// Stop stops accepting new dispatches and waits for the driver loops to exit. In-flight runs keep their current status
// and are handled as interrupted by a restart on the next start.
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	// After cancellation no new overview push goroutines are started under the lock, so waiting for them from here on
	// is safe.
	s.overviewMu.Lock()
	s.overviewMu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); s.overviewWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.endInFlight(ctx)
	return nil
}

// endInFlight runs after all driver loops have exited and closes this instance's in-flight runs as interrupted by a
// server restart, saving the reply text produced so far. The reply text lives only in memory: if closing were left to
// the next start (recoverAfterRestart) it would be lost. When the process crashes there is no time to do this, and that
// text is lost.
func (s *Service) endInFlight(ctx context.Context) {
	runs, err := s.st.ActiveRuns(ctx)
	if err != nil {
		s.log.Warn("stop: failed to read active runs; they are finished at the next start", "err", err)
		return
	}
	for _, r := range runs {
		if r.InstanceID == nil || *r.InstanceID != s.o.InstanceID {
			continue
		}
		text, _ := s.co.RunText(ctx, r.ID.String())
		s.endRun(ctx, r.AgentID, r.ID, store.RunEnd{Status: "interrupted", EndReason: "server_restart", Text: text})
	}
}

// ---- Sending messages ----

// Send accepts a console message. When the Agent is idle it starts processing right away; when it is busy the caller
// must choose interrupt or queue.
func (s *Service) Send(ctx context.Context, agentID uuid.UUID, text, whenBusy string, imageIDs ...uuid.UUID) (apitypes.Message, error) {
	if strings.TrimSpace(text) == "" && len(imageIDs) == 0 {
		return apitypes.Message{}, apperr.Invalid("text", "Must not be blank unless images are attached.")
	}
	if int64(len(text)) > s.o.MaxMessage {
		return apitypes.Message{}, apperr.TooLarge("The message text exceeds the size limit.")
	}
	if whenBusy != "" && whenBusy != "interrupt" && whenBusy != "queue" {
		return apitypes.Message{}, apperr.Invalid("whenBusy", "Must be interrupt or queue.")
	}
	if s.ctx.Err() != nil {
		return apitypes.Message{}, errors.New("the server is shutting down")
	}

	runID := uuid.Must(uuid.NewV7())
	owner := coord.LockValue(runID.String(), s.o.InstanceID.String())
	acquired, err := s.co.Acquire(ctx, agentID.String(), owner)
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
		case errors.Is(err, store.ErrActiveRunExists), errors.Is(err, store.ErrQueuedRunsExist):
			// The lock is free, but the database has an in-flight or queued run (e.g. right after a restart, or between
			// handoffs): restore the lock from the database, then treat the Agent as busy.
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
		details.CurrentRunID = &active.ID
	} else if len(queued) > 0 {
		details.CurrentRunID = &queued[0].ID
		s.kick(ctx, agentID)
	}
	e := apperr.Conflict(apperr.CodeAgentBusy, "The Agent is replying. Choose to interrupt it or queue the message.")
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

// interruptActive turns the in-flight run into aborting (byRunID is the run of the message that caused the interrupt)
// and notifies the driver loop that holds it.
func (s *Service) interruptActive(ctx context.Context, agentID, byRunID uuid.UUID) {
	if h := testHookBeforeMarkAborting.Load(); h != nil {
		(*h)(byRunID)
	}
	active, err := s.st.MarkAborting(ctx, agentID, byRunID)
	if err != nil {
		s.log.Error("failed to mark the interrupt", "agentId", agentID, "err", err)
		return
	}
	if active == nil {
		return
	}
	s.emitRun(ctx, agentID, active.ID)
	if err := s.co.PublishInterrupt(ctx, agentID.String(), active.ID.String()); err != nil {
		s.log.Warn("failed to publish the interrupt command; waiting for the driver loop's periodic check", "agentId", agentID, "err", err)
	}
}

// restoreLock re-sets the busy lock from the in-flight run in the database.
func (s *Service) restoreLock(ctx context.Context, agentID uuid.UUID) {
	active, err := s.st.ActiveRun(ctx, agentID)
	if err != nil || active == nil {
		return
	}
	s.restoreRunLock(ctx, agentID, *active)
}

// restoreRunLock re-sets the busy lock for an in-flight run read from the database (only when the lock is free).
//
// Between reading "in flight" and re-setting the lock, the run may already have ended and its driver may already have
// released the lock (the lock can only be re-set while it is free, so a successful set means the driver no longer holds
// it). The lock just set then belongs to a finished run and nobody will ever release it: reconciliation only re-sets
// locks for in-flight runs and only hands off for Agents that have queued runs, so the Agent would show as busy
// forever. So after setting it, look at the run's status once more; if the run has already ended, release the lock just
// set (only while it is still that run's lock) and go on processing the queue.
func (s *Service) restoreRunLock(ctx context.Context, agentID uuid.UUID, run store.Run) {
	if run.InstanceID == nil {
		return
	}
	owner := coord.LockValue(run.ID.String(), run.InstanceID.String())
	if h := testHookBeforeRestoreLock.Load(); h != nil {
		(*h)(run.ID)
	}
	set, err := s.co.Restore(ctx, agentID.String(), owner)
	if err != nil || !set {
		return
	}
	status, err := s.st.RunStatus(ctx, run.ID)
	if err != nil {
		s.log.Warn("failed to confirm the run status after re-setting the busy lock; keeping the lock", "agentId", agentID, "runId", run.ID, "err", err)
		return
	}
	switch status {
	case "starting", "running", "aborting":
		return
	}
	if released, _ := s.co.Release(ctx, agentID.String(), owner); released {
		s.kick(ctx, agentID)
	}
}

// ---- Driver loop ----

// kick takes the message at the head of the queue and starts processing it, when this process has no driver loop for
// the Agent and the lock is free.
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
	next, ok, err := s.handoff(ctx, agentID, "")
	if err != nil || !ok || next == "" {
		return
	}
	s.startNext(ctx, agentID, next)
}

// handoff hands off the Agent's busy lock; an empty current means the lock must be free. It picks the next queued run
// that can start from the database (interrupts first, then normal ones, each in the order they were queued) and swaps
// the lock to it; when there is no next run and current is non-empty it releases the lock. When the lock is no longer
// current it returns ok=false and changes nothing. It does not matter if the queue changes between picking and
// swapping: if the run the lock went to is no longer queued, StartQueuedRun will not start it and the caller keeps
// handing off; runs queued in the meantime are picked up by the sender's kick or by the kick when the driver loop
// exits.
func (s *Service) handoff(ctx context.Context, agentID uuid.UUID, current string) (string, bool, error) {
	id, found, err := s.st.NextQueuedRun(ctx, agentID)
	if err != nil {
		return "", false, err
	}
	next, owner := "", ""
	if found {
		next = id.String()
		owner = coord.LockValue(next, s.o.InstanceID.String())
	}
	if ok, err := s.co.Swap(ctx, agentID.String(), current, owner); err != nil || !ok {
		return "", false, err
	}
	return next, true, nil
}

// startNext starts with the run that has just acquired the lock; if it is no longer queued, it keeps handing off to the
// next one.
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
				s.log.Error("failed to start a queued run", "agentId", agentID, "runId", next, "err", err)
				_, _ = s.co.Release(ctx, agentID.String(), owner)
				return
			}
		}
		var ok bool
		next, ok, err = s.handoff(ctx, agentID, owner)
		if err != nil || !ok {
			return
		}
	}
}

func (s *Service) startDriver(agentID uuid.UUID, run store.Run, owner string) {
	s.mu.Lock()
	if s.drivers[agentID] {
		// Handoff has released the lock but the old driver has not yet removed its marker: the new run already holds
		// the lock, so kick cannot take it over later. Stash this start so the old driver continues with it directly
		// when it exits; regular messages and maintenance runs share this path.
		s.pendingDrivers[agentID] = driverStart{run: run, owner: owner}
		s.mu.Unlock()
		if h := testHookAfterDriverPending.Load(); h != nil {
			(*h)(run.ID)
		}
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
		pending, hasPending := s.pendingDrivers[agentID]
		delete(s.pendingDrivers, agentID)
		s.mu.Unlock()
		if ctx.Err() == nil {
			if hasPending {
				s.startDriver(agentID, pending.run, pending.owner)
			} else {
				s.kick(context.Background(), agentID) // if new messages were queued while exiting, process them next
			}
		}
	}()
	subCtx, cancelSub := context.WithCancel(ctx)
	defer cancelSub()
	var cur sync.Map // "run" -> uuid.UUID
	interrupts, err := s.co.SubscribeInterrupts(subCtx, agentID.String())
	if err != nil {
		s.log.Warn("failed to subscribe to interrupt commands; periodic checks will detect interrupts instead", "agentId", agentID, "err", err)
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
		res, command := s.execute(ctx, agentID, run, owner)
		if ctx.Err() != nil {
			return // server stopping: the run keeps its status and is marked interrupted by restart on the next start
		}
		// First write the end to the database and hand off the busy lock, then announce the end: a console that sends
		// as soon as it receives the final event (or as soon as the manual compaction request returns) then does not
		// run into the lock this run has not yet released and get treated as "still replying". The database must record
		// the end first: picking the next run in handoff and starting a run for a new send both require that the
		// database has no in-flight run.
		publish := s.finish(ctx, run, res, command)
		next, ok, err := s.handoff(ctx, agentID, owner)
		if err == nil && !ok {
			// The lock is no longer held by this run (busy locks do not expire, but the lock can have been released
			// elsewhere, as the lost-lock test simulates): hand off directly if the lock is free.
			next, ok, err = s.handoff(ctx, agentID, "")
		}
		publish()
		if err != nil || !ok || next == "" {
			if err != nil {
				s.log.Warn("failed to hand off the busy lock; waiting for reconciliation", "agentId", agentID, "err", err)
			}
			if err == nil && ok && next == "" {
				if h := testHookAfterDriveHandoff.Load(); h != nil {
					(*h)(agentID)
				}
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
					s.log.Error("failed to start a queued run", "agentId", agentID, "runId", next, "err", serr)
					_, _ = s.co.Release(ctx, agentID.String(), owner)
					return
				}
			}
			if !started {
				if next, ok, err = s.handoff(ctx, agentID, owner); err != nil || !ok {
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

// testHookBeforeInterrupt is for tests only: called after a run is confirmed to be aborting and before the executor's
// Interrupt is called, to inject timing between those two steps (e.g. let that run end first and the next run start).
// Nil in production.
var testHookBeforeInterrupt atomic.Pointer[func(runID uuid.UUID)]

// testHookBeforeMarkAborting is for tests only: called after the run that causes the interrupt is determined and before
// the in-flight run is marked aborting, so that the interrupting run can be dequeued and started first. Nil in
// production.
var testHookBeforeMarkAborting atomic.Pointer[func(byRunID uuid.UUID)]

// testHookBeforeRestoreLock is for tests only: called before re-setting the busy lock from an in-flight run that was
// read (reconciliation and restoreLock).
var testHookBeforeRestoreLock atomic.Pointer[func(runID uuid.UUID)]

// testHookAfterDriveHandoff is for tests only: called when a Handoff with an empty queue has released the lock and the
// driver has not yet removed its marker.
var testHookAfterDriveHandoff atomic.Pointer[func(agentID uuid.UUID)]

// testHookAfterDriverPending is for tests only: called after a new run has been registered in pendingDrivers.
var testHookAfterDriverPending atomic.Pointer[func(runID uuid.UUID)]

// testHookAfterRunEndPublished is for tests only: called after the driver loop has announced the end of a run (the
// final event, any waiting manual compaction request) and before it goes on to the next one.
var testHookAfterRunEndPublished atomic.Pointer[func(runID uuid.UUID)]

func beforeInterrupt(runID uuid.UUID) {
	if h := testHookBeforeInterrupt.Load(); h != nil {
		(*h)(runID)
	}
}

// execute calls the executor to process one run, periodically checking the database for the interrupt flag (in case the
// interrupt notification is lost). execute returns the result of the run and the command name handed to Pi (from a
// /pi:<name> message on the conversation page; empty when it is not a command).
func (s *Service) execute(ctx context.Context, agentID uuid.UUID, run store.Run, owner string) (executor.Result, string) {
	attrs := []any{"runId", run.ID, "agentId", agentID, "whenBusy", run.WhenBusy}
	if run.StartedAt != nil {
		attrs = append(attrs, "queuedMs", run.StartedAt.Sub(run.QueuedAt).Milliseconds())
	}
	s.log.Info("reply started", attrs...)
	if st, err := s.st.RunStatus(ctx, run.ID); err == nil && st == "aborting" {
		return executor.Result{Outcome: executor.OutcomeInterrupted}, ""
	}
	msg := store.Message{}
	if run.Origin == executor.OriginConsoleCompact {
		msg.Text = store.ParseCompaction(run.Compaction).Instructions // instructions recorded at run start; runs even if the request has disconnected
	} else {
		var err error
		msg, err = s.st.GetMessage(ctx, run.UserMessageID)
		if err != nil {
			return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "Could not read the message: " + err.Error()}, ""
		}
	}
	if run.Origin == "sleep_wake" {
		s.emitMessage(ctx, agentID, msg, run)
	}
	if run.Origin == "inbox_reminder" {
		text, void, err := s.prepareReminder(ctx, agentID, run)
		if err != nil {
			return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "Could not prepare the platform reminder: " + err.Error()}, ""
		}
		if void {
			return executor.Result{Outcome: outcomeVoided}, ""
		}
		msg.Text = text
	}
	pollCtx, stopPoll := context.WithCancel(ctx)
	defer stopPoll()
	go func() {
		t := time.NewTicker(s.o.abortPollInterval())
		defer t.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-t.C:
				if st, err := s.st.RunStatus(pollCtx, run.ID); err == nil && st == "aborting" {
					beforeInterrupt(run.ID)
					_ = s.ex.Interrupt(pollCtx, agentID, run.ID)
				}
			}
		}
	}()
	sink := &runSink{s: s, agentID: agentID, runID: run.ID}
	release, gateErr := s.PauseDispatch(ctx, agentID)
	if gateErr != nil {
		return executor.Result{Outcome: executor.OutcomeInterrupted}, ""
	}
	defer release()
	if st, err := s.st.RunStatus(ctx, run.ID); err == nil && st == "aborting" {
		return executor.Result{Outcome: executor.OutcomeInterrupted}, ""
	}
	prompt := msg.Text
	if run.WakeSleepID != nil && run.Origin != "sleep_wake" {
		prompt = "Platform notice: your sleep was ended by new work (sleepId=" + run.WakeSleepID.String() + ").\n" + prompt
	}
	// Pi command from the conversation page: /pi:<name> is rewritten verbatim to /<name> and sent to Pi, without the
	// run ID and platform notices (Pi only recognizes commands whose first character is /).
	command, piMessage := "", ""
	if run.Origin == "console" {
		command, piMessage = PiCommand(msg.Text)
	}
	images, imageErr := s.st.ImageContents(ctx, msg.Images)
	if imageErr != nil {
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: "The image content is not available."}, ""
	}
	trigger := run.UserMessageID
	if run.TriggerInboxID != nil {
		trigger = *run.TriggerInboxID
	}
	message := prompt
	if run.Origin != executor.OriginConsoleCompact {
		message = executor.RunIDLine(run.ID) + prompt
	}
	if command != "" {
		message = piMessage
	}
	// After a change of runtime, the new runtime gets the saved conversation with this message, in a block between the
	// run ID line and the message. A Pi command or a manual compaction goes to the runtime as it is, without it; the
	// handoff then waits for the next message.
	if pending, err := s.st.DispatchHandoff(ctx, agentID, run.ID); err != nil {
		s.log.Warn("failed to read the pending handoff; the message goes without it", "agentId", agentID, "runId", run.ID, "err", err)
	} else if pending != nil && run.Origin != executor.OriginConsoleCompact && command == "" {
		if block, record := s.handoffBlock(ctx, agentID, run.ID, pending); block != "" {
			message = executor.RunIDLine(run.ID) + block + "\n\n" + prompt
			sink.handoff = record
		}
	}
	res, err := s.ex.Execute(ctx, executor.Request{Images: images, AgentID: agentID, RunID: run.ID, Message: message,
		Origin: run.Origin, MessageID: trigger}, sink)
	if err != nil {
		if ctx.Err() != nil {
			return res, command
		}
		return executor.Result{Outcome: executor.OutcomeFailed, ErrorCode: executor.ErrInternal, ErrorMessage: err.Error()}, command
	}
	return res, command
}

// handoffBlock writes the saved conversation for a pending handoff, with the record of what it carries. It is empty
// when there is nothing saved to carry, and the handoff then ends.
func (s *Service) handoffBlock(ctx context.Context, agentID, runID uuid.UUID, pending *store.PendingHandoff) (string, *store.HandoffRecord) {
	a, err := s.st.GetAgent(ctx, agentID)
	if err != nil {
		s.log.Warn("failed to read the Agent for the handoff; the message goes without it", "agentId", agentID, "runId", runID, "err", err)
		return "", nil
	}
	entries, err := s.st.HandoffConversation(ctx, agentID, runID)
	if err != nil {
		s.log.Warn("failed to read the conversation for the handoff; the message goes without it", "agentId", agentID, "runId", runID, "err", err)
		return "", nil
	}
	if len(entries) == 0 {
		if err := s.st.EndHandoff(ctx, agentID, pending.ID); err != nil {
			s.log.Warn("failed to end an empty handoff", "agentId", agentID, "err", err)
		}
		return "", nil
	}
	messages := make([]contexthandoff.Message, 0, len(entries))
	for _, e := range entries {
		m := contexthandoff.Message{ID: e.ID, Role: e.Role, Origin: e.Origin, Status: e.RunStatus, Text: e.Text, Images: e.Images, CreatedAt: e.CreatedAt}
		for _, t := range e.Tools {
			m.Tools = append(m.Tools, contexthandoff.Tool{Name: t.Name, Calls: t.Calls, Failed: t.Failed})
		}
		messages = append(messages, m)
	}
	to := agentruntime.ForProvider(a.ModelProvider)
	from := pending.From
	if rt, ok := agentruntime.ByID(pending.From); ok {
		from = rt.Name
	}
	budget := contexthandoff.ClampBudget(s.o.HandoffBudget)
	block := contexthandoff.Build(messages, from, to.Name, budget)
	return block.Text, &store.HandoffRecord{ID: pending.ID, FromRuntime: pending.From, ToRuntime: to.ID, IncludedMessages: block.Included,
		OmittedMessages: block.Omitted, Images: block.Images, Bytes: block.Bytes, BudgetBytes: budget}
}

// finish writes the end of the run to the database and returns a function that announces the end (final event, missing
// session, waiting manual compaction request). The driver loop calls that function only after handing off the busy
// lock.
func (s *Service) finish(ctx context.Context, run store.Run, res executor.Result, command string) (publish func()) {
	end := store.RunEnd{Text: res.Text, ConfigVersion: res.ConfigVersion, PiCommand: command, PiCommandHandled: command != "" && res.CommandHandled}
	if end.PiCommandHandled {
		notices := res.CommandNotices
		if notices == nil {
			notices = []executor.CommandNotice{}
		}
		end.PiCommandNotices, _ = json.Marshal(map[string]any{"notices": notices, "truncated": res.CommandNoticesTruncated})
	}
	if c := res.Compaction; c != nil && res.Outcome == executor.OutcomeCompleted {
		if c.Skipped != "" {
			end.Compaction = &store.Compaction{Outcome: store.CompactionSkipped, SkipReason: c.Skipped}
		} else {
			before, after := c.TokensBefore, c.EstimatedTokensAfter
			end.Compaction = &store.Compaction{Outcome: store.CompactionCompacted, TokensBefore: &before, EstimatedTokensAfter: &after}
		}
	}
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
	ended, msg, finished := s.recordRunEnd(ctx, run.AgentID, run.ID, end)
	return func() {
		if finished {
			s.publishRunEnd(ctx, run.AgentID, ended, msg)
		}
		if res.ErrorCode == executor.ErrSessionMissing {
			s.emitSession(ctx, run.AgentID)
		}
		if value, ok := s.commands.Load(run.ID); ok {
			value.(*consoleCommandRun).done <- res
		}
		if h := testHookAfterRunEndPublished.Load(); h != nil {
			(*h)(run.ID)
		}
	}
}

// endRun ends a run whose lock is not held by a driver loop (server stop, cleanup after a restart): it records the end
// and announces it right away.
func (s *Service) endRun(ctx context.Context, agentID, runID uuid.UUID, end store.RunEnd) {
	if r, msg, finished := s.recordRunEnd(ctx, agentID, runID, end); finished {
		s.publishRunEnd(ctx, agentID, r, msg)
	}
}

// recordRunEnd writes the end of the run to the database; finished is false when this call did not end it (it had
// already ended, or the write failed).
func (s *Service) recordRunEnd(ctx context.Context, agentID, runID uuid.UUID, end store.RunEnd) (store.Run, *store.Message, bool) {
	r, msg, finished, err := s.st.FinishRun(ctx, runID, end)
	if errors.Is(err, store.ErrSleepSessionNotSaved) {
		end.Status = "failed"
		end.ErrorCode = executor.ErrInternal
		end.ErrorMessage = "Could not save the session record."
		r, msg, finished, err = s.st.FinishRun(ctx, runID, end)
	}
	if err != nil {
		s.log.Error("failed to end the run", "agentId", agentID, "runId", runID, "err", err)
		return store.Run{}, nil, false
	}
	if !finished {
		return store.Run{}, nil, false
	}
	attrs := []any{"runId", r.ID, "agentId", agentID, "status", r.Status, "endReason", r.EndReason, "errorCode", r.ErrorCode, "tokens", r.Usage.Total}
	if r.EndedAt != nil {
		begin := r.QueuedAt
		if r.StartedAt != nil {
			begin = *r.StartedAt
		}
		attrs = append(attrs, "durationMs", r.EndedAt.Sub(begin).Milliseconds())
	}
	s.log.Info("reply ended", attrs...)
	return r, msg, true
}

// publishRunEnd emits the run's final event and refreshes the overview status.
func (s *Service) publishRunEnd(ctx context.Context, agentID uuid.UUID, r store.Run, msg *store.Message) {
	if msg == nil {
		// A manual compaction has no conversation message: its final state is announced with run.updated, since
		// run.ended always carries a message.
		s.emit(ctx, agentID, apitypes.EventRunUpdated, apitypes.SseRunUpdated{Run: apitypes.FromRun(r)})
	} else {
		s.emit(ctx, agentID, apitypes.EventRunEnded, apitypes.SseRunEnded{Run: apitypes.FromRun(r), Message: apitypes.FromMessage(*msg, r)})
	}
	_ = s.co.DeleteRunText(ctx, r.ID.String())
	s.EmitAgentStatus(agentID)
}

// runSink writes executor events to the database and the event stream.
type runSink struct {
	s       *Service
	agentID uuid.UUID
	runID   uuid.UUID
	// handoff is what the message carried after a change of runtime; nil when it carried nothing.
	handoff *store.HandoffRecord
}

func (k *runSink) Phase(phase, runtime string) {
	ctx := k.s.ctx
	if err := k.s.st.SetRunPhase(ctx, k.runID, phase, runtime); err != nil {
		k.s.log.Warn("failed to update the run phase", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emitRun(ctx, k.agentID, k.runID)
}

func (k *runSink) Running(configVersion int64, sel executor.Selection) {
	ctx := k.s.ctx
	var start store.RunStart
	if sel.Runtime != "" {
		start.Selection = &store.RunSelection{Runtime: sel.Runtime, Provider: sel.Provider, ModelID: sel.ModelID, ThinkingLevel: sel.ThinkingLevel}
	}
	if k.handoff != nil && sel.Runtime != "" {
		carried := *k.handoff
		carried.ToRuntime = sel.Runtime // the runtime that actually took the message
		start.Handoff = &carried
	}
	if err := k.s.st.SetRunRunningWith(ctx, k.runID, configVersion, start); err != nil {
		k.s.log.Warn("failed to update the run status", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emitRun(ctx, k.agentID, k.runID)
}

func (k *runSink) TextDelta(delta string) {
	data, _ := json.Marshal(apitypes.SseRunText{RunID: k.runID, Delta: delta})
	if _, err := k.s.co.AppendText(k.s.ctx, k.agentID.String(), k.runID.String(), delta, apitypes.EventRunText, data); err != nil {
		k.s.log.Warn("failed to write the text delta", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
}

func (k *runSink) Tool(callID, name, state string) {
	ctx := k.s.ctx
	ev := store.ToolEvent{ToolCallID: callID, ToolName: name, State: state, At: time.Now()}
	if err := k.s.st.AddToolEvent(ctx, k.runID, ev); err != nil {
		k.s.log.Warn("failed to record the tool status", "runId", k.runID, "agentId", k.agentID, "err", err)
	}
	k.s.emit(ctx, k.agentID, apitypes.EventRunTool, apitypes.SseRunTool{RunID: k.runID, ToolCallID: callID, ToolName: name, State: state, At: ev.At})
}

// ---- Restart handling and reconciliation ----

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
		s.log.Warn("run left active by the previous server run; marked as interrupted by a server restart", "agentId", r.AgentID, "runId", r.ID)
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

// reconcile re-sets locks for in-flight runs and starts driver loops for queued messages that nobody is processing.
func (s *Service) reconcile(ctx context.Context) {
	active, err := s.st.ActiveRuns(ctx)
	if err != nil {
		s.log.Warn("reconcile: failed to read active runs", "err", err)
		return
	}
	for _, r := range active {
		s.restoreRunLock(ctx, r.AgentID, r)
	}
	agents, err := s.st.AgentsWithQueuedRuns(ctx)
	if err != nil {
		s.log.Warn("reconcile: failed to read queued runs", "err", err)
		return
	}
	for _, agentID := range agents {
		if !slices.ContainsFunc(active, func(r store.Run) bool { return r.AgentID == agentID }) {
			s.kick(ctx, agentID)
		}
	}
}

// ---- Events ----

func (s *Service) emit(ctx context.Context, agentID uuid.UUID, typ string, payload any) bool {
	data, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("failed to encode an event", "type", typ, "err", err)
		return false
	}
	if _, err := s.co.AppendEvent(ctx, agentID.String(), typ, data); err != nil {
		s.log.Warn("failed to write to the event stream", "agentId", agentID, "type", typ, "err", err)
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
		s.log.Warn("failed to read the run", "runId", runID, "err", err)
		return
	}
	s.emit(ctx, agentID, apitypes.EventRunUpdated, apitypes.SseRunUpdated{Run: apitypes.FromRun(r)})
	s.EmitAgentStatus(agentID)
}

// emitQueue pushes the queue list: all queued runs and the individual queue items (same shape and order as queuedRuns
// and queueItems in the snapshot).
func (s *Service) emitQueue(ctx context.Context, agentID uuid.UUID) {
	runs, items, err := s.queueState(ctx, agentID)
	if err != nil {
		s.log.Warn("failed to read the queue", "agentId", agentID, "err", err)
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
	s.publishSession(ctx, agentID, apitypes.SessionInfo{State: sess.State, Epoch: sess.Epoch}, true)
	s.EmitAgentStatus(agentID)
}

// sessionPush is the session.updated most recently pushed for an Agent, with the runtime the Agent had then.
type sessionPush struct {
	info    apitypes.SessionInfo
	runtime string
}

// publishSession pushes session.updated on the Agent's event stream; see publishSessionOn.
func (s *Service) publishSession(ctx context.Context, agentID uuid.UUID, info apitypes.SessionInfo, force bool) {
	s.publishSessionOn(ctx, agentID, info, "", force)
}

// publishSessionOn pushes session.updated on the Agent's event stream. When force is false it only pushes if the
// session state or epoch, or the Agent's runtime, differs from the last push: it is called on every recomputation of
// the overview status, so every session change gets a session.updated, including none becoming active after the first
// reply and a change of runtime (which keeps the session number; neither goes through a session command). An empty
// runtime keeps the one of the last push.
func (s *Service) publishSessionOn(ctx context.Context, agentID uuid.UUID, info apitypes.SessionInfo, runtime string, force bool) {
	last, ok := s.sessions.Load(agentID)
	prev, _ := last.(sessionPush)
	if runtime == "" {
		runtime = prev.runtime
	}
	if ok && !force && prev.info == info && prev.runtime == runtime {
		return
	}
	if s.emit(ctx, agentID, apitypes.EventSessionUpdated, apitypes.SseSessionUpdated{State: info.State, Epoch: info.Epoch}) {
		s.sessions.Store(agentID, sessionPush{info: info, runtime: runtime})
	}
}

// ---- Overview event stream ----

// overviewWorker records whether an Agent's overview push goroutine is running, and whether something changed again
// while it was running.
type overviewWorker struct{ running, dirty bool }

// EmitAgentStatus asynchronously pushes the Agent's current run status to the overview event stream (a removal event
// when the Agent has been deleted) without blocking the caller, so it does not lengthen the time between a run ending
// and the busy lock being handed off. There is at most one push goroutine per Agent: new changes while it runs are
// merged into one more push, so the last push always carries the latest status. Nothing is pushed after the service
// stops. Failures are only logged.
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
		// Deleted (including when its project was deleted): include the former ownership so scope-filtered overview
		// subscriptions can filter on it.
		pid, perr := s.st.AgentProjectAny(ctx, agentID)
		if perr != nil && !apperr.Is(perr, apperr.CodeNotFound) {
			s.log.Warn("failed to read the owner of a deleted Agent", "agentId", agentID, "err", perr)
		}
		s.appendOverview(ctx, apitypes.EventAgentRemoved, apitypes.SseAgentRemoved{AgentID: agentID, Scope: apitypes.ScopeOf(pid), ProjectID: pid})
		s.sessions.Delete(agentID)
		return
	}
	if err != nil {
		s.log.Warn("failed to compute the overview status", "agentId", agentID, "err", err)
		return
	}
	s.appendOverview(ctx, apitypes.EventAgentStatus, apitypes.SseAgentStatus{Agent: item})
	s.publishSessionOn(ctx, agentID, item.Status.Session, agentruntime.ForProvider(item.Model.Provider).ID, false)
}

func (s *Service) appendOverview(ctx context.Context, typ string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("failed to encode an overview event", "type", typ, "err", err)
		return
	}
	if _, err := s.co.AppendOverview(ctx, typ, data); err != nil {
		s.log.Warn("failed to write to the overview event stream", "type", typ, "err", err)
	}
}

// overviewItem returns the overview status of an Agent that has not been deleted.
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

// OverviewSnapshot returns the overview snapshot for the scope and its event ID. It takes the event stream's latest
// position first and then reads the status: later events may overlap with the snapshot (status events carry the full
// status, so duplicates are harmless), but none are missed.
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

// ---- Queries ----

// Status returns the Agent's status.
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

// Snapshot returns the SSE snapshot and its event ID: the text in the snapshot contains exactly the deltas up to that
// ID.
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

// History returns one page of conversation history.
func (s *Service) History(ctx context.Context, agentID uuid.UUID, q store.MessageQuery) (apitypes.MessagePage, error) {
	page, err := s.st.ListMessages(ctx, agentID, q)
	if err != nil {
		return apitypes.MessagePage{}, err
	}
	out := apitypes.MessagePage{Messages: make([]apitypes.Message, 0, len(page.Items)), HasMore: page.HasMore, HasMoreAfter: page.HasMoreAfter,
		TurnsBefore: page.TurnsBefore, TurnsTotal: page.TurnsTotal}
	for _, it := range page.Items {
		out.Messages = append(out.Messages, apitypes.FromMessage(it.Message, it.Run))
	}
	return out, nil
}

// ResetSession confirms reopening the session.
func (s *Service) ResetSession(ctx context.Context, agentID uuid.UUID) (apitypes.SessionInfo, error) {
	sess, err := s.st.ResetSession(ctx, agentID)
	if err != nil {
		return apitypes.SessionInfo{}, err
	}
	return s.sessionChanged(ctx, agentID, sess), nil // Inbox messages received while the session was lost got no reminder; add it after reopening
}

// PiCommand recognizes a Pi command from the conversation page: when the text starts with /pi:<name> (name non-empty,
// without whitespace) it returns the command name and /<name><rest of the original text> to send to Pi; otherwise both
// are empty. Only the leading prefix is rewritten; arguments are kept as they are.
func PiCommand(text string) (name, message string) {
	rest, ok := strings.CutPrefix(text, "/pi:")
	if !ok {
		return "", ""
	}
	end := strings.IndexFunc(rest, unicode.IsSpace)
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return "", ""
	}
	return rest[:end], "/" + rest
}
