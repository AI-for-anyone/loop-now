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
	"loop-now/internal/server/store"
)

// cleanup 先持久化每个实例的完整会话，再回收目录。归档失败保留所有目录供重试。
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
		return errors.New("任务归档器未配置")
	}
	instances, err := s.Store.TaskInstances(ctx, id)
	if err != nil {
		return err
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
		data, e := s.Source.archiveSession(inst)
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
	// 所有会话已落盘，之后任一步失败均可用数据库归档重试。
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
			return nil, errors.New("只能清理结束的任务")
		}
		m.Record.View.Cleanup = "archived"
		return nil, nil
	})
	return err
}
func (s *Source) archiveSession(inst store.TaskInstance) ([]byte, error) {
	if inst.SessionState == "missing" {
		return nil, errors.New("任务会话丢失，保留目录等待修复")
	}
	var data []byte
	if inst.SessionFile != nil {
		base := filepath.Join(s.WorkRoot, "agents", inst.ID.String(), "sessions")
		rel, err := filepath.Rel(base, *inst.SessionFile)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, errors.New("任务会话不在该实例目录")
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		info, err := root.Lstat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("任务会话不是普通文件")
		}
		f, err := root.Open(rel)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, s.MaxSessionBytes+1))
		if err != nil || int64(len(data)) > s.MaxSessionBytes {
			return nil, errors.New("任务会话读取失败或归档超限")
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, errors.New("任务会话为空")
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if !json.Valid([]byte(line)) {
				return nil, errors.New("任务会话含不完整记录")
			}
		}
	}
	return json.Marshal(struct {
		RuntimeID uuid.UUID `json:"runtimeId"`
		State     string    `json:"state"`
		Session   []byte    `json:"session"`
	}{inst.ID, inst.SessionState, data})
}
