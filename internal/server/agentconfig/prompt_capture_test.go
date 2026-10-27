package agentconfig_test

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"loop-now/internal/server/agentconfig"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/modelauth"
	"loop-now/internal/server/store"
	"strings"
	"sync"
	"testing"
	"time"
)

type pausePromptCapture struct {
	once             sync.Once
	entered, release chan struct{}
}

func (p *pausePromptCapture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, "FROM agents WHERE id=$1") && strings.Contains(d.SQL, "FOR SHARE") {
		p.once.Do(func() {
			close(p.entered)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
	return ctx
}
func (*pausePromptCapture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestPromptCaptureProjectLockAndAccountCoherence(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := e.agent(t)
	project := *a.ProjectID
	if _, err := e.st.PutProjectPrompt(ctx, project, "project-A", 1); err != nil {
		t.Fatal(err)
	}
	coord := modelauth.NewCoordinator(e.st, nil, modelauth.CoordinatorOptions{}, nil)
	auth := modelauth.NewService(e.st, nil, coord, e.svc.Models, modelauth.ServiceOptions{}, nil)
	defer auth.Close(ctx)
	e.svc.ModelAuth = auth
	projectAuth := modelauth.NewProjectService(auth, e.svc.Templates, 4096)
	provider := "anthropic"
	account, err := projectAuth.Create(ctx, project, cm.Create{Purpose: "model", Provider: &provider, Name: "capture-A", Credential: cm.CredentialInput{Type: "api_key", Key: "fixture-A"}})
	if err != nil {
		t.Fatal(err)
	}
	trace := &pausePromptCapture{entered: make(chan struct{}), release: make(chan struct{})}
	original := e.st.Pool
	cfg := original.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.st.Pool = pool
	defer func() { e.st.Pool = original; pool.Close() }()
	var once sync.Once
	release := func() { once.Do(func() { close(trace.release) }) }
	defer release()
	result := make(chan agentconfig.Snapshot, 1)
	failure := make(chan error, 1)
	go func() { s, err := e.svc.ExecutionSnapshot(ctx, a.ID); result <- s; failure <- err }()
	select {
	case <-trace.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Project SHARE has completed; Agent SHARE has not started. A real SQL UPDATE
	// must wait, while an Agent/account transaction can still commit coherently.
	writer, err := original.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	pid := writer.Conn().PgConn().PID()
	written := make(chan error, 1)
	go func() {
		_, err := writer.Exec(ctx, `UPDATE projects SET prompt='project-B',prompt_version=prompt_version+1 WHERE id=$1`, project)
		written <- err
	}()
	for {
		var blocked bool
		err = original.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-written:
			t.Fatal("project lock did not block write", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
	}
	release()
	snap := <-result
	if err = <-failure; err != nil {
		t.Fatal(err)
	}
	if snap.PromptLayers.ProjectPrompt != "project-A" || snap.PromptLayers.ProjectVersion != 2 || snap.PromptLayers.AgentVersion != 1 || snap.ModelBinding == nil || snap.ModelBinding.VersionID != *account.CurrentVersionID {
		t.Fatal("capture mixed blocked project update")
	}
	if err = <-written; err != nil {
		t.Fatal(err)
	}
	text := "agent-B"
	v := int64(1)
	if _, err = e.st.UpdateAgent(ctx, a.ID, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: &v}); err != nil {
		t.Fatal(err)
	}
	b, err := projectAuth.Replace(ctx, project, account.ID, cm.Replace{ExpectedRevision: account.Revision, Credential: cm.CredentialInput{Type: "api_key", Key: "fixture-B"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.svc.ExecutionSnapshot(ctx, a.ID)
	if err != nil || next.PromptLayers.ProjectPrompt != "project-B" || next.PromptLayers.ProjectVersion != 3 || next.ModelBinding.VersionID != *b.CurrentVersionID || next.PromptLayers.AgentPrompt != text {
		t.Fatal("next capture missed project edit", err)
	}
}
