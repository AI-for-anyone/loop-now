package store

import "github.com/google/uuid"

// Scope is where an Agent belongs: a project, or the platform (not in any organization or project). An Agent's scope
// never changes after creation.
type Scope struct {
	ProjectID uuid.UUID // valid when Platform is false
	Platform  bool
}

// ProjectScope returns a project scope.
func ProjectScope(id uuid.UUID) Scope { return Scope{ProjectID: id} }

// PlatformScope returns the platform scope.
func PlatformScope() Scope { return Scope{Platform: true} }

// projectArg returns the value written to agents.project_id: NULL for the platform.
func (s Scope) projectArg() *uuid.UUID {
	if s.Platform {
		return nil
	}
	id := s.ProjectID
	return &id
}

// ScopeFilter is the filter for the overview, usage and anomalies; the zero value means everything (all projects and
// the platform).
type ScopeFilter struct {
	ProjectID *uuid.UUID // only this project
	Platform  bool       // only platform Agents
}

// args returns the two arguments of the SQL condition
// `($n IS NULL OR a.project_id = $n) AND (NOT $m OR a.project_id IS NULL)`.
func (f ScopeFilter) args() (*uuid.UUID, bool) { return f.ProjectID, f.Platform }
