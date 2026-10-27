package app_test

import (
	"context"
	"crypto/sha256"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/taskruntime"
	"loop-now/internal/server/workflow"
	"testing"
)

func captureLarkAppTask(t *testing.T, s *server, agent uuid.UUID) store.TaskInstance {
	t.Helper()
	raw, e := s.a.Tasks.Create(context.Background(), auth.Principal{Kind: auth.KindAgent, ID: agent}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "隔离Lark固定捕获", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "work", Steps: []workflow.Step{{ID: "work", Title: "捕获检查", AgentID: agent, Inputs: map[string]workflow.Source{}, InputFields: []workflow.Field{}, OutputFields: []workflow.Field{}}}, Edges: []workflow.Edge{}}})
	if e != nil {
		t.Fatal("real Create capture failed", e)
	}
	d := decode[taskmodel.Detail](t, raw)
	instances, e := s.a.Store.TaskInstances(context.Background(), d.Task.ID)
	if e != nil || len(instances) != 1 || instances[0].ID == d.Task.ID {
		t.Fatal("runtime/task identity invalid")
	}
	return instances[0]
}
func TestLarkAppRealTaskCaptureRetainsBotAndUserVersions(t *testing.T) {
	w := githubAppWorld(t)
	provider := larktest.New(t)
	s := w.start(nil)
	defer s.stop()
	s.a.CLIAuth.Lark = provider.Client
	ctx := context.Background()
	// Capture proof only: stop the isolated task driver before real Create. No
	// Pi/tool/model request or prepareNode is represented by this test.
	if e := s.a.Tasks.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	base := "/projects/" + project.String() + "/cli-auth/operations"
	accounts := "/projects/" + project.String() + "/credential-accounts"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "离线模型", "credential": map[string]string{"type": "api_key", "key": "synthetic-only-model"}})
	bot := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "旧bot", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	path, agent := s.createAgent("Lark固定任务Agent")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": agent.ID})
	oldBot := captureLarkAppTask(t, s, agent.ID)
	botHash := sha256.Sum256(oldBot.Snapshot)
	a := provider.Add("ou-captured", "固定用户", []string{"offline_access", "im:message", "im:message.send_as_user", "im:message.group_msg:get_as_user"}, "")
	a.Release()
	id := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": id, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": bot.ID, "expectedSourceRevision": bot.Revision})
	user := *waitLarkApp(t, s, base, id, "succeeded").Account
	ag := decode[apitypes.Agent](t, s.must(200, "GET", path, nil))
	s.must(200, "PUT", path+"/credential-selections", map[string]any{"purpose": "cli", "cli": "lark-cli", "accountId": user.ID, "expectedConfigVersion": ag.ConfigVersion})
	userTask := captureLarkAppTask(t, s, agent.ID)
	userHash := sha256.Sum256(userTask.Snapshot)
	read := func(inst store.TaskInstance) taskruntime.Credentials {
		t.Helper()
		snap, e := taskruntime.Decode(inst.Snapshot)
		if e != nil {
			t.Fatal(e)
		}
		c, e := snap.Credentials()
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	if read(oldBot).LarkBinding != nil || len(read(oldBot).CLI["lark-cli"].Files) == 0 {
		t.Fatal("old bot task was retrofitted")
	}
	bound := read(userTask)
	if bound.LarkBinding == nil || bound.LarkBinding.VersionID != *user.CurrentVersionID || bound.CLI["lark-cli"].Files != nil {
		t.Fatal("user task did not freeze dynamic version without refresh copy")
	}
	// Existing explicit static replacement is already a confirmed account
	// operation. It does not exercise or decide the relogin-user policy.
	replaced := decode[cm.Account](t, s.must(200, "PUT", accounts+"/"+user.ID.String()+"/credential", map[string]any{"expectedRevision": user.Revision, "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-new-bot", "appSecret": "new-static-secret", "brand": "feishu"}}}))
	if replaced.LarkIdentity != nil || replaced.Status != "saved" {
		t.Fatal("static replacement retained current user identity")
	}
	for _, inst := range []store.TaskInstance{oldBot, userTask} {
		current, e := s.a.Store.TaskInstance(ctx, inst.ID)
		if e != nil {
			t.Fatal(e)
		}
		want := botHash
		if inst.ID == userTask.ID {
			want = userHash
		}
		if sha256.Sum256(current.Snapshot) != want {
			t.Fatal("existing task snapshot rewritten")
		}
	}
	if read(userTask).LarkBinding.VersionID != *user.CurrentVersionID {
		t.Fatal("old task followed account current")
	}
	var grant int
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM lark_cli_grants WHERE version_id=$1`, *user.CurrentVersionID).Scan(&grant); e != nil || grant != 1 {
		t.Fatal("old user grant lost on static replacement")
	}
	var runs, turns int
	if e := s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns)`).Scan(&runs, &turns); e != nil || runs+turns != 0 || provider.DeniedDials.Load() != 0 {
		t.Fatal("capture test dispatched work or external provider")
	}
}
