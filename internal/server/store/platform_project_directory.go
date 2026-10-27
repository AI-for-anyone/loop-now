package store

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PlatformProjectDirectoryItem deliberately does not embed Project or Agent:
// adding management fields to either must not widen the discovery response.
type PlatformProjectDirectoryItem struct {
	ID           uuid.UUID                     `json:"id"`
	Name         string                        `json:"name"`
	Organization PlatformDirectoryOrganization `json:"organization"`
	Manager      *PlatformDirectoryManager     `json:"manager"`
}

type PlatformDirectoryOrganization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type PlatformDirectoryManager struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	Address string    `json:"address"`
}

// ListPlatformProjectDirectory returns only current, safe communication locators.
// A single statement keeps names and Manager membership in the same read view.
func (s *Store) ListPlatformProjectDirectory(ctx context.Context) ([]PlatformProjectDirectoryItem, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.name,o.id,o.name,a.id,a.name
		FROM projects p JOIN organizations o ON o.id=p.organization_id
		LEFT JOIN agents a ON a.id=p.manager_agent_id AND a.project_id=p.id AND a.deleted_at IS NULL
		WHERE p.deleted_at IS NULL ORDER BY o.name,o.id,p.name,p.id`)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (PlatformProjectDirectoryItem, error) {
		var item PlatformProjectDirectoryItem
		var managerID *uuid.UUID
		var managerName *string
		err := row.Scan(&item.ID, &item.Name, &item.Organization.ID, &item.Organization.Name, &managerID, &managerName)
		if err == nil && managerID != nil && managerName != nil {
			item.Manager = &PlatformDirectoryManager{ID: *managerID, Name: *managerName,
				Address: item.Organization.Name + "/" + item.Name + "/" + *managerName}
		}
		return item, err
	})
	if err == nil && items == nil {
		items = []PlatformProjectDirectoryItem{}
	}
	return items, err
}
