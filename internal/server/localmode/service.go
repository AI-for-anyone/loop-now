package localmode

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Service 读写本机模式的开关与本机目录清单，并给出启动 Pi 时生效的部分。
type Service struct {
	Store *store.Store
}

// DirectoryView 是读回的一项，带服务端此刻 stat 的状态。
type DirectoryView struct {
	Path   string  `json:"path"`
	Note   *string `json:"note"`
	Status string  `json:"status"`
}

// ProjectDirectories 是项目的清单（api/openapi.yaml 的 ProjectLocalDirectories）。
type ProjectDirectories struct {
	Revision    int64           `json:"revision"`
	Directories []DirectoryView `json:"directories"`
}

// AgentDirectories 是 Agent 自己的清单与继承自项目的条目（api/openapi.yaml 的 AgentLocalDirectories）。
type AgentDirectories struct {
	Revision    int64           `json:"revision"`
	Directories []DirectoryView `json:"directories"`
	ProjectID   *uuid.UUID      `json:"projectId"`
	Inherited   []DirectoryView `json:"inherited"`
}

// PutInput 是整体替换一份清单的请求（api/openapi.yaml 的 LocalDirectoriesPut）。
type PutInput struct {
	ExpectedRevision *int64           `json:"expectedRevision"`
	Directories      []DirectoryInput `json:"directories"`
}

// SettingsPatch 是修改开关的请求（api/openapi.yaml 的 LocalModeSettingsPatch）。
type SettingsPatch struct {
	Directories *bool `json:"directories"`
	Git         *bool `json:"git"`
	CLI         *bool `json:"cli"`
	Logins      *bool `json:"logins"`
}

// Settings 读取开关。
func (s *Service) Settings(ctx context.Context) (store.LocalModeSettings, error) {
	return s.Store.GetLocalModeSettings(ctx)
}

// UpdateSettings 只改请求里带的开关，至少要带一个。
func (s *Service) UpdateSettings(ctx context.Context, p SettingsPatch) (store.LocalModeSettings, error) {
	if p.Directories == nil && p.Git == nil && p.CLI == nil && p.Logins == nil {
		return store.LocalModeSettings{}, apperr.Invalid("body", "至少修改 directories、git、cli、logins 中的一项")
	}
	return s.Store.UpdateLocalModeSettings(ctx, store.LocalModeSettingsPatch{Directories: p.Directories, Git: p.Git, CLI: p.CLI, Logins: p.Logins})
}

// ProjectDirectories 返回未删除项目的清单。
func (s *Service) ProjectDirectories(ctx context.Context, projectID uuid.UUID) (ProjectDirectories, error) {
	if _, err := s.Store.GetProject(ctx, projectID); err != nil {
		return ProjectDirectories{}, err
	}
	list, err := s.Store.ProjectLocalDirectories(ctx, projectID)
	if err != nil {
		return ProjectDirectories{}, err
	}
	return ProjectDirectories{Revision: list.Revision, Directories: views(list.Directories)}, nil
}

// PutProjectDirectories 整体替换项目的清单。
func (s *Service) PutProjectDirectories(ctx context.Context, projectID uuid.UUID, in PutInput) (ProjectDirectories, error) {
	if _, err := s.Store.GetProject(ctx, projectID); err != nil {
		return ProjectDirectories{}, err
	}
	expected, dirs, err := validatePut(in)
	if err != nil {
		return ProjectDirectories{}, err
	}
	list, err := s.Store.PutProjectLocalDirectories(ctx, projectID, expected, dirs)
	if err != nil {
		return ProjectDirectories{}, err
	}
	return ProjectDirectories{Revision: list.Revision, Directories: views(list.Directories)}, nil
}

// AgentDirectories 返回未删除的常态或平台 Agent 自己的清单与继承自项目的条目。
func (s *Service) AgentDirectories(ctx context.Context, agentID uuid.UUID) (AgentDirectories, error) {
	a, err := s.Store.GetAgent(ctx, agentID)
	if err != nil {
		return AgentDirectories{}, err
	}
	list, err := s.Store.AgentLocalDirectories(ctx, agentID)
	if err != nil {
		return AgentDirectories{}, err
	}
	return s.agentView(ctx, a, list)
}

// PutAgentDirectories 整体替换 Agent 自己的清单。
func (s *Service) PutAgentDirectories(ctx context.Context, agentID uuid.UUID, in PutInput) (AgentDirectories, error) {
	a, err := s.Store.GetAgent(ctx, agentID)
	if err != nil {
		return AgentDirectories{}, err
	}
	expected, dirs, err := validatePut(in)
	if err != nil {
		return AgentDirectories{}, err
	}
	list, err := s.Store.PutAgentLocalDirectories(ctx, agentID, expected, dirs)
	if err != nil {
		return AgentDirectories{}, err
	}
	return s.agentView(ctx, a, list)
}

func (s *Service) agentView(ctx context.Context, a store.Agent, own store.LocalDirectoryList) (AgentDirectories, error) {
	out := AgentDirectories{Revision: own.Revision, Directories: views(own.Directories), ProjectID: a.ProjectID, Inherited: []DirectoryView{}}
	if a.ProjectID != nil {
		inherited, err := s.Store.ProjectLocalDirectories(ctx, *a.ProjectID)
		if err != nil {
			return AgentDirectories{}, err
		}
		out.Inherited = views(inherited.Directories)
	}
	return out, nil
}

func validatePut(in PutInput) (int64, []store.LocalDirectoryEntry, error) {
	var errs []apperr.FieldError
	if in.ExpectedRevision == nil {
		errs = append(errs, apperr.Field("expectedRevision", "必须提供读到的 revision（从未保存过为 0）"))
	} else if *in.ExpectedRevision < 0 {
		errs = append(errs, apperr.Field("expectedRevision", "必须是不小于 0 的整数"))
	}
	dirs, err := Validate(in.Directories)
	if err != nil {
		if ae, ok := apperr.As(err); ok {
			errs = append(errs, ae.Fields...)
		} else {
			return 0, nil, err
		}
	}
	if len(errs) > 0 {
		return 0, nil, apperr.Validation(errs...)
	}
	return *in.ExpectedRevision, dirs, nil
}

func views(list []store.LocalDirectoryEntry) []DirectoryView {
	out := make([]DirectoryView, 0, len(list))
	for _, d := range list {
		v := DirectoryView{Path: d.Path, Status: Status(d.Path)}
		if d.Note != "" {
			note := d.Note
			v.Note = &note
		}
		out = append(out, v)
	}
	return out
}

// Access 返回 agentID（常态或平台 Agent；任务内实例传它的基础 Agent）启动 Pi 时本机模式生效的部分：
// “绑定本机目录”开启时为项目的清单加 Agent 自己的清单（同一路径只算一次，项目的在前），“用本机的 CLI”开启时接上用户的 PATH，
// “用本机的登录状态”开启时 Pi 沿用用户真实的 HOME。
// 已删除的基础 Agent（任务仍在进行）照样按它的记录取清单。
func (s *Service) Access(ctx context.Context, agentID uuid.UUID) (agentconfig.LocalAccess, error) {
	settings, err := s.Store.GetLocalModeSettings(ctx)
	if err != nil {
		return agentconfig.LocalAccess{}, err
	}
	out := agentconfig.LocalAccess{UserPath: settings.CLI, Logins: settings.Logins}
	if !settings.Directories {
		return out, nil
	}
	projectID, err := s.Store.AgentProjectID(ctx, agentID)
	if store.IsNotFound(err) {
		return out, nil
	}
	if err != nil {
		return agentconfig.LocalAccess{}, err
	}
	seen := map[string]bool{}
	add := func(list []store.LocalDirectoryEntry, inherited bool) {
		for _, d := range list {
			if k := pathKey(d.Path); !seen[k] {
				seen[k] = true
				out.Directories = append(out.Directories, agentconfig.LocalDirectory{Path: d.Path, Note: d.Note, Inherited: inherited})
			}
		}
	}
	if projectID != nil {
		list, err := s.Store.ProjectLocalDirectories(ctx, *projectID)
		if err != nil {
			return agentconfig.LocalAccess{}, err
		}
		add(list.Directories, true)
	}
	own, err := s.Store.AgentLocalDirectories(ctx, agentID)
	if err != nil {
		return agentconfig.LocalAccess{}, err
	}
	add(own.Directories, false)
	return out, nil
}
