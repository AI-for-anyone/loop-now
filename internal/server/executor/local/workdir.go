package local

import (
	"context"
	"os"
	"slices"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// workDir is where the Agent's CLI works: the task's worktree for a step of a task that has one (every runtime); the
// working folder a regular Agent was given, when its runtime can work in a folder the person picks; otherwise, and for
// a task step without a worktree (which keeps its own copy of the Agent's folder), the Agent's own folder.
func workDir(snap agentconfig.Snapshot, d agentDirs) string {
	if snap.TaskWorkspace != "" && snap.TaskID != uuid.Nil {
		return snap.TaskWorkspace
	}
	if snap.WorkingDirectory != "" && snap.TaskID == uuid.Nil && capabilities(snap).WorkingDirectory {
		return snap.WorkingDirectory
	}
	return d.workspace
}

// localConfig reports whether the runtime loads the person's own CLI configuration, as in their terminal: an Agent
// that chose it, on a runtime that can, in its regular conversation or in a step of a task with a worktree (other task
// steps keep today's limits).
func localConfig(snap agentconfig.Snapshot) bool {
	return snap.UseLocalConfig && (snap.TaskID == uuid.Nil || snap.TaskWorkspace != "") && capabilities(snap).LocalConfig
}

// checkWorkDir fails a start whose working folder is not a folder on this computer any more.
func checkWorkDir(snap agentconfig.Snapshot, d agentDirs) error {
	dir := workDir(snap, d)
	if dir == d.workspace {
		return nil
	}
	if info, err := os.Stat(dir); (err != nil || !info.IsDir()) && dir == snap.TaskWorkspace {
		return fail(executor.ErrRuntimeStartFailed, "The task's worktree %s is not a folder on this computer any more.", dir)
	} else if err != nil || !info.IsDir() {
		return fail(executor.ErrRuntimeStartFailed, "The Agent's working folder %s is not a folder on this computer any more. Pick another one on the Agent's settings page.", dir)
	}
	return nil
}

// permissionMode is the permission mode the runtime runs in: the Agent's when the runtime offers it, else supervised
// when it can ask, else full (Pi). An empty mode (a task snapshot from before modes) is full.
func permissionMode(snap agentconfig.Snapshot) string {
	mode := snap.PermissionMode
	if mode == "" {
		return executor.ModeFull
	}
	offered := capabilities(snap).PermissionModes
	switch {
	case slices.Contains(offered, mode):
		return mode
	case slices.Contains(offered, executor.ModeSupervised):
		return executor.ModeSupervised
	}
	return executor.ModeFull
}

// askPermission asks a person about something p's runtime wants to do, for the run p is replying to; it waits until
// someone answers or ctx ends (the request is then cancelled and the answer is a rejection).
func (e *Executor) askPermission(ctx context.Context, p *proc, kind, title, detail string) executor.PermissionAnswer {
	run := p.curRun.Load()
	if e.o.Permissions == nil || run == nil {
		return executor.PermissionAnswer{Reason: "No one can answer permission requests for this reply."}
	}
	return e.o.Permissions.AskPermission(ctx, executor.PermissionAsk{AgentID: p.agentID, RunID: *run, Runtime: p.drv.runtime(),
		Kind: kind, Title: title, Detail: executor.ClipDetail(detail)})
}
