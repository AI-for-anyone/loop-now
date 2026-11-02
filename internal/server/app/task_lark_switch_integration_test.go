package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"loop-now/internal/server/apitypes"
	"loop-now/internal/server/auth"
	"loop-now/internal/server/contracttest"
	cm "loop-now/internal/server/credentialmodel"
	"loop-now/internal/server/inbox"
	"loop-now/internal/server/larkauth/larktest"
	"loop-now/internal/server/larksidecar"
	"loop-now/internal/server/store"
	"loop-now/internal/server/taskmodel"
	"loop-now/internal/server/tasks"
	"loop-now/internal/server/workflow"
)

// Real App/TEST PG/Redis and canonical HTTP account/login/task capture, with a
// loopback-only official-protocol provider fixture. Original blocked/session
// facts are explicitly synthetic; this is not native execution or public POST.
func taskLarkSwitchAppReady(t *testing.T) (*server, store.TaskLarkFenceReservation, map[uuid.UUID][32]byte, *larktest.Provider) {
	t.Helper()
	w := taskNodeInboxWorld(t)
	provider := larktest.New(t)
	s := w.start(nil)
	t.Cleanup(s.stop)
	ctx := context.Background()
	// Stop only this self-owned App's driver before constructing synthetic
	// blocked history. No fakePi/model/native command should run in this slice.
	if err := s.a.Tasks.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	s.a.CLIAuth.Lark = provider.Client
	project := decode[apitypes.Context](t, s.must(200, "GET", "/context", nil)).Project.ID
	accounts := "/projects/" + project.String() + "/credential-accounts"
	base := "/projects/" + project.String() + "/cli-auth/operations"
	s.must(201, "POST", accounts, map[string]any{"purpose": "model", "provider": "anthropic", "name": "offline model", "credential": map[string]string{"type": "api_key", "key": "synthetic-model"}})
	bot := decode[cm.Account](t, s.must(201, "POST", accounts, map[string]any{"purpose": "cli", "cli": "lark-cli", "name": "original bot", "credential": map[string]any{"type": "cli", "fields": map[string]string{"appId": "app-fixture", "appSecret": "fixture-app-secret", "brand": "feishu"}}}))
	path, publisher := s.createAgent("SwitchPublisher")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	path, followup := s.createAgent("SwitchFollowup")
	s.must(200, "PUT", path+"/clis/lark-cli", nil)
	s.must(200, "PUT", "/projects/"+project.String()+"/manager", map[string]any{"agentId": publisher.ID})
	raw, err := s.a.Tasks.Create(ctx, auth.Principal{Kind: auth.KindAgent, ID: publisher.ID}, taskmodel.CreateInput{ActionID: uuid.New(), Title: "isolated switch capture", Request: map[string]any{}, Workflow: workflow.Definition{StartStepID: "qa", Steps: []workflow.Step{{ID: "qa", Title: "QA", AgentID: publisher.ID}, {ID: "followup", Title: "Followup", AgentID: followup.ID}}, Edges: []workflow.Edge{{ID: "qa-done", From: "qa", To: "followup", Label: "done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	detail := decode[taskmodel.Detail](t, raw)
	instances, err := s.a.Store.TaskInstances(ctx, detail.Task.ID)
	if err != nil || len(instances) != 2 {
		t.Fatal("real task capture", err)
	}
	in := store.TaskLarkFenceReservation{TaskID: detail.Task.ID, ActionID: uuid.New(), NodeRunID: uuid.New(), Capture: &store.TaskLarkSwitchCapture{}}
	before := map[uuid.UUID][32]byte{}
	for _, inst := range instances {
		role := "followup"
		if inst.AgentID == publisher.ID {
			role = "publisher"
		}
		in.Members = append(in.Members, store.TaskLarkFenceMember{AgentID: inst.AgentID, RuntimeID: inst.ID, Role: role})
		before[inst.ID] = sha256.Sum256(inst.Snapshot)
	}
	var pub store.TaskLarkFenceMember
	for _, m := range in.Members {
		if m.Role == "publisher" {
			pub = m
		}
	}
	session := filepath.Join(t.TempDir(), "synthetic-retained.jsonl")
	if err = os.WriteFile(session, []byte("synthetic original retained session\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.a.Store.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Record.View.Task.Status = "blocked"
		m.Record.View.Task.CurrentNodeRunID = &in.NodeRunID
		m.Record.View.Nodes = []taskmodel.NodeRun{{ID: in.NodeRunID, TaskID: in.TaskID, StepID: "qa", Visit: 1, Attempt: 1, AgentID: pub.AgentID, RuntimeID: pub.RuntimeID, Status: "blocked", RunIDs: []uuid.UUID{}}}
		_, err := m.Tx.Exec(ctx, `UPDATE task_instances SET prepared=true,session_state='active',session_file=$2 WHERE id=$1`, pub.RuntimeID, session)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := provider.Add("ou-switch", "Switch User", nil, "")
	fixture.Release()
	operation := uuid.New()
	s.must(202, "POST", base, map[string]any{"operationId": operation, "cli": "lark-cli", "authType": "lark_user", "sourceAccountId": bot.ID, "expectedSourceRevision": bot.Revision})
	user := waitLarkApp(t, s, base, operation, "succeeded").Account
	if user == nil || user.LarkIdentity == nil || user.LarkIdentity.GrantedScopes != nil {
		t.Fatal("canonical unknown grant metadata changed")
	}
	_, err = s.a.Store.MutateTask(ctx, in.TaskID, nil, func(m *store.TaskMutation) (any, error) {
		m.Unchanged()
		in.ExpectedTaskVersion = m.Record.View.Task.Version
		var err error
		in.Capture.Target, err = s.a.Store.CaptureTaskLarkTargetTx(ctx, m, user.ID, *user.CurrentVersionID, user.Revision)
		if err != nil {
			return nil, err
		}
		for _, member := range in.Members {
			actual, err := s.a.Store.CaptureTaskLarkMemberTx(ctx, m, member)
			if err != nil {
				return nil, err
			}
			in.Capture.Members = append(in.Capture.Members, actual)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, in, before, provider
}

// Adapter only delegates to the actual manager, matching production app's
// private adapter. This slice has no preexisting native scopes/processes and
// does not prove native execution assets, sandbox, or production assembly.
type taskLarkAppNative struct{ manager *larksidecar.Manager }
type taskLarkAppLease struct{ gate *larksidecar.RuntimeGate }

func (n *taskLarkAppNative) Acquire(ctx context.Context, ids []uuid.UUID) (tasks.TaskLarkNativeLease, error) {
	g, err := n.manager.AcquireRuntimeGate(ctx, ids)
	if err != nil {
		return nil, err
	}
	return &taskLarkAppLease{g}, nil
}
func (l *taskLarkAppLease) Seal(ctx context.Context) error { return l.gate.Seal(ctx) }
func (l *taskLarkAppLease) Release()                       { l.gate.Release() }

func TestTaskLarkSwitchAppHTTPActualEmptyScopeManagers(t *testing.T) {
	s, in, before, provider := taskLarkSwitchAppReady(t)
	ctx := context.Background()
	input := taskmodel.TaskLarkSwitchInput{ActionID: in.ActionID, ExpectedNodeRunID: in.NodeRunID, ExpectedTaskVersion: in.ExpectedTaskVersion, ExpectedLarkBindingRevision: in.Capture.ExpectedBindingRevision, TargetAccountID: in.Capture.Target.AccountID, TargetVersionID: in.Capture.Target.VersionID, ExpectedAccountRevision: in.Capture.Target.AccountRevision}
	for _, member := range in.Capture.Members {
		input.ExpectedMembers = append(input.ExpectedMembers, taskmodel.TaskLarkSwitchMemberInput{AgentID: member.AgentID, RuntimeID: member.RuntimeID, ExpectedBindingRevision: member.PreviousRevision})
		if member.Role == "publisher" {
			input.ExpectedAgentID, input.ExpectedRuntimeID = member.AgentID, member.RuntimeID
		}
	}
	path := "/tasks/" + in.TaskID.String() + "/lark-account-switches"
	// Actual default composition has no native factory/manager installed yet.
	// Prove it fails before A rather than silently accepting a success callback.
	s.a.Tasks.LarkSwitchNative = nil
	s.must(503, "POST", path, input)
	var count int
	if err := s.a.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM task_lark_switch_actions`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unready native created an action", err)
	}
	s.a.Tasks.LarkSwitchNative = &taskLarkAppNative{larksidecar.NewManager()}
	raw := s.must(202, "POST", path, input)
	receipt := decode[taskmodel.TaskLarkSwitchReceipt](t, raw)
	if receipt.Status != "succeeded" || receipt.Continuation == nil || receipt.BindingRevision == nil || receipt.DispatchStatus == nil || *receipt.DispatchStatus != "queued" {
		t.Fatal("actual HTTP did not publish unique intent")
	}
	doc := contracttest.Load(t)
	var wire any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if err := doc.Components.Schemas["TaskLarkSwitchReceipt"].Value.VisitJSON(wire, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatal("real receipt differs from formal contract", err)
	}
	again := decode[taskmodel.TaskLarkSwitchReceipt](t, s.must(202, "POST", path, input))
	got := decode[taskmodel.TaskLarkSwitchReceipt](t, s.must(200, "GET", path+"/"+in.ActionID.String(), nil))
	if again.Continuation == nil || got.Continuation == nil || again.Continuation.RunID != receipt.Continuation.RunID || got.Continuation.RunID != receipt.Continuation.RunID {
		t.Fatal("HTTP replay/GET repeated C")
	}
	out, err := s.a.Tasks.Source.ExecutionSnapshot(ctx, input.ExpectedRuntimeID)
	if err != nil || out.LarkBinding == nil || out.LarkBinding.VersionID != input.TargetVersionID || !strings.Contains(out.CredentialIdentity, ";lark-overlay:1:"+out.LarkBinding.RuntimeVariantVersion) || len(out.CLIPayloads["lark-cli"].Files) != 0 {
		t.Fatal("actual Source did not select fixed overlay", err)
	}
	for _, member := range in.Members {
		inst, err := s.a.Store.TaskInstance(ctx, member.RuntimeID)
		if err != nil || sha256.Sum256(inst.Snapshot) != before[member.RuntimeID] {
			t.Fatal("HTTP C changed original snapshot", err)
		}
		if member.Role == "followup" && (inst.Prepared || inst.SessionFile != nil) {
			t.Fatal("C prepared or woke followup")
		}
	}
	var groups, members, turns, runs int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_credential_bindings),(SELECT count(*) FROM task_lark_binding_members),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM runs)`).Scan(&groups, &members, &turns, &runs); err != nil || groups != 1 || members != 2 || turns != 1 || runs != 0 || provider.DeniedDials.Load() != 0 {
		t.Fatal("actual App repeated publication or invoked model", groups, members, turns, runs, err)
	}
	// Controlled real CLI with an isolated task-runtime fixture token. No Pi
	// autonomously calls it; no host HOME/credentials are inherited.
	bin := inboxActualCLI(t)
	home := t.TempDir()
	tok, err := auth.NewToken(auth.AgentPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.a.Store.InsertAgentToken(ctx, tok.ID, input.ExpectedRuntimeID, s.a.InstanceID, tok.Hash); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "submit.json")
	if err = os.WriteFile(file, []byte(`{"actionId":"`+uuid.NewString()+`","output":{},"edgeId":"qa-done"}`), 0600); err != nil {
		t.Fatal(err)
	}
	runCLI := func(exit int, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "LOOPCTL_AGENT_URL=" + strings.TrimSuffix(s.url, "/api/v1") + "/agent/v1", "LOOPCTL_AGENT_TOKEN=" + tok.Plain}
		raw, err := cmd.Output()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal("controlled CLI did not start")
			}
		}
		if code != exit {
			t.Fatalf("controlled CLI exit=%d want=%d", code, exit)
		}
		var out map[string]any
		if json.Unmarshal(raw, &out) != nil {
			t.Fatal("controlled CLI output was not JSON")
		}
		return out
	}
	runCLI(8, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file, "--run", receipt.Continuation.RunID.String())
	claimed, found, err := s.a.Store.ClaimTaskTurn(ctx, in.TaskID, input.ExpectedRuntimeID)
	if err != nil || !found || claimed.ID != receipt.Continuation.RunID {
		t.Fatal("actual recovery claim", err)
	}
	if err = s.a.Store.MarkTaskRunning(ctx, claimed, 1); err != nil {
		t.Fatal(err)
	}
	runCLI(8, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file)
	submitted := runCLI(0, "task", "submit", in.TaskID.String(), receipt.Continuation.NodeRunID.String(), "--from-file", file, "--run", claimed.ID.String())
	if submitted["data"].(map[string]any)["status"] != "submitted" {
		t.Fatal("actual CLI did not submit exact run")
	}
}

func TestTaskLarkSwitchAppInternalPhaseAAndOrdinaryInbox(t *testing.T) {
	s, in, before, provider := taskLarkSwitchAppReady(t)
	ctx := context.Background()
	var pub store.TaskLarkFenceMember
	for _, member := range in.Members {
		if member.Role == "publisher" {
			pub = member
		}
	}
	state, created, err := s.a.Tasks.ReserveLarkSwitch(ctx, auth.Console(), in)
	if err != nil || !created || state.Status != "pending" {
		t.Fatal("actual App internal A", err)
	}
	if _, created, err = s.a.Tasks.ReserveLarkSwitch(ctx, auth.Console(), in); err != nil || created {
		t.Fatal("App replay repeated reservation", err)
	}
	msg, err := s.a.API.Inbox.Send(ctx, auth.Console(), "默认项目/SwitchPublisher#"+in.TaskID.String(), "durable message during switch", "interrupt", inbox.QuestionInput{})
	if err != nil {
		t.Fatal("actual Inbox transaction", err)
	}
	read, err := s.a.Store.GetInboxMessage(ctx, msg.ID, 160)
	if err != nil || read.ReadAt != nil || read.To.RuntimeID != pub.RuntimeID {
		t.Fatal("Inbox target/read changed", err)
	}
	var deferred, runs, turns, bindings int
	if err = s.a.Store.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM task_lark_deferred_inbox WHERE message_id=$1),(SELECT count(*) FROM runs),(SELECT count(*) FROM task_turns),(SELECT count(*) FROM task_lark_credential_bindings)`, msg.ID).Scan(&deferred, &runs, &turns, &bindings); err != nil || deferred != 1 || runs+turns+bindings != 0 {
		t.Fatal("A/Inbox launched work or published scope", deferred, runs, turns, bindings, err)
	}
	for _, member := range in.Members {
		if _, found, err := s.a.Store.ClaimTaskTurn(ctx, in.TaskID, member.RuntimeID); err != nil || found {
			t.Fatal("workflow claim bypassed fence", err)
		}
		if _, found, err := s.a.Store.ClaimTaskInboxTurn(ctx, in.TaskID, member.RuntimeID); err != nil || found {
			t.Fatal("Inbox claim bypassed fence", err)
		}
		inst, err := s.a.Store.TaskInstance(ctx, member.RuntimeID)
		if err != nil || sha256.Sum256(inst.Snapshot) != before[member.RuntimeID] {
			t.Fatal("A rewrote original snapshot", err)
		}
	}
	if provider.DeniedDials.Load() != 0 {
		t.Fatal("fixture attempted non-loopback provider")
	}
}
