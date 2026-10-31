package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/pluginhost"
)

// 同步 Hook 的调用点（plugins/SPEC.md §4）：在写入之前、不在数据库事务里调用，超时与失败按 §3 的策略处理。

// hook 调用一个同步 Hook；没有配置插件时返回空结果。
func (s *Server) hook(r *http.Request, kind pluginhost.Kind, typ string, project, agent, task, run *uuid.UUID, fields map[string]any) (pluginhost.Outcome, error) {
	if s.Hooks == nil {
		return pluginhost.Outcome{}, nil
	}
	return s.Hooks.Hook(r.Context(), kind, pluginhost.Event(typ, project, agent, task, run, fields), project)
}

// intercept 调用拦截类 Hook，被阻止时返回 409 plugin_blocked。
func (s *Server) intercept(r *http.Request, typ string, project, agent, task, run *uuid.UUID, fields map[string]any) error {
	o, err := s.hook(r, pluginhost.Intercept, typ, project, agent, task, run, fields)
	if err != nil {
		return err
	}
	if o.Blocked {
		return pluginhost.BlockedError(o)
	}
	return nil
}

// modifyText 调用修改类 Hook，返回插件改过的 text；patch 里的 text 不是字符串时忽略。
func (s *Server) modifyText(r *http.Request, typ string, project, agent, task *uuid.UUID, text string, fields map[string]any) (string, error) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["text"] = text
	o, err := s.hook(r, pluginhost.Modify, typ, project, agent, task, nil, fields)
	if err != nil {
		return text, err
	}
	if v, ok := o.Patch["text"].(string); ok {
		return v, nil
	}
	return text, nil
}

// party 是事件里的一方（PluginEventParty）。
func party(p auth.Principal) map[string]any {
	out := map[string]any{"kind": string(p.Kind), "id": nil, "address": nil}
	if p.ID != uuid.Nil {
		out["id"] = p.ID
	}
	if p.Address != "" {
		out["address"] = p.Address
	}
	return out
}

// agentProject 返回 Agent 所在的项目；平台 Agent 或找不到时为空。
func (s *Server) agentProject(r *http.Request, agent uuid.UUID) *uuid.UUID {
	a, err := s.Store.GetAgent(r.Context(), agent)
	if err != nil {
		return nil
	}
	return a.ProjectID
}

// taskProject 返回任务所在的项目。
func (s *Server) taskProject(r *http.Request, task uuid.UUID) *uuid.UUID {
	var pid uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT project_id FROM task_records WHERE id=$1`, task).Scan(&pid); err != nil {
		return nil
	}
	return &pid
}

// principalScope 返回请求身份对应的项目、Agent 与任务（只有 Agent 身份有）。
func (s *Server) principalScope(r *http.Request, p auth.Principal) (project, agent, task *uuid.UUID) {
	if p.Kind != auth.KindAgent {
		return nil, nil, nil
	}
	id := p.ID
	agent = &id
	if p.TaskID != uuid.Nil {
		t := p.TaskID
		task = &t
	}
	return s.agentProject(r, id), agent, task
}

// consoleCommand 先处理平台内置命令，再把其余斜杠消息交给认领类 Hook console.command。
func (s *Server) consoleCommand(w http.ResponseWriter, r *http.Request, agent uuid.UUID, project *uuid.UUID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	if result, handled, err := s.platformConsoleCommand(r, agent, text); handled {
		if err != nil {
			s.fail(w, r, err)
		} else {
			writeJSON(w, http.StatusOK, result)
		}
		return true
	}
	if s.Hooks == nil {
		return false
	}
	command, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "/"), " ")
	o, err := s.hook(r, pluginhost.Claim, "console.command", project, &agent, nil, nil, map[string]any{"command": command, "text": text, "from": party(principal(r))})
	if err != nil {
		s.fail(w, r, err)
		return true
	}
	if !o.Handled {
		return false
	}
	var v struct {
		Reply *string `json:"reply"`
	}
	_ = json.Unmarshal(o.Value, &v)
	writeJSON(w, http.StatusOK, map[string]any{"handled": true, "source": "plugin", "plugin": o.Plugin, "reply": v.Reply, "command": ""})
	return true
}

// skipReplayedHook 报告任务动作是否已经执行过：重放直接返回原结果，不再询问插件。
func (s *Server) skipReplayedHook(r *http.Request, p auth.Principal, action uuid.UUID) (bool, error) {
	if s.Hooks == nil || action == uuid.Nil {
		return s.Hooks == nil, nil
	}
	return s.Tasks.ActionRecorded(r.Context(), p, action)
}
