package apitypes

import (
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// Agent scopes (who owns an Agent).
const (
	ScopeProject  = "project"
	ScopePlatform = "platform"
)

// ScopeOf derives the scope from the owning project: no project means the platform owns the Agent.
func ScopeOf(projectID *uuid.UUID) string {
	if projectID == nil {
		return ScopePlatform
	}
	return ScopeProject
}

// Project mirrors the Project schema.
type Project struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organizationId"`
	Name           string     `json:"name"`
	IsDefault      bool       `json:"isDefault"`
	ManagerAgentID *uuid.UUID `json:"managerAgentId"`
	AgentCount     int        `json:"agentCount"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

// ProjectList is the response of GET /projects.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// AgentList mirrors the AgentList schema.
type AgentList struct {
	Agents []AgentListItem `json:"agents"`
}

// ProjectCreate mirrors the ProjectCreate and ProjectUpdate schemas.
type ProjectCreate struct {
	Name string `json:"name"`
}

// ProjectManagerSet mirrors the ProjectManagerSet schema.
type ProjectManagerSet struct {
	AgentID uuid.UUID `json:"agentId"`
}

// FromProject converts a project.
func FromProject(p store.Project) Project {
	return Project{ID: p.ID, OrganizationID: p.OrganizationID, Name: p.Name, IsDefault: p.IsDefault, ManagerAgentID: p.ManagerAgentID,
		AgentCount: p.AgentCount, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}
