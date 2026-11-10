package store

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
)

// LocalModeSettings 是个人版“本机模式”的三个全局开关。
type LocalModeSettings struct {
	Directories bool `json:"directories"` // 绑定本机目录
	Git         bool `json:"git"`         // “从 Git 拉取”用本机的 git 身份
	CLI         bool `json:"cli"`         // 执行 PATH 之后接上用户的 PATH
	Logins      bool `json:"logins"`      // Pi 的执行环境沿用用户真实的 HOME（默认关闭）
}

// LocalModeSettingsPatch 只改非空的字段。
type LocalModeSettingsPatch struct {
	Directories *bool
	Git         *bool
	CLI         *bool
	Logins      *bool
}

// LocalDirectoryEntry 是本机目录清单的一项；备注为空表示没有备注。
type LocalDirectoryEntry struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

// LocalDirectoryList 是一份本机目录清单；从未保存过时 Revision 为 0、清单为空。
type LocalDirectoryList struct {
	Revision    int64
	Directories []LocalDirectoryEntry
}

// GetLocalModeSettings 读取本机模式开关。
func (s *Store) GetLocalModeSettings(ctx context.Context) (LocalModeSettings, error) {
	var out LocalModeSettings
	err := s.Pool.QueryRow(ctx, `SELECT directories, git, cli, logins FROM local_mode_settings WHERE singleton`).Scan(&out.Directories, &out.Git, &out.CLI, &out.Logins)
	return out, err
}

// UpdateLocalModeSettings 修改 p 中给出的开关，返回修改后的全部开关。
func (s *Store) UpdateLocalModeSettings(ctx context.Context, p LocalModeSettingsPatch) (LocalModeSettings, error) {
	var out LocalModeSettings
	err := s.Pool.QueryRow(ctx, `UPDATE local_mode_settings SET directories = coalesce($1, directories), git = coalesce($2, git), cli = coalesce($3, cli), logins = coalesce($4, logins), updated_at = now()
		WHERE singleton RETURNING directories, git, cli, logins`, p.Directories, p.Git, p.CLI, p.Logins).Scan(&out.Directories, &out.Git, &out.CLI, &out.Logins)
	return out, err
}

// localDirectoryOwner 是清单所在的表与主键列。
type localDirectoryOwner struct{ table, column string }

var (
	projectLocalDirectories = localDirectoryOwner{"project_local_directories", "project_id"}
	agentLocalDirectories   = localDirectoryOwner{"agent_local_directories", "agent_id"}
)

// ProjectLocalDirectories 读取项目的本机目录清单（不检查项目是否存在）。
func (s *Store) ProjectLocalDirectories(ctx context.Context, projectID uuid.UUID) (LocalDirectoryList, error) {
	return s.localDirectories(ctx, projectLocalDirectories, projectID)
}

// AgentLocalDirectories 读取 Agent 自己的本机目录清单（不检查 Agent 是否存在）。
func (s *Store) AgentLocalDirectories(ctx context.Context, agentID uuid.UUID) (LocalDirectoryList, error) {
	return s.localDirectories(ctx, agentLocalDirectories, agentID)
}

// PutProjectLocalDirectories 在修订号仍为 expected 时整体替换项目的清单；否则返回 412。
func (s *Store) PutProjectLocalDirectories(ctx context.Context, projectID uuid.UUID, expected int64, dirs []LocalDirectoryEntry) (LocalDirectoryList, error) {
	return s.putLocalDirectories(ctx, projectLocalDirectories, projectID, expected, dirs)
}

// PutAgentLocalDirectories 在修订号仍为 expected 时整体替换 Agent 自己的清单；否则返回 412。
func (s *Store) PutAgentLocalDirectories(ctx context.Context, agentID uuid.UUID, expected int64, dirs []LocalDirectoryEntry) (LocalDirectoryList, error) {
	return s.putLocalDirectories(ctx, agentLocalDirectories, agentID, expected, dirs)
}

func (s *Store) localDirectories(ctx context.Context, o localDirectoryOwner, id uuid.UUID) (LocalDirectoryList, error) {
	out := LocalDirectoryList{Directories: []LocalDirectoryEntry{}}
	err := s.Pool.QueryRow(ctx, `SELECT revision, directories FROM `+o.table+` WHERE `+o.column+` = $1`, id).Scan(&out.Revision, &out.Directories)
	if IsNotFound(err) {
		return LocalDirectoryList{Directories: []LocalDirectoryEntry{}}, nil
	}
	return out, err
}

func (s *Store) putLocalDirectories(ctx context.Context, o localDirectoryOwner, id uuid.UUID, expected int64, dirs []LocalDirectoryEntry) (LocalDirectoryList, error) {
	if expected < 0 {
		return LocalDirectoryList{}, apperr.Invalid("expectedRevision", "必须是不小于 0 的整数")
	}
	if dirs == nil {
		dirs = []LocalDirectoryEntry{}
	}
	out := LocalDirectoryList{Directories: []LocalDirectoryEntry{}}
	var err error
	if expected == 0 {
		err = s.Pool.QueryRow(ctx, `INSERT INTO `+o.table+` (`+o.column+`, directories) VALUES ($1, $2)
			ON CONFLICT (`+o.column+`) DO NOTHING RETURNING revision, directories`, id, dirs).Scan(&out.Revision, &out.Directories)
	} else {
		err = s.Pool.QueryRow(ctx, `UPDATE `+o.table+` SET directories = $2, revision = revision + 1, updated_at = now()
			WHERE `+o.column+` = $1 AND revision = $3 RETURNING revision, directories`, id, dirs, expected).Scan(&out.Revision, &out.Directories)
	}
	if IsNotFound(err) {
		return LocalDirectoryList{}, apperr.PreconditionFailed("本机目录清单已被修改，请重新读取后再保存")
	}
	return out, err
}

// AgentProjectID 返回 Agent（含已删除的，任务可能仍在用它的配置）所属的项目；平台 Agent 为 nil。
func (s *Store) AgentProjectID(ctx context.Context, agentID uuid.UUID) (*uuid.UUID, error) {
	var projectID *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, agentID).Scan(&projectID)
	return projectID, notFoundIfNoRows(err, "Agent")
}
