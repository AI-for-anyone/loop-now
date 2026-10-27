package agentconfig

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/apperr"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// ListProjects 列出默认组织下未删除的项目。
func (s *Service) ListProjects(ctx context.Context) ([]store.Project, error) {
	return s.Store.ListProjects(ctx, s.OrgID)
}

// CreateProject 在默认组织下新建项目。
func (s *Service) CreateProject(ctx context.Context, name string) (store.Project, error) {
	if err := validateProjectName(name); err != nil {
		return store.Project{}, err
	}
	return s.Store.CreateProject(ctx, s.OrgID, name)
}

// RenameProject 改名；项目内 Agent 的地址随之改变。
func (s *Service) RenameProject(ctx context.Context, id uuid.UUID, name string) (store.Project, error) {
	if err := validateProjectName(name); err != nil {
		return store.Project{}, err
	}
	return s.Store.RenameProject(ctx, id, name)
}

// DeleteProject 软删除项目：其中每个 Agent 按删除 Agent 处理（停止 Pi、删除凭证，保留工作文件夹、会话文件与对话历史）。
// 执行器释放失败只记录日志：数据库中已删除，执行器启动时还会清理已删除 Agent 的 HOME 中的凭证。
func (s *Service) DeleteProject(ctx context.Context, id uuid.UUID) error {
	ids, err := s.Store.DeleteProject(ctx, id)
	if err != nil {
		return err
	}
	if s.ModelAuth != nil {
		if err := s.ModelAuth.CancelProjectLogins(ctx, id, nil); err != nil && s.Logger != nil {
			s.Logger.Warn("项目登录资源清理超时", "projectId", id, "code", "auth_cleanup_timeout")
		}
	}
	if s.CancelProjectCLILogins != nil {
		if e := s.CancelProjectCLILogins(ctx, id); e != nil && s.Logger != nil {
			s.Logger.Warn("项目CLI登录资源清理超时", "stage", "deleted_project_login_cleanup", "projectId", id.String())
		}
	}
	s.cancelDeletedAgentLogins(ctx, ids)
	for _, aid := range ids {
		if s.Releaser != nil {
			if err := s.Releaser.Release(ctx, aid); err != nil {
				s.Logger.Error("释放已删除项目中 Agent 的执行资源失败", "projectId", id, "agentId", aid, "err", err)
			}
		}
		if s.AgentRemoved != nil {
			s.AgentRemoved(aid)
		}
	}
	return nil
}

// SetProjectManager 指定或更换项目 Manager。
func (s *Service) SetProjectManager(ctx context.Context, projectID, agentID uuid.UUID) (store.Project, error) {
	p, err := s.Store.SetProjectManager(ctx, projectID, agentID)
	s.managerChanged(ctx, projectID)
	return p, err
}

// ClearProjectManager 取消项目 Manager。
func (s *Service) ClearProjectManager(ctx context.Context, projectID uuid.UUID) (store.Project, error) {
	p, err := s.Store.ClearProjectManager(ctx, projectID)
	s.managerChanged(ctx, projectID)
	return p, err
}

// managerChanged 让概览刷新该项目的 Agent（isManager 可能变化）。
func (s *Service) managerChanged(ctx context.Context, projectID uuid.UUID) {
	if s.AgentChanged == nil {
		return
	}
	items, err := s.Store.ListAgents(ctx, store.ProjectScope(projectID))
	if err != nil {
		return
	}
	for _, it := range items {
		s.AgentChanged(it.ID)
	}
}

func validateProjectName(name string) error {
	if msg := naming.Validate("项目名称", name); msg != "" {
		return apperr.Invalid("name", msg)
	}
	return nil
}
