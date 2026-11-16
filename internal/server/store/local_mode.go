package store

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
)

// LocalModeSettings are the four global switches of "本机模式" ("Local mode") in the personal edition.
type LocalModeSettings struct {
	Directories bool `json:"directories"` // bind local directories
	Git         bool `json:"git"`         // "从 Git 拉取" ("Pull from Git") uses the local git identity
	CLI         bool `json:"cli"`         // append the user's PATH after the run PATH
	Logins      bool `json:"logins"`      // Pi's run environment keeps the user's real HOME (off by default)
}

// LocalModeSettingsPatch changes only the non-nil fields.
type LocalModeSettingsPatch struct {
	Directories *bool
	Git         *bool
	CLI         *bool
	Logins      *bool
}

// LocalDirectoryEntry is one item in a local directory list; an empty note means there is no note.
type LocalDirectoryEntry struct {
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

// LocalDirectoryList is a local directory list; when it has never been saved, Revision is 0 and the list is empty.
type LocalDirectoryList struct {
	Revision    int64
	Directories []LocalDirectoryEntry
}

// GetLocalModeSettings reads the local mode switches.
func (s *Store) GetLocalModeSettings(ctx context.Context) (LocalModeSettings, error) {
	var out LocalModeSettings
	err := s.Pool.QueryRow(ctx, `SELECT directories, git, cli, logins FROM local_mode_settings WHERE singleton`).Scan(&out.Directories, &out.Git, &out.CLI, &out.Logins)
	return out, err
}

// UpdateLocalModeSettings changes the switches given in p and returns all switches after the change.
func (s *Store) UpdateLocalModeSettings(ctx context.Context, p LocalModeSettingsPatch) (LocalModeSettings, error) {
	var out LocalModeSettings
	err := s.Pool.QueryRow(ctx, `UPDATE local_mode_settings SET directories = coalesce($1, directories), git = coalesce($2, git), cli = coalesce($3, cli), logins = coalesce($4, logins), updated_at = now()
		WHERE singleton RETURNING directories, git, cli, logins`, p.Directories, p.Git, p.CLI, p.Logins).Scan(&out.Directories, &out.Git, &out.CLI, &out.Logins)
	return out, err
}

// localDirectoryOwner is the table and primary key column that hold the list.
type localDirectoryOwner struct{ table, column string }

var (
	projectLocalDirectories = localDirectoryOwner{"project_local_directories", "project_id"}
	agentLocalDirectories   = localDirectoryOwner{"agent_local_directories", "agent_id"}
)

// ProjectLocalDirectories reads the project's local directory list (without checking that the project exists).
func (s *Store) ProjectLocalDirectories(ctx context.Context, projectID uuid.UUID) (LocalDirectoryList, error) {
	return s.localDirectories(ctx, projectLocalDirectories, projectID)
}

// AgentLocalDirectories reads the Agent's own local directory list (without checking that the Agent exists).
func (s *Store) AgentLocalDirectories(ctx context.Context, agentID uuid.UUID) (LocalDirectoryList, error) {
	return s.localDirectories(ctx, agentLocalDirectories, agentID)
}

// PutProjectLocalDirectories replaces the project's list as a whole while the revision is still expected; otherwise
// returns 412.
func (s *Store) PutProjectLocalDirectories(ctx context.Context, projectID uuid.UUID, expected int64, dirs []LocalDirectoryEntry) (LocalDirectoryList, error) {
	return s.putLocalDirectories(ctx, projectLocalDirectories, projectID, expected, dirs)
}

// PutAgentLocalDirectories replaces the Agent's own list as a whole while the revision is still expected; otherwise
// returns 412.
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

// AgentProjectID returns the project the Agent belongs to (including deleted Agents, whose configuration a task may
// still be using); nil for a platform Agent.
func (s *Store) AgentProjectID(ctx context.Context, agentID uuid.UUID) (*uuid.UUID, error) {
	var projectID *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT project_id FROM agents WHERE id = $1`, agentID).Scan(&projectID)
	return projectID, notFoundIfNoRows(err, "Agent")
}
