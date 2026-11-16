package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/naming"
	"loop-now/internal/server/store"
)

// getProjectAgentStatus exposes only normal-instance status, never Agent config
// or a task runtime. The caller's project comes from the live base Agent row.
func (s *Server) getProjectAgentStatus(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.Kind != auth.KindAgent {
		s.fail(w, r, apperr.Forbidden("Only an Agent can query the status of Agents in its project."))
		return
	}
	caller, err := s.Store.GetAddressableAgent(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if caller.ProjectID == nil {
		s.fail(w, r, apperr.Forbidden("A platform Agent cannot query the status of project Agents."))
		return
	}
	refs, present := r.URL.Query()["agent"]
	if !present || len(refs) != 1 || refs[0] == "" {
		s.fail(w, r, apperr.Invalid("agent", "Give exactly one Agent UUID or an address in your project."))
		return
	}
	ref := refs[0]
	if strings.Contains(ref, "#") {
		s.fail(w, r, apperr.Invalid("agent", "Only regular Agents can be queried here. Task addresses (with #) are not accepted."))
		return
	}
	var target store.Agent
	if naming.IsUUIDForm(ref) {
		id, _ := uuid.Parse(ref)
		target, err = s.Store.GetAddressableAgent(r.Context(), id)
	} else {
		parts := strings.Split(ref, "/")
		if len(parts) > 3 {
			s.fail(w, r, apperr.Invalid("agent", "The Agent address is invalid. Use agent, project/agent or org/project/agent."))
			return
		}
		for _, part := range parts {
			if reason := naming.Validate("Each address segment", part); reason != "" {
				s.fail(w, r, apperr.Invalid("agent", reason))
				return
			}
		}
		org, project, name := caller.OrgName, caller.ProjectName, parts[len(parts)-1]
		orgID := caller.OrgID
		if len(parts) >= 2 {
			project = parts[len(parts)-2]
		}
		if len(parts) == 3 {
			org = parts[0]
			orgID = nil
		}
		target, err = s.Store.FindAgentByPath(r.Context(), org, orgID, project, name)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if target.ProjectID == nil || *target.ProjectID != *caller.ProjectID {
		s.fail(w, r, apperr.Forbidden("You can only view the status of regular Agents in your own project."))
		return
	}
	status, err := s.Conv.Status(r.Context(), target.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
