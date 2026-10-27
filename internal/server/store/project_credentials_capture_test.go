package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/store"
)

type projectCaptureGate struct {
	once             sync.Once
	entered, release chan struct{}
}

func (g *projectCaptureGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "FROM agents WHERE id=$1") && strings.Contains(d.SQL, "FOR SHARE") {
		g.once.Do(func() {
			close(g.entered)
			select {
			case <-g.release:
			case <-ctx.Done():
			}
		})
	}
	return ctx
}
func (*projectCaptureGate) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestProjectCredentialCaptureSerializesEffectiveChanges(t *testing.T) {
	for _, mutation := range []string{"replace", "permission", "prompt"} {
		t.Run(mutation, func(t *testing.T) {
			e := projectMigrationFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if err := e.s.Migrate(ctx, quietLogger()); err != nil {
				t.Fatal(err)
			}
			id := e.agent(t, "锁内捕获", "openai")
			w := projectCoreWrite(t, e, "synthetic-capture-A")
			account, err := e.s.CreateProjectCredential(ctx, e.project, "model", "openai", "模型A", w)
			if err != nil {
				t.Fatal(err)
			}
			if err = e.s.AddAgentCLI(ctx, id, "gh"); err != nil {
				t.Fatal(err)
			}
			before, err := e.s.CaptureExecutionIdentity(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			gate := &projectCaptureGate{entered: make(chan struct{}), release: make(chan struct{})}
			readConfig := e.s.Pool.Config()
			readConfig.ConnConfig.Tracer = gate
			readPool, err := pgxpool.NewWithConfig(ctx, readConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer readPool.Close()
			reader := &store.Store{Pool: readPool}
			writeConfig := e.s.Pool.Config()
			writeConfig.MaxConns = 1
			writePool, err := pgxpool.NewWithConfig(ctx, writeConfig)
			if err != nil {
				t.Fatal(err)
			}
			defer writePool.Close()
			writer := &store.Store{Pool: writePool}
			var pid int
			if err = writePool.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			capture := make(chan store.ExecutionIdentity, 1)
			failure := make(chan error, 1)
			var once sync.Once
			release := func() { once.Do(func() { close(gate.release) }) }
			defer release()
			go func() { v, err := reader.CaptureExecutionIdentity(ctx, id); capture <- v; failure <- err }()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("capture gate", ctx.Err())
			}
			written := make(chan error, 1)
			go func() {
				switch mutation {
				case "replace":
					next := projectCoreWrite(t, e, "synthetic-capture-B")
					_, err := writer.MutateProjectCredential(ctx, e.project, account.View.ID, 1, nil, &next, false)
					written <- err
				case "permission":
					written <- writer.RemoveAgentCLI(ctx, id, "gh")
				case "prompt":
					text := "prompt-B"
					version := int64(1)
					_, err := writer.UpdateAgent(ctx, id, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: &version})
					written <- err
				}
			}()
			// PG's actual lock wait proves the writer reached the project fence.
			for {
				var blocked bool
				if err = e.s.Pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-written:
					t.Fatal("writer bypassed capture fence", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				default:
				}
			}
			release()
			old := <-capture
			if err = <-failure; err != nil {
				t.Fatal(err)
			}
			if old.ModelAuth.VersionID != before.ModelAuth.VersionID || old.CredentialIdentity != before.CredentialIdentity || old.Agent.ConfigVersion != before.Agent.ConfigVersion || old.Prompts.AgentPrompt != before.Prompts.AgentPrompt || len(old.CLIs) != 1 {
				t.Fatal("captured mixed effective version")
			}
			if err = <-written; err != nil {
				t.Fatal(err)
			}
			next, err := e.s.CaptureExecutionIdentity(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "replace":
				if next.ModelAuth.VersionID == old.ModelAuth.VersionID || next.CredentialIdentity == old.CredentialIdentity {
					t.Fatal("next capture missed replacement")
				}
			case "permission":
				if len(next.CLIs) != 0 || next.Agent.ConfigVersion <= old.Agent.ConfigVersion {
					t.Fatal("next capture missed permission removal")
				}
			case "prompt":
				if next.Prompts.AgentPrompt != "prompt-B" || next.Agent.ConfigVersion <= old.Agent.ConfigVersion {
					t.Fatal("next capture missed Prompt")
				}
			}
		})
	}
}
