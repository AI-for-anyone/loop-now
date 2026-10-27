package commands_test

// 符合 api/openapi.yaml 的样例响应（已包含 add-observability、add-project-layer 与 add-inbox-and-external-access 的字段）。契约文件更新后，按新契约同步修改这里。

const now = "2026-10-13T00:00:00Z"

var (
	modelRef    = map[string]any{"provider": "anthropic", "modelId": "claude-x"}
	sessionInfo = map[string]any{"state": "active", "epoch": 1}
)

const (
	projectUUID  = "0192f1a2-0000-7000-8000-0000000000a1"
	agentAddress = "默认组织/默认项目/dev"
)

func project() map[string]any {
	return map[string]any{"id": projectUUID, "organizationId": "0192f1a2-0000-7000-8000-0000000000a0", "name": "默认项目",
		"isDefault": true, "managerAgentId": nil, "agentCount": 1, "createdAt": now, "updatedAt": now}
}

func agentListItem() map[string]any {
	return map[string]any{"id": agentUUID, "scope": "project", "projectId": projectUUID, "isManager": false, "address": agentAddress, "name": "dev", "model": modelRef, "configVersion": 1, "busy": false,
		"sessionState": "active", "sleep": nil, "sleepRevision": 0, "createdAt": now, "updatedAt": now}
}

func agentDetail() map[string]any {
	return map[string]any{"id": agentUUID, "scope": "project", "projectId": projectUUID, "isManager": false, "address": agentAddress, "name": "dev", "model": modelRef,
		"prompt": "p", "promptVersion": 1, "configVersion": 1, "busy": false, "session": sessionInfo, "clis": []any{},
		"modelCredential": nil, "createdAt": now, "updatedAt": now}
}

func credentialMeta() map[string]any {
	return map[string]any{"status": "set", "updatedAt": now, "fieldNames": []any{"token"}, "envNames": []any{"GH_TOKEN"},
		"filePaths": []any{}, "warnings": []any{}}
}

func agentCli() map[string]any {
	return map[string]any{"name": "gh", "hasTemplate": true, "credential": nil}
}

func skillFile() map[string]any { return map[string]any{"path": "SKILL.md", "size": 10, "sha256": "x"} }

func skillTemplate() map[string]any {
	return map[string]any{"id": "0192f1a2-0000-7000-8000-0000000000c1", "name": "review", "description": "d",
		"files": []any{skillFile()}, "skillMd": "---\nname: review\n---\n", "createdAt": now, "updatedAt": now}
}

func skillTemplateSummary() map[string]any {
	return map[string]any{"id": "0192f1a2-0000-7000-8000-0000000000c1", "name": "review", "description": "d", "fileCount": 1,
		"createdAt": now, "updatedAt": now}
}

func agentSkill() map[string]any {
	return map[string]any{"id": "0192f1a2-0000-7000-8000-0000000000d1", "name": "review", "description": "d",
		"sourceTemplateId": nil, "files": []any{skillFile()}, "skillMd": "---\nname: review\n---\n", "copiedAt": now, "updatedAt": now}
}

func agentSkillSummary() map[string]any {
	return map[string]any{"id": "0192f1a2-0000-7000-8000-0000000000d1", "name": "review", "description": "d",
		"sourceTemplateId": nil, "fileCount": 1, "copiedAt": now, "updatedAt": now}
}

const runUUID = "0192f1a2-0000-7000-8000-0000000000e1"

func usageTotals() map[string]any {
	return map[string]any{"calls": 2, "input": 100, "output": 20, "cacheRead": 0, "cacheWrite": 0, "total": 120}
}

func run() map[string]any {
	return map[string]any{"id": runUUID, "status": "failed", "phase": nil, "endReason": nil,
		"errorCode": "pi_error", "errorMessage": "模型返回错误", "configVersion": 1, "whenBusy": nil, "queuedAt": now, "startedAt": now,
		"runningAt": now, "abortRequestedAt": nil, "endedAt": now, "tools": []any{}, "usage": usageTotals(), "model": modelRef}
}

func runTimeline() map[string]any {
	return map[string]any{"run": run(), "usage": usageTotals(), "entries": []any{
		map[string]any{"type": "dispatched", "at": now},
		map[string]any{"type": "running", "at": now},
		map[string]any{"type": "ended", "at": now, "status": "failed", "endReason": nil, "errorCode": "pi_error"},
	}}
}

func usagePage() map[string]any {
	row := map[string]any{"agentId": agentUUID, "agentName": "dev", "agentDeleted": false, "scope": "project", "projectId": projectUUID,
		"provider": "anthropic", "model": "claude-x"}
	for k, v := range usageTotals() {
		row[k] = v
	}
	return map[string]any{"from": "2026-10-12T16:00:00Z", "to": nil, "rows": []any{row}}
}

func anomalyPage(next any) map[string]any {
	item := map[string]any{"runId": runUUID, "kind": "run_failed", "at": now, "agentId": agentUUID, "agentName": "dev", "scope": "project", "projectId": projectUUID,
		"errorCode": "pi_error", "errorMessage": "模型返回错误", "endReason": nil}
	return map[string]any{"items": []any{item}, "total": 2, "nextCursor": next}
}

func health(pg bool) map[string]any {
	return map[string]any{"ok": pg, "postgres": map[string]any{"ok": pg}, "redis": map[string]any{"ok": true}}
}

func messagePage() map[string]any {
	msg := map[string]any{"id": "0192f1a2-0000-7000-8000-0000000000f1", "role": "user", "origin": "console", "text": "hi", "createdAt": now,
		"sessionEpoch": 1, "run": run()}
	return map[string]any{"messages": []any{msg}, "hasMore": false, "hasMoreAfter": false}
}

func agentStatus() map[string]any {
	return map[string]any{"agentId": agentUUID, "busy": false, "sleep": nil, "sleepRevision": 0, "currentRun": nil, "queued": map[string]any{"interrupt": 0, "normal": 0},
		"processState": "idle", "processPid": 4242, "processStartedAt": now, "session": sessionInfo}
}

func modelProviderList() map[string]any {
	return map[string]any{"piVersion": "1.1.0", "catalogGeneratedAt": nil,
		"providers": []any{map[string]any{"id": "anthropic", "apiKeyEnvVars": []any{"ANTHROPIC_API_KEY"}, "authMethods": []any{"api_key"}, "modelCount": 1}}}
}

func modelList() map[string]any {
	return map[string]any{"providerId": "anthropic", "models": []any{map[string]any{"id": "claude-x", "name": "Claude X",
		"reasoning": true, "contextWindow": 200000, "maxTokens": 8192, "input": []any{"text"}}}}
}

func cliTemplate() map[string]any {
	return map[string]any{"name": "gh", "version": "2.100.0", "fields": []any{}, "injection": "env",
		"install": map[string]any{"status": "installed", "error": nil}, "warnings": []any{}}
}

func apiError(code, msg string) map[string]any {
	return map[string]any{"error": map[string]any{"code": code, "message": msg}}
}

const (
	messageUUID  = "0192f1a2-0000-7000-8000-000000000101"
	threadUUID   = "0192f1a2-0000-7000-8000-000000000100"
	identityUUID = "0192f1a2-0000-7000-8000-000000000201"
)

func consoleParticipant() map[string]any {
	return map[string]any{"kind": "console", "id": nil, "address": "@console", "name": "控制台", "deleted": false, "pluginId": nil, "channelId": nil}
}

func agentParticipant() map[string]any {
	return map[string]any{"kind": "agent", "id": agentUUID, "address": agentAddress, "name": "dev", "deleted": false, "pluginId": nil, "channelId": nil}
}

func externalParticipant() map[string]any {
	return map[string]any{"kind": "external", "id": identityUUID, "address": "@ci-bot", "name": "ci-bot", "deleted": false, "pluginId": nil, "channelId": nil}
}

func inboxSummary(from, to map[string]any, read bool) map[string]any {
	m := map[string]any{"id": messageUUID, "threadId": threadUUID, "inReplyTo": nil, "from": from, "to": to,
		"preview": "构建完成", "whenBusy": "queue", "createdAt": now, "readAt": nil, "cancelledAt": nil, "question": nil, "answerTo": nil, "notice": nil, "task": nil}
	if read {
		m["readAt"] = now
	}
	if to["kind"] != "agent" {
		m["whenBusy"] = nil
	}
	return m
}

func inboxMessage(from, to map[string]any) map[string]any {
	m := inboxSummary(from, to, true)
	m["text"] = "构建完成"
	return m
}

func inboxPage(next any, msgs ...map[string]any) map[string]any {
	items := []any{}
	for _, m := range msgs {
		items = append(items, m)
	}
	return map[string]any{"messages": items, "nextCursor": next}
}

func whoamiBody(kind, address, name string) map[string]any {
	return map[string]any{"kind": kind, "address": address, "name": name}
}

func threadPage() map[string]any {
	t := map[string]any{"threadId": threadUUID, "participants": []any{externalParticipant(), agentParticipant()},
		"lastMessage": inboxSummary(agentParticipant(), externalParticipant(), false), "unreadCount": 1, "updatedAt": now}
	return map[string]any{"threads": []any{t}, "unreadTotal": 1, "nextCursor": nil}
}

func thread() map[string]any {
	return map[string]any{"threadId": threadUUID, "messages": []any{
		inboxMessage(externalParticipant(), agentParticipant()),
		inboxMessage(agentParticipant(), externalParticipant()),
	}}
}

func inboxQuestion(state string) map[string]any {
	q := map[string]any{"options": []any{map[string]any{"id": "1", "label": "合并"}, map[string]any{"id": "2", "label": "暂缓"}}, "expiresAt": now, "state": state, "answer": nil}
	if state == "answered" {
		q["answer"] = map[string]any{"messageId": messageUUID, "optionId": "2", "optionLabel": "暂缓", "textPreview": "选择：暂缓", "by": consoleParticipant(), "at": now}
	}
	return q
}

func platformParticipant() map[string]any {
	return map[string]any{"kind": "platform", "id": nil, "address": "@platform", "name": "平台", "deleted": false, "pluginId": nil, "channelId": nil}
}

func pluginSource() map[string]any {
	return map[string]any{"kind": "builtin", "location": "hello", "ref": nil, "commit": nil, "npmVersion": nil, "market": nil}
}

func pluginSample() map[string]any {
	return map[string]any{"id": "hello", "displayName": "Hello", "description": "示例", "version": "1.0.0", "source": pluginSource(), "status": "installed",
		"lastError": nil, "hasPlatform": true, "enabledProjects": 0, "grantedAgents": 0, "installedAt": now, "updatedAt": now}
}

func pluginPreviewSample() map[string]any {
	manifest := map[string]any{"id": "hello", "name": "@loop-now/hello", "version": "1.0.0", "displayName": "Hello", "description": "示例", "minPlatformVersion": nil,
		"events": []any{"inbox.received"}, "permissions": []any{"state"}, "failClosed": false, "configSchema": map[string]any{}, "accounts": []any{},
		"components": map[string]any{"agentExtensions": 1, "skills": []any{}, "mcpServers": []any{}, "bin": []any{}, "platform": true, "actions": []any{map[string]any{"id": "resend", "label": "重发", "where": []any{"identity"}, "fields": map[string]any{}}}, "channels": []any{}, "http": false, "schedules": []any{}}}
	return map[string]any{"previewId": actionUUID, "expiresAt": now, "source": pluginSource(), "trusted": true, "manifest": manifest, "installedVersion": nil}
}
