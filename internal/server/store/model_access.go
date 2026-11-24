package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"loop-now/internal/server/db"
)

// ModelAccess is what an Agent, a project or a new platform Agent has for running model providers without entering an
// account first (the console's ModelProvider.usable).
type ModelAccess struct {
	// Providers have an account or key the platform keeps and can use: a project model account whose current version
	// is not unavailable, or a platform Agent's own model key or login.
	Providers map[string]bool
	// LegacyEnv are the environment variable names of a platform Agent's older single key. Runs read it only when the
	// Agent has no managed model credential, so it is empty otherwise.
	LegacyEnv []string
}

// usableVersion is the condition for a model_auth_versions row v that runs can use.
const usableVersion = `v.refresh_state <> 'unavailable'`

// ProjectModelAccess returns the model providers a project has a usable account for; not_found when the project does
// not exist.
func (s *Store) ProjectModelAccess(ctx context.Context, project uuid.UUID) (ModelAccess, error) {
	if _, err := s.GetProject(ctx, project); err != nil {
		return ModelAccess{}, err
	}
	return projectModelAccess(ctx, s.Pool, project)
}

// AgentModelAccess returns what the Agent can run models with: its project's accounts for a project Agent, its own key
// or login (or older single key) for a platform Agent.
func (s *Store) AgentModelAccess(ctx context.Context, agentID uuid.UUID) (ModelAccess, error) {
	a, err := s.GetAgent(ctx, agentID)
	if err != nil {
		return ModelAccess{}, err
	}
	if a.ProjectID != nil {
		return projectModelAccess(ctx, s.Pool, *a.ProjectID)
	}
	out := ModelAccess{Providers: map[string]bool{}}
	var provider *string
	err = s.Pool.QueryRow(ctx, `SELECT v.provider FROM agent_model_auth a LEFT JOIN model_auth_versions v ON v.id = a.version_id AND `+usableVersion+`
		WHERE a.agent_id = $1`, agentID).Scan(&provider)
	switch {
	case err == nil:
		if provider != nil {
			out.Providers[*provider] = true
		}
		return out, nil
	case !errors.Is(err, db.ErrNoRows):
		return out, err
	}
	var names []byte
	err = s.Pool.QueryRow(ctx, `SELECT env_names FROM agent_credentials WHERE agent_id = $1 AND purpose = $2`, agentID, PurposeModel).Scan(&names)
	if errors.Is(err, db.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(names, &out.LegacyEnv)
}

func projectModelAccess(ctx context.Context, q queueQuerier, project uuid.UUID) (ModelAccess, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT p.provider FROM project_credential_accounts p JOIN model_auth_versions v ON v.id = p.current_model_version_id
		WHERE p.project_id = $1 AND p.purpose = 'model' AND p.deleted_at IS NULL AND `+usableVersion, project)
	if err != nil {
		return ModelAccess{}, err
	}
	providers, err := db.CollectRows(rows, db.RowTo[string])
	out := ModelAccess{Providers: map[string]bool{}}
	for _, p := range providers {
		out.Providers[p] = true
	}
	return out, err
}
