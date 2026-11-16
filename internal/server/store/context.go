package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// Organization is an organization.
type Organization struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Project is a project. IsDefault, ManagerAgentID, AgentCount and UpdatedAt are filled in only by project queries
// (projects.go).
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

// DefaultContext is the default organization and default project used by the first closed loop.
type DefaultContext struct {
	Organization Organization
	Project      Project
}

// EnsureDefaults makes sure exactly one default organization exists, with exactly one default project under it;
// repeated or concurrent calls do not create duplicate records. The names are used only on first creation.
func (s *Store) EnsureDefaults(ctx context.Context, orgName, projectName string) (DefaultContext, error) {
	var dc DefaultContext
	err := s.inTx(ctx, func(tx *db.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO organizations (id, name, is_default) VALUES ($1, $2, true)
			ON CONFLICT (is_default) WHERE is_default DO NOTHING`, uuid.Must(uuid.NewV7()), orgName); err != nil {
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
