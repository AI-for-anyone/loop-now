package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
	"loop-now/internal/server/executor/local/runturn"
)

// maintenanceErr marks expected failures in maintenance operations with executor's sentinel errors (the original error
// stays in the chain).
func maintenanceErr(err error) error {
	if errors.Is(err, ErrRuntimeBusy) {
		return fmt.Errorf("%w: %w", executor.ErrMaintenanceBusy, err)
	}
	return err
}

// compact runs a manual compaction inside the current real run, reusing the interrupt, event, usage and session record
// chain. The caller has checked that the driver is a compactor.
func (e *Executor) compact(ctx context.Context, p *proc, r *run, req executor.Request, snap agentconfig.Snapshot) executor.Result {
	runID := req.RunID
	result, err := p.drv.(compactor).compact(ctx, p, req.Message)
	if errors.Is(err, runturn.ErrExited) {
		e.discard(p, &runID)
	}
	sessionErr := e.turnFinished(p)
	res := executor.Result{ConfigVersion: snap.ConfigVersion}
	if r.interrupted.Load() {
		res.Outcome = executor.OutcomeInterrupted
		return res
	}
	if err != nil {
		res.Outcome = executor.OutcomeFailed
		res.ErrorCode, res.ErrorMessage = executor.ErrInternal, err.Error()
		var f *failure
		if errors.As(err, &f) {
			res.ErrorCode, res.ErrorMessage = f.code, f.msg
		}
		if errors.Is(err, runturn.ErrExited) {
			res.ErrorCode, _ = p.drv.exited()
			select {
			case <-p.rt.Done():
			case <-time.After(e.o.StopGrace):
			}
			res.ErrorMessage = strings.TrimSpace(p.drv.crashMessage(p, tail(p.rt.Stderr(), e.o.StderrTailBytes)))
		}
		return res
	}
	if sessionErr != nil {
		return failed(executor.ErrInternal, "Could not save the session record.", snap.ConfigVersion)
	}
	res.Outcome = executor.OutcomeCompleted
	res.Compaction = &result
	return res
}

// StopIdle stops an idle runtime that has no run; with no process it returns false without error. reason is the stop
// reason of the platform command (idleStopReasons).
func (e *Executor) StopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error) {
	if !slices.Contains(idleStopReasons, reason) {
		return false, fmt.Errorf("unknown stop reason %q", reason)
	}
	return e.stopIdle(ctx, agentID, reason)
}

// stopIdle stops the Agent's idle process under a maintenance reservation and waits for it to exit (shared by platform
// commands and task forks).
func (e *Executor) stopIdle(ctx context.Context, agentID uuid.UUID, reason string) (bool, error) {
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return false, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	if e.runs[agentID] != nil || p != nil && p.busy {
		e.mu.Unlock()
		return false, fmt.Errorf("%w: the regular turn has not fully finished", executor.ErrMaintenanceBusy)
	}
	delete(e.procs, agentID)
	e.mu.Unlock()
	if p == nil {
		return false, nil
	}
	e.stop(p, stopCause{reason: reason})
	select {
	case <-p.rt.Done():
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}

// ExportSession exports the Agent's current session through the driver of runtime (the Agent's runtime).
// sessionFile is the current session file recorded in the database. Exporting needs no run snapshot or run
// preparation, starts no runtime and does not touch a live idle process; the Agent must be idle. A runtime without
// session export returns executor.ErrUnsupportedRuntime.
func (e *Executor) ExportSession(ctx context.Context, agentID uuid.UUID, runtime, sessionFile, format string) (executor.SessionExport, error) {
	if format != "jsonl" && format != "html" {
		return executor.SessionExport{}, fmt.Errorf("unsupported export format %q", format)
	}
	exporter, ok := e.driverByID(runtime).(sessionExporter)
	if !ok {
		return executor.SessionExport{}, fmt.Errorf("%w: %s", executor.ErrUnsupportedRuntime, runtimeName(runtime))
	}
	free, err := e.reserveMaintenance(ctx, agentID)
	if err != nil {
		return executor.SessionExport{}, maintenanceErr(err)
	}
	defer free()
	e.mu.Lock()
	p := e.procs[agentID]
	busy := e.runs[agentID] != nil || p != nil && p.busy
	e.mu.Unlock()
	if busy {
		return executor.SessionExport{}, maintenanceErr(ErrRuntimeBusy)
	}
	return exporter.exportSession(ctx, agentID, sessionFile, format)
}

// ListSessions lists the Agent's sessions that can be resumed, through the driver of runtime. It only reads files: it
// takes no run snapshot, starts no process and does not need the Agent to be idle. A runtime without a session list
// returns executor.ErrUnsupportedRuntime.
func (e *Executor) ListSessions(ctx context.Context, agentID uuid.UUID, runtime, currentFile string) ([]executor.ManagedSession, error) {
	lister, ok := e.driverByID(runtime).(sessionLister)
	if !ok {
		return nil, fmt.Errorf("%w: %s", executor.ErrUnsupportedRuntime, runtimeName(runtime))
	}
	return lister.listSessions(ctx, agentID, currentFile)
}

// ResolveSession finds the session with the full session ID sessionID through the driver of runtime; it returns nil
// when there is none or the ID is ambiguous.
func (e *Executor) ResolveSession(ctx context.Context, agentID uuid.UUID, runtime, sessionID, currentFile string) (*executor.ManagedSession, error) {
	lister, ok := e.driverByID(runtime).(sessionLister)
	if !ok {
		return nil, fmt.Errorf("%w: %s", executor.ErrUnsupportedRuntime, runtimeName(runtime))
	}
	return lister.resolveSession(ctx, agentID, sessionID, currentFile)
}

// ForkBaseline reads the regular session sessionFile of the Agent agentID for a task fork, through the driver of runtime
// (the Agent's runtime), and returns what the task instance's baseline file holds; it reads at most maxBytes. A runtime
// whose task instances cannot fork returns executor.ErrUnsupportedRuntime.
func (e *Executor) ForkBaseline(ctx context.Context, runtime string, agentID uuid.UUID, sessionFile string, maxBytes int64) ([]byte, error) {
	forker, ok := e.driverByID(runtime).(taskForker)
	if !ok {
		return nil, fmt.Errorf("%w: %s", executor.ErrUnsupportedRuntime, runtimeName(runtime))
	}
	return forker.forkBaseline(ctx, agentID, sessionFile, maxBytes)
}

// commandsFile is the runtime's own command list read the last time the Agent started a runtime with native commands
// (for Pi, RPC get_commands), kept in the Agent directory for the "/" menu on the conversation page; the platform never
// starts a runtime just to list commands. The name is kept for existing data.
const commandsFile = "pi-commands.json"

// cacheCommands asks the runtime that just started for its commands and writes them into the Agent directory; a
// failure is only logged and does not affect the run.
func (e *Executor) cacheCommands(p *proc, nc nativeCommander) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commands, err := nc.commands(ctx, p)
	if err != nil {
		p.log.Warn("failed to read the "+p.drv.name()+" command list", "err", err)
		return
	}
	out := executor.PiCommands{CapturedAt: time.Now().UTC(), Commands: commands}
	b, err := json.Marshal(out)
	if err != nil {
		return
	}
	path := filepath.Join(p.dirs.root, commandsFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		p.log.Warn("failed to save the "+p.drv.name()+" command list", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		p.log.Warn("failed to save the "+p.drv.name()+" command list", "err", err)
	}
}

// Commands returns the commands read the last time the Agent started a runtime with native commands; nil when none has
// started yet (or they have not been read yet).
func (e *Executor) Commands(agentID uuid.UUID) (*executor.PiCommands, error) {
	b, err := os.ReadFile(filepath.Join(e.dirs(agentID).root, commandsFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out executor.PiCommands
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, nil // half-written or old format: treat as none
	}
	return &out, nil
}
