package store

import (
	"context"
	cm "loop-now/internal/server/credentialmodel"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/modelcredential"
	"loop-now/internal/server/prompts"
	"loop-now/internal/server/taskruntime"
)

type TaskCapture struct {
	ModelAuthManaged bool
	ModelAuth        *modelcredential.Binding
	Agent            Agent
	Configuration    taskruntime.Configuration
	CredentialRows   []CredentialRow
	Sources          []TaskFrozenSource
}

// CaptureTaskAgent 在调用者创建事务中锁住Agent，配置变更同样锁Agent，因此本次读取不混版本。
func (s *Store) CaptureTaskAgent(ctx context.Context, tx pgx.Tx, agentID, runtimeID uuid.UUID, project prompts.ProjectRules, credentialRevision ...int64) (TaskCapture, error) {
	var out TaskCapture
	var err error
	out.Agent, err = scanAgent(tx.QueryRow(ctx, `SELECT `+agentColumns+` FROM agents WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, agentID))
	if err != nil {
		return out, notFoundIfNoRows(err, "Agent")
	}
	if out.Agent.ProjectID == nil || *out.Agent.ProjectID != project.ID {
		return out, apperr.Invalid("workflow.steps.agentId", "执行Agent必须属于本项目")
	}
	out.Configuration = taskruntime.Configuration{AgentID: agentID, ConfigVersion: out.Agent.ConfigVersion, Provider: out.Agent.ModelProvider, ModelID: out.Agent.ModelID, Prompt: out.Agent.Prompt, Prompts: &prompts.Layers{Format: prompts.FormatV1, ProjectID: &project.ID, ProjectPrompt: project.Prompt, ProjectVersion: project.Version, AgentPrompt: out.Agent.Prompt, AgentVersion: out.Agent.PromptVersion}, Skills: []taskruntime.Skill{}, CLIs: []string{}}
	rows, err := tx.Query(ctx, `SELECT cli_name FROM agent_clis WHERE agent_id=$1 ORDER BY cli_name`, agentID)
	if err != nil {
		return out, err
	}
	out.Configuration.CLIs, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return out, err
	}
	rows, err = tx.Query(ctx, `SELECT sk.name,f.path,b.data FROM agent_skills sk JOIN agent_skill_files f ON f.agent_skill_id=sk.id JOIN blobs b ON b.sha256=f.blob_sha256 WHERE sk.agent_id=$1 ORDER BY sk.name,f.path`, agentID)
	if err != nil {
		return out, err
	}
	skillIndex := map[string]int{}
	for rows.Next() {
		var name, path string
		var content []byte
		if err = rows.Scan(&name, &path, &content); err != nil {
			rows.Close()
			return out, err
		}
		idx, ok := skillIndex[name]
		if !ok {
			idx = len(out.Configuration.Skills)
			skillIndex[name] = idx
			out.Configuration.Skills = append(out.Configuration.Skills, taskruntime.Skill{Name: name, Files: []taskruntime.File{}})
		}
		out.Configuration.Skills[idx].Files = append(out.Configuration.Skills[idx].Files, taskruntime.File{Path: path, Content: content})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.ModelAuthManaged = true
	var resolved []cm.Selection
	out.ModelAuth, out.CredentialRows, resolved, err = captureProjectCredentialsTx(ctx, tx, out.Agent, out.Configuration.CLIs)
	if err != nil {
		return out, err
	}
	if len(credentialRevision) > 0 {
		out.Configuration.CredentialsRevision = credentialRevision[0]
	} else {
		out.Configuration.CredentialsRevision, err = s.ProjectCredentialRevisionTx(ctx, tx, project.ID)
		if err != nil {
			return out, err
		}
	}
	out.Configuration.CredentialIdentity = projectCredentialIdentity(out.Configuration.CLIs, resolved, out.CredentialRows)
	// 快照 v9 起任务工作区复制自基础 Agent 的文件夹，不再冻结文件夹来源。
	out.Sources = []TaskFrozenSource{}
	return out, nil
}

func (s *Store) RecordTaskSession(ctx context.Context, runtimeID uuid.UUID, file string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE task_instances SET session_file=$2,session_state='active' WHERE id=$1 AND retired=false`, runtimeID, file)
	return err
}
func (s *Store) MarkTaskSessionMissing(ctx context.Context, runtimeID uuid.UUID) error {
	_, err := s.Pool.Exec(ctx, `UPDATE task_instances SET session_state='missing' WHERE id=$1`, runtimeID)
	return err
}

func (s *Store) FrozenTaskSource(ctx context.Context, sourceID uuid.UUID) (TaskFrozenSource, uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,sources FROM task_instances WHERE sources @> $1::jsonb`, []byte(`[{"Source":{"ID":"`+sourceID.String()+`"}}]`))
	if err != nil {
		return TaskFrozenSource{}, uuid.Nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return TaskFrozenSource{}, uuid.Nil, err
		}
		var list []TaskFrozenSource
		if err = decodeTaskJSON(raw, &list); err != nil {
			return TaskFrozenSource{}, uuid.Nil, err
		}
		for _, src := range list {
			if src.Source.ID == sourceID {
				return src, id, nil
			}
		}
	}
	return TaskFrozenSource{}, uuid.Nil, notFoundIfNoRows(pgx.ErrNoRows, "任务来源")
}
func (s *Store) MarkTaskSourceSynced(ctx context.Context, runtimeID, sourceID uuid.UUID, files []SyncFile) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT sources FROM task_instances WHERE id=$1 FOR UPDATE`, runtimeID).Scan(&raw); err != nil {
			return err
		}
		var list []TaskFrozenSource
		if err := decodeTaskJSON(raw, &list); err != nil {
			return err
		}
		for i := range list {
			if list[i].Source.ID != sourceID {
				continue
			}
			now := time.Now().UTC()
			list[i].Source.SyncedAt = &now
			for j := range list[i].Files {
				list[i].Files[j].Pending = "none"
			}
		}
		b, err := encodeTaskJSON(list)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE task_instances SET sources=$2 WHERE id=$1`, runtimeID, b)
		return err
	})
}

// TaskRuntimeActive is a defensive source guard; terminal cleanup timing must
// never make a completed/cancelled runtime eligible to execute again.
func (s *Store) TaskRuntimeActive(ctx context.Context, runtime uuid.UUID) (bool, error) {
	var active bool
	e := s.Pool.QueryRow(ctx, `SELECT t.ended_at IS NULL FROM task_instances i JOIN task_records t ON t.id=i.task_id WHERE i.id=$1`, runtime).Scan(&active)
	return active, notFoundIfNoRows(e, "任务实例")
}
