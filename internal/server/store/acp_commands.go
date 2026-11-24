package store

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/google/uuid"

	"loop-now/internal/server/agentruntime"
	"loop-now/internal/server/apperr"
	"loop-now/internal/server/db"
)

// ACPCommand is a local ACP command a person added: a program on this computer that speaks the Agent Client Protocol
// over stdio, run as the runtime Runtime() with one model provider of the same id.
type ACPCommand struct {
	ID        uuid.UUID
	Name      string
	Command   string
	Args      []string
	Env       map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Runtime is the id of the runtime (and model provider) that runs the command.
func (c ACPCommand) Runtime() string { return agentruntime.LocalCommandRuntimeID(c.ID) }

// ACPCommandPatch changes a command; nil fields stay as they are. Env is merged into the saved environment: a value
// sets the variable, a nil value removes it, names not in Env keep their saved values.
type ACPCommandPatch struct {
	Name, Command *string
	Args          *[]string
	Env           map[string]*string
}

// RuntimeInUseDetails are the details of runtime_in_use: the Agents (not deleted) whose model provider the command runs.
type RuntimeInUseDetails struct {
	Agents []RuntimeInUseAgent `json:"agents"`
}

type RuntimeInUseAgent struct {
	ID        uuid.UUID  `json:"id"`
	Name      string     `json:"name"`
	ProjectID *uuid.UUID `json:"projectId"`
}

const acpCommandColumns = `id, name, command, args, env, created_at, updated_at`

func scanACPCommand(row db.Scanner) (ACPCommand, error) {
	var c ACPCommand
	var args, env []byte
	if err := row.Scan(&c.ID, &c.Name, &c.Command, &args, &env, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	if err := json.Unmarshal(args, &c.Args); err != nil {
		return c, err
	}
	if err := json.Unmarshal(env, &c.Env); err != nil {
		return c, err
	}
	if c.Args == nil {
		c.Args = []string{}
	}
	if c.Env == nil {
		c.Env = map[string]string{}
	}
	return c, nil
}

// ACPCommands returns every command, oldest first.
func (s *Store) ACPCommands(ctx context.Context) ([]ACPCommand, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+acpCommandColumns+` FROM acp_commands ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ACPCommand{}
	for rows.Next() {
		c, err := scanACPCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateACPCommand adds a command with a new id.
func (s *Store) CreateACPCommand(ctx context.Context, name, command string, args []string, env map[string]string) (ACPCommand, error) {
	var out ACPCommand
	err := s.inTx(ctx, func(tx *db.Tx) error {
		a, e, err := acpCommandJSON(args, env)
		if err != nil {
			return err
		}
		now := tx.Now()
		out, err = scanACPCommand(tx.QueryRow(ctx, `INSERT INTO acp_commands (id, name, command, args, env, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)
			RETURNING `+acpCommandColumns, uuid.Must(uuid.NewV7()), name, command, a, e, now))
		return err
	})
	return out, err
}

// UpdateACPCommand changes a command.
func (s *Store) UpdateACPCommand(ctx context.Context, id uuid.UUID, p ACPCommandPatch) (ACPCommand, error) {
	var out ACPCommand
	err := s.inTx(ctx, func(tx *db.Tx) error {
		c, err := scanACPCommand(tx.QueryRow(ctx, `SELECT `+acpCommandColumns+` FROM acp_commands WHERE id = $1`, id))
		if err != nil {
			return notFoundIfNoRows(err, "ACP command")
		}
		if p.Name != nil {
			c.Name = *p.Name
		}
		if p.Command != nil {
			c.Command = *p.Command
		}
		if p.Args != nil {
			c.Args = *p.Args
		}
		for k, v := range p.Env {
			if v == nil {
				delete(c.Env, k)
			} else {
				c.Env[k] = *v
			}
		}
		a, e, err := acpCommandJSON(c.Args, c.Env)
		if err != nil {
			return err
		}
		out, err = scanACPCommand(tx.QueryRow(ctx, `UPDATE acp_commands SET name = $2, command = $3, args = $4, env = $5, updated_at = $6 WHERE id = $1
			RETURNING `+acpCommandColumns, id, c.Name, c.Command, a, e, tx.Now()))
		return err
	})
	return out, err
}

// DeleteACPCommand removes a command; it answers runtime_in_use while an Agent that is not deleted has its provider.
func (s *Store) DeleteACPCommand(ctx context.Context, id uuid.UUID) error {
	return s.inTx(ctx, func(tx *db.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, project_id FROM agents WHERE model_provider = $1 AND deleted_at IS NULL ORDER BY name, id`,
			agentruntime.LocalCommandRuntimeID(id))
		if err != nil {
			return err
		}
		var inUse []RuntimeInUseAgent
		for rows.Next() {
			var a RuntimeInUseAgent
			if err := rows.Scan(&a.ID, &a.Name, &a.ProjectID); err != nil {
				rows.Close()
				return err
			}
			inUse = append(inUse, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(inUse) > 0 {
			e := apperr.Conflict(apperr.CodeRuntimeInUse, "Agents still run on this command; move them to another model first.")
			e.Details = RuntimeInUseDetails{Agents: inUse}
			return e
		}
		tag, err := tx.Exec(ctx, `DELETE FROM acp_commands WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return apperr.NotFound("ACP command")
		}
		return nil
	})
}

func acpCommandJSON(args []string, env map[string]string) (string, string, error) {
	if args == nil {
		args = []string{}
	}
	if env == nil {
		env = map[string]string{}
	}
	a, err := json.Marshal(args)
	if err != nil {
		return "", "", err
	}
	e, err := json.Marshal(env)
	return string(a), string(e), err
}

// EnvNames are the names of the command's environment variables, sorted.
func (c ACPCommand) EnvNames() []string {
	names := slices.Sorted(maps.Keys(c.Env))
	if names == nil {
		names = []string{}
	}
	return names
}
