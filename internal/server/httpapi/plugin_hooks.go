package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"loop-now/internal/server/auth"
	"loop-now/internal/server/pluginhost"
)

// Call sites of synchronous Hooks (plugins/SPEC.md §4): called before the write and outside any database transaction;
// timeouts and failures are handled by the policy in §3.

// hook calls one synchronous Hook; returns an empty result when no plugins are configured.
func (s *Server) hook(r *http.Request, kind pluginhost.Kind, typ string, project, agent, task, run *uuid.UUID, fields map[string]any) (pluginhost.Outcome, error) {
	if s.Hooks == nil {
		return pluginhost.Outcome{}, nil
	}
	return s.Hooks.Hook(r.Context(), kind, pluginhost.Event(typ, project, agent, task, run, fields), project)
}

// intercept calls an intercept Hook and returns 409 plugin_blocked when the action is blocked.
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

// modifyText calls a modify Hook and returns the text as changed by plugins; a text in the patch that is not a string
// is ignored.
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

// party is one party in an event (PluginEventParty).
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

// agentProject returns the Agent's project; nil for a platform Agent or when the Agent is not found.
func (s *Server) agentProject(r *http.Request, agent uuid.UUID) *uuid.UUID {
	a, err := s.Store.GetAgent(r.Context(), agent)
	if err != nil {
		return nil
	}
	return a.ProjectID
}

// taskProject returns the task's project.
func (s *Server) taskProject(r *http.Request, task uuid.UUID) *uuid.UUID {
	var pid uuid.UUID
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT project_id FROM task_records WHERE id=$1`, task).Scan(&pid); err != nil {
		return nil
	}
	return &pid
}

// principalScope returns the project, Agent and task of the request identity (only Agent identities have them).
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

// consoleCommand handles the platform's built-in commands first, then hands other slash messages to the claim Hook
// console.command.
func (s *Server) consoleCommand(w http.ResponseWriter, r *http.Request, agent uuid.UUID, project *uuid.UUID, text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "/pi:") {
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

// skipReplayedHook reports whether a task action has already been performed: a replay returns the original result
// directly without asking plugins again.
func (s *Server) skipReplayedHook(r *http.Request, p auth.Principal, action uuid.UUID) (bool, error) {
	if s.Hooks == nil || action == uuid.Nil {
		return s.Hooks == nil, nil
	}
	return s.Tasks.ActionRecorded(r.Context(), p, action)
}
