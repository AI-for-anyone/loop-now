package localmode

import (
	"context"

	"github.com/google/uuid"

	"loop-now/internal/server/agentconfig"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/store"
)

// Service reads and writes the local mode switches and the lists of local directories, and returns the part that takes
// effect when Pi starts.
type Service struct {
	Store *store.Store
}

// DirectoryView is one entry as read back, with the status the server stats right now.
type DirectoryView struct {
	Path   string  `json:"path"`
	Note   *string `json:"note"`
	Status string  `json:"status"`
}

// ProjectDirectories is a project's list (ProjectLocalDirectories in api/openapi.yaml).
type ProjectDirectories struct {
	Revision    int64           `json:"revision"`
	Directories []DirectoryView `json:"directories"`
}

// AgentDirectories is an Agent's own list plus the entries inherited from the project (AgentLocalDirectories in
// api/openapi.yaml).
type AgentDirectories struct {
	Revision    int64           `json:"revision"`
	Directories []DirectoryView `json:"directories"`
	ProjectID   *uuid.UUID      `json:"projectId"`
	Inherited   []DirectoryView `json:"inherited"`
}

// PutInput is a request that replaces a whole list (LocalDirectoriesPut in api/openapi.yaml).
type PutInput struct {
	ExpectedRevision *int64           `json:"expectedRevision"`
	Directories      []DirectoryInput `json:"directories"`
}

// SettingsPatch is a request that changes switches (LocalModeSettingsPatch in api/openapi.yaml).
type SettingsPatch struct {
	Directories *bool `json:"directories"`
	Git         *bool `json:"git"`
	CLI         *bool `json:"cli"`
	Logins      *bool `json:"logins"`
}

// Settings reads the switches.
func (s *Service) Settings(ctx context.Context) (store.LocalModeSettings, error) {
	return s.Store.GetLocalModeSettings(ctx)
}

// UpdateSettings changes only the switches present in the request; at least one is required.
func (s *Service) UpdateSettings(ctx context.Context, p SettingsPatch) (store.LocalModeSettings, error) {
	if p.Directories == nil && p.Git == nil && p.CLI == nil && p.Logins == nil {
		return store.LocalModeSettings{}, apperr.Invalid("body", "至少修改 directories、git、cli、logins 中的一项")
	}
	return s.Store.UpdateLocalModeSettings(ctx, store.LocalModeSettingsPatch{Directories: p.Directories, Git: p.Git, CLI: p.CLI, Logins: p.Logins})
}

// ProjectDirectories returns the list of a project that is not deleted.
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

// PutProjectDirectories replaces a project's whole list.
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

// AgentDirectories returns the own list of a regular or platform Agent that is not deleted, plus the entries inherited
// from the project.
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

// PutAgentDirectories replaces an Agent's own whole list.
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

// Access returns the part of local mode that takes effect when agentID (a regular or platform Agent; for an instance
// inside a task, pass its base Agent) starts Pi: when "Bind local directories" is on, the project's list plus the
// Agent's own list (each path counted once, the project's first); when "Use local CLIs" is on, the user's PATH is
// appended; when "Use local logins" is on, Pi keeps the user's real HOME. A deleted base Agent (whose task is still
// running) still gets its list from its record.
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
