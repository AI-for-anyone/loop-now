package store

import "github.com/google/uuid"

// Scope 是 Agent 的归属：某个项目，或平台（不属于任何组织或项目）。Agent 创建后归属不变。
type Scope struct {
	ProjectID uuid.UUID // Platform 为 false 时有效
	Platform  bool
}

// ProjectScope 返回项目范围。
func ProjectScope(id uuid.UUID) Scope { return Scope{ProjectID: id} }

// PlatformScope 返回平台范围。
func PlatformScope() Scope { return Scope{Platform: true} }

// projectArg 返回写入 agents.project_id 的值：平台为 NULL。
func (s Scope) projectArg() *uuid.UUID {
	if s.Platform {
		return nil
	}
	id := s.ProjectID
	return &id
}

// ScopeFilter 是概览、用量与异常的筛选条件；零值表示全部（所有项目与平台）。
type ScopeFilter struct {
	ProjectID *uuid.UUID // 只看这个项目
	Platform  bool       // 只看平台 Agent
}

// args 返回 SQL 条件 `($n::uuid IS NULL OR a.project_id = $n) AND (NOT $m OR a.project_id IS NULL)` 的两个参数。
func (f ScopeFilter) args() (*uuid.UUID, bool) { return f.ProjectID, f.Platform }
