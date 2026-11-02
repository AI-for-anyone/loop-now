package httpapi_test

import (
	"context"
	"encoding/json"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
	"loop-now/internal/testutil/sqlitetest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type pauseTaskPrompt struct {
	once             sync.Once
	entered, release chan struct{}
}

// hook 在第一个 Agent 的采集读它的 CLI 时停住：这时项目规则和第一个 Agent 已读，第二个还没读，创建事务还开着。
func (p *pauseTaskPrompt) hook(ctx context.Context, q string) {
	if strings.Contains(q, "SELECT cli_name FROM agent_clis WHERE agent_id=$1") {
		p.once.Do(func() {
			close(p.entered)
			select {
			case <-p.release:
			case <-ctx.Done():
			}
		})
	}
}

func TestTaskPromptSingleProjectVersionAndFrozenRestore(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	account := e.sharedCredential(t, "model", "anthropic", "任务共享模型", "synthetic-fixture")
	ids := []uuid.UUID{}
	for _, name := range []string{"Manager", "Worker"} {
		a := decode[apitypes.Agent](t, e.expect(201, "POST", e.agents(), map[string]any{"name": name, "model": map[string]string{"provider": "anthropic", "modelId": "claude-x"}, "prompt": name + "-old"}))
		ids = append(ids, a.ID)
	}
	if _, err := e.st.SetProjectManager(ctx, e.dc.Project.ID, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.PutProjectPrompt(ctx, e.dc.Project.ID, "project-old", 1); err != nil {
		t.Fatal(err)
	}
	svc := tasks.New(e.st, e.api.Config, nil, tasks.Options{MaxSteps: 100, MaxEdges: 100, MaxVisits: 100, MaxRequest: 1 << 20}, e.api.Log)
	input := taskmodel.CreateInput{ActionID: uuid.New(), Title: "frozen", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "one", Steps: []workflow.Step{{ID: "one", Title: "one", AgentID: ids[0], InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}, {ID: "two", Title: "two", AgentID: ids[1], InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}, Inputs: map[string]workflow.Source{}}}, Edges: []workflow.Edge{{ID: "next", From: "one", To: "two"}}}}
	trace := &pauseTaskPrompt{entered: make(chan struct{}), release: make(chan struct{})}
	e.st.Pool.SetQueryHook(trace.hook)
	defer e.st.Pool.SetQueryHook(nil)
	var once sync.Once
	release := func() { once.Do(func() { close(trace.release) }) }
	defer release()
	raw := make(chan json.RawMessage, 1)
	errors := make(chan error, 1)
	go func() {
		data, err := svc.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: ids[0]}, input)
		raw <- data
		errors <- err
	}()
	select {
	case <-trace.entered:
	case <-ctx.Done():
		t.Fatal("second Agent capture not reached", ctx.Err())
	}
	// 创建事务占着唯一的写连接：项目 UPDATE 要等它提交。
	updated := make(chan error, 1)
	go func() {
		_, err := e.st.Pool.Exec(ctx, `UPDATE projects SET prompt='project-new',prompt_version=prompt_version+1 WHERE id=$1`, e.dc.Project.ID)
		updated <- err
	}()
	sqlitetest.WaitWriteWaiters(t, ctx, e.st.Pool, 1)
	select {
	case err := <-updated:
		t.Fatal("task project fence failed", err)
	default:
	}
	release()
	data := <-raw
	if err := <-errors; err != nil {
		t.Fatal(err)
	}
	detail := decode[taskmodel.Detail](t, data)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	snapshots := map[uuid.UUID][]byte{}
	rows, err := e.st.Pool.Query(ctx, `SELECT id,snapshot FROM task_instances WHERE task_id=$1`, detail.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var encoded []byte
		if err = rows.Scan(&id, &encoded); err != nil {
			t.Fatal(err)
		}
		snapshots[id] = encoded
	}
	rows.Close()
	if len(snapshots) != 2 {
		t.Fatal("expected two instances")
	}
	for _, encoded := range snapshots {
		snap, err := taskruntime.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if snap.Configuration().CredentialsRevision != 2 {
			t.Fatal("multi-Agent task did not capture one project credential revision")
		}
		creds, err := snap.Credentials()
		if err != nil || creds.ModelAuth == nil || creds.ModelAuth.VersionID != *account.CurrentVersionID || creds.ModelAuth.ProjectID == nil || *creds.ModelAuth.ProjectID != e.dc.Project.ID {
			t.Fatal("task credential capture mismatch", err)
		}
		layers := snap.Configuration().Prompts
		if layers == nil || layers.ProjectPrompt != "project-old" || layers.ProjectVersion != 2 || layers.AgentVersion != 1 {
			t.Fatal("mixed task project version")
		}
	}
	for _, id := range ids {
		text := "changed"
		v := int64(1)
		if _, err = e.st.UpdateAgent(ctx, id, store.AgentPatch{Prompt: &text, ExpectedPromptVersion: &v}); err != nil {
			t.Fatal(err)
		}
	}
	input.ActionID = uuid.New()
	nextData, err := svc.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: ids[0]}, input)
	if err != nil {
		t.Fatal(err)
	}
	next := decode[taskmodel.Detail](t, nextData)
	var encoded []byte
	if err = e.st.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE task_id=$1 LIMIT 1`, next.Task.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	snap, err := taskruntime.Decode(encoded)
	if err != nil || snap.Configuration().Prompts.ProjectPrompt != "project-new" || snap.Configuration().Prompts.ProjectVersion != 3 || snap.Configuration().Prompts.AgentVersion != 2 {
		t.Fatal("new task missed edits", err)
	}

	// The actual retry operation creates a new attempt against the existing frozen
	// runtime, and must not capture the edited project or Agent configuration.
	if _, err = e.st.MutateTask(ctx, detail.Task.ID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "blocked"
		m.Record.Current().Status = "blocked"
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	retryData, err := svc.Retry(ctx, auth.Console(), detail.Task.ID, taskmodel.RetryInput{ActionID: uuid.New(), ExpectedNodeRunID: detail.Nodes[0].ID, Note: "retry with frozen rules"})
	if err != nil {
		t.Fatal(err)
	}
	retry := decode[taskmodel.Detail](t, retryData)
	if len(retry.Nodes) != 2 || retry.Nodes[1].RuntimeID != detail.Nodes[0].RuntimeID || retry.Nodes[1].Attempt != 2 {
		t.Fatal("retry replaced frozen runtime")
	}
	// Remove current Prompt content while retaining the live owner identity.
	// Actual Agent deletion is forbidden for unfinished tasks; bypassing that
	// guard would invalidate the new authorization binding, not test Prompt restore.
	if _, err = e.st.Pool.Exec(ctx, `UPDATE agents SET prompt='',prompt_version=prompt_version+1,config_version=config_version+1 WHERE id IN ($1,$2)`, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = e.st.Pool.Exec(ctx, `UPDATE task_instances SET prepared=true WHERE task_id=$1`, detail.Task.ID); err != nil {
		t.Fatal(err)
	}
	source := &tasks.Source{Store: e.st, ModelAuth: e.api.Config.ModelAuth.Coordinator}
	for id, original := range snapshots {
		for i := 0; i < 3; i++ {
			captured, err := source.ExecutionSnapshot(ctx, id)
			if err != nil || !strings.Contains(captured.Prompt, "project-old") || !strings.Contains(captured.Prompt, "-old") || strings.Contains(captured.Prompt, "project-new") {
				t.Fatal("frozen restore read current config", err)
			}
		}
		var persisted []byte
		if err = e.st.Pool.QueryRow(ctx, `SELECT snapshot FROM task_instances WHERE id=$1`, id).Scan(&persisted); err != nil || string(persisted) != string(original) {
			t.Fatal("restore rewrote snapshot", err)
		}
	}
}
