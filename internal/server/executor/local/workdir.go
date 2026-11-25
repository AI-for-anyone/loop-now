package local

import (
	"os"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/executor"
)

// workDir is where the Agent's CLI works: the working folder a regular Agent was given, when its runtime can work in a
// folder the person picks; otherwise, and always for a task step (which keeps its own copy of the Agent's folder), the
// Agent's own folder.
func workDir(snap agentconfig.Snapshot, d agentDirs) string {
	if snap.WorkingDirectory != "" && snap.TaskID == uuid.Nil && capabilities(snap).WorkingDirectory {
		return snap.WorkingDirectory
	}
	return d.workspace
}

// localConfig reports whether the runtime loads the person's own CLI configuration, as in their terminal: a regular
// Agent that chose it, on a runtime that can (task steps keep today's limits).
func localConfig(snap agentconfig.Snapshot) bool {
	return snap.UseLocalConfig && snap.TaskID == uuid.Nil && capabilities(snap).LocalConfig
}

// checkWorkDir fails a start whose working folder is not a folder on this computer any more.
func checkWorkDir(snap agentconfig.Snapshot, d agentDirs) error {
	dir := workDir(snap, d)
	if dir == d.workspace {
		return nil
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fail(executor.ErrRuntimeStartFailed, "The Agent's working folder %s is not a folder on this computer any more. Pick another one on the Agent's settings page.", dir)
	}
	return nil
}
