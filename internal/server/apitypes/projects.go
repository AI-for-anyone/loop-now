package apitypes

import (
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/store"
)

// Agent 的归属。
const (
	ScopeProject  = "project"
	ScopePlatform = "platform"
)

// ScopeOf 由所属项目得出归属：没有项目即为平台持有。
func ScopeOf(projectID *uuid.UUID) string {
	if projectID == nil {
		return ScopePlatform
	}
	return ScopeProject
}

// Project 对应 Project。
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

// ProjectList 是 GET /projects 的响应。
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// AgentList 对应 AgentList。
type AgentList struct {
	Agents []AgentListItem `json:"agents"`
}

// ProjectCreate 对应 ProjectCreate 与 ProjectUpdate。
type ProjectCreate struct {
	Name string `json:"name"`
}

// ProjectManagerSet 对应 ProjectManagerSet。
type ProjectManagerSet struct {
	AgentID uuid.UUID `json:"agentId"`
}

// FromProject 转换项目。
func FromProject(p store.Project) Project {
	return Project{ID: p.ID, OrganizationID: p.OrganizationID, Name: p.Name, IsDefault: p.IsDefault, ManagerAgentID: p.ManagerAgentID,
		AgentCount: p.AgentCount, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}
