package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/coord"
	"loop-now/internal/server/store"
)

// ---- 项目 ----

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Config.ListProjects(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.ProjectList{Projects: make([]apitypes.Project, 0, len(ps))}
	for _, p := range ps {
		out.Projects = append(out.Projects, apitypes.FromProject(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in apitypes.ProjectCreate
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Config.CreateProject(r.Context(), in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apitypes.FromProject(p))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Store.GetProject(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromProject(p))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in apitypes.ProjectCreate
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Config.RenameProject(r.Context(), id, in.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromProject(p))
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := s.Config.DeleteProject(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setProjectManager(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var in struct {
		AgentID *uuid.UUID `json:"agentId"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.AgentID == nil {
		s.fail(w, r, apperr.Invalid("agentId", "必填"))
		return
	}
	p, err := s.Config.SetProjectManager(r.Context(), id, *in.AgentID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromProject(p))
}

func (s *Server) clearProjectManager(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p, err := s.Config.ClearProjectManager(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, apitypes.FromProject(p))
}

// ---- 按范围列出与创建 Agent ----

// projectScope 解析路径中的项目，并确认项目未删除。
func (s *Server) projectScope(r *http.Request) (store.Scope, error) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		return store.Scope{}, err
	}
	if _, err := s.Store.GetProject(r.Context(), id); err != nil {
		return store.Scope{}, err
	}
	return store.ProjectScope(id), nil
}

func (s *Server) listProjectAgents(w http.ResponseWriter, r *http.Request) {
	scope, err := s.projectScope(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAgentList(w, r, scope)
}

func (s *Server) createProjectAgent(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "projectId")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.createAgentIn(w, r, store.ProjectScope(id))
}

func (s *Server) listPlatformAgents(w http.ResponseWriter, r *http.Request) {
	s.writeAgentList(w, r, store.PlatformScope())
}

func (s *Server) createPlatformAgent(w http.ResponseWriter, r *http.Request) {
	s.createAgentIn(w, r, store.PlatformScope())
}

func (s *Server) writeAgentList(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	items, err := s.Store.ListAgents(r.Context(), scope)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := apitypes.AgentList{Agents: make([]apitypes.AgentListItem, 0, len(items))}
	for _, it := range items {
		out.Agents = append(out.Agents, apitypes.FromAgentListItem(it))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createAgentIn(w http.ResponseWriter, r *http.Request, scope store.Scope) {
	var in struct {
		Name          string          `json:"name"`
		ThinkingLevel json.RawMessage `json:"thinkingLevel"`
		ServiceTier   json.RawMessage `json:"serviceTier"`
		Model         *modelInput     `json:"model"`
		Prompt        string          `json:"prompt"`
	}
	if err := readJSON(w, r, jsonLimit, &in); err != nil {
		s.fail(w, r, err)
		return
	}
	if in.Model == nil {
		in.Model = &modelInput{}
	}
	var thinking *string
	if len(in.ThinkingLevel) > 0 {
		if err := json.Unmarshal(in.ThinkingLevel, &thinking); err != nil {
			s.fail(w, r, apperr.Invalid("thinkingLevel", "须为合法思考等级或null"))
			return
		}
	}
	var tier *string
	if len(in.ServiceTier) > 0 {
		if err := json.Unmarshal(in.ServiceTier, &tier); err != nil {
			s.fail(w, r, apperr.Invalid("serviceTier", "须为速度档位或null"))
			return
		}
	}
	a, err := s.Config.CreateAgent(r.Context(), scope, in.Name, in.Model.Provider, in.Model.ModelID, in.Prompt, thinking, tier)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeAgent(w, r, http.StatusCreated, a.ID)
}

// ---- 概览、用量与异常的范围 ----

// scopeFilter 解析 projectId 与 platform 查询参数：二选一，都不给表示全部；projectId 必须是未删除的项目。
func (s *Server) scopeFilter(r *http.Request) (store.ScopeFilter, error) {
	q := r.URL.Query()
	var f store.ScopeFilter
	if v := q.Get("platform"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, apperr.Invalid("platform", "必须是 true 或 false")
		}
		f.Platform = b
	}
	if v := q.Get("projectId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, apperr.Invalid("projectId", "不是有效的 UUID")
		}
		if f.Platform {
			return f, apperr.Invalid("projectId", "projectId 与 platform 只能指定一个")
		}
		if _, err := s.Store.GetProject(r.Context(), id); err != nil {
			return f, err
		}
		f.ProjectID = &id
	}
	return f, nil
}

// overviewKeep 返回概览事件的过滤函数：只保留范围内 Agent 的事件；不筛选时保留全部。
func overviewKeep(f store.ScopeFilter) func(coord.Event) bool {
	if f.ProjectID == nil && !f.Platform {
		return nil
	}
	return func(ev coord.Event) bool {
		var scope struct {
			Scope     string     `json:"scope"`
			ProjectID *uuid.UUID `json:"projectId"`
			Agent     *struct {
				Scope     string     `json:"scope"`
				ProjectID *uuid.UUID `json:"projectId"`
			} `json:"agent"`
		}
		if err := json.Unmarshal(ev.Data, &scope); err != nil {
			return false
		}
		pid := scope.ProjectID
		if scope.Agent != nil {
			pid = scope.Agent.ProjectID
		}
		if f.Platform {
			return pid == nil
		}
		return pid != nil && *pid == *f.ProjectID
	}
}

// readOverviewFiltered 读取概览事件并按范围过滤；被过滤的事件仍推进游标（见 pumpEventsFiltered）。
func (s *Server) readOverview(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
	return s.Coord.ReadOverview(ctx, after, 100, block)
}
