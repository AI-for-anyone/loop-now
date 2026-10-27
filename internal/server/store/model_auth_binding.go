package store

import (
	"context"
	"loop-now/internal/server/modelcredential"
)

// ValidateModelAuthBinding verifies immutable owner and consumer relationships;
// it deliberately does not read the account's current version or active flag.
func (s *Store) ValidateModelAuthBinding(ctx context.Context, b modelcredential.Binding) bool {
	if !b.Valid() {
		return false
	}
	var ok bool
	if b.OwnerKind == "project" {
		err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_auth_versions v JOIN project_credential_accounts c ON c.id=v.project_account_id JOIN agents a ON a.id=$2 JOIN projects p ON p.id=c.project_id WHERE v.id=$1 AND v.project_account_id=$3 AND c.project_id=$4 AND c.purpose='model' AND c.provider=$5 AND v.provider=$5 AND a.project_id=c.project_id AND a.deleted_at IS NULL AND p.deleted_at IS NULL)`, b.VersionID, b.AgentID, *b.AccountID, *b.ProjectID, b.Provider).Scan(&ok)
		return err == nil && ok
	}
	if b.OwnerKind == "agent" {
		err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_auth_versions v JOIN agents a ON a.id=v.agent_id WHERE v.id=$1 AND v.agent_id=$2 AND v.provider=$3 AND a.project_id IS NULL AND a.deleted_at IS NULL)`, b.VersionID, b.AgentID, b.Provider).Scan(&ok)
		return err == nil && ok
	}
	// The old triplet is not permission to claim someone else's migrated grant.
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM model_auth_versions v JOIN agents a ON a.id=$2 WHERE v.id=$1 AND v.provider=$3 AND a.deleted_at IS NULL AND ((v.agent_id=$2 AND a.project_id IS NULL) OR EXISTS(SELECT 1 FROM project_model_legacy_bindings m JOIN projects p ON p.id=m.project_id WHERE m.version_id=v.id AND m.source_agent_id=$2 AND m.provider=$3 AND m.account_id=v.project_account_id AND m.project_id=a.project_id AND p.deleted_at IS NULL)))`, b.VersionID, b.AgentID, b.Provider).Scan(&ok)
	return err == nil && ok
}
