package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/claudecli"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskruntime"
)

// cleanup first persists each instance's full session and then reclaims the directories. When archiving fails, all
// directories are kept for a retry.
func (s *Service) cleanup(ctx context.Context, id uuid.UUID) (err error) {
	defer func() {
		if err != nil && ctx.Err() == nil {
			_, _ = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
				if m.Record.View.Cleanup == "failed" {
					m.Unchanged()
					return nil, nil
				}
				m.Record.View.Cleanup = "failed"
				return nil, nil
			})
		}
	}()
	if s.Source == nil || s.Source.FreezeNormal == nil {
		return errors.New("the task archiver is not configured")
	}
	instances, err := s.Store.TaskInstances(ctx, id)
	if err != nil {
		return err
	}
	record, err := s.Store.GetTask(ctx, id)
	if err != nil {
		return err
	}
	worktree := ""
	if w := record.View.Workspace; w != nil && within(s.Source.WorktreeRoot(), w.Path) {
		worktree = w.Path
	}
	for _, inst := range instances {
		if inst.Retired {
			continue
		}
		if err = s.Source.FreezeNormal(ctx, inst.ID); err != nil {
			return err
		}
		rows, e := s.Store.Pool.Query(ctx, `SELECT id FROM task_turns WHERE runtime_id=$1 AND status IN ('starting','running','aborting')`, inst.ID)
		if e != nil {
			return e
		}
		turns := []uuid.UUID{}
		for rows.Next() {
			var turn uuid.UUID
			if e = rows.Scan(&turn); e != nil {
				rows.Close()
				return e
			}
			turns = append(turns, turn)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, turn := range turns {
			old, e := s.Store.TaskTurn(ctx, turn)
			if e != nil {
				return e
			}
			if _, e = s.Store.UpdateTaskTurn(ctx, id, turn, "interrupted", old.Reply, nil, nil); e != nil {
				return e
			}
		}

		var exists bool
		if err = s.Store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM task_archives WHERE runtime_id=$1)`, inst.ID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		data, e := s.Source.archiveSession(ctx, inst, worktree)
		if e != nil {
			return e
		}
		sha, e := s.Store.PutBlob(ctx, data)
		if e != nil {
			return e
		}
		_, e = s.Store.Pool.Exec(ctx, `INSERT INTO task_archives(runtime_id,blob_sha256) VALUES($1,$2) ON CONFLICT DO NOTHING`, inst.ID, sha)
		if e != nil {
			return e
		}
	}
	// All sessions are on disk now; if any later step fails, it can be retried from the database archive.
	for _, inst := range instances {
		if inst.Retired {
			continue
		}
		if err = s.Executor.Release(ctx, inst.ID); err != nil {
			return err
		}
		root, err := os.OpenRoot(s.Source.WorkRoot)
		if err != nil {
			return err
		}
		err = root.RemoveAll(filepath.Join("agents", inst.ID.String()))
		root.Close()
		if err != nil {
			return err
		}
		_, err = s.Store.Pool.Exec(ctx, `UPDATE task_instances SET retired=true WHERE id=$1`, inst.ID)
		if err != nil {
			return err
		}
		_, err = s.Store.Pool.Exec(ctx, `UPDATE agent_tokens SET revoked_at=coalesce(revoked_at,now()) WHERE runtime_id=$1`, inst.ID)
		if err != nil {
			return err
		}
	}
	// Every step has been released: what Claude Code keeps for the worktree (the steps' session records, archived
	// above) belongs to this task alone.
	if worktree != "" && s.Source.PurgeClaudeFolder != nil {
		if e := s.Source.PurgeClaudeFolder(ctx, worktree); e != nil && s.Log != nil {
			s.Log.Warn("could not clean up Claude Code's data for the task's worktree", "task_id", id, "error", e)
		}
	}
	if err = s.cleanupWorkspace(ctx, id); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.Source.WorkRoot)
	if err != nil {
		return err
	}
	err = root.RemoveAll(filepath.Join("task-baselines", id.String()))
	root.Close()
	if err != nil {
		return err
	}
	_, err = s.Store.MutateTask(ctx, id, nil, func(m *store.TaskMutation) (any, error) {
		if !m.Record.Ended() {
			return nil, errors.New("only ended tasks can be cleaned up")
		}
		m.Record.View.Cleanup = "archived"
		return nil, nil
	})
	return err
}

// archiveSession reads a task instance's session for archiving: Pi's is in the instance's own sessions directory;
// Claude Code's is the one <session ID>.jsonl that claude left for this instance's workspace, or for the task's
// worktree (only the instance's own recorded file in the subdirectory of claude's session record directory that
// corresponds to that folder is accepted; the task's other steps keep theirs beside it). A runtime
// that keeps its sessions itself (Codex) is not read: the archive records its runtime and session reference, and the
// runtime keeps the session (the executor archives it there when the instance is released).
func (s *Source) archiveSession(ctx context.Context, inst store.TaskInstance, worktree string) ([]byte, error) {
	if inst.SessionState == "missing" {
		return nil, errors.New("the task session is missing; the directory is kept for repair")
	}
	if runtime := s.keptSessionRuntime(inst); runtime != "" {
		return json.Marshal(struct {
			RuntimeID  uuid.UUID `json:"runtimeId"`
			State      string    `json:"state"`
			Runtime    string    `json:"runtime"`
			SessionRef string    `json:"sessionRef"`
			Session    []byte    `json:"session"`
		}{inst.ID, inst.SessionState, runtime, *inst.SessionFile, nil})
	}
	var data []byte
	if inst.SessionFile != nil {
		base := filepath.Join(s.WorkRoot, "agents", inst.ID.String(), "sessions")
		rel, err := filepath.Rel(base, *inst.SessionFile)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			base, rel = s.claudeInstanceSession(ctx, inst, *inst.SessionFile, worktree)
			if base == "" {
				return nil, errors.New("the task session is not in the instance's directory")
			}
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("the task session is not a regular file")
		}
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, s.MaxSessionBytes+1))
		if err != nil || int64(len(data)) > s.MaxSessionBytes {
			return nil, errors.New("the task session cannot be read or exceeds the archive size limit")
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, errors.New("the task session is empty")
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if !json.Valid([]byte(line)) {
				return nil, errors.New("the task session contains incomplete records")
			}
		}
	}
	return json.Marshal(struct {
		RuntimeID uuid.UUID `json:"runtimeId"`
		State     string    `json:"state"`
		Session   []byte    `json:"session"`
	}{inst.ID, inst.SessionState, data})
}

// keptSessionRuntime returns the instance's runtime when that runtime keeps its sessions itself and the instance has a
// session; otherwise empty.
func (s *Source) keptSessionRuntime(inst store.TaskInstance) string {
	if inst.SessionFile == nil || s.KeepsSessions == nil {
		return ""
	}
	snap, err := taskruntime.Decode(inst.Snapshot)
	if err != nil {
		return ""
	}
	runtime := agentruntime.ForProvider(snap.Configuration().Provider).ID
	if !s.KeepsSessions(runtime) {
		return ""
	}
	return runtime
}

// claudeInstanceSession decides whether file is a session record claude left for this task instance's workspace, or
// for the task's worktree (worktree, empty when there is none): if so it returns the session record directory and the
// relative path, otherwise both are empty. file is the instance's own recorded session, never another step's.
func (s *Source) claudeInstanceSession(ctx context.Context, inst store.TaskInstance, file, worktree string) (string, string) {
	if s.ClaudeProjects == nil {
		return "", ""
	}
	projects := s.ClaudeProjects(ctx)
	if projects == "" || !claudecli.InProjects(projects, file) {
		return "", ""
	}
	if inst.SessionFile == nil || *inst.SessionFile != file {
		return "", ""
	}
	folders := []string{filepath.Join(s.WorkRoot, "agents", inst.ID.String(), "workspace")}
	if worktree != "" {
		folders = append(folders, worktree)
	}
	owned := false
	for _, folder := range folders {
		for _, name := range claudecli.ProjectDirNames(folder) {
			owned = owned || filepath.Dir(file) == filepath.Join(projects, name)
		}
	}
	if !owned {
		return "", ""
	}
	rel, err := filepath.Rel(projects, file)
	if err != nil {
		return "", ""
	}
	return projects, rel
}
