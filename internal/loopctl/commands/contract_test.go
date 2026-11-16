package commands_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"loop-now/internal/loopctl/cli"
	"loop-now/internal/loopctl/clitest"
	"loop-now/internal/loopctl/commands"
	"loop-now/internal/server/content"
)

// adminSamples gives one success sample for every operation in the contract used by admin commands.
func adminSamples() map[string]clitest.Sample {
	ok := func(body any) clitest.Sample { return clitest.Sample{Status: 200, Body: body} }
	created := func(body any) clitest.Sample { return clitest.Sample{Status: 201, Body: body} }
	none := clitest.Sample{Status: 204}
	return map[string]clitest.Sample{
		"GET /health": ok(health(true)),
		"GET /agents/{agentId}/runs/{runId}/timeline": ok(runTimeline()),
		"GET /usage":           ok(usagePage()),
		"GET /anomalies":       ok(anomalyPage(nil)),
		"GET /model-providers": ok(modelProviderList()),
		"GET /model-providers/{providerId}/models":             ok(modelList()),
		"GET /cli-templates":                                   ok(map[string]any{"templates": []any{cliTemplate()}}),
		"GET /projects":                                        ok(map[string]any{"projects": []any{project()}}),
		"GET /projects/{projectId}":                            ok(project()),
		"GET /plugins":                                         ok(map[string]any{"plugins": []any{pluginSample()}}),
		"POST /plugin-previews":                                created(pluginPreviewSample()),
		"POST /plugins":                                        created(pluginSample()),
		"POST /plugins/{pluginId}/update":                      ok(pluginSample()),
		"DELETE /plugins/{pluginId}":                           none,
		"POST /projects":                                       created(project()),
		"PATCH /projects/{projectId}":                          ok(project()),
		"DELETE /projects/{projectId}":                         none,
		"PUT /projects/{projectId}/manager":                    ok(project()),
		"DELETE /projects/{projectId}/manager":                 ok(project()),
		"GET /projects/{projectId}/agents":                     ok(map[string]any{"agents": []any{agentListItem()}}),
		"GET /platform/agents":                                 ok(map[string]any{"agents": []any{}}),
		"POST /platform/agents":                                created(agentDetail()),
		"POST /projects/{projectId}/agents":                    created(agentDetail()),
		"GET /agents/{agentId}":                                ok(agentDetail()),
		"PATCH /agents/{agentId}":                              ok(agentDetail()),
		"DELETE /agents/{agentId}":                             none,
		"GET /agents/{agentId}/status":                         ok(agentStatus()),
		"GET /agents/{agentId}/messages":                       ok(messagePage()),
		"POST /agents/{agentId}/session/reset":                 ok(sessionInfo),
		"PUT /agents/{agentId}/clis/{cli}":                     ok(agentCli()),
		"DELETE /agents/{agentId}/clis/{cli}":                  none,
		"PUT /agents/{agentId}/clis/{cli}/credential":          ok(credentialMeta()),
		"DELETE /agents/{agentId}/clis/{cli}/credential":       none,
		"PUT /agents/{agentId}/model-credential":               ok(credentialMeta()),
		"DELETE /agents/{agentId}/model-credential":            none,
		"GET /agents/{agentId}/skills":                         ok(map[string]any{"skills": []any{agentSkillSummary()}}),
		"POST /agents/{agentId}/skills":                        created(agentSkill()),
		"GET /agents/{agentId}/skills/{agentSkillId}":          ok(agentSkill()),
		"DELETE /agents/{agentId}/skills/{agentSkillId}":       none,
		"PUT /agents/{agentId}/skills/{agentSkillId}/skill-md": ok(agentSkill()),
		"POST /agents/{agentId}/skills/{agentSkillId}/recopy":  ok(agentSkill()),
		"GET /skills":                                          ok(map[string]any{"skills": []any{skillTemplateSummary()}}),
		"POST /skills":                                         created(skillTemplate()),
		"GET /skills/{skillId}":                                ok(skillTemplate()),
		"PUT /skills/{skillId}":                                ok(skillTemplate()),
		"DELETE /skills/{skillId}":                             none,
		"PUT /skills/{skillId}/skill-md":                       ok(skillTemplate()),
	}
}

// agentSamples gives one success sample for every operation in the Agent contract (api/agent-openapi.yaml).
func agentSamples() map[string]clitest.Sample {
	me := agentParticipant()
	samples := map[string]clitest.Sample{
		"GET /agents/status":                     {Status: 200, Body: agentStatus()},
		"GET /projects":                          {Status: 200, Body: map[string]any{"projects": []any{platformDirectorySample()}}},
		"GET /sleep":                             {Status: 200, Body: map[string]any{"currentRunId": nil, "sleep": nil}},
		"POST /sleep":                            {Status: 202, Body: map[string]any{"sleep": sleepRecord("requested", false), "endTurn": true}},
		"GET /whoami":                            {Status: 200, Body: whoamiBody("agent", agentAddress, "dev")},
		"GET /inbox/messages":                    {Status: 200, Body: inboxPage(nil, inboxSummary(consoleParticipant(), me, false))},
		"POST /inbox/messages":                   {Status: 201, Body: inboxMessage(me, consoleParticipant())},
		"POST /inbox/messages/{messageId}/read":  {Status: 200, Body: inboxMessage(consoleParticipant(), me)},
		"POST /inbox/messages/{messageId}/reply": {Status: 201, Body: inboxMessage(me, consoleParticipant())},
	}
	for op, sample := range taskSamples() {
		samples[op] = sample
	}
	return samples
}

// externalInboxSamples gives success samples for the Inbox, whoami and lookup-by-address operations on the external
// port.
func externalInboxSamples() map[string]clitest.Sample {
	me := externalParticipant()
	return map[string]clitest.Sample{
		"GET /whoami":                            {Status: 200, Body: whoamiBody("external", "@ci-bot", "ci-bot")},
		"GET /inbox/messages":                    {Status: 200, Body: inboxPage(nil, inboxSummary(agentParticipant(), me, false))},
		"POST /inbox/messages":                   {Status: 201, Body: inboxMessage(me, agentParticipant())},
		"POST /inbox/messages/{messageId}/read":  {Status: 200, Body: inboxMessage(agentParticipant(), me)},
		"POST /inbox/messages/{messageId}/reply": {Status: 201, Body: inboxMessage(me, agentParticipant())},
		"GET /inbox/threads":                     {Status: 200, Body: threadPage()},
		"GET /inbox/threads/{threadId}":          {Status: 200, Body: thread()},
		"GET /agents":                            {Status: 200, Body: map[string]any{"agents": []any{agentListItem()}}},
	}
}

// Every command that connects to the server (with all option combinations) is sent, for each identity it allows,
// to the matching contract stub: every request is an operation in the contract and matches it; the external
// identity only calls operations whose x-access is console-and-external; the Agent identity only calls operations
// in api/agent-openapi.yaml (tasks 4.2, 6.8).
func TestCommandsMatchContract(t *testing.T) {
	external := adminSamples()
	for op, smp := range cliAuthSamples() {
		external[op] = smp
	}
	for op, smp := range dbConnectionSamples() {
		external[op] = smp
	}
	for op, smp := range projectCredentialSamples() {
		external[op] = smp
	}
	for op, smp := range externalInboxSamples() {
		external[op] = smp
	}
	for _, c := range commands.Registry().Commands() {
		if !c.Network {
			continue
		}
		if c.AllowedFor(cli.IdentityExternal) {
			t.Run(c.Name()+"/外部身份", func(t *testing.T) {
				s, e := stub(t)
				for op, smp := range external {
					s.On(op, smp)
				}
				for _, argv := range sampleArgv(t, c) {
					e.run(argv...).ok(t)
				}
				reqs := s.Requests()
				if len(reqs) == 0 {
					t.Fatal("没有发出请求")
				}
				for _, r := range reqs {
					if a := s.Access(r.Op); a != "console-and-external" {
						t.Errorf("%s 调用了 x-access 为 %q 的操作 %s", c.Name(), a, r.Op)
					}
				}
			})
		}
		if c.AllowedFor(cli.IdentityAgent) {
			t.Run(c.Name()+"/Agent 身份", func(t *testing.T) {
				s, e := agentStub(t)
				for op, smp := range agentSamples() {
					s.On(op, smp)
				}
				for _, argv := range sampleArgv(t, c) {
					e.run(argv...).ok(t)
				}
				if len(s.Requests()) == 0 {
					t.Fatal("没有发出请求")
				}
			})
		}
	}
}

// After a package built by loopctl is unpacked with the server's parsing logic, the directory structure matches the
// original directory.
func TestZipDirMatchesServerParser(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"SKILL.md":           "---\nname: review\ndescription: 代码评审\n---\n正文\n",
		"scripts/run.sh":     "#!/bin/sh\necho hi\n",
		"references/深入/a.md": "中文路径\n",
		"empty.txt":          "",
	}
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "emptydir"), 0o755)
	data, err := commands.ZipDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, meta, err := content.ExtractSkillZip(data, content.SkillArchiveLimits{MaxTotal: 1 << 20, MaxEntries: 100})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, f := range got {
		m[f.Path] = string(f.Data)
	}
	if !reflect.DeepEqual(m, files) || meta.Name != "review" {
		t.Fatalf("服务端解开的内容 %v", m)
	}
}
