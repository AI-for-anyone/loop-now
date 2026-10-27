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
		s.fail(w, r, apperr.Forbidden("仅 Agent 可查询同项目状态"))
		return
	}
	caller, err := s.Store.GetAddressableAgent(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if caller.ProjectID == nil {
		s.fail(w, r, apperr.Forbidden("平台 Agent 不能查询项目 Agent 状态"))
		return
	}
	refs, present := r.URL.Query()["agent"]
	if !present || len(refs) != 1 || refs[0] == "" {
		s.fail(w, r, apperr.Invalid("agent", "必须提供一个 Agent UUID 或同项目地址"))
		return
	}
	ref := refs[0]
	if strings.Contains(ref, "#") {
		s.fail(w, r, apperr.Invalid("agent", "这里只查询常态 Agent，不接受任务地址"))
		return
	}
	var target store.Agent
	if naming.IsUUIDForm(ref) {
		id, _ := uuid.Parse(ref)
		target, err = s.Store.GetAddressableAgent(r.Context(), id)
	} else {
		parts := strings.Split(ref, "/")
		if len(parts) > 3 {
			s.fail(w, r, apperr.Invalid("agent", "Agent 地址格式无效"))
			return
		}
		for _, part := range parts {
			if reason := naming.Validate("地址", part); reason != "" {
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
		s.fail(w, r, apperr.Forbidden("只能查看同项目常态 Agent 状态"))
		return
	}
	status, err := s.Conv.Status(r.Context(), target.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
