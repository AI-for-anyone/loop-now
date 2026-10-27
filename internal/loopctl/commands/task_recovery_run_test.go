package commands_test

import (
	"testing"

	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/output"
)

func TestTaskRecoveryRunExplicitSinglePost(t *testing.T) {
	s, e := withTaskSamples(t)
	file := jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})
	e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run", artifactUUID).ok(t)
	if got := lastBody(t, s)["runId"]; got != artifactUUID || len(s.Requests()) != 1 {
		t.Fatal("run not explicit/single POST", got, len(s.Requests()))
	}
	e.stdin = "failed safely"
	e.run("task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-", "--run", artifactUUID).ok(t)
	if got := lastBody(t, s)["runId"]; got != artifactUUID || len(s.Requests()) != 2 {
		t.Fatal("fail implicit GET/retry", got, len(s.Requests()))
	}
	s.On("POST /tasks/{taskId}/nodes/{nodeRunId}/submit", clitest.Sample{Status: 409, Body: apiError("task_conflict", "原run已失效")})
	e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run", artifactUUID).fails(t, "task_conflict", 8)
	if len(s.Requests()) != 3 {
		t.Fatal("conflict triggered re-read/retry")
	}
}
func TestTaskRecoveryRunInvalidZeroDial(t *testing.T) {
	for _, id := range []string{"latest", "00000000-0000-0000-0000-000000000000"} {
		s, e := withTaskSamples(t)
		e.block = true
		file := jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})
		e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run", id).fails(t, output.CodeInvalidArgument, 2)
		e.stdin = "reason"
		e.run("task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-", "--run", id).fails(t, output.CodeInvalidArgument, 2)
		if e.dialCount() != 0 || len(s.Requests()) != 0 {
			t.Fatal("invalid run dialed")
		}
	}
	s, e := withTaskSamples(t)
	e.block = true
	file := jsonFile(t, map[string]any{"actionId": actionUUID, "runId": artifactUUID, "output": map[string]any{}, "edgeId": nil})
	e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run", artifactUUID).fails(t, output.CodeInvalidArgument, 2)
	if len(s.Requests()) != 0 || e.dialCount() != 0 {
		t.Fatal("dual run sources silently merged")
	}
}

func TestTaskRecoveryQuestionExplicitRunSinglePost(t *testing.T) {
	s, e := agentStub(t)
	response := inboxMessage(agentParticipant(), consoleParticipant())
	response["question"] = inboxQuestion("open")
	s.On("POST /inbox/messages", clitest.Sample{Status: 201, Body: response})
	e.run("inbox", "send", "@console", "--text", "continue?", "--option", "yes", "--node-run", nodeUUID, "--run", artifactUUID).ok(t)
	if len(s.Requests()) != 1 || lastBody(t, s)["runId"] != artifactUUID || lastBody(t, s)["nodeRunId"] != nodeUUID {
		t.Fatal("question inferred/retried run")
	}
	s.On("POST /inbox/messages", clitest.Sample{Status: 409, Body: apiError("task_conflict", "恢复run失效")})
	e.run("inbox", "send", "@console", "--text", "continue?", "--option", "yes", "--node-run", nodeUUID, "--run", artifactUUID).fails(t, "task_conflict", 8)
	if len(s.Requests()) != 2 {
		t.Fatal("question write retried")
	}
}

func TestTaskRecoveryRunIDAlias(t *testing.T) {
	s, e := withTaskSamples(t)
	file := jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})
	e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run-id", artifactUUID).ok(t)
	e.stdin = "failed safely"
	e.run("task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-", "--run-id", artifactUUID).ok(t)
	if len(s.Requests()) != 2 || lastBody(t, s)["runId"] != artifactUUID {
		t.Fatal("alias lost explicit run/single POST")
	}
	s2, e2 := agentStub(t)
	response := inboxMessage(agentParticipant(), consoleParticipant())
	response["question"] = inboxQuestion("open")
	s2.On("POST /inbox/messages", clitest.Sample{Status: 201, Body: response})
	e2.run("inbox", "send", "@console", "--text", "continue?", "--option", "yes", "--node-run", nodeUUID, "--run-id", artifactUUID).ok(t)
	if len(s2.Requests()) != 1 || lastBody(t, s2)["runId"] != artifactUUID {
		t.Fatal("question alias lost exact turn")
	}
}

func TestTaskRecoveryRunIDAliasRejectsAmbiguousInputBeforeDial(t *testing.T) {
	s, e := withTaskSamples(t)
	e.block = true
	file := jsonFile(t, map[string]any{"actionId": actionUUID, "output": map[string]any{}, "edgeId": nil})
	for _, args := range [][]string{
		{"task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run", artifactUUID, "--run-id", artifactUUID},
		{"task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run-id", "00000000-0000-0000-0000-000000000000"},
		{"task", "fail", taskUUID, nodeUUID, "--action-id", actionUUID, "--reason-file", "-", "--run-id", "latest"},
		{"inbox", "send", "@console", "--text", "question", "--option", "yes", "--node-run", nodeUUID, "--run", artifactUUID, "--run-id", artifactUUID},
	} {
		e.run(args...).fails(t, output.CodeInvalidArgument, 2)
	}
	file = jsonFile(t, map[string]any{"actionId": actionUUID, "runId": artifactUUID, "output": map[string]any{}, "edgeId": nil})
	e.run("task", "submit", taskUUID, nodeUUID, "--from-file", file, "--run-id", artifactUUID).fails(t, output.CodeInvalidArgument, 2)
	if e.dialCount() != 0 || len(s.Requests()) != 0 {
		t.Fatal("ambiguous/invalid run dialed")
	}
}
