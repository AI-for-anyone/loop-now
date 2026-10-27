package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Organization 是组织。
type Organization struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Project 是项目。IsDefault、ManagerAgentID、AgentCount、UpdatedAt 只在项目查询（projects.go）中填写。
type Project struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	CreatedAt      time.Time
	IsDefault      bool
	ManagerAgentID *uuid.UUID
	AgentCount     int
	UpdatedAt      time.Time
}

// DefaultContext 是第一个闭环使用的默认组织与默认项目。
type DefaultContext struct {
	Organization Organization
	Project      Project
}

// EnsureDefaults 确保恰好存在一个默认组织，且其下恰好存在一个默认项目；
// 重复或并发调用不会产生重复记录。名称只在首次创建时使用。
func (s *Store) EnsureDefaults(ctx context.Context, orgName, projectName string) (DefaultContext, error) {
	var dc DefaultContext
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, name, is_default) VALUES ($1, $2, true)
			ON CONFLICT ((true)) WHERE is_default DO NOTHING`, uuid.Must(uuid.NewV7()), orgName); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT id, name, created_at FROM organizations WHERE is_default`).
			Scan(&dc.Organization.ID, &dc.Organization.Name, &dc.Organization.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO projects (id, organization_id, name, is_default) VALUES ($1, $2, $3, true)
			ON CONFLICT (organization_id) WHERE is_default DO NOTHING`, uuid.Must(uuid.NewV7()), dc.Organization.ID, projectName); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT id, organization_id, name, created_at FROM projects WHERE organization_id = $1 AND is_default`, dc.Organization.ID).
			Scan(&dc.Project.ID, &dc.Project.OrganizationID, &dc.Project.Name, &dc.Project.CreatedAt)
	})
	return dc, err
}
