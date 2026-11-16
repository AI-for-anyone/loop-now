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

// ---- Projects ----

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
		s.fail(w, r, apperr.Invalid("agentId", "Required. Give the ID of the Agent to make the project Manager."))
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

// ---- Listing and creating Agents by scope ----

// projectScope parses the project in the path and checks that it is not deleted.
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
			s.fail(w, r, apperr.Invalid("thinkingLevel", "Must be a thinking level string or null."))
			return
		}
	}
	var tier *string
	if len(in.ServiceTier) > 0 {
		if err := json.Unmarshal(in.ServiceTier, &tier); err != nil {
			s.fail(w, r, apperr.Invalid("serviceTier", "Must be a service tier string or null."))
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

// ---- Scope of the overview, usage and anomalies ----

// scopeFilter parses the projectId and platform query parameters: at most one of them; neither means everything;
// projectId must be a project that is not deleted.
func (s *Server) scopeFilter(r *http.Request) (store.ScopeFilter, error) {
	q := r.URL.Query()
	var f store.ScopeFilter
	if v := q.Get("platform"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return f, apperr.Invalid("platform", "Must be true or false.")
		}
		f.Platform = b
	}
	if v := q.Get("projectId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, apperr.Invalid("projectId", "Must be a valid UUID.")
		}
		if f.Platform {
			return f, apperr.Invalid("projectId", "Give either projectId or platform=true, not both.")
		}
		if _, err := s.Store.GetProject(r.Context(), id); err != nil {
			return f, err
		}
		f.ProjectID = &id
	}
	return f, nil
}

// overviewKeep returns the filter for overview events: keep only events of Agents in scope; keep everything when not
// filtering.
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

// readOverview reads overview events (at most 100 per call). It does not filter: the overview stream handler passes
// these events through pumpEventsFiltered with overviewKeep, so events outside the scope are dropped there and still
// advance the cursor.
func (s *Server) readOverview(ctx context.Context, after string, block time.Duration) ([]coord.Event, error) {
	return s.Coord.ReadOverview(ctx, after, 100, block)
}
